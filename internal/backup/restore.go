package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ValidateArchive(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar header: %w", err)
		}
		if hdr.Name == "manifest.json" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read manifest: %w", err)
			}
			var m Manifest
			if err := json.Unmarshal(data, &m); err != nil {
				return nil, fmt.Errorf("unmarshal manifest: %w", err)
			}
			return &m, nil
		}
	}

	return nil, fmt.Errorf("manifest.json not found in archive")
}

func ExtractArchive(archivePath, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	destDir = filepath.Clean(destDir)
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar header: %w", err)
		}

		cleanName := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, "..") {
			return fmt.Errorf("refusing to extract entry with '..' path: %s", hdr.Name)
		}

		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			continue
		}

		target := filepath.Join(destDir, cleanName)

		if !strings.HasPrefix(target, destDir+string(filepath.Separator)) && target != destDir {
			return fmt.Errorf("refusing to extract entry outside destination: %s", hdr.Name)
		}

		if hdr.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("create dir %s: %w", target, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("create parent dir %s: %w", target, err)
		}

		dst, err := os.OpenFile(target, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return fmt.Errorf("create file %s: %w", target, err)
		}
		if _, err := io.Copy(dst, tr); err != nil {
			dst.Close()
			return fmt.Errorf("write file %s: %w", target, err)
		}
		dst.Close()
	}

	return nil
}

func ReplaceFiles(extractDir string, db *sql.DB, dbPath, configPath, storageDir string) error {
	manifestData, err := os.ReadFile(filepath.Join(extractDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("unmarshal manifest: %w", err)
	}

	if manifest.Format == "sql-dump" {
		if db == nil {
			return fmt.Errorf("database connection required for sql-dump restore")
		}
		sqlPath := filepath.Join(extractDir, "edub.sql")
		data, err := os.ReadFile(sqlPath)
		if err != nil {
			return fmt.Errorf("read sql dump: %w", err)
		}
		if _, err := db.ExecContext(context.Background(), string(data)); err != nil {
			return fmt.Errorf("execute sql dump: %w", err)
		}
	} else {
		extractDB := filepath.Join(extractDir, "edub.db")
		if _, err := os.Stat(extractDB); err == nil {
			tmpDB, err := os.CreateTemp(filepath.Dir(dbPath), "db-swap-*.db")
			if err != nil {
				return fmt.Errorf("create temp db: %w", err)
			}
			tmpDBPath := tmpDB.Name()
			tmpDB.Close()
			defer os.Remove(tmpDBPath)

			if err := copyFile(extractDB, tmpDBPath); err != nil {
				return fmt.Errorf("copy db to temp: %w", err)
			}

			if err := os.Rename(tmpDBPath, dbPath); err != nil {
				return fmt.Errorf("rename db into place: %w", err)
			}
		}
	}

	extractStorage := filepath.Join(extractDir, "storage")
	if _, err := os.Stat(extractStorage); err == nil {
		if err := os.MkdirAll(filepath.Dir(storageDir), 0755); err != nil {
			return fmt.Errorf("create storage parent: %w", err)
		}
		tmpStorage, err := os.MkdirTemp(filepath.Dir(storageDir), "storage-swap-*")
		if err != nil {
			return fmt.Errorf("create temp storage dir: %w", err)
		}
		defer os.RemoveAll(tmpStorage)

		if err := copyDir(extractStorage, tmpStorage); err != nil {
			return fmt.Errorf("copy storage to temp: %w", err)
		}

		oldBackup := storageDir + ".old"
		os.RemoveAll(oldBackup)

		if err := os.Rename(storageDir, oldBackup); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rename old storage: %w", err)
		}

		if err := os.Rename(tmpStorage, storageDir); err != nil {
			os.Rename(oldBackup, storageDir)
			return fmt.Errorf("rename new storage: %w", err)
		}

		os.RemoveAll(oldBackup)
	}

	extractConfig := filepath.Join(extractDir, "config.yaml")
	if _, err := os.Stat(extractConfig); err == nil {
		if err := copyFile(extractConfig, configPath); err != nil {
			return fmt.Errorf("replace config: %w", err)
		}
	}

	return nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}

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

	"gopkg.in/yaml.v3"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
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

func ReplaceFiles(extractDir string, db *sql.DB, dbCfg config.DatabaseConfig, dsn, configPath, storageDir string) error {
	manifestData, err := os.ReadFile(filepath.Join(extractDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("unmarshal manifest: %w", err)
	}

	if manifest.Format != "sql-dump" {
		return fmt.Errorf("unknown backup format %q", manifest.Format)
	}
	mode := manifest.Mode
	if mode == "" {
		mode = BackupModeFull
	}

	if mode != BackupModeDocuments {
		if db == nil {
			return fmt.Errorf("database connection required for sql-dump restore")
		}
		sqlPath := filepath.Join(extractDir, "edub.sql")
		if err := database.RestoreDumpViaPSQL(context.Background(), dbCfg.Runtime, dbCfg.Container, dsn, sqlPath); err != nil {
			return fmt.Errorf("restore sql dump: %w", err)
		}
	}

	if mode != BackupModeDocuments {
		oldDir := manifest.StorageDir
		if oldDir == "" {
			archivedDir, err := storageDirFromArchivedConfig(filepath.Join(extractDir, "config.yaml"))
			if err != nil {
				fmt.Printf("Warning: cannot determine archived storage dir (%v) — skipping database path rewrite\n", err)
			} else {
				oldDir = archivedDir
			}
		}

		if oldDir != "" {
			oldDir = filepath.Clean(oldDir)
			storageDir = filepath.Clean(storageDir)
		}
		if oldDir != "" && oldDir != storageDir {
			if err := rewriteStoragePaths(context.Background(), db, oldDir, storageDir); err != nil {
				return fmt.Errorf("rewrite storage paths: %w", err)
			}
		}
	}

	if mode != BackupModeDatabase {
		extractStorage := filepath.Join(extractDir, "storage")
		if _, err := os.Stat(extractStorage); err == nil {
			if err := os.MkdirAll(filepath.Dir(storageDir), 0755); err != nil {
				return fmt.Errorf("create storage parent: %w", err)
			}

			renameInPlace, err := utils.SameDevice(extractStorage, storageDir)
			if err != nil {
				return fmt.Errorf("stat storage dirs: %w", err)
			}

			tmpStorage := ""
			if !renameInPlace {
				tmpStorage, err = os.MkdirTemp(filepath.Dir(storageDir), "storage-swap-*")
				if err != nil {
					return fmt.Errorf("create temp storage dir: %w", err)
				}
				defer os.RemoveAll(tmpStorage)

				if err := copyDir(extractStorage, tmpStorage); err != nil {
					return fmt.Errorf("copy storage to temp: %w", err)
				}
			}

			oldBackup := storageDir + ".old"
			os.RemoveAll(oldBackup)

			if err := os.Rename(storageDir, oldBackup); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("rename old storage: %w", err)
			}

			if renameInPlace {
				if err := os.Rename(extractStorage, storageDir); err != nil {
					os.Rename(oldBackup, storageDir)
					return fmt.Errorf("rename new storage: %w", err)
				}
			} else {
				if err := os.Rename(tmpStorage, storageDir); err != nil {
					os.Rename(oldBackup, storageDir)
					return fmt.Errorf("rename new storage: %w", err)
				}
			}

			os.RemoveAll(oldBackup)
		}
	}

	extractConfig := filepath.Join(extractDir, "config.yaml")
	if _, err := os.Stat(extractConfig); err == nil {
		restoredConfig := configPath + ".restored"
		if err := copyFile(extractConfig, restoredConfig); err != nil {
			return fmt.Errorf("save restored config: %w", err)
		}
		fmt.Printf("Restored config saved as %s\n", restoredConfig)
	}

	return nil
}

func rewriteStoragePaths(ctx context.Context, db *sql.DB, oldDir, newDir string) error {
	if err := database.RewriteStoragePaths(ctx, db, oldDir, newDir); err != nil {
		return fmt.Errorf("rewrite storage paths: %w", err)
	}
	return nil
}

// Old backups predate the manifest StorageDir field, so the archived config
// is the only record of where storage lived.
func storageDirFromArchivedConfig(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read archived config: %w", err)
	}
	var cfg struct {
		Storage struct {
			StorageDir string `yaml:"storage_dir"`
		} `yaml:"storage"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse archived config: %w", err)
	}
	if cfg.Storage.StorageDir == "" {
		return "", fmt.Errorf("archived config has no storage.storage_dir")
	}
	if strings.HasPrefix(cfg.Storage.StorageDir, "~") {
		return "", fmt.Errorf("archived storage_dir uses ~ which is host-dependent; set manifest.storage_dir or update manually")
	}
	return cfg.Storage.StorageDir, nil
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

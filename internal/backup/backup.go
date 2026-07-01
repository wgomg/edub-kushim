package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/version"
	_ "modernc.org/sqlite"
)

type Manifest struct {
	Version           int    `json:"version"`
	Timestamp         string `json:"timestamp"`
	AppVersion        string `json:"app_version"`
	DbSizeBytes       int64  `json:"db_size_bytes"`
	StorageFilesCount int64  `json:"storage_files_count"`
	StorageSizeBytes  int64  `json:"storage_size_bytes"`
	ConfigHash        string `json:"config_hash"`
}

type BackupResult struct {
	Path        string    `json:"path"`
	SizeBytes   int64     `json:"size_bytes"`
	FilesCount  int64     `json:"files_count"`
	DbSizeBytes int64     `json:"db_size_bytes"`
	Manifest    *Manifest `json:"manifest"`
}

func Create(ctx context.Context, dbPath, backupDir, configPath, storageDir string) (*BackupResult, error) {
	tmpDir, err := os.MkdirTemp("", "edub-backup-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db for snapshot: %w", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return nil, fmt.Errorf("wal checkpoint: %w", err)
	}

	vacuumPath := filepath.Join(tmpDir, "edub.db")
	stmt := fmt.Sprintf("VACUUM INTO '%s'", vacuumPath)
	if _, err := db.Exec(stmt); err != nil {
		return nil, fmt.Errorf("vacuum into: %w", err)
	}

	dbInfo, err := os.Stat(vacuumPath)
	if err != nil {
		return nil, fmt.Errorf("stat vacuum db: %w", err)
	}

	configHash, err := fileHash(configPath)
	if err != nil {
		return nil, fmt.Errorf("hash config: %w", err)
	}

	storageFilesCount := int64(0)
	storageSizeBytes := int64(0)
	filepath.Walk(storageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			storageFilesCount++
			storageSizeBytes += info.Size()
		}
		return nil
	})

	configCopyPath := filepath.Join(tmpDir, "config.yaml")
	if err := copyFile(configPath, configCopyPath); err != nil {
		return nil, fmt.Errorf("copy config: %w", err)
	}

	manifest := &Manifest{
		Version:           1,
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		AppVersion:        version.Version,
		DbSizeBytes:       dbInfo.Size(),
		StorageFilesCount: storageFilesCount,
		StorageSizeBytes:  storageSizeBytes,
		ConfigHash:        fmt.Sprintf("sha256:%x", configHash),
	}

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), manifestData, 0644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	backupName := fmt.Sprintf("edub-backup-%s.tar.gz", time.Now().UTC().Format("2006-01-02T15-04-05"))
	backupPath := filepath.Join(backupDir, backupName)

	if err := createTarGz(backupPath, tmpDir, storageDir); err != nil {
		os.Remove(backupPath)
		return nil, fmt.Errorf("create tar.gz: %w", err)
	}

	archiveInfo, err := os.Stat(backupPath)
	if err != nil {
		return nil, fmt.Errorf("stat archive: %w", err)
	}

	return &BackupResult{
		Path:        backupPath,
		SizeBytes:   archiveInfo.Size(),
		FilesCount:  storageFilesCount,
		DbSizeBytes: dbInfo.Size(),
		Manifest:    manifest,
	}, nil
}

func ApplyRetention(backupDir string, keep int) error {
	if keep <= 0 {
		return nil
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return fmt.Errorf("read backup dir: %w", err)
	}

	var backups []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "edub-backup-") && strings.HasSuffix(e.Name(), ".tar.gz") {
			backups = append(backups, filepath.Join(backupDir, e.Name()))
		}
	}

	if len(backups) <= keep {
		return nil
	}

	slices.Sort(backups)

	toRemove := backups[:len(backups)-keep]
	for _, p := range toRemove {
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("remove old backup %s: %w", p, err)
		}
	}

	return nil
}

func createTarGz(dstPath, tmpDir, storageDir string) error {
	f, err := os.OpenFile(dstPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer f.Close()

	gw, err := gzip.NewWriterLevel(f, gzip.BestSpeed)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return fmt.Errorf("read tmp dir: %w", err)
	}

	for _, entry := range entries {
		path := filepath.Join(tmpDir, entry.Name())
		if err := addToTar(tw, path, entry.Name()); err != nil {
			return fmt.Errorf("add %s to tar: %w", entry.Name(), err)
		}
	}

	if _, err := os.Stat(storageDir); err == nil {
		if err := addToTar(tw, storageDir, "storage"); err != nil {
			return fmt.Errorf("add storage to tar: %w", err)
		}
	}

	return nil
}

func addToTar(tw *tar.Writer, srcPath, arcPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.Walk(srcPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(srcPath, path)
			if err != nil {
				return err
			}
			relPath := filepath.Join(arcPath, rel)
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = relPath
			if info.IsDir() {
				header.Name += "/"
			}
			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if !info.IsDir() {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				if _, err := io.Copy(tw, f); err != nil {
					return err
				}
			}
			return nil
		})
	}

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = arcPath
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()

	if _, err := io.Copy(d, s); err != nil {
		return err
	}

	if info, err := os.Stat(src); err == nil {
		os.Chmod(dst, info.Mode())
	}

	return nil
}

func fileHash(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

package service

import (
	"context"
	"fmt"
	"mime"
	"os"

	_mime "github.com/wgomg/edub-kushim/internal/documents/mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/log"
)

// ErroredFileInfo is one file in the errors directory: name, subdirectory,
// size, detected content type, and modification time.
type ErroredFileInfo struct {
	Name         string
	Subdir       string
	Size         int64
	OriginalType string
	ModifiedAt   time.Time
}

// ErroredFiles administers the errors directory: listing, path
// resolution with traversal protection, and deletion.
type ErroredFiles struct {
	cfg    *config.Config
	logger *log.Logger
}

// NewErroredFiles builds the admin service over the configured errors
// directory.
func NewErroredFiles(cfg *config.Config, logger *log.Logger) *ErroredFiles {
	return &ErroredFiles{cfg: cfg, logger: logger}
}

// List walks the errors directory tree and reports the files;
// subdirectories are not recursed beyond their own entries.
func (s *ErroredFiles) List(_ context.Context) ([]ErroredFileInfo, error) {
	storageDir := s.cfg.Storage.StorageDir
	subdirs := []string{storage.DirErrors, storage.DirErrorsDuplicates}

	var files []ErroredFileInfo
	for _, subdir := range subdirs {
		dir := filepath.Join(storageDir, storage.DirErrors, subdir)
		if subdir == storage.DirErrors {
			dir = filepath.Join(storageDir, storage.DirErrors)
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read dir %s: %w", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			mimeType := mime.TypeByExtension(filepath.Ext(entry.Name()))
			if mimeType == "" {
				mimeType = _mime.OctetStream
			}

			files = append(files, ErroredFileInfo{
				Name:         entry.Name(),
				Subdir:       subdir,
				Size:         info.Size(),
				OriginalType: mimeType,
				ModifiedAt:   info.ModTime(),
			})
		}
	}

	return files, nil
}

// GetPath resolves a subdirectory and file name to a path inside the
// errors directory, refusing traversal and unknown subdirectories.
func (s *ErroredFiles) GetPath(subdir, filename string) (string, error) {
	clean := filepath.Clean(filename)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("invalid filename")
	}
	if subdir != storage.DirErrors && subdir != storage.DirErrorsDuplicates {
		return "", fmt.Errorf("invalid subdir")
	}
	var resolved string
	if subdir == storage.DirErrorsDuplicates {
		resolved = filepath.Join(s.cfg.Storage.StorageDir, storage.DirErrors, subdir, clean)
	} else {
		resolved = filepath.Join(s.cfg.Storage.StorageDir, subdir, clean)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	expectedPrefix := filepath.Join(s.cfg.Storage.StorageDir, storage.DirErrors)
	absPrefix, err := filepath.Abs(expectedPrefix)
	if err != nil {
		return "", fmt.Errorf("resolve prefix: %w", err)
	}
	if !strings.HasPrefix(abs, absPrefix) {
		return "", fmt.Errorf("path traversal blocked")
	}
	return abs, nil
}

// Delete removes one errored file by subdirectory and name.
func (s *ErroredFiles) Delete(subdir, filename string) error {
	path, err := s.GetPath(subdir, filename)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("file not found")
	}
	return os.Remove(path)
}

// DeleteAll clears the whole errors directory tree and returns how many
// files went away.
func (s *ErroredFiles) DeleteAll(_ context.Context) (int, error) {
	storageDir := s.cfg.Storage.StorageDir
	subdirs := []string{storage.DirErrors, storage.DirErrorsDuplicates}

	count := 0
	for _, subdir := range subdirs {
		dir := filepath.Join(storageDir, storage.DirErrors, subdir)
		if subdir == storage.DirErrors {
			dir = filepath.Join(storageDir, storage.DirErrors)
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return count, fmt.Errorf("read dir %s: %w", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				s.logger.Error(nil, "delete errored file %s/%s: %v", subdir, entry.Name(), err)
				continue
			}
			count++
		}
	}

	return count, nil
}

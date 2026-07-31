package service

import (
	"context"
	"fmt"
	"mime"
	"os"

	_mime "github.com/wgomg/edub-kushim/internal/mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	erroredSubdir      = "errors"
	erroredSubdirDupes = "duplicated"
)

type ErroredFileInfo struct {
	Name         string
	Subdir       string
	Size         int64
	OriginalType string
	ModifiedAt   time.Time
}

type ErroredFiles struct {
	cfg    *config.Config
	logger *utils.Logger
}

func NewErroredFiles(cfg *config.Config, logger *utils.Logger) *ErroredFiles {
	return &ErroredFiles{cfg: cfg, logger: logger}
}

func (s *ErroredFiles) List(_ context.Context) ([]ErroredFileInfo, error) {
	storageDir := s.cfg.Storage.StorageDir
	subdirs := []string{erroredSubdir, erroredSubdirDupes}

	var files []ErroredFileInfo
	for _, subdir := range subdirs {
		dir := filepath.Join(storageDir, erroredSubdir, subdir)
		if subdir == erroredSubdir {
			dir = filepath.Join(storageDir, erroredSubdir)
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

func (s *ErroredFiles) GetPath(subdir, filename string) (string, error) {
	clean := filepath.Clean(filename)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("invalid filename")
	}
	if subdir != erroredSubdir && subdir != erroredSubdirDupes {
		return "", fmt.Errorf("invalid subdir")
	}
	var resolved string
	if subdir == erroredSubdirDupes {
		resolved = filepath.Join(s.cfg.Storage.StorageDir, erroredSubdir, subdir, clean)
	} else {
		resolved = filepath.Join(s.cfg.Storage.StorageDir, subdir, clean)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	expectedPrefix := filepath.Join(s.cfg.Storage.StorageDir, erroredSubdir)
	absPrefix, err := filepath.Abs(expectedPrefix)
	if err != nil {
		return "", fmt.Errorf("resolve prefix: %w", err)
	}
	if !strings.HasPrefix(abs, absPrefix) {
		return "", fmt.Errorf("path traversal blocked")
	}
	return abs, nil
}

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

func (s *ErroredFiles) DeleteAll(_ context.Context) (int, error) {
	storageDir := s.cfg.Storage.StorageDir
	subdirs := []string{erroredSubdir, erroredSubdirDupes}

	count := 0
	for _, subdir := range subdirs {
		dir := filepath.Join(storageDir, erroredSubdir, subdir)
		if subdir == erroredSubdir {
			dir = filepath.Join(storageDir, erroredSubdir)
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



package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_mime "github.com/wgomg/edub-kushim/internal/mime"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var dbidRe = regexp.MustCompile(`^\d+$`)

type OrphanedFileInfo struct {
	DocumentKey     string
	DocumentKeyType string
	FilePath        string
	OriginalPath    string
	SourceDir       string
	FileSize        int64
	OriginalType    string
}

func DetectFileType(stem string) (keyType string) {
	lower := strings.ToLower(stem)
	if uuidRe.MatchString(lower) {
		return "uuid"
	}
	if dbidRe.MatchString(stem) {
		return "dbid"
	}
	return ""
}

func WalkStorageDir(storageDir string) (<-chan OrphanedFileInfo, <-chan error) {
	infos := make(chan OrphanedFileInfo)
	errs := make(chan error, 1)

	go func() {
		defer close(infos)
		defer close(errs)

		subdirs := []string{"originals", "processed"}
		for _, sub := range subdirs {
			root := filepath.Join(storageDir, sub)
			if err := walkDir(root, sub, infos); err != nil {
				errs <- err
				return
			}
		}
	}()

	return infos, errs
}

func walkDir(root, sourceDir string, infos chan<- OrphanedFileInfo) error {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}

	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		if time.Since(info.ModTime()) < 30*time.Second {
			return nil
		}

		stem := strings.TrimSuffix(d.Name(), ".pdf")
		keyType := DetectFileType(stem)
		if keyType == "" {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = d.Name()
		}

		originalType := _mime.PDF

		infos <- OrphanedFileInfo{
			DocumentKey:     stem,
			DocumentKeyType: keyType,
			FilePath:        path,
			OriginalPath:    filepath.Join(sourceDir, rel),
			SourceDir:       sourceDir,
			FileSize:        info.Size(),
			OriginalType:    originalType,
		}

		return nil
	})
}

func QuarantineFile(storageDir string, info OrphanedFileInfo) (string, error) {
	quarantineDir := filepath.Join(storageDir, "orphaned")
	if err := os.MkdirAll(quarantineDir, 0755); err != nil {
		return "", fmt.Errorf("create orphaned dir: %w", err)
	}

	destDir := filepath.Join(quarantineDir, info.SourceDir)
	destPath := filepath.Join(destDir, filepath.Base(info.FilePath))

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("create source dir in orphaned: %w", err)
	}

	if err := os.Rename(info.FilePath, destPath); err != nil {
		return "", fmt.Errorf("move file to orphaned: %w", err)
	}

	return destPath, nil
}

func RemoveOrphanedFile(filePath string) error {
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove orphaned file: %w", err)
	}
	return nil
}

func CopyToConsumptionDir(consumptionDir, sourcePath string) (string, error) {
	if err := os.MkdirAll(consumptionDir, 0755); err != nil {
		return "", fmt.Errorf("ensure consumption dir: %w", err)
	}

	destPath := filepath.Join(consumptionDir, filepath.Base(sourcePath))

	src, err := os.Open(sourcePath)
	if err != nil {
		return "", fmt.Errorf("open source file: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create destination file: %w", err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(destPath)
		return "", fmt.Errorf("copy file: %w", err)
	}

	if err := dst.Close(); err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("close destination file: %w", err)
	}

	return destPath, nil
}

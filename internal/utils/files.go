package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

func ListFilePaths(src string, exts []string) ([]string, error) {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil, fmt.Errorf("directory `%s` does not exist", src)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, fmt.Errorf("error reading directory: %w", err)
	}

	var paths []string

	if len(entries) == 0 {
		return paths, nil
	}

	supportedFiles := make(map[string]bool)
	for _, ext := range exts {
		supportedFiles[strings.ToLower(ext)] = true
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		entryPath := filepath.Join(src, entry.Name())

		mtype, err := mimetype.DetectFile(entryPath)
		if err != nil {
			fmt.Printf("Warning: Failed to detect MIME type for %s: %v\n", entry.Name(), err)
			continue
		}

		fileExt := strings.ToLower(mtype.Extension())
		if !supportedFiles[fileExt] {
			continue
		}

		paths = append(paths, entryPath)
	}

	return paths, nil
}

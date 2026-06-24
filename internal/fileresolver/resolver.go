package fileresolver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

type File struct {
	OriginalPath string
}

func GetFiles(src string, exts []string) ([]File, error) {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil, fmt.Errorf("consumption directory `%s` does not exist", src)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, fmt.Errorf("error reading directory: %w", err)
	}

	var files []File

	if len(entries) == 0 {
		return files, nil
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

		files = append(files, File{
			OriginalPath: entryPath,
		})
	}

	return files, nil
}

func FilePaths(files []File) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.OriginalPath
	}
	return paths
}

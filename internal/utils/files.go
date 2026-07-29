package utils

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gabriel-vasile/mimetype"

	_mime "github.com/wgomg/edub-kushim/internal/mime"
)

func CalculateMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("calculate MD5: %w", err)
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("calculate MD5: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ListFilePaths(src string, exts []string, maxFiles int) ([]string, error) {
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

	supportedFiles := _mime.BuildExtensionSet(exts)

	type entryWithTime struct {
		path  string
		ctime time.Time
	}

	var timedEntries []entryWithTime

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		entryPath := filepath.Join(src, entry.Name())

		entryInfo, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("failed to get file information: %w", err)
		}

		stat, ok := entryInfo.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}

		ctime := time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)

		timedEntries = append(timedEntries, entryWithTime{
			path:  entryPath,
			ctime: ctime,
		})
	}

	sort.Slice(timedEntries, func(i, j int) bool {
		return timedEntries[i].ctime.Before(timedEntries[j].ctime)
	})

	for _, te := range timedEntries {
		mtype, err := mimetype.DetectFile(te.path)
		if err != nil {
			fmt.Printf("Warning: Failed to detect MIME type for %s: %v\n", filepath.Base(te.path), err)
			continue
		}

		fileExt := strings.ToLower(mtype.Extension())
		if !supportedFiles[fileExt] {
			continue
		}

		paths = append(paths, te.path)

		if maxFiles > 0 && len(paths) >= maxFiles {
			fmt.Printf("Warning: Reached limit of %d files, stopping scan\n", maxFiles)
			break
		}
	}

	return paths, nil
}

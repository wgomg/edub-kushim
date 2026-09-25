package storage

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gabriel-vasile/mimetype"

	_mime "github.com/wgomg/edub-kushim/internal/documents/mime"
)

// CalculateMD5 returns the hex MD5 of a file's contents, used for duplicate
// detection at intake.
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

// ListFilePaths lists the regular files in src whose detected content type
// matches the accepted extensions, oldest first by creation time, stopping
// after maxFiles (zero means no limit). A missing directory is an error.
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
		ok, err := isSupportedEntry(src, entry, supportedFiles)
		if err != nil {
			fmt.Printf("Warning: Failed to detect MIME type for %s: %v\n", entry.Name(), err)
			continue
		}
		if !ok {
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
		paths = append(paths, te.path)

		if maxFiles > 0 && len(paths) >= maxFiles {
			fmt.Printf("Warning: Reached limit of %d files, stopping scan\n", maxFiles)
			break
		}
	}

	return paths, nil
}

func isSupportedEntry(src string, entry fs.DirEntry, supportedFiles map[string]bool) (bool, error) {
	if !entry.Type().IsRegular() {
		return false, nil
	}
	mtype, err := mimetype.DetectFile(filepath.Join(src, entry.Name()))
	if err != nil {
		return false, err
	}
	return supportedFiles[strings.ToLower(mtype.Extension())], nil
}

// CountFilePaths counts the supported files in src without listing them;
// a missing directory counts as zero.
func CountFilePaths(src string, exts []string) (int, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("error reading directory: %w", err)
	}

	supportedFiles := _mime.BuildExtensionSet(exts)
	count := 0
	for _, entry := range entries {
		ok, err := isSupportedEntry(src, entry, supportedFiles)
		if err != nil {
			continue
		}
		if ok {
			count++
		}
	}
	return count, nil
}

// SameDevice reports whether two paths sit on the same filesystem device,
// so a rename can replace a copy when moving files into the layout. A
// missing target falls back to its parent directory.
func SameDevice(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		sb, err = os.Stat(filepath.Dir(b))
		if err != nil {
			return false, err
		}
	}
	da, ok := sa.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("unexpected stat type for %s", a)
	}
	db, ok := sb.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("unexpected stat type for %s", b)
	}
	return da.Dev == db.Dev, nil
}

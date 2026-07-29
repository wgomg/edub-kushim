package consumption

import (
	"crypto/md5"
	"crypto/sha512"
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

func GetFiles(src string, exts []string, maxFiles int) ([]File, error) {
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

	supportedFiles := _mime.BuildExtensionSet(exts)

	type entryWithInfo struct {
		path  string
		info  os.FileInfo
		ctime time.Time
	}

	var timedEntries []entryWithInfo

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

		timedEntries = append(timedEntries, entryWithInfo{
			path:  entryPath,
			info:  entryInfo,
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

		md5Hash, sha512Hash, err := calculateChecksums(te.path)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to calculate checksums for %s: %w",
				filepath.Base(te.path),
				err,
			)
		}

		files = append(
			files,
			File{
				Name:           filepath.Base(te.path),
				OriginalPath:   te.path,
				FileSize:       te.info.Size(),
				Date:           time.Now(),
				MD5Checksum:    md5Hash,
				SHA512Checksum: sha512Hash,
				MimeType:       mtype.String(),
			},
		)

		if maxFiles > 0 && len(files) >= maxFiles {
			fmt.Printf("Warning: Reached limit of %d files, stopping scan\n", maxFiles)
			break
		}
	}

	return files, nil
}

func FileFromPath(path string) (File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return File{}, fmt.Errorf("stat file: %w", err)
	}

	mtype, err := mimetype.DetectFile(path)
	if err != nil {
		return File{}, fmt.Errorf("detect mime type: %w", err)
	}

	md5Hash, sha512Hash, err := calculateChecksums(path)
	if err != nil {
		return File{}, fmt.Errorf("calculate checksums: %w", err)
	}

	return File{
		Name:           filepath.Base(path),
		OriginalPath:   path,
		FileSize:       info.Size(),
		Date:           time.Now(),
		MD5Checksum:    md5Hash,
		SHA512Checksum: sha512Hash,
		MimeType:       mtype.String(),
	}, nil
}

func FilePaths(files []File) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.OriginalPath
	}
	return paths
}

func RemoveFile(path string) error {
	if path == "" {
		return nil
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to remove file %s: %w", path, err)
	}

	return nil
}

func MoveFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("source file does not exist: %w", err)
	}

	if srcInfo.IsDir() {
		return fmt.Errorf("source is a directory, not a file: %s", src)
	}

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dstDir, err)
	}

	err = os.Rename(src, dst)
	if err == nil {
		return nil
	}

	return moveFileCrossDevice(src, dst, srcInfo)
}

func CopyFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("source file does not exist: %w", err)
	}

	if srcInfo.IsDir() {
		return fmt.Errorf("source is a directory, not a file: %s", src)
	}

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dstDir, err)
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		os.Remove(dst)
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	if err := dstFile.Sync(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("failed to sync destination file: %w", err)
	}

	return nil
}

func CleanUp(path string) error {
	if path == "" {
		return nil
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	if err := os.Remove(path); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}

func moveFileCrossDevice(src, dst string, srcInfo os.FileInfo) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		os.Remove(dst)
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	if err := dstFile.Sync(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("failed to sync destination file: %w", err)
	}

	srcFile.Close()
	dstFile.Close()

	if err := os.Remove(src); err != nil {
		// if we can't remove source, at least we have the copy
		// log the error but don't fail the operation
		fmt.Printf("Warning: Failed to remove source file after copy: %v\n", err)
	}

	return nil
}

func calculateChecksums(
	filePath string,
) (md5Hash string, sha512Hash string, err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	md5Hasher := md5.New()
	sha512Hasher := sha512.New()
	multiWriter := io.MultiWriter(md5Hasher, sha512Hasher)

	if _, err := io.Copy(multiWriter, file); err != nil {
		return "", "", fmt.Errorf("failed to calculate checksums: %w", err)
	}

	md5Hash = hex.EncodeToString(md5Hasher.Sum(nil))
	sha512Hash = hex.EncodeToString(sha512Hasher.Sum(nil))

	return md5Hash, sha512Hash, nil
}

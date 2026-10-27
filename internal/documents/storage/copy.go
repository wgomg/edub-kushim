package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

// copyFile copies a single file, aborting with ctx's error when the context ends: a watchdog closes the
// source so a read blocked on a stalled filesystem returns immediately, and an incomplete destination
// is removed so a retry starts clean.
func copyFile(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}

	watchdogDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = in.Close()
		case <-watchdogDone:
		}
	}()

	_, copyErr := io.Copy(out, in)
	close(watchdogDone)
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		copyErr = ctxErr
	}
	if copyErr != nil {
		// a partial destination must not be mistaken for a complete file
		_ = os.Remove(dst)
		return copyErr
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// CopyTree copies a file or a whole directory tree to dst, preserving modes and modification times,
// and aborts with ctx's error when the context ends; it is the fallback where a cross-device move cannot
// replace a copy.
func CopyTree(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(ctx, src, dst)
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := CopyTree(ctx, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

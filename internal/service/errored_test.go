package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/storage"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func newTestErroredFiles(t *testing.T) (*ErroredFiles, *config.Config) {
	t.Helper()
	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	cfg.Storage.StorageDir = filepath.Join(configDir, "storage")
	os.MkdirAll(cfg.Storage.StorageDir, 0755)

	logger := utils.NewDiscardLogger()
	svc := NewErroredFiles(cfg, logger)
	return svc, cfg
}

func createErroredFile(t *testing.T, dir, name string) {
	t.Helper()
	os.MkdirAll(dir, 0755)
	testutil.CreateTestPDF(t, filepath.Join(dir, name), "error content")
}

func TestNewErroredFiles(t *testing.T) {
	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	logger := testutil.NewTestLogger()

	svc := NewErroredFiles(cfg, logger)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestErroredFiles_List_Empty(t *testing.T) {
	svc, _ := newTestErroredFiles(t)
	ctx := context.Background()

	files, err := svc.List(ctx)
	testutil.AssertNoError(t, err, "list empty")
	testutil.AssertEqual(t, len(files), 0, "empty list")
}

func TestErroredFiles_List_WithFiles(t *testing.T) {
	svc, cfg := newTestErroredFiles(t)
	ctx := context.Background()

	errorsDir := filepath.Join(cfg.Storage.StorageDir, "errors")
	dupesDir := filepath.Join(errorsDir, "duplicated")
	createErroredFile(t, errorsDir, "err1.pdf")
	createErroredFile(t, dupesDir, "dup1.pdf")

	files, err := svc.List(ctx)
	testutil.AssertNoError(t, err, "list")
	testutil.AssertEqual(t, len(files), 2, "two files from both dirs")

	names := map[string]bool{}
	subdirs := map[string]bool{}
	for _, f := range files {
		names[f.Name] = true
		subdirs[f.Subdir] = true
	}
	testutil.AssertEqual(t, names["err1.pdf"], true, "found err1.pdf")
	testutil.AssertEqual(t, names["dup1.pdf"], true, "found dup1.pdf")
	testutil.AssertEqual(t, subdirs[storage.DirErrors], true, "has errors subdir")
	testutil.AssertEqual(t, subdirs[storage.DirErrorsDuplicates], true, "has duplicated subdir")
}

func TestErroredFiles_List_SkipsDirectories(t *testing.T) {
	svc, cfg := newTestErroredFiles(t)
	ctx := context.Background()

	errorsDir := filepath.Join(cfg.Storage.StorageDir, "errors")
	os.MkdirAll(filepath.Join(errorsDir, "subdir"), 0755)
	createErroredFile(t, errorsDir, "real.pdf")

	files, err := svc.List(ctx)
	testutil.AssertNoError(t, err, "list")
	testutil.AssertEqual(t, len(files), 1, "skips subdirs")
	testutil.AssertEqual(t, files[0].Name, "real.pdf", "correct file")
}

func TestErroredFiles_GetPath_Valid(t *testing.T) {
	svc, cfg := newTestErroredFiles(t)

	errorsDir := filepath.Join(cfg.Storage.StorageDir, "errors")
	dupesDir := filepath.Join(errorsDir, "duplicated")
	createErroredFile(t, errorsDir, "err.pdf")
	createErroredFile(t, dupesDir, "dup.pdf")

	path, err := svc.GetPath(storage.DirErrors, "err.pdf")
	testutil.AssertNoError(t, err, "get errors path")
	testutil.AssertEqual(t, filepath.Base(path), "err.pdf", "filename in path")
	testutil.AssertEqual(t, filepath.Dir(path), errorsDir, "dir is errors")

	path, err = svc.GetPath(storage.DirErrorsDuplicates, "dup.pdf")
	testutil.AssertNoError(t, err, "get duplicated path")
	testutil.AssertEqual(t, filepath.Base(path), "dup.pdf", "filename in dupes path")
	testutil.AssertEqual(t, filepath.Dir(path), dupesDir, "dir is errors/duplicated")
}

func TestErroredFiles_GetPath_TraversalBlocked(t *testing.T) {
	svc, _ := newTestErroredFiles(t)

	_, err := svc.GetPath(storage.DirErrors, "../etc/passwd")
	testutil.AssertError(t, err, "reject traversal with ..")

	_, err = svc.GetPath(storage.DirErrors, "sub/../../etc/passwd")
	testutil.AssertError(t, err, "reject nested traversal")
}

func TestErroredFiles_GetPath_InvalidSubdir(t *testing.T) {
	svc, _ := newTestErroredFiles(t)

	_, err := svc.GetPath("invalid", "file.pdf")
	testutil.AssertError(t, err, "reject invalid subdir")
}

func TestErroredFiles_Delete(t *testing.T) {
	svc, cfg := newTestErroredFiles(t)
	ctx := context.Background()

	errorsDir := filepath.Join(cfg.Storage.StorageDir, "errors")
	createErroredFile(t, errorsDir, "to-delete.pdf")

	err := svc.Delete(storage.DirErrors, "to-delete.pdf")
	testutil.AssertNoError(t, err, "delete")

	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 0, "file deleted")
}

func TestErroredFiles_Delete_NotFound(t *testing.T) {
	svc, _ := newTestErroredFiles(t)

	err := svc.Delete(storage.DirErrors, "nonexistent.pdf")
	testutil.AssertError(t, err, "delete nonexistent")
}

func TestErroredFiles_DeleteAll(t *testing.T) {
	svc, cfg := newTestErroredFiles(t)
	ctx := context.Background()

	errorsDir := filepath.Join(cfg.Storage.StorageDir, "errors")
	dupesDir := filepath.Join(errorsDir, "duplicated")
	createErroredFile(t, errorsDir, "err1.pdf")
	createErroredFile(t, errorsDir, "err2.pdf")
	createErroredFile(t, dupesDir, "dup1.pdf")

	deleted, err := svc.DeleteAll(ctx)
	testutil.AssertNoError(t, err, "delete all")
	testutil.AssertEqual(t, deleted, 3, "deleted count")

	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 0, "all gone")
}

func TestErroredFiles_DeleteAll_Empty(t *testing.T) {
	svc, _ := newTestErroredFiles(t)
	ctx := context.Background()

	deleted, err := svc.DeleteAll(ctx)
	testutil.AssertNoError(t, err, "delete all empty")
	testutil.AssertEqual(t, deleted, 0, "nothing to delete")
}

package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/testutil"
)

func TestDetectFileType(t *testing.T) {
	tests := []struct {
		name     string
		stem     string
		expected string
	}{
		{"uuid lowercase", "550e8400-e29b-41d4-a716-446655440000", "uuid"},
		{"uuid uppercase", "550E8400-E29B-41D4-A716-446655440000", "uuid"},
		{"uuid mixed case", "550e8400-E29b-41D4-a716-446655440000", "uuid"},
		{"dbid simple", "42", "dbid"},
		{"dbid large", "999999", "dbid"},
		{"dbid zero", "0", "dbid"},
		{"random string", "invoice", ""},
		{"empty", "", ""},
		{"hyphenated non-uuid", "abc-def-ghi", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectFileType(tt.stem)
			testutil.AssertEqual(t, got, tt.expected, "key type")
		})
	}
}

func TestWalkStorageDir_FindsPDFs(t *testing.T) {
	storageDir := t.TempDir()
	origDir := filepath.Join(storageDir, "originals")
	procDir := filepath.Join(storageDir, "processed")
	testutil.AssertNoError(t, os.MkdirAll(origDir, 0755), "mkdir originals")
	testutil.AssertNoError(t, os.MkdirAll(procDir, 0755), "mkdir processed")

	uuid1 := "550e8400-e29b-41d4-a716-446655440000"
	uuid2 := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	createOldPDF(t, filepath.Join(origDir, uuid1+".pdf"))
	createOldPDF(t, filepath.Join(procDir, uuid2+".pdf"))

	infos, errsCh := WalkStorageDir(storageDir)
	var results []OrphanedFileInfo
	for info := range infos {
		results = append(results, info)
	}
	err, ok := <-errsCh
	if ok && err != nil {
		t.Fatalf("walk error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 orphaned files, got %d", len(results))
	}

	byKey := map[string]OrphanedFileInfo{}
	for _, r := range results {
		byKey[r.DocumentKey] = r
	}

	r1 := byKey[uuid1]
	testutil.AssertEqual(t, r1.DocumentKeyType, "uuid", "key type 1")
	testutil.AssertEqual(t, r1.SourceDir, "originals", "source dir 1")
	testutil.AssertEqual(t, r1.MimeType, "application/pdf", "mime type 1")

	r2 := byKey[uuid2]
	testutil.AssertEqual(t, r2.DocumentKeyType, "uuid", "key type 2")
	testutil.AssertEqual(t, r2.SourceDir, "processed", "source dir 2")
}

func TestWalkStorageDir_SkipsNonPDFAndRecent(t *testing.T) {
	storageDir := t.TempDir()
	origDir := filepath.Join(storageDir, "originals")
	testutil.AssertNoError(t, os.MkdirAll(origDir, 0755), "mkdir")

	uuid := "550e8400-e29b-41d4-a716-446655440000"

	testutil.CreateTestFile(t, filepath.Join(origDir, "notes.txt"), "not a pdf")
	createOldPDF(t, filepath.Join(origDir, uuid+".pdf"))

	recentPath := filepath.Join(origDir, "6ba7b810-9dad-11d1-80b4-00c04fd430c8.pdf")
	testutil.CreateTestPDF(t, recentPath, "recent")
	// recentPath was just written, mtime is now — should be skipped

	infos, errsCh := WalkStorageDir(storageDir)
	var count int
	for range infos {
		count++
	}
	err, ok := <-errsCh
	if ok && err != nil {
		t.Fatalf("walk error: %v", err)
	}

	testutil.AssertEqual(t, count, 1, "should skip non-PDF and recent file")
}

func TestWalkStorageDir_SkipsUnknownKeyTypes(t *testing.T) {
	storageDir := t.TempDir()
	origDir := filepath.Join(storageDir, "originals")
	testutil.AssertNoError(t, os.MkdirAll(origDir, 0755), "mkdir")

	createOldPDF(t, filepath.Join(origDir, "invoice-123.pdf"))
	createOldPDF(t, filepath.Join(origDir, "550e8400-e29b-41d4-a716-446655440000.pdf"))

	infos, errsCh := WalkStorageDir(storageDir)
	var count int
	for range infos {
		count++
	}
	err, ok := <-errsCh
	if ok && err != nil {
		t.Fatalf("walk error: %v", err)
	}

	testutil.AssertEqual(t, count, 1, "should skip non-uuid/non-dbid filenames")
}

func TestWalkStorageDir_MissingDirs(t *testing.T) {
	storageDir := t.TempDir()

	infos, errsCh := WalkStorageDir(storageDir)
	var count int
	for range infos {
		count++
	}
	err, ok := <-errsCh
	if ok && err != nil {
		t.Fatalf("walk error: %v", err)
	}
	testutil.AssertEqual(t, count, 0, "no files in empty dirs")
}

func TestQuarantineFile(t *testing.T) {
	storageDir := t.TempDir()
	origDir := filepath.Join(storageDir, "originals")
	testutil.AssertNoError(t, os.MkdirAll(origDir, 0755), "mkdir")

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	srcPath := filepath.Join(origDir, uuid+".pdf")
	createOldPDF(t, srcPath)

	info := OrphanedFileInfo{
		DocumentKey:     uuid,
		DocumentKeyType: "uuid",
		FilePath:        srcPath,
		OriginalPath:    "originals/" + uuid + ".pdf",
		SourceDir:       "originals",
		FileSize:        100,
		MimeType:        "application/pdf",
	}

	newPath, err := QuarantineFile(storageDir, info)
	testutil.AssertNoError(t, err, "quarantine")

	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Fatalf("quarantined file should exist at %s", newPath)
	}
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Fatalf("original file should have been moved")
	}

	expectedDir := filepath.Join(storageDir, "orphaned", "originals")
	testutil.AssertEqual(t, filepath.Dir(newPath), expectedDir, "quarantine dir")
}

func TestRemoveOrphanedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pdf")
	testutil.CreateTestPDF(t, path, "test")

	testutil.AssertNoError(t, RemoveOrphanedFile(path), "remove existing")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should be removed")
	}

	testutil.AssertNoError(t, RemoveOrphanedFile(path), "remove missing should not error")
}

func TestCopyToConsumptionDir(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "550e8400-e29b-41d4-a716-446655440000.pdf")
	createOldPDF(t, srcPath)

	destDir := filepath.Join(t.TempDir(), "inbox")
	destPath, err := CopyToConsumptionDir(destDir, srcPath)
	testutil.AssertNoError(t, err, "copy")

	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Fatalf("copied file should exist at %s", destPath)
	}

	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		t.Fatal("source file should still exist after copy")
	}
}

func TestCopyToConsumptionDir_MissingSource(t *testing.T) {
	destDir := t.TempDir()
	_, err := CopyToConsumptionDir(destDir, "/nonexistent/file.pdf")
	testutil.AssertError(t, err, "copy missing source")
}

func createOldPDF(t *testing.T, path string) {
	t.Helper()
	testutil.CreateTestPDF(t, path, "test")
	// backdate mtime so it's not skipped by the 30s filter
	past := time.Now().Add(-1 * time.Minute)
	os.Chtimes(path, past, past)
}

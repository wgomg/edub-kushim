package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/testutil"
)

func TestTrashDir(t *testing.T) {
	testutil.AssertEqual(t, TrashDir("/data"), "/data/trash", "basic path")
	testutil.AssertEqual(t, TrashDir("/data/"), "/data/trash", "trailing slash")
}

func TestDocumentTrashDir(t *testing.T) {
	got := DocumentTrashDir("/data", "abc-123")
	testutil.AssertEqual(t, got, "/data/trash/abc-123", "path")
}

func TestMoveToTrash_HappyPath(t *testing.T) {
	storageDir := t.TempDir()
	origDir := filepath.Join(storageDir, "originals")
	procDir := filepath.Join(storageDir, "processed")
	os.MkdirAll(origDir, 0755)
	os.MkdirAll(procDir, 0755)

	origPath := filepath.Join(origDir, "doc1.pdf")
	storagePath := filepath.Join(procDir, "doc1.pdf")
	testutil.CreateTestPDF(t, origPath, "original")
	testutil.CreateTestPDF(t, storagePath, "processed")

	newOrig, newStore, err := MoveToTrash(storageDir, "doc-uuid", origPath, storagePath)
	testutil.AssertNoError(t, err, "move to trash")

	if _, err := os.Stat(newOrig); os.IsNotExist(err) {
		t.Fatalf("trashed original should exist at %s", newOrig)
	}
	if _, err := os.Stat(newStore); os.IsNotExist(err) {
		t.Fatalf("trashed storage should exist at %s", newStore)
	}
	if _, err := os.Stat(origPath); !os.IsNotExist(err) {
		t.Fatal("original should no longer exist")
	}
	if _, err := os.Stat(storagePath); !os.IsNotExist(err) {
		t.Fatal("storage should no longer exist")
	}

	expectedOrig := filepath.Join(storageDir, "trash", "doc-uuid", "originals", "doc1.pdf")
	testutil.AssertEqual(t, newOrig, expectedOrig, "trash original path")
	expectedStore := filepath.Join(storageDir, "trash", "doc-uuid", "processed", "doc1.pdf")
	testutil.AssertEqual(t, newStore, expectedStore, "trash storage path")
}

func TestMoveToTrash_MissingOriginal(t *testing.T) {
	storageDir := t.TempDir()
	procDir := filepath.Join(storageDir, "processed")
	os.MkdirAll(procDir, 0755)

	storagePath := filepath.Join(procDir, "doc1.pdf")
	testutil.CreateTestPDF(t, storagePath, "processed")

	newOrig, newStore, err := MoveToTrash(storageDir, "doc-uuid", "/nonexistent/original.pdf", storagePath)
	testutil.AssertNoError(t, err, "missing original should be tolerated")

	testutil.AssertEqual(t, newOrig, filepath.Join(storageDir, "trash", "doc-uuid", "originals", "original.pdf"), "returned orig path")
	testutil.AssertEqual(t, newStore, filepath.Join(storageDir, "trash", "doc-uuid", "processed", "doc1.pdf"), "returned store path")

	if _, err := os.Stat(newStore); os.IsNotExist(err) {
		t.Fatal("storage file should be moved")
	}
}

func TestMoveToTrash_MissingStorage(t *testing.T) {
	storageDir := t.TempDir()
	origDir := filepath.Join(storageDir, "originals")
	os.MkdirAll(origDir, 0755)

	origPath := filepath.Join(origDir, "doc1.pdf")
	testutil.CreateTestPDF(t, origPath, "original")

	newOrig, _, err := MoveToTrash(storageDir, "doc-uuid", origPath, "/nonexistent/storage.pdf")
	testutil.AssertNoError(t, err, "missing storage should be tolerated")

	if _, err := os.Stat(newOrig); os.IsNotExist(err) {
		t.Fatal("original file should be moved")
	}
}

func TestRestoreFromTrash_HappyPath(t *testing.T) {
	storageDir := t.TempDir()
	origDir := filepath.Join(storageDir, "originals")
	procDir := filepath.Join(storageDir, "processed")
	os.MkdirAll(origDir, 0755)
	os.MkdirAll(procDir, 0755)

	trashOrig := filepath.Join(storageDir, "trash", "doc-uuid", "originals", "doc1.pdf")
	trashStore := filepath.Join(storageDir, "trash", "doc-uuid", "processed", "doc1.pdf")
	os.MkdirAll(filepath.Dir(trashOrig), 0755)
	os.MkdirAll(filepath.Dir(trashStore), 0755)
	testutil.CreateTestPDF(t, trashOrig, "trashed original")
	testutil.CreateTestPDF(t, trashStore, "trashed processed")

	newOrig, newStore, err := RestoreFromTrash(storageDir, "doc-uuid", trashOrig, trashStore)
	testutil.AssertNoError(t, err, "restore")

	if _, err := os.Stat(newOrig); os.IsNotExist(err) {
		t.Fatalf("restored original should exist at %s", newOrig)
	}
	if _, err := os.Stat(newStore); os.IsNotExist(err) {
		t.Fatalf("restored storage should exist at %s", newStore)
	}
	if _, err := os.Stat(trashOrig); !os.IsNotExist(err) {
		t.Fatal("trashed original should no longer exist")
	}
	if _, err := os.Stat(trashStore); !os.IsNotExist(err) {
		t.Fatal("trashed storage should no longer exist")
	}

	expectedOrig := filepath.Join(origDir, "doc1.pdf")
	testutil.AssertEqual(t, newOrig, expectedOrig, "restored original path")
	expectedStore := filepath.Join(procDir, "doc1.pdf")
	testutil.AssertEqual(t, newStore, expectedStore, "restored storage path")
}

func TestRestoreFromTrash_MissingTrashOriginal(t *testing.T) {
	storageDir := t.TempDir()
	procDir := filepath.Join(storageDir, "processed")
	os.MkdirAll(procDir, 0755)

	trashStore := filepath.Join(storageDir, "trash", "doc-uuid", "processed", "doc1.pdf")
	os.MkdirAll(filepath.Dir(trashStore), 0755)
	testutil.CreateTestPDF(t, trashStore, "trashed processed")

	newOrig, newStore, err := RestoreFromTrash(storageDir, "doc-uuid", "/nonexistent/trash/original.pdf", trashStore)
	testutil.AssertNoError(t, err, "missing trash original should be tolerated")

	testutil.AssertEqual(t, newOrig, filepath.Join(storageDir, "originals", "original.pdf"), "restored orig path")

	if _, err := os.Stat(newStore); os.IsNotExist(err) {
		t.Fatal("storage file should be restored")
	}
}

func TestRestoreFromTrash_MissingBoth(t *testing.T) {
	storageDir := t.TempDir()

	newOrig, newStore, err := RestoreFromTrash(storageDir, "doc-uuid", "/nonexistent/o.pdf", "/nonexistent/s.pdf")
	testutil.AssertNoError(t, err, "both missing should be tolerated")

	testutil.AssertEqual(t, newOrig, filepath.Join(storageDir, "originals", "o.pdf"), "returned orig path")
	testutil.AssertEqual(t, newStore, filepath.Join(storageDir, "processed", "s.pdf"), "returned store path")
}

func TestRemoveFromTrash(t *testing.T) {
	storageDir := t.TempDir()
	docTrash := filepath.Join(storageDir, "trash", "doc-uuid")
	os.MkdirAll(filepath.Join(docTrash, "originals"), 0755)
	os.MkdirAll(filepath.Join(docTrash, "processed"), 0755)
	testutil.CreateTestPDF(t, filepath.Join(docTrash, "originals", "doc1.pdf"), "test")
	testutil.CreateTestPDF(t, filepath.Join(docTrash, "processed", "doc1.pdf"), "test")

	testutil.AssertNoError(t, RemoveFromTrash(storageDir, "doc-uuid"), "remove")

	if _, err := os.Stat(docTrash); !os.IsNotExist(err) {
		t.Fatal("trash dir should be removed")
	}
}

func TestRemoveFromTrash_Nonexistent(t *testing.T) {
	storageDir := t.TempDir()
	testutil.AssertNoError(t, RemoveFromTrash(storageDir, "nonexistent"), "remove nonexistent should not error")
}

func TestMoveToTrash_RoundTrip(t *testing.T) {
	storageDir := t.TempDir()
	origDir := filepath.Join(storageDir, "originals")
	procDir := filepath.Join(storageDir, "processed")
	os.MkdirAll(origDir, 0755)
	os.MkdirAll(procDir, 0755)

	origPath := filepath.Join(origDir, "roundtrip.pdf")
	storagePath := filepath.Join(procDir, "roundtrip.pdf")
	testutil.CreateTestPDF(t, origPath, "roundtrip")
	testutil.CreateTestPDF(t, storagePath, "roundtrip")

	newOrig, newStore, err := MoveToTrash(storageDir, "doc-rr", origPath, storagePath)
	testutil.AssertNoError(t, err, "move to trash")

	restOrig, restStore, err := RestoreFromTrash(storageDir, "doc-rr", newOrig, newStore)
	testutil.AssertNoError(t, err, "restore")

	if _, err := os.Stat(restOrig); os.IsNotExist(err) {
		t.Fatal("restored original should exist")
	}
	if _, err := os.Stat(restStore); os.IsNotExist(err) {
		t.Fatal("restored storage should exist")
	}

	expectedOrig := filepath.Join(origDir, "roundtrip.pdf")
	testutil.AssertEqual(t, restOrig, expectedOrig, "restored original path")
	expectedStore := filepath.Join(procDir, "roundtrip.pdf")
	testutil.AssertEqual(t, restStore, expectedStore, "restored storage path")
}

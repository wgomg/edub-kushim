package utils

import (
	"os"
	"path/filepath"
	"testing"
)

// pdfBytes is a minimal header mimetype recognises as application/pdf.
var pdfBytes = []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

func writePDF(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pdfBytes, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestCountFilePaths_SupportedOnly(t *testing.T) {
	dir := t.TempDir()
	writePDF(t, dir, "a.pdf")
	// Unsupported extension — .txt is not in the supported set.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := CountFilePaths(dir, []string{".pdf"})
	if err != nil {
		t.Fatalf("count inbox: %v", err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1 (only the .pdf)", n)
	}
}

func TestCountFilePaths_MissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")

	n, err := CountFilePaths(missing, []string{".pdf"})
	if err != nil {
		t.Fatalf("missing directory must not error, got %v", err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

func TestCountFilePaths_SkipsNonRegular(t *testing.T) {
	dir := t.TempDir()
	pdf := writePDF(t, dir, "a.pdf")

	// Symlink to the PDF: without the IsRegular guard DetectFile follows it
	// and counts the target, double-counting. With the guard, the symlink
	// is skipped and only the real file is counted.
	if err := os.Symlink(pdf, filepath.Join(dir, "link.pdf")); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	n, err := CountFilePaths(dir, []string{".pdf"})
	if err != nil {
		t.Fatalf("count inbox: %v", err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1 (symlink must be skipped)", n)
	}
}

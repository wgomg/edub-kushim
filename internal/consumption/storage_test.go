package consumption

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetFiles_NonExistentDir(t *testing.T) {
	_, err := GetFiles("/nonexistent/path", []string{".pdf"})
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestGetFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	files, err := GetFiles(dir, []string{".pdf"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestGetFiles_FiltersByExtension(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte("%PDF-1.4"), 0644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(dir, "image.png"), []byte("\x89PNG"), 0644)

	files, err := GetFiles(dir, []string{".pdf"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Name != "doc.pdf" {
		t.Errorf("Name = %q, want doc.pdf", files[0].Name)
	}
}

func TestGetFiles_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)
	os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte("%PDF-1.4"), 0644)

	files, err := GetFiles(dir, []string{".pdf"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d", len(files))
	}
}

func TestGetFiles_PopulatesFileFields(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte("%PDF-1.4 test content"), 0644)

	files, err := GetFiles(dir, []string{".pdf"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f := files[0]
	if f.Name != "doc.pdf" {
		t.Errorf("Name = %q", f.Name)
	}
	if f.OriginalPath != filepath.Join(dir, "doc.pdf") {
		t.Errorf("OriginalPath = %q", f.OriginalPath)
	}
	if f.FileSize != 21 {
		t.Errorf("FileSize = %d, want 21", f.FileSize)
	}
	if f.MD5Checksum == "" {
		t.Error("MD5Checksum is empty")
	}
	if f.SHA512Checksum == "" {
		t.Error("SHA512Checksum is empty")
	}
	if f.MimeType != "application/pdf" {
		t.Errorf("MimeType = %q, want application/pdf", f.MimeType)
	}
}

func TestFileFromPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.pdf")
	os.WriteFile(path, []byte("%PDF-1.4"), 0644)

	f, err := FileFromPath(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Name != "doc.pdf" {
		t.Errorf("Name = %q", f.Name)
	}
	if f.OriginalPath != path {
		t.Errorf("OriginalPath = %q", f.OriginalPath)
	}
}

func TestFileFromPath_NonExistent(t *testing.T) {
	_, err := FileFromPath("/nonexistent/file.pdf")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestFilePaths(t *testing.T) {
	files := []File{
		{OriginalPath: "/tmp/a.pdf"},
		{OriginalPath: "/tmp/b.pdf"},
	}
	paths := FilePaths(files)
	if len(paths) != 2 {
		t.Fatalf("len = %d, want 2", len(paths))
	}
	if paths[0] != "/tmp/a.pdf" || paths[1] != "/tmp/b.pdf" {
		t.Errorf("paths = %v", paths)
	}
}

func TestRemoveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "to-remove")
	os.WriteFile(path, []byte("x"), 0644)

	if err := RemoveFile(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file still exists after RemoveFile")
	}
}

func TestRemoveFile_EmptyPath(t *testing.T) {
	if err := RemoveFile(""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveFile_NonExistent(t *testing.T) {
	if err := RemoveFile("/nonexistent/file"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMoveFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "sub", "dst")
	os.WriteFile(src, []byte("content"), 0644)

	if err := MoveFile(src, dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source still exists after move")
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "content" {
		t.Errorf("dst content = %q, want content", string(data))
	}
}

func TestMoveFile_SourceNotExist(t *testing.T) {
	err := MoveFile("/nonexistent/src", "/tmp/dst")
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
}

func TestMoveFile_DestinationExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	os.WriteFile(src, []byte("a"), 0644)
	os.WriteFile(dst, []byte("b"), 0644)

	err := MoveFile(src, dst)
	if err == nil {
		t.Fatal("expected error for existing destination")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "sub", "dst")
	os.WriteFile(src, []byte("content"), 0644)

	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Error("source should still exist after copy")
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "content" {
		t.Errorf("dst content = %q, want content", string(data))
	}
}

func TestCopyFile_SourceNotExist(t *testing.T) {
	err := CopyFile("/nonexistent/src", "/tmp/dst")
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
}

func TestCopyFile_DestinationExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	os.WriteFile(src, []byte("a"), 0644)
	os.WriteFile(dst, []byte("b"), 0644)

	err := CopyFile(src, dst)
	if err == nil {
		t.Fatal("expected error for existing destination")
	}
}

func TestCleanUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "to-clean")
	os.WriteFile(path, []byte("x"), 0644)

	if err := CleanUp(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file still exists after CleanUp")
	}
}

func TestCleanUp_EmptyPath(t *testing.T) {
	if err := CleanUp(""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCleanUp_NonExistent(t *testing.T) {
	if err := CleanUp("/nonexistent/file"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCalculateChecksums(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	os.WriteFile(path, []byte("hello"), 0644)

	md5, sha512, err := calculateChecksums(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md5 == "" {
		t.Error("md5 is empty")
	}
	if sha512 == "" {
		t.Error("sha512 is empty")
	}
	if md5 == sha512 {
		t.Error("md5 and sha512 should differ")
	}
}

func TestCalculateChecksums_NonExistent(t *testing.T) {
	_, _, err := calculateChecksums("/nonexistent/file")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

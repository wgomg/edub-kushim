package textextractor

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/testutil"
)

func TestDocx_CanHandle(t *testing.T) {
	d := &Docx{}
	tests := []struct {
		mime string
		want bool
	}{
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", true},
		{"application/pdf", false},
		{"application/vnd.oasis.opendocument.text", false},
		{"image/png", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := d.CanHandle(tt.mime); got != tt.want {
			t.Errorf("CanHandle(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}

func TestDocx_Name(t *testing.T) {
	d := &Docx{}
	if d.Name() != "docx" {
		t.Errorf("Name() = %q, want %q", d.Name(), "docx")
	}
}

func TestDocx_Extract_SingleParagraph(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.docx")
	testutil.CreateMinimalDocx(t, path, "Hello, world!")
	ctx := context.Background()

	result, err := (&Docx{}).Extract(ctx, path, "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if strings.TrimSpace(*result) != "Hello, world!" {
		t.Fatalf("got %q, want %q", strings.TrimSpace(*result), "Hello, world!")
	}
}

func TestDocx_Extract_MultiParagraph(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	zw.Create("[Content_Types].xml")
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>First paragraph</w:t></w:r></w:p>
    <w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p>
    <w:p><w:r><w:t>Third paragraph</w:t></w:r></w:p>
  </w:body>
</w:document>`))
	zw.Close()
	f.Close()

	ctx := context.Background()
	result, err := (&Docx{}).Extract(ctx, path, "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	lines := strings.Split(strings.TrimSpace(*result), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), *result)
	}
	if lines[0] != "First paragraph" {
		t.Errorf("line 0 = %q, want %q", lines[0], "First paragraph")
	}
	if lines[1] != "Second paragraph" {
		t.Errorf("line 1 = %q, want %q", lines[1], "Second paragraph")
	}
	if lines[2] != "Third paragraph" {
		t.Errorf("line 2 = %q, want %q", lines[2], "Third paragraph")
	}
}

func TestDocx_Extract_EmptyDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.docx")
	testutil.CreateMinimalDocx(t, path, "")
	ctx := context.Background()

	result, err := (&Docx{}).Extract(ctx, path, "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if strings.TrimSpace(*result) != "" {
		t.Fatalf("expected empty result, got %q", *result)
	}
}

func TestDocx_Extract_InvalidZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.docx")
	os.WriteFile(path, []byte("not a zip"), 0644)
	ctx := context.Background()

	_, err := (&Docx{}).Extract(ctx, path, "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	if err == nil {
		t.Fatal("expected error for invalid zip")
	}
}

func TestDocx_Extract_MissingDocumentXML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.docx")
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	zw.Create("some-other-file.xml")
	zw.Close()
	f.Close()

	ctx := context.Background()
	_, err := (&Docx{}).Extract(ctx, path, "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	if err == nil {
		t.Fatal("expected error for missing word/document.xml")
	}
}

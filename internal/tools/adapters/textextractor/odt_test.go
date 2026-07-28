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

func TestOdt_CanHandle(t *testing.T) {
	o := &Odt{}
	tests := []struct {
		mime string
		want bool
	}{
		{"application/vnd.oasis.opendocument.text", true},
		{"application/pdf", false},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", false},
		{"image/png", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := o.CanHandle(tt.mime); got != tt.want {
			t.Errorf("CanHandle(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}

func TestOdt_Name(t *testing.T) {
	o := &Odt{}
	if o.Name() != "odt" {
		t.Errorf("Name() = %q, want %q", o.Name(), "odt")
	}
}

func TestOdt_Extract_SingleParagraph(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.odt")
	testutil.CreateMinimalOdt(t, path, "Hello, world!")
	ctx := context.Background()

	result, err := (&Odt{}).Extract(ctx, path, "application/vnd.oasis.opendocument.text")
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

func TestOdt_Extract_MultiParagraph(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.odt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	zw.Create("mimetype")
	w, _ := zw.Create("content.xml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
  xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">
  <office:body>
    <office:text>
      <text:p>First paragraph</text:p>
      <text:p>Second paragraph</text:p>
      <text:p>Third paragraph</text:p>
    </office:text>
  </office:body>
</office:document-content>`))
	zw.Close()
	f.Close()

	ctx := context.Background()
	result, err := (&Odt{}).Extract(ctx, path, "application/vnd.oasis.opendocument.text")
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

func TestOdt_Extract_EmptyDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.odt")
	testutil.CreateMinimalOdt(t, path, "")
	ctx := context.Background()

	result, err := (&Odt{}).Extract(ctx, path, "application/vnd.oasis.opendocument.text")
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

func TestOdt_Extract_InvalidZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.odt")
	os.WriteFile(path, []byte("not a zip"), 0644)
	ctx := context.Background()

	_, err := (&Odt{}).Extract(ctx, path, "application/vnd.oasis.opendocument.text")
	if err == nil {
		t.Fatal("expected error for invalid zip")
	}
}

func TestOdt_Extract_MissingContentXML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.odt")
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	zw.Create("some-other-file.xml")
	zw.Close()
	f.Close()

	ctx := context.Background()
	_, err := (&Odt{}).Extract(ctx, path, "application/vnd.oasis.opendocument.text")
	if err == nil {
		t.Fatal("expected error for missing content.xml")
	}
}

package textextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestMuPDF_Extract(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.pdf")
	os.WriteFile(src, []byte(minimalPDF), 0644)

	ext, err := NewMuPDF(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 30})
	if err != nil {
		t.Fatalf("NewMuPDF: %v", err)
	}

	text, err := ext.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if text == nil || *text == "" {
		t.Fatal("expected non-empty text")
	}
}

func TestMuPDF_CanHandle(t *testing.T) {
	ext, _ := NewMuPDF(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 30})
	if !ext.CanHandle("application/pdf") {
		t.Error("CanHandle(application/pdf) = false")
	}
	if ext.CanHandle("image/png") {
		t.Error("CanHandle(image/png) = true")
	}
}

func TestMuPDF_Name(t *testing.T) {
	ext, _ := NewMuPDF(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 30})
	if ext.Name() != "mupdf" {
		t.Errorf("Name() = %q, want mupdf", ext.Name())
	}
}

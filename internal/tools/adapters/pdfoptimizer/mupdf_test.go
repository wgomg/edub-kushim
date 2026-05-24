package pdfoptimizer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestMuPDF_Optimize(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.pdf")
	os.WriteFile(src, []byte(minimalPDF), 0644)

	opt, err := NewMuPDF(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 30})
	if err != nil {
		t.Fatalf("NewMuPDF: %v", err)
	}

	out, err := opt.Optimize(context.Background(), src)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if out == nil || *out == "" {
		t.Fatal("expected non-empty output path")
	}
	defer os.Remove(*out)

	if _, err := os.Stat(*out); os.IsNotExist(err) {
		t.Fatal("output file does not exist")
	}
}

func TestMuPDF_Name(t *testing.T) {
	opt, _ := NewMuPDF(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 30})
	if opt.Name() != "mupdf" {
		t.Errorf("Name() = %q, want mupdf", opt.Name())
	}
}

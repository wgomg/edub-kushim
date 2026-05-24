package ocr

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestGosseract_Process(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(src, imageBasedPDF(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tessdataDir := filepath.Join(dir, "tessdata")
	os.MkdirAll(tessdataDir, 0755)

	ocr, err := NewGosseract(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 60}, "mupdf", []string{"eng"}, tessdataDir)
	if err != nil {
		t.Fatalf("NewGosseract: %v", err)
	}

	out, err := ocr.Process(context.Background(), src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if out == nil || *out == "" {
		t.Fatal("expected non-empty output path")
	}
	defer os.Remove(*out)

	if _, err := os.Stat(*out); os.IsNotExist(err) {
		t.Fatal("output file does not exist")
	}
}

func TestGosseract_CanHandle(t *testing.T) {
	ocr, err := NewGosseract(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 30}, "mupdf", []string{"eng"}, "/tmp")
	if err != nil {
		t.Fatalf("NewGosseract: %v", err)
	}
	if !ocr.CanHandle("application/pdf") {
		t.Error("CanHandle(application/pdf) = false")
	}
	if ocr.CanHandle("image/png") {
		t.Error("CanHandle(image/png) = true")
	}
}

func TestGosseract_Name(t *testing.T) {
	ocr, err := NewGosseract(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 30}, "mupdf", []string{"eng"}, "/tmp")
	if err != nil {
		t.Fatalf("NewGosseract: %v", err)
	}
	if ocr.Name() != "gosseract" {
		t.Errorf("Name() = %q, want gosseract", ocr.Name())
	}
}

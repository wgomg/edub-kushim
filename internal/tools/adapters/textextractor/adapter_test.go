package textextractor

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestNewTextExtractor_PDFToText(t *testing.T) {
	cfg := config.ToolConfig{Command: "pdftotext", Timeout: 30}
	ext, err := NewTextExtractor(utils.NewDiscardLogger(), cfg)
	if err != nil {
		t.Fatalf("NewTextExtractor: %v", err)
	}
	if ext.Name() != "pdftotext" {
		t.Errorf("Name() = %q, want pdftotext", ext.Name())
	}
}

func TestNewTextExtractor_Gopdf(t *testing.T) {
	cfg := config.ToolConfig{Command: "gopdf", Timeout: 30}
	ext, err := NewTextExtractor(utils.NewDiscardLogger(), cfg)
	if err != nil {
		t.Fatalf("NewTextExtractor: %v", err)
	}
	if ext.Name() != "gopdf" {
		t.Errorf("Name() = %q, want gopdf", ext.Name())
	}
}

func TestNewTextExtractor_MuPDF(t *testing.T) {
	cfg := config.ToolConfig{Command: "mupdf", Timeout: 30}
	ext, err := NewTextExtractor(utils.NewDiscardLogger(), cfg)
	if err != nil {
		t.Fatalf("NewTextExtractor: %v", err)
	}
	if ext.Name() != "mupdf" {
		t.Errorf("Name() = %q, want mupdf", ext.Name())
	}
}

func TestNewTextExtractor_Default(t *testing.T) {
	cfg := config.ToolConfig{Command: "", Timeout: 30}
	ext, err := NewTextExtractor(utils.NewDiscardLogger(), cfg)
	if err != nil {
		t.Fatalf("NewTextExtractor: %v", err)
	}
	if ext.Name() != "mupdf" {
		t.Errorf("Name() = %q, want mupdf", ext.Name())
	}
}

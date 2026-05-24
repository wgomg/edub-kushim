package pdfoptimizer

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestNewPdfOptimizer_Ghostscript(t *testing.T) {
	cfg := config.ToolConfig{Command: "gs", Timeout: 30}
	opt, err := NewPdfOptimizer(utils.NewDiscardLogger(), cfg)
	if err != nil {
		t.Fatalf("NewPdfOptimizer: %v", err)
	}
	if opt.Name() != "ghostscript" {
		t.Errorf("Name() = %q, want ghostscript", opt.Name())
	}
}

func TestNewPdfOptimizer_MuPDF(t *testing.T) {
	cfg := config.ToolConfig{Command: "mupdf", Timeout: 30}
	opt, err := NewPdfOptimizer(utils.NewDiscardLogger(), cfg)
	if err != nil {
		t.Fatalf("NewPdfOptimizer: %v", err)
	}
	if opt.Name() != "mupdf" {
		t.Errorf("Name() = %q, want mupdf", opt.Name())
	}
}

func TestNewPdfOptimizer_Default(t *testing.T) {
	cfg := config.ToolConfig{Command: "", Timeout: 30}
	opt, err := NewPdfOptimizer(utils.NewDiscardLogger(), cfg)
	if err != nil {
		t.Fatalf("NewPdfOptimizer: %v", err)
	}
	if opt.Name() != "mupdf" {
		t.Errorf("Name() = %q, want mupdf", opt.Name())
	}
}

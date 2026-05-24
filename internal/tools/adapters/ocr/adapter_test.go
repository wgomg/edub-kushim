package ocr

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestNewOCR_OcrMyPdf(t *testing.T) {
	cfg := config.ToolConfig{Command: "ocrmypdf", Timeout: 30}
	ocr, err := NewOCR(utils.NewDiscardLogger(), cfg, "mupdf", []string{"eng"}, "/tmp/tessdata")
	if err != nil {
		t.Fatalf("ocrmypdf not available: %v", err)
	}
	if ocr.Name() != "ocrmypdf" {
		t.Errorf("Name() = %q, want ocrmypdf", ocr.Name())
	}
}

func TestNewOCR_Gosseract(t *testing.T) {
	cfg := config.ToolConfig{Command: "gosseract", Timeout: 30}
	ocr, err := NewOCR(utils.NewDiscardLogger(), cfg, "mupdf", []string{"eng"}, "/tmp/tessdata")
	if err != nil {
		t.Fatalf("NewOCR: %v", err)
	}
	if ocr.Name() != "gosseract" {
		t.Errorf("Name() = %q, want gosseract", ocr.Name())
	}
}

func TestNewOCR_Default(t *testing.T) {
	cfg := config.ToolConfig{Command: "", Timeout: 30}
	ocr, err := NewOCR(utils.NewDiscardLogger(), cfg, "mupdf", []string{"eng"}, "/tmp/tessdata")
	if err != nil {
		t.Fatalf("NewOCR: %v", err)
	}
	if ocr.Name() != "gosseract" {
		t.Errorf("Name() = %q, want gosseract", ocr.Name())
	}
}

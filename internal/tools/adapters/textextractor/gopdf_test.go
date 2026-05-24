package textextractor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestGopdf_Extract(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.pdf")

	pdf := fpdf.New("P", "pt", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Helvetica", "", 12)
	pdf.Text(100, 700, "Hello World from gopdf test")
	if err := pdf.OutputFileAndClose(src); err != nil {
		t.Fatalf("generate test PDF: %v", err)
	}

	ext, err := NewGopdf(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 30})
	if err != nil {
		t.Fatalf("NewGopdf: %v", err)
	}

	text, err := ext.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if text == nil {
		t.Fatal("expected non-nil text")
	}
}

func TestGopdf_CanHandle(t *testing.T) {
	ext, _ := NewGopdf(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 30})
	if !ext.CanHandle("application/pdf") {
		t.Error("CanHandle(application/pdf) = false")
	}
	if ext.CanHandle("image/png") {
		t.Error("CanHandle(image/png) = true")
	}
}

func TestGopdf_Name(t *testing.T) {
	ext, _ := NewGopdf(utils.NewDiscardLogger(), config.ToolConfig{Timeout: 30})
	if ext.Name() != "gopdf" {
		t.Errorf("Name() = %q, want gopdf", ext.Name())
	}
}

//go:build integration

package textextractor

import (
	"path/filepath"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func writePDF(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Helvetica", "", 12)
	pdf.Cell(40, 10, "Hello, World!")
	if err := pdf.OutputFileAndClose(path); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	return path
}

func TestMuPDFExtractFromPDF(t *testing.T) {
	logger := utils.NewDiscardLogger()
	m, err := NewMuPDF(logger, config.ToolConfig{Command: "mupdf"})
	if err != nil {
		t.Fatalf("NewMuPDF: %v", err)
	}

	dir := t.TempDir()
	path := writePDF(t, dir, "test.pdf")

	text, err := m.Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if text == nil {
		t.Fatal("expected non-nil text")
	}
}

func TestMuPDFExtractNonExistent(t *testing.T) {
	logger := utils.NewDiscardLogger()
	m, err := NewMuPDF(logger, config.ToolConfig{Command: "mupdf"})
	if err != nil {
		t.Fatalf("NewMuPDF: %v", err)
	}

	_, err = m.Extract("/nonexistent/file.pdf")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestMuPDFCanHandle(t *testing.T) {
	logger := utils.NewDiscardLogger()
	m, err := NewMuPDF(logger, config.ToolConfig{Command: "mupdf"})
	if err != nil {
		t.Fatalf("NewMuPDF: %v", err)
	}

	if !m.CanHandle("application/pdf") {
		t.Error("expected true for application/pdf")
	}
	if m.CanHandle("text/plain") {
		t.Error("expected false for text/plain")
	}
}

func TestMuPDFName(t *testing.T) {
	logger := utils.NewDiscardLogger()
	m, err := NewMuPDF(logger, config.ToolConfig{Command: "mupdf"})
	if err != nil {
		t.Fatalf("NewMuPDF: %v", err)
	}
	if m.Name() != "mupdf" {
		t.Errorf("expected 'mupdf', got %q", m.Name())
	}
}

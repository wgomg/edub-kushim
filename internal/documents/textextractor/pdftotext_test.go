package textextractor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// TestPDFToText_Extract_DiscardsDiagonalText pins the extraction flag that keeps watermark glyphs out of
// text_content. It uses a fake binary so the assertion does not depend on an installed poppler version.
func TestPDFToText_Extract_DiscardsDiagonalText(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := filepath.Join(dir, "pdftotext-fake")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatalf("write fake pdftotext: %v", err)
	}

	p, err := NewPDFToText(log.NewDiscardLogger(), config.ToolConfig{Command: script, Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewPDFToText: %v", err)
	}
	if _, err := p.Extract(context.Background(), filepath.Join(dir, "input.pdf"), "application/pdf"); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	captured, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read captured arguments: %v", err)
	}
	if !strings.Contains(string(captured), "-nodiag") {
		t.Errorf("arguments = %q, want -nodiag", string(captured))
	}
}

func TestPDFToText_Extract_RejectsRelativePath(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}

	p, err := NewPDFToText(log.NewLogger("error"), config.ToolConfig{Command: "pdftotext", Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewPDFToText: %v", err)
	}

	_, err = p.Extract(context.Background(), "relative/-file.pdf", "application/pdf")
	if err == nil {
		t.Fatal("expected error for relative input path, got nil")
	}
}

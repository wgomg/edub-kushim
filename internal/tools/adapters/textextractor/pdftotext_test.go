package textextractor

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestPDFToText_Extract_RejectsRelativePath(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}

	p, err := NewPDFToText(utils.NewLogger("error"), config.ToolConfig{Command: "pdftotext", Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewPDFToText: %v", err)
	}

	_, err = p.Extract(context.Background(), "relative/-file.pdf")
	if err == nil {
		t.Fatal("expected error for relative input path, got nil")
	}
}

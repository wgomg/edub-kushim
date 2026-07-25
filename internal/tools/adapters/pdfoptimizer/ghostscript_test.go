package pdfoptimizer

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestGhostscript_Optimize_RejectsRelativePath(t *testing.T) {
	if _, err := exec.LookPath("gs"); err != nil {
		t.Skip("gs not installed")
	}

	g, err := NewGhostscript(utils.NewLogger("error"), config.ToolConfig{Command: "gs", Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewGhostscript: %v", err)
	}

	_, err = g.Optimize(context.Background(), "doc1", "relative/-file.pdf")
	if err == nil {
		t.Fatal("expected error for relative input path, got nil")
	}
}

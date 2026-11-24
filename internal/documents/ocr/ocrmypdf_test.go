package ocr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// TestOcrMyPdfProcessPageModeFlags pins the page-mode contract of the OCR seam: a missing text layer gets
// --skip-text so mixed documents pass through, while a garbled layer gets --force-ocr to rebuild it.
func TestOcrMyPdfProcessPageModeFlags(t *testing.T) {
	tests := []struct {
		name      string
		mode      Mode
		wantFlag  string
		otherFlag string
	}{
		{"missing text skips pages that carry text", ModeMissingText, "--skip-text", "--force-ocr"},
		{"garbled layer forces a full re-OCR", ModeForce, "--force-ocr", "--skip-text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "args")
			script := filepath.Join(dir, "ocrmypdf-fake")
			body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nfor last do :; done\n: > \"$last\"\n"
			if err := os.WriteFile(script, []byte(body), 0755); err != nil {
				t.Fatalf("write fake ocrmypdf: %v", err)
			}

			engine, err := NewOcrMyPdf(log.NewDiscardLogger(), config.ToolConfig{Command: script, Timeout: time.Second}, []string{"eng"})
			if err != nil {
				t.Fatalf("NewOcrMyPdf: %v", err)
			}
			out, err := engine.Process(context.Background(), "doc-1", filepath.Join(dir, "input.pdf"), tt.mode)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if out == nil {
				t.Fatal("expected an output path")
			}
			defer os.Remove(*out)

			captured, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatalf("read captured arguments: %v", err)
			}
			args := string(captured)
			if !strings.Contains(args, tt.wantFlag) {
				t.Errorf("arguments = %q, want %s", args, tt.wantFlag)
			}
			if strings.Contains(args, tt.otherFlag) {
				t.Errorf("arguments = %q, must not contain %s", args, tt.otherFlag)
			}
		})
	}
}

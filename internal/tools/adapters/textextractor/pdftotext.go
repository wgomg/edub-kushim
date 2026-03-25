package textextractor

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type PDFToText struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewPDFToText(logger *utils.Logger, cfg config.ToolConfig) (*PDFToText, error) {
	if _, err := exec.LookPath(cfg.Command); err != nil {
		return nil, fmt.Errorf("%s not found in PATH: %w", cfg.Command, err)
	}

	return &PDFToText{logger: logger, config: cfg}, nil
}

func (p *PDFToText) Extract(path string) (*string, error) {
	cmd := exec.Command(p.config.Command, "-raw", "-nopgbrk", "-enc", "UTF-8", path, "-")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("%s failed: %w, stderr: %s", p.Name(), err, stderr.String())
		}
	case <-time.After(p.config.Timeout):
		cmd.Process.Kill()
		return nil, fmt.Errorf("%s timed out after 30 seconds", p.Name())
	}

	text := stdout.String()
	text = strings.TrimSpace(text)

	return &text, nil
}

func (p *PDFToText) CanHandle(mimeType string) bool {
	return mimeType == "application/pdf"
}

func (p *PDFToText) Name() string {
	return "pdftotext"
}

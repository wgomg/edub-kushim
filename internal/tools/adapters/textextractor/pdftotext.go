package textextractor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type PDFToText struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewPDFToText(logger *utils.Logger, cfg config.ToolConfig) (*PDFToText, error) {
	if _, err := exec.LookPath(cfg.Command); err != nil {
		return nil, fmt.Errorf("%s not found in PATH: %w\nInstall it (e.g. 'sudo apt install poppler-utils' or 'brew install poppler') or switch to the built-in 'mupdf' engine", cfg.Command, err)
	}

	return &PDFToText{logger: logger, config: cfg}, nil
}

func (p *PDFToText) Extract(ctx context.Context, path string, _ string) (*string, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%s: input path must be absolute, got %q", p.Name(), path)
	}

	cmd := exec.CommandContext(ctx, p.config.Command, "-raw", "-nopgbrk", "-enc", "UTF-8", path, "-")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s failed: %w, stderr: %s", p.Name(), err, stderr.String())
	}

	text := stdout.String()
	text = strings.TrimSpace(text)

	return &text, nil
}

func (p *PDFToText) CanHandle(mimeType string) bool {
	return mimeType == "application/pdf"
}

func (p *PDFToText) Name() string {
	return config.TextExtractor.PdfToText
}

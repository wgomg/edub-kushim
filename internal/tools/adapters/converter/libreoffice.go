package converter

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"

	_mime "github.com/wgomg/edub-kushim/internal/mime"
)

type LibreOffice struct {
	logger *utils.Logger
	config config.ToolConfig
	binary string
}

func NewLibreOffice(logger *utils.Logger, cfg config.ToolConfig, binary string) (*LibreOffice, error) {
	resolvedPath, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("libreoffice not found: %w", err)
	}
	return &LibreOffice{logger: logger, config: cfg, binary: resolvedPath}, nil
}

func (l *LibreOffice) CanHandle(mimeType string) bool {
	return mimeType == _mime.DOCX || mimeType == _mime.ODT
}

func (l *LibreOffice) Name() string {
	return "libreoffice"
}

func (l *LibreOffice) Convert(ctx context.Context, path string, mimeType string) (*string, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%s: input path must be absolute, got %q", l.Name(), path)
	}

	outDir := filepath.Dir(path)

	cmd := exec.CommandContext(ctx, l.binary, "--headless", "--convert-to", "pdf", "--outdir", outDir, path)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s failed: %w, stderr: %s", l.Name(), err, stderr.String())
	}

	srcName := filepath.Base(path)
	pdfName := strings.TrimSuffix(srcName, filepath.Ext(srcName)) + ".pdf"
	pdfPath := filepath.Join(outDir, pdfName)

	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: output PDF not found at %s, stderr: %s", l.Name(), pdfPath, stderr.String())
	}

	return &pdfPath, nil
}

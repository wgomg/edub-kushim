package pdfoptimizer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// MuPDF rewrites PDFs through the MuPDF C library's clean command.
type MuPDF struct {
	logger *log.Logger
	config config.ToolConfig
}

// NewMuPDF builds the engine; construction never fails.
func NewMuPDF(logger *log.Logger, cfg config.ToolConfig) (*MuPDF, error) {
	return &MuPDF{logger: logger, config: cfg}, nil
}

// Name identifies the engine.
func (m *MuPDF) Name() string {
	return config.PdfOptimizer.MuPDF
}

// Optimize runs the clean pass over a temp copy and returns its temp
// path. The path must be absolute.
func (m *MuPDF) Optimize(ctx context.Context, docId, path string) (*string, error) {
	tmpDir := os.TempDir()
	ogName := filepath.Base(path)
	outputName := fmt.Sprintf(
		"mupdf_%s_%d.pdf",
		strings.TrimSuffix(ogName, filepath.Ext(ogName)),
		time.Now().Unix(),
	)
	outputPath := filepath.Join(tmpDir, outputName)

	m.logger.Debug(&docId, "mupdf: cleaning %s -> %s (PID=%d)", path, outputPath, os.Getpid())

	cmd := exec.CommandContext(ctx, os.Args[0], "internal-mupdf-clean", "--input", path, "--output", outputPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(outputPath)
		return nil, fmt.Errorf("mupdf pdf_clean_file: %w (stderr: %s)", err, stderr.String())
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("mupdf did not create output file")
	}

	m.logger.Debug(&docId, "mupdf processed %s -> %s", path, outputPath)
	return &outputPath, nil
}

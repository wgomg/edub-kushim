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
	"github.com/wgomg/edub-kushim/internal/utils"
)

type MuPDF struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewMuPDF(logger *utils.Logger, cfg config.ToolConfig) (*MuPDF, error) {
	return &MuPDF{logger: logger, config: cfg}, nil
}

func (m *MuPDF) Name() string {
	return config.PdfOptimizer.MuPDF
}

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

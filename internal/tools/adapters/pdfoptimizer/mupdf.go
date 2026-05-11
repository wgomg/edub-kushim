package pdfoptimizer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// MuPDF implements PdfOptimizer using MuPDF's pdf_clean_file via CGo wrapper.
type MuPDF struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewMuPDF(logger *utils.Logger, cfg config.ToolConfig) (*MuPDF, error) {
	return &MuPDF{logger: logger, config: cfg}, nil
}

func (m *MuPDF) Name() string {
	return "mupdf"
}

func (m *MuPDF) Optimize(path string) (*string, error) {
	tmpDir := os.TempDir()
	ogName := filepath.Base(path)
	outputName := fmt.Sprintf(
		"mupdf_%s_%d.pdf",
		strings.TrimSuffix(ogName, filepath.Ext(ogName)),
		time.Now().Unix(),
	)
	outputPath := filepath.Join(tmpDir, outputName)

	ctx, err := adapters.NewMuContext()
	if err != nil {
		return nil, fmt.Errorf("mupdf context: %w", err)
	}
	defer ctx.Close()

	if err := ctx.PdfCleanFile(path, outputPath); err != nil {
		os.Remove(outputPath)
		return nil, fmt.Errorf("mupdf pdf_clean_file: %w", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("mupdf did not create output file")
	}

	m.logger.Debug(nil, "mupdf processed %s -> %s", path, outputPath)
	return &outputPath, nil
}

package pdfoptimizer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters"
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

	mupdfCtx, err := adapters.NewMuContext()
	if err != nil {
		return nil, fmt.Errorf("mupdf context: %w", err)
	}
	defer mupdfCtx.Close()

	opts := adapters.NewCleanOptions()
	if err := mupdfCtx.PdfCleanFile(path, outputPath, opts); err != nil {
		os.Remove(outputPath)
		return nil, fmt.Errorf("mupdf pdf_clean_file: %w", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("mupdf did not create output file")
	}

	m.logger.Debug(&docId, "mupdf processed %s -> %s", path, outputPath)
	return &outputPath, nil
}

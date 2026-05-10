package pdfoptimizer

import (
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// MuPDF implements PdfOptimizer using MuPDF's pdf_clean_file via CGo.
// Currently stubbed — MuPDF CGo wrapper not yet built.
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
	m.logger.Debug(nil, "mupdf optimization not available — MuPDF dependency removed")
	return nil, fmt.Errorf("not implemented: mupdf optimization (MuPDF dependency removed)")
}

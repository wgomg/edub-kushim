package pdfoptimizer

import (
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type PdfOptimizer interface {
	Optimize(path string) (*string, error)
	Name() string
}

func NewPdfOptimizer(logger *utils.Logger, cfg config.ToolConfig) (PdfOptimizer, error) {
	defaultOptimizer, err := NewMuPDF(logger, cfg)

	switch cfg.Command {
	case "gs":
		gs, err := NewGhostscript(logger, cfg)
		return gs, err
	case "mupdf":
		m, err := NewMuPDF(logger, cfg)
		return m, err
	default:
		return defaultOptimizer, err
	}
}

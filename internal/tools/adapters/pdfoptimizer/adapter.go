package pdfoptimizer

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type PdfOptimizer interface {
	Optimize(ctx context.Context, path string) (*string, error)
	Name() string
}

func NewPdfOptimizer(logger *utils.Logger, cfg config.ToolConfig) (PdfOptimizer, error) {
	switch cfg.Command {
	case "gs":
		gs, err := NewGhostscript(logger, cfg)
		return gs, err
	case "mupdf":
		m, err := NewMuPDF(logger, cfg)
		return m, err
	default:
		return NewMuPDF(logger, cfg)
	}
}

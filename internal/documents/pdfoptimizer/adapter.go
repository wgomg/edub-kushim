// Package pdfoptimizer rewrites PDFs to smaller, cleaner files. Ghostscript
// shells out to gs; the MuPDF engine renders through the linked C library.
// The runner walks the configured engine and its fallback.
package pdfoptimizer

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// PdfOptimizer rewrites a PDF into an optimized copy.
type PdfOptimizer interface {
	// Optimize rewrites the PDF and returns the temp path of the result;
	// the original is untouched. The path must be absolute.
	Optimize(ctx context.Context, docId, path string) (*string, error)
	// Name identifies the engine.
	Name() string
}

// NewPdfOptimizer builds the configured engine, MuPDF by default.
func NewPdfOptimizer(logger *log.Logger, cfg config.ToolConfig) (PdfOptimizer, error) {
	switch cfg.Command {
	case config.PdfOptimizer.GS:
		gs, err := NewGhostscript(logger, cfg)
		return gs, err
	case config.PdfOptimizer.MuPDF:
		m, err := NewMuPDF(logger, cfg)
		return m, err
	default:
		return NewMuPDF(logger, cfg)
	}
}

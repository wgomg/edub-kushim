package textextractor

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TextExtractor interface {
	Extract(ctx context.Context, path string) (*string, error)
	CanHandle(mimeType string) bool
	Name() string
}

func NewTextExtractor(logger *utils.Logger, cfg config.ToolConfig) (TextExtractor, error) {
	switch cfg.Command {
	case config.TextExtractor.PdfToText:
		pdfToText, err := NewPDFToText(logger, cfg)
		return pdfToText, err
	case config.TextExtractor.GoPdf:
		gopdf, err := NewGopdf(logger, cfg)
		return gopdf, err
	case config.TextExtractor.MuPDF:
		mupdf, err := NewMuPDF(logger, cfg)
		return mupdf, err
	default:
		return NewMuPDF(logger, cfg)
	}
}

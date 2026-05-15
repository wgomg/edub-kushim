package textextractor

import (
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TextExtractor interface {
	Extract(path string) (*string, error)
	CanHandle(mimeType string) bool
	Name() string
}

func NewTextExtractor(logger *utils.Logger, cfg config.ToolConfig) (TextExtractor, error) {
	defaultExtractor, err := NewPDFToText(logger, cfg)

	switch cfg.Command {
	case "pdftotext":
		pdfToText, err := NewPDFToText(logger, cfg)
		return pdfToText, err
	case "gopdf":
		gopdf, err := NewGopdf(logger, cfg)
		return gopdf, err
	case "mupdf":
		mupdf, err := NewMuPDF(logger, cfg)
		return mupdf, err
	default:
		return defaultExtractor, err
	}
}

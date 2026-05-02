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
	defaultExtractor, err := NewFitz(logger, cfg)

	switch cfg.Command {
	case "pdftotext":
		pdfToText, err := NewPDFToText(logger, cfg)
		return pdfToText, err
	case "go-fitz":
		fitz, err := NewFitz(logger, cfg)
		return fitz, err
	default:
		return defaultExtractor, err
	}
}

package ocr

import (
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type OCR interface {
	Process(path string) (*string, error)
	CanHandle(mimeType string) bool
	Name() string
}

func NewOCR(logger *utils.Logger, cfg config.ToolConfig) (OCR, error) {
	defaultOCR, err := NewOcrMyPdf(logger, cfg)

	switch cfg.Command {
	case "ocrmypdf":
		ocrMyPdf, err := NewOcrMyPdf(logger, cfg)
		return ocrMyPdf, err
	case "gosseract":
		gosseract, err := NewGosseract(logger, cfg)
		return gosseract, err
	default:
		return defaultOCR, err
	}
}

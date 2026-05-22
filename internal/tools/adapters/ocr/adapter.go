package ocr

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type OCR interface {
	Process(ctx context.Context, path string) (*string, error)
	CanHandle(mimeType string) bool
	Name() string
}

func NewOCR(logger *utils.Logger, cfg config.ToolConfig, pdfOptimizerCmd string, languages []string, dataDir string) (OCR, error) {
	switch cfg.Command {
	case "ocrmypdf":
		ocrMyPdf, err := NewOcrMyPdf(logger, cfg)
		return ocrMyPdf, err
	case "gosseract":
		gosseract, err := NewGosseract(logger, cfg, pdfOptimizerCmd, languages, dataDir)
		return gosseract, err
	default:
		return NewGosseract(logger, cfg, pdfOptimizerCmd, languages, dataDir)
	}
}

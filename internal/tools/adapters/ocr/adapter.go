package ocr

import (
	"context"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// OCR is the interface for optical character recognition adapters.
type OCR interface {
	Process(ctx context.Context, docId, path string) (*string, error)
	CanHandle(mimeType string) bool
	Name() string
}

// newGosseract is overridden by gosseract.go via init() when CGo is
// available. The default returns an error.
var newGosseract = func(logger *utils.Logger, cfg config.ToolConfig, optimizerCmd string, languages []string, dataDir string) (OCR, error) {
	return nil, fmt.Errorf("gosseract OCR requires CGo — rebuild with CGO_ENABLED=1 and install the Tesseract dev headers")
}

func NewOCR(logger *utils.Logger, cfg config.ToolConfig, pdfOptimizerCmd string, languages []string, dataDir string) (OCR, error) {
	switch cfg.Command {
	case config.OCR.OcrMyPdf:
		return NewOcrMyPdf(logger, cfg, languages)
	case config.OCR.Gosseract:
		return newGosseract(logger, cfg, pdfOptimizerCmd, languages, dataDir)
	default:
		return newGosseract(logger, cfg, pdfOptimizerCmd, languages, dataDir)
	}
}

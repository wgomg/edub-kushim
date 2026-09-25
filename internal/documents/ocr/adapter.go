// Package ocr turns scanned or empty-text documents into searchable PDFs.
// The gosseract engine renders pages through MuPDF, recognizes word boxes
// with Tesseract, and writes an invisible-text overlay so the result stays
// a searchable PDF; the ocrmypdf engine wraps the external tool. The
// gosseract constructor is wired in through init when CGo is available,
// so CGO_ENABLED=0 builds still compile.
package ocr

import (
	"context"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// OCR is the optical-character-recognition seam: Process runs one
// document through the engine and returns the searchable PDF's path when
// one was produced.
type OCR interface {
	// Process recognizes the document at path and returns the searchable
	// PDF's path; nil when nothing was recognized.
	Process(ctx context.Context, docId, path string) (*string, error)
	// CanHandle reports whether the engine accepts the content type.
	CanHandle(mimeType string) bool
	// Name identifies the engine.
	Name() string
}

// newGosseract is overridden by gosseract.go via init() when CGo is
// available. The default returns an error.
var newGosseract = func(logger *log.Logger, cfg config.ToolConfig, optimizerCmd string, languages []string, dataDir string, ocrWorkers int) (OCR, error) {
	return nil, fmt.Errorf("gosseract OCR requires CGo — rebuild with CGO_ENABLED=1 and install the Tesseract dev headers")
}

// NewOCR builds the configured engine; the gosseract engine requires a
// CGo build and its optimizer command for rendering pages.
func NewOCR(logger *log.Logger, cfg config.ToolConfig, pdfOptimizerCmd string, languages []string, dataDir string, ocrWorkers int) (OCR, error) {
	switch cfg.Command {
	case config.OCR.OcrMyPdf:
		return NewOcrMyPdf(logger, cfg, languages)
	case config.OCR.Gosseract:
		return newGosseract(logger, cfg, pdfOptimizerCmd, languages, dataDir, ocrWorkers)
	default:
		return newGosseract(logger, cfg, pdfOptimizerCmd, languages, dataDir, ocrWorkers)
	}
}

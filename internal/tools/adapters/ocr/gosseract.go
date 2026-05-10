package ocr

import (
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// Gosseract implements OCR using Tesseract (gosseract) with MuPDF for page
// rendering. Currently stubbed — MuPDF CGo wrapper not yet built.
type Gosseract struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewGosseract(logger *utils.Logger, cfg config.ToolConfig, optimizerCmd string, languages []string, dataDir string) (*Gosseract, error) {
	return &Gosseract{logger: logger, config: cfg}, nil
}
func (o *Gosseract) Process(path string) (*string, error) {
	o.logger.Debug(nil, "gosseract OCR not available — MuPDF dependency removed")
	return nil, fmt.Errorf("not implemented: gosseract OCR (MuPDF dependency removed)")
}

func (o *Gosseract) CanHandle(mimeType string) bool {
	return mimeType == "application/pdf"
}

func (o *Gosseract) Name() string {
	return "gosseract"
}

package textextractor

import (
	"context"
	"fmt"

	"github.com/razvandimescu/gopdf/pdf"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// Gopdf extracts text from PDFs using the razvandimescu/gopdf library (pure Go).
//
// NOTE: gopdf reads the entire PDF file into memory via os.ReadFile and parses
// the full object tree upfront. For very large PDFs (1000+ pages), this can
// cause significant memory spikes (observed: ~2 GB for a 1014-page text PDF).
// If this becomes a problem, switch to pdftotext (external tool, streams) or
// use the MuPDF CGo wrapper's ExtractPageText (also streams).
type Gopdf struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewGopdf(logger *utils.Logger, cfg config.ToolConfig) (*Gopdf, error) {
	return &Gopdf{logger: logger, config: cfg}, nil
}

func (f *Gopdf) Extract(ctx context.Context, path string, _ string) (*string, error) {
	doc, err := pdf.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("gopdf: %w", err)
	}

	text, err := doc.Text()
	if err != nil {
		return nil, fmt.Errorf("gopdf: %w", err)
	}

	return &text, nil
}

func (f *Gopdf) CanHandle(mimeType string) bool {
	return mimeType == "application/pdf"
}

func (f *Gopdf) Name() string {
	return config.TextExtractor.GoPdf
}

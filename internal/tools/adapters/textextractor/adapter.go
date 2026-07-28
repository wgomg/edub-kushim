package textextractor

import (
	"context"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TextExtractor interface {
	Extract(ctx context.Context, path string, mimeType string) (*string, error)
	CanHandle(mimeType string) bool
	Name() string
}

type CompositeExtractor struct {
	extractors []TextExtractor
}

func NewCompositeExtractor(extractors []TextExtractor) *CompositeExtractor {
	return &CompositeExtractor{extractors: extractors}
}

func (c *CompositeExtractor) Extract(ctx context.Context, path string, mimeType string) (*string, error) {
	for _, ext := range c.extractors {
		if ext.CanHandle(mimeType) {
			return ext.Extract(ctx, path, mimeType)
		}
	}

	return nil, fmt.Errorf("composite: no extractor found for MIME type %s", mimeType)
}

func (c *CompositeExtractor) CanHandle(mimeType string) bool {
	for _, ext := range c.extractors {
		if ext.CanHandle(mimeType) {
			return true
		}
	}
	return false
}

func (c *CompositeExtractor) Name() string {
	return "composite"
}

func NewTextExtractor(logger *utils.Logger, cfg config.ToolConfig) (TextExtractor, error) {
	var pdfExtractor TextExtractor
	switch cfg.Command {
	case config.TextExtractor.PdfToText:
		var err error
		pdfExtractor, err = NewPDFToText(logger, cfg)
		if err != nil {
			return nil, err
		}
	case config.TextExtractor.GoPdf:
		var err error
		pdfExtractor, err = NewGopdf(logger, cfg)
		if err != nil {
			return nil, err
		}
	case config.TextExtractor.MuPDF:
		var err error
		pdfExtractor, err = NewMuPDF(logger, cfg)
		if err != nil {
			return nil, err
		}
	default:
		var err error
		pdfExtractor, err = NewMuPDF(logger, cfg)
		if err != nil {
			return nil, err
		}
	}

	docxExtractor := NewDocx(logger)
	odtExtractor := NewOdt(logger)

	return NewCompositeExtractor([]TextExtractor{
		pdfExtractor,
		docxExtractor,
		odtExtractor,
	}), nil
}

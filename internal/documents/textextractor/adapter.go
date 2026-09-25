// Package textextractor turns documents into plain text: a PDF extractor
// chosen by configuration (MuPDF, gopdf or pdftotext), the DOCX and ODT
// readers, and the composite that dispatches by content type. The runner
// holds the composite behind the TextExtractor seam.
package textextractor

import (
	"context"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// TextExtractor reads one document's text.
type TextExtractor interface {
	// Extract returns the document's plain text; nil when the document
	// carries none. The path must be absolute.
	Extract(ctx context.Context, path string, mimeType string) (*string, error)
	// CanHandle reports whether the extractor accepts the content type.
	CanHandle(mimeType string) bool
	// Name identifies the extractor.
	Name() string
}

// CompositeExtractor dispatches to the first extractor that claims the
// content type.
type CompositeExtractor struct {
	extractors []TextExtractor
}

// NewCompositeExtractor composes the extractors in order; the first
// CanHandle match serves the request.
func NewCompositeExtractor(extractors []TextExtractor) *CompositeExtractor {
	return &CompositeExtractor{extractors: extractors}
}

// Extract dispatches by content type, erroring when no child claims it.
func (c *CompositeExtractor) Extract(ctx context.Context, path string, mimeType string) (*string, error) {
	for _, ext := range c.extractors {
		if ext.CanHandle(mimeType) {
			return ext.Extract(ctx, path, mimeType)
		}
	}

	return nil, fmt.Errorf("composite: no extractor found for MIME type %s", mimeType)
}

// CanHandle reports whether any child claims the content type.
func (c *CompositeExtractor) CanHandle(mimeType string) bool {
	for _, ext := range c.extractors {
		if ext.CanHandle(mimeType) {
			return true
		}
	}
	return false
}

// Name identifies the composite.
func (c *CompositeExtractor) Name() string {
	return "composite"
}

// NewTextExtractor builds the composite: the configured PDF extractor
// (MuPDF by default) plus the always-present DOCX and ODT readers.
func NewTextExtractor(logger *log.Logger, cfg config.ToolConfig) (TextExtractor, error) {
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

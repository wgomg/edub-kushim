package textextractor

import (
	"context"
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/cgo"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"

	_mime "github.com/wgomg/edub-kushim/internal/documents/mime"
)

// MuPDF extracts PDF text through the linked MuPDF C library, one page at
// a time, joined with newlines in stream order.
type MuPDF struct {
	logger *log.Logger
	config config.ToolConfig
}

// NewMuPDF builds the extractor; construction never fails (page access
// errors surface per call).
func NewMuPDF(logger *log.Logger, cfg config.ToolConfig) (*MuPDF, error) {
	return &MuPDF{logger: logger, config: cfg}, nil
}

// Extract opens the document on a fresh MuPDF context and walks its
// pages, honoring context cancellation between pages. The path must be
// absolute.
func (m *MuPDF) Extract(ctx context.Context, path string, _ string) (*string, error) {
	mupdfCtx, err := cgo.NewMuContext()
	if err != nil {
		return nil, fmt.Errorf("mupdf: %w", err)
	}
	defer mupdfCtx.Close()

	doc, err := mupdfCtx.OpenMuDocument(path)
	if err != nil {
		return nil, fmt.Errorf("mupdf: %w", err)
	}
	defer doc.Close(mupdfCtx)

	var buf strings.Builder
	for i := range doc.NumPages(mupdfCtx) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		text, err := doc.ExtractPageText(mupdfCtx, i)
		if err != nil {
			return nil, fmt.Errorf("mupdf: page %d: %w", i, err)
		}
		buf.WriteString(text)
		buf.WriteByte('\n')
	}

	result := buf.String()
	return &result, nil
}

// CanHandle reports PDF content types.
func (m *MuPDF) CanHandle(mimeType string) bool {
	return _mime.IsPDF(mimeType)
}

// Name identifies the extractor.
func (m *MuPDF) Name() string {
	return config.TextExtractor.MuPDF
}

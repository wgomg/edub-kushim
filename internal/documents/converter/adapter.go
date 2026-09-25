// Package converter turns office documents into PDFs. The current engine
// is LibreOffice headless, invoked through the configured binary.
package converter

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// DocumentConverter turns an office document into a PDF.
type DocumentConverter interface {
	// Convert converts the document at path to PDF and returns the temp
	// path of the converted file; the original is untouched. The path must
	// be absolute.
	Convert(ctx context.Context, path string, mimeType string) (*string, error)
	// CanHandle reports whether the engine accepts the content type.
	CanHandle(mimeType string) bool
	// Name identifies the engine.
	Name() string
}

// NewDocumentConverter builds the converter; LibreOffice is the only
// engine today.
func NewDocumentConverter(logger *log.Logger, cfg config.ToolConfig, binary string) (DocumentConverter, error) {
	return NewLibreOffice(logger, cfg, binary)
}

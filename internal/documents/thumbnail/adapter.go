// Package thumbnail renders a document's first page as a JPEG thumbnail.
// The MuPDF engine renders through the linked C library; its constructor
// is wired in through init when CGo is available, so CGO_ENABLED=0 builds
// still compile and report the engine unavailable.
package thumbnail

import (
	"context"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// Thumbnailer renders one document's first-page thumbnail.
type Thumbnailer interface {
	// Generate writes the JPEG to outputPath and returns the image's pixel
	// dimensions. The path must be absolute.
	Generate(ctx context.Context, docId, path, outputPath string) (width, height int, err error)
	// CanHandle reports whether the engine accepts the content type.
	CanHandle(mimeType string) bool
	// Name identifies the engine.
	Name() string
}

// newMuPDF is overridden by mupdf.go via init() when CGo is available.
// The default returns an error.
var newMuPDF = func(logger *log.Logger, cfg config.ToolConfig, dpi, maxWidth, quality int) (Thumbnailer, error) {
	return nil, fmt.Errorf("mupdf thumbnails require CGo — rebuild with CGO_ENABLED=1 and install the MuPDF dev headers")
}

// NewThumbnailer builds the configured engine; imagemagick is a
// configuration placeholder that is not implemented, and anything else
// resolves to MuPDF.
func NewThumbnailer(logger *log.Logger, cfg config.ToolConfig, dpi, maxWidth, quality int) (Thumbnailer, error) {
	switch cfg.Command {
	case config.Thumbnail.MuPDF:
		return newMuPDF(logger, cfg, dpi, maxWidth, quality)
	case config.Thumbnail.Imagemagick:
		return nil, fmt.Errorf("imagemagick thumbnail engine is not implemented yet")
	default:
		return newMuPDF(logger, cfg, dpi, maxWidth, quality)
	}
}

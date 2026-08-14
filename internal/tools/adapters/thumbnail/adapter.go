package thumbnail

import (
	"context"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// Thumbnailer is the interface for document thumbnail adapters.
type Thumbnailer interface {
	Generate(ctx context.Context, docId, path, outputPath string) (width, height int, err error)
	CanHandle(mimeType string) bool
	Name() string
}

// newMuPDF is overridden by mupdf.go via init() when CGo is available.
// The default returns an error.
var newMuPDF = func(logger *utils.Logger, cfg config.ToolConfig, dpi, maxWidth, quality int) (Thumbnailer, error) {
	return nil, fmt.Errorf("mupdf thumbnails require CGo — rebuild with CGO_ENABLED=1 and install the MuPDF dev headers")
}

func NewThumbnailer(logger *utils.Logger, cfg config.ToolConfig, dpi, maxWidth, quality int) (Thumbnailer, error) {
	switch cfg.Command {
	case config.Thumbnail.MuPDF:
		return newMuPDF(logger, cfg, dpi, maxWidth, quality)
	case config.Thumbnail.Imagemagick:
		return nil, fmt.Errorf("imagemagick thumbnail engine is not implemented yet")
	default:
		return newMuPDF(logger, cfg, dpi, maxWidth, quality)
	}
}

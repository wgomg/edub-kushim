package converter

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type DocumentConverter interface {
	Convert(ctx context.Context, path string, mimeType string) (*string, error)
	CanHandle(mimeType string) bool
	Name() string
}

func NewDocumentConverter(logger *utils.Logger, cfg config.ToolConfig, binary string) (DocumentConverter, error) {
	return NewLibreOffice(logger, cfg, binary)
}

package textextractor

import (
	"context"
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters"
	"github.com/wgomg/edub-kushim/internal/utils"

	_mime "github.com/wgomg/edub-kushim/internal/mime"
)

type MuPDF struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewMuPDF(logger *utils.Logger, cfg config.ToolConfig) (*MuPDF, error) {
	return &MuPDF{logger: logger, config: cfg}, nil
}

func (m *MuPDF) Extract(ctx context.Context, path string, _ string) (*string, error) {
	mupdfCtx, err := adapters.NewMuContext()
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

func (m *MuPDF) CanHandle(mimeType string) bool {
	return _mime.IsPDF(mimeType)
}

func (m *MuPDF) Name() string {
	return config.TextExtractor.MuPDF
}

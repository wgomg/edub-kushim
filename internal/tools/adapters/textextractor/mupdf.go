package textextractor

import (
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type MuPDF struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewMuPDF(logger *utils.Logger, cfg config.ToolConfig) (*MuPDF, error) {
	return &MuPDF{logger: logger, config: cfg}, nil
}

func (m *MuPDF) Extract(path string) (*string, error) {
	ctx, err := adapters.NewMuContext()
	if err != nil {
		return nil, fmt.Errorf("mupdf: %w", err)
	}
	defer ctx.Close()

	doc, err := ctx.OpenMuDocument(path)
	if err != nil {
		return nil, fmt.Errorf("mupdf: %w", err)
	}
	defer doc.Close(ctx)

	var buf strings.Builder
	for i := range doc.NumPages(ctx) {
		text, err := doc.ExtractPageText(ctx, i)
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
	return mimeType == "application/pdf"
}

func (m *MuPDF) Name() string {
	return "mupdf"
}

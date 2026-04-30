package textextractor

import (
	"fmt"
	"strings"

	"github.com/gen2brain/go-fitz"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Fitz struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewFitz(logger *utils.Logger, cfg config.ToolConfig) (*Fitz, error) {
	return &Fitz{logger: logger, config: cfg}, nil
}

func (f *Fitz) Extract(path string) (*string, error) {
	doc, err := fitz.New(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF with MuPDF: %w", err)
	}
	defer doc.Close()

	var buf strings.Builder
	n := doc.NumPage()
	for i := range n {
		text, err := doc.Text(i)
		if err != nil {
			f.logger.Debug(nil, "page %d: text extraction error: %v", i, err)
			continue
		}
		buf.WriteString(text)
		buf.WriteString("\n")
	}
	result := strings.TrimSpace(buf.String())
	if result == "" {
		return nil, nil
	}
	return &result, nil
}

func (f *Fitz) CanHandle(mimeType string) bool {
	return mimeType == "application/pdf"
}

func (f *Fitz) Name() string {
	return "go-fitz"
}

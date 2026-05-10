package textextractor

import (
	"fmt"

	"github.com/razvandimescu/gopdf/pdf"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Gopdf struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewGopdf(logger *utils.Logger, cfg config.ToolConfig) (*Gopdf, error) {
	return &Gopdf{logger: logger, config: cfg}, nil
}

func (f *Gopdf) Extract(path string) (*string, error) {
	doc, err := pdf.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("gopdf: %w", err)
	}

	text, err := doc.Text()
	if err != nil {
		return nil, fmt.Errorf("gopdf: %w", err)
	}

	return &text, nil
}

func (f *Gopdf) CanHandle(mimeType string) bool {
	return mimeType == "application/pdf"
}

func (f *Gopdf) Name() string {
	return "gopdf"
}

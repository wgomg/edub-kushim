package tools

import (
	"fmt"
	"os"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/ocr"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/textextractor"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Runner struct {
	logger *utils.Logger
	config *config.ConsumerConfig
}

type TextExtractionResult struct {
	Text     *string
	Metadata map[string]any
}

type OCRResult struct {
	Success    bool
	TmpPath    *string
	Confidence *float64
}

func NewRunner(logger *utils.Logger, cfg *config.ConsumerConfig) *Runner {
	return &Runner{logger: logger, config: cfg}
}

func (r *Runner) ExtractText(path string) (*TextExtractionResult, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does note xist: %s", path)
	}

	cfg := config.ToolConfig{
		Command: r.config.TextExtractor,
		Timeout: 30 * time.Second,
	}

	textExtractor, err := textextractor.NewTextExtractor(r.logger, cfg)
	if err != nil {
		return nil, err
	}

	text, err := textExtractor.Extract(path)
	if err != nil {
		return nil, err
	}

	result := TextExtractionResult{Text: text}

	return &result, nil
}

func (r *Runner) OCR(path string) (*OCRResult, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does note xist: %s", path)
	}

	cfg := config.ToolConfig{
		Command: r.config.OCR,
		Timeout: 30 * time.Second,
	}

	ocr, err := ocr.NewOCR(r.logger, cfg)
	if err != nil {
		return nil, err
	}

	outputPath, err := ocr.Process(path)
	if err != nil {
		return nil, err
	}

	result := OCRResult{
		Success: true,
		TmpPath: outputPath,
	}

	return &result, nil
}

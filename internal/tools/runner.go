package tools

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/ocr"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/pdfoptimizer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/textextractor"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Runner struct {
	logger        *utils.Logger
	config        *config.ConsumerConfig
	textExtractor textextractor.TextExtractor
	ocr           ocr.OCR
	pdfOptimizer  pdfoptimizer.PdfOptimizer
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

type PdfOptimizationResult struct {
	Success bool
	TmpPath *string
}

func NewRunner(logger *utils.Logger, cfg *config.ConsumerConfig) *Runner {
	textCfg := config.ToolConfig{Command: cfg.TextExtractor, Timeout: 30 * time.Second}
	textExtractor, _ := textextractor.NewTextExtractor(logger, textCfg)

	ocrCfg := config.ToolConfig{Command: cfg.OCR, Timeout: 30 * time.Second}
	ocr, _ := ocr.NewOCR(logger, ocrCfg, cfg.PdfOptimizer, cfg.OCRLanguages, cfg.OCRDataDir)

	optCfg := config.ToolConfig{Command: cfg.PdfOptimizer, Timeout: time.Duration(cfg.OptimizationTimeout) * time.Second}
	pdfOptimizer, _ := pdfoptimizer.NewPdfOptimizer(logger, optCfg)

	return NewRunnerWithAdapters(logger, cfg, textExtractor, ocr, pdfOptimizer)
}

func NewRunnerWithAdapters(
	logger *utils.Logger,
	cfg *config.ConsumerConfig,
	textExtractor textextractor.TextExtractor,
	ocr ocr.OCR,
	pdfOptimizer pdfoptimizer.PdfOptimizer,
) *Runner {
	return &Runner{
		logger:        logger,
		config:        cfg,
		textExtractor: textExtractor,
		ocr:           ocr,
		pdfOptimizer:  pdfOptimizer,
	}
}

func (r *Runner) ExtractText(ctx context.Context, path string) (*TextExtractionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.configTimeout("ExtractText"))
	defer cancel()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does note xist: %s", path)
	}

	text, err := r.textExtractor.Extract(path)
	if err != nil {
		return nil, err
	}

	result := TextExtractionResult{Text: text}

	return &result, nil
}

func (r *Runner) OCR(ctx context.Context, path string) (*OCRResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.configTimeout("OCR"))
	defer cancel()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does note xist: %s", path)
	}

	outputPath, err := r.ocr.Process(path)
	if err != nil {
		return nil, err
	}

	result := OCRResult{
		Success: true,
		TmpPath: outputPath,
	}

	return &result, nil
}

func (r *Runner) OptimizePdf(ctx context.Context, path string) (*PdfOptimizationResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.configTimeout("OptimizePdf"))
	defer cancel()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %s", path)
	}

	outputPath, err := r.pdfOptimizer.Optimize(path)
	if err != nil {
		if r.config.OptimizationFallback == "" {
			return nil, err
		}
		r.logger.Info(nil, "primary optimizer (%s) failed: %v — falling back to %s",
			r.config.PdfOptimizer, err, r.config.OptimizationFallback)
		fbCfg := config.ToolConfig{
			Command: r.config.OptimizationFallback,
			Timeout: time.Duration(r.config.OptimizationTimeout) * time.Second,
		}
		fbOptimizer, fbErr := pdfoptimizer.NewPdfOptimizer(r.logger, fbCfg)
		if fbErr != nil {
			return nil, fmt.Errorf("%s: %w; fallback %s: %w",
				r.config.PdfOptimizer, err, r.config.OptimizationFallback, fbErr)
		}
		outputPath, err = fbOptimizer.Optimize(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w; fallback %s: %w",
				r.config.PdfOptimizer, err, r.config.OptimizationFallback, err)
		}
	}

	return &PdfOptimizationResult{
		Success: true,
		TmpPath: outputPath,
	}, nil
}

func (r *Runner) configTimeout(field string) time.Duration {
	switch field {
	case "OptimizePdf":
		return time.Duration(r.config.OptimizationTimeout) * time.Second
	default:
		return 30 * time.Second
	}
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/contentanalyzer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/ocr"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/pdfoptimizer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/textextractor"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/textreducer"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Runner struct {
	logger          *utils.Logger
	config          *config.Config
	textExtractor   textextractor.TextExtractor
	ocr             ocr.OCR
	pdfOptimizer    pdfoptimizer.PdfOptimizer
	tagMatcher      tagmatcher.Matcher
	contentAnalyzer contentanalyzer.ContentAnalyzer
	textReducer     textreducer.TextReducer
}

type TextExtractionResult struct {
	Text *string
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

type TextReducerResult struct {
	Text            string
	WordCount       int
	CharCount       int
	TargetWordCount int
}

type TagMatchResult struct {
	Tags []string
}

type ContentAnalysisResult struct {
	Title    string                         `json:"title"`
	DocType  string                         `json:"type"`
	Tags     []string                       `json:"tags"`
	People   []contentanalyzer.PeopleResult `json:"people"`
	Language string                         `json:"language"`
	Stats    *json.RawMessage               `json:"stats"`
	Prompt   string                         `json:"prompt"`
}

// runWithTimeout runs fn in a goroutine and returns its result,
// or ctx.Err() if the context expires first. This ensures timeout
// detection works for any adapter, regardless of whether it checks
// ctx internally (CommandContext, per-page selects, or none at all).
func runWithTimeout[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, e := fn()
		ch <- result{v, e}
	}()
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case r := <-ch:
		return r.val, r.err
	}
}

func NewRunner(logger *utils.Logger, cfg *config.Config, tools []string) *Runner {
	r := &Runner{logger: logger, config: cfg}
	for _, name := range tools {
		switch name {
		case "textextractor":
			toolCfg := config.ToolConfig{
				Command: cfg.Consumer.TextExtractor.Engine,
				Timeout: time.Duration(cfg.Consumer.TextExtractor.Timeout) * time.Second,
			}
			r.textExtractor, _ = textextractor.NewTextExtractor(logger, toolCfg)
		case "ocr":
			toolCfg := config.ToolConfig{
				Command: cfg.Consumer.OCR.Engine,
				Timeout: time.Duration(cfg.Consumer.OCR.Timeout) * time.Second,
			}
			r.ocr, _ = ocr.NewOCR(logger, toolCfg, cfg.Consumer.PdfOptimizer.Engine, cfg.Consumer.OCR.Languages, cfg.Consumer.OCR.DataDir)
		case "pdfoptimizer":
			toolCfg := config.ToolConfig{
				Command: cfg.Consumer.PdfOptimizer.Engine,
				Timeout: time.Duration(cfg.Consumer.PdfOptimizer.Timeout) * time.Second,
			}
			r.pdfOptimizer, _ = pdfoptimizer.NewPdfOptimizer(logger, toolCfg)
		case "textreducer":
			toolCfg := config.ToolConfig{
				Command: cfg.Enricher.TextReducer.Engine,
				Timeout: time.Duration(cfg.Enricher.TextReducer.Timeout) * time.Second,
			}
			r.textReducer, _ = textreducer.NewTextReducer(logger, toolCfg)
		case "contentanalyzer":
			toolCfg := config.ToolConfig{
				Command: cfg.Enricher.ContentAnalyzer.Engine,
				Timeout: time.Duration(cfg.Enricher.ContentAnalyzer.Timeout) * time.Second,
			}
			r.contentAnalyzer, _ = contentanalyzer.NewContentAnalyzer(logger, toolCfg, &cfg.Enricher.ContentAnalyzer.Llm)
		}
	}
	return r
}

func NewRunnerWithMatcher(logger *utils.Logger, cfg *config.Config, tools []string, matcher tagmatcher.Matcher) *Runner {
	r := NewRunner(logger, cfg, tools)
	if matcher != nil && slices.Contains(tools, "tagmatcher") {
		r.tagMatcher = matcher
	}
	return r
}

func (r *Runner) ExtractText(ctx context.Context, path string) (*TextExtractionResult, error) {
	if r.textExtractor == nil {
		return nil, fmt.Errorf("text extractor not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.config.Consumer.TextExtractor.Timeout)*time.Second)
	defer cancel()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %s", path)
	}

	text, err := runWithTimeout(ctx, func() (*string, error) {
		return r.textExtractor.Extract(ctx, path)
	})
	if err != nil {
		return nil, fmt.Errorf("text extractor: %w", err)
	}

	result := TextExtractionResult{Text: text}

	return &result, nil
}

func (r *Runner) OCR(ctx context.Context, docId, path string) (*OCRResult, error) {
	if r.ocr == nil {
		return nil, fmt.Errorf("OCR not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.config.Consumer.OCR.Timeout)*time.Second)
	defer cancel()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %s", path)
	}

	outputPath, err := runWithTimeout(ctx, func() (*string, error) {
		return r.ocr.Process(ctx, docId, path)
	})
	if err != nil {
		return nil, fmt.Errorf("ocr: %w", err)
	}

	result := OCRResult{
		Success: true,
		TmpPath: outputPath,
	}

	return &result, nil
}

func (r *Runner) OptimizePdf(ctx context.Context, docId, path string) (*PdfOptimizationResult, error) {
	if r.pdfOptimizer == nil {
		return nil, fmt.Errorf("PDF optimizer not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.config.Consumer.PdfOptimizer.Timeout)*time.Second)
	defer cancel()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %s", path)
	}

	outputPath, err := runWithTimeout(ctx, func() (*string, error) {
		return r.pdfOptimizer.Optimize(ctx, docId, path)
	})
	if err != nil {
		if r.config.Consumer.PdfOptimizer.Fallback == "" {
			return nil, fmt.Errorf("pdf optimizer: %w", err)
		}
		r.logger.Info(nil, "primary optimizer (%s) failed: %v — falling back to %s",
			r.config.Consumer.PdfOptimizer.Engine, err, r.config.Consumer.PdfOptimizer.Fallback)
		fbCfg := config.ToolConfig{
			Command: r.config.Consumer.PdfOptimizer.Fallback,
			Timeout: time.Duration(r.config.Consumer.PdfOptimizer.Timeout) * time.Second,
		}
		fbOptimizer, fbErr := pdfoptimizer.NewPdfOptimizer(r.logger, fbCfg)
		if fbErr != nil {
			return nil, fmt.Errorf("%s: %w; fallback %s: %w",
				r.config.Consumer.PdfOptimizer.Engine, err, r.config.Consumer.PdfOptimizer.Fallback, fbErr)
		}
		outputPath, err = runWithTimeout(ctx, func() (*string, error) {
			return fbOptimizer.Optimize(ctx, docId, path)
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w; fallback %s: %w",
				r.config.Consumer.PdfOptimizer.Engine, err, r.config.Consumer.PdfOptimizer.Fallback, err)
		}
	}

	return &PdfOptimizationResult{
		Success: true,
		TmpPath: outputPath,
	}, nil
}

func (r *Runner) ReduceContent(ctx context.Context, content string, chunkSize, targetWordCount int) (*TextReducerResult, error) {
	contentWordCount := len(strings.Fields(content))
	charWordCount := utf8.RuneCountInString(content)

	result := &TextReducerResult{
		Text:            content,
		WordCount:       contentWordCount,
		CharCount:       charWordCount,
		TargetWordCount: targetWordCount,
	}

	if targetWordCount == 0 || contentWordCount < targetWordCount {
		return result, nil
	}

	if r.textReducer == nil {
		return result, fmt.Errorf("text reducer not configured")
	}
	reducedContent, err := runWithTimeout(ctx, func() (*string, error) {
		return r.textReducer.Reduce(ctx, content, chunkSize, targetWordCount)
	})
	if err != nil {
		return result, fmt.Errorf("text reducer: %w", err)
	}
	return &TextReducerResult{
		Text:            *reducedContent,
		WordCount:       len(strings.Fields(*reducedContent)),
		CharCount:       utf8.RuneCountInString(*reducedContent),
		TargetWordCount: targetWordCount,
	}, nil
}

func (r *Runner) MatchTags(ctx context.Context, docId, input string, candidateTags []string) (*TagMatchResult, error) {
	if r.tagMatcher == nil {
		return nil, fmt.Errorf("tag matcher not configured")
	}
	tags, err := runWithTimeout(ctx, func() ([]string, error) {
		return r.tagMatcher.Match(ctx, docId, input, candidateTags)
	})
	if err != nil {
		return nil, fmt.Errorf("tag matcher: %w", err)
	}
	return &TagMatchResult{Tags: tags}, nil
}

func (r *Runner) AnalyzeContent(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*ContentAnalysisResult, error) {
	if r.contentAnalyzer == nil {
		return nil, fmt.Errorf("content analyzer not configured")
	}
	result, err := runWithTimeout(ctx, func() (*contentanalyzer.AnalysisResult, error) {
		return r.contentAnalyzer.Analyze(ctx, text, docTypes, peopleTypes, tagSuggestions)
	})
	if err != nil {
		return nil, fmt.Errorf("content analyzer: %w", err)
	}

	return &ContentAnalysisResult{
		Title:    result.Title,
		DocType:  result.DocType,
		Tags:     result.Tags,
		People:   result.People,
		Language: result.Language,
		Stats:    result.Stats,
		Prompt:   result.Prompt,
	}, nil
}

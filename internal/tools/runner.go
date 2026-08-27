package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/contentanalyzer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/converter"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/ocr"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/pdfoptimizer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/textextractor"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/textreducer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/thumbnail"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Runner struct {
	logger            *utils.Logger
	config            *config.Config
	textExtractor     textextractor.TextExtractor
	ocr               ocr.OCR
	pdfOptimizer      pdfoptimizer.PdfOptimizer
	thumbnailer       thumbnail.Thumbnailer
	documentConverter converter.DocumentConverter
	tagMatcher        tagmatcher.Matcher
	contentAnalyzer   contentanalyzer.ContentAnalyzer
	fallbackAnalyzers []contentanalyzer.ContentAnalyzer
	fallbackMeta      []fallbackMeta
	textReducer       textreducer.TextReducer
}

type fallbackMeta struct {
	Provider string
	Model    string
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

type ThumbnailResult struct {
	Success bool
	Width   int
	Height  int
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
	Title       string                         `json:"title"`
	DocType     string                         `json:"type"`
	Tags        []string                       `json:"tags"`
	People      []contentanalyzer.PeopleResult `json:"people"`
	Language    string                         `json:"language"`
	Stats       *json.RawMessage               `json:"stats"`
	Prompt      string                         `json:"prompt"`
	PassContext *json.RawMessage               `json:"-"`
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

	reg, err := llm.NewRegistry(filepath.Join(cfg.App.ConfigDir, "model_catalog.json"))
	if err != nil {
		logger.Error(nil, "load model catalog: %v", err)
	}

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
			r.ocr, _ = ocr.NewOCR(logger, toolCfg, cfg.Consumer.PdfOptimizer.Engine, cfg.Consumer.OCR.Languages, cfg.Consumer.OCR.DataDir, cfg.Consumer.OCR.OcrWorkers)
		case "pdfoptimizer":
			toolCfg := config.ToolConfig{
				Command: cfg.Consumer.PdfOptimizer.Engine,
				Timeout: time.Duration(cfg.Consumer.PdfOptimizer.Timeout) * time.Second,
			}
			r.pdfOptimizer, _ = pdfoptimizer.NewPdfOptimizer(logger, toolCfg)
		case "thumbnail":
			toolCfg := config.ToolConfig{
				Command: cfg.Consumer.Thumbnail.Engine,
				Timeout: time.Duration(cfg.Consumer.Thumbnail.Timeout) * time.Second,
			}
			r.thumbnailer, _ = thumbnail.NewThumbnailer(logger, toolCfg, cfg.Consumer.Thumbnail.DPI, cfg.Consumer.Thumbnail.MaxWidth, cfg.Consumer.Thumbnail.Quality)
		case "converter":
			if cfg.Consumer.Converter.Enabled {
				r.documentConverter, _ = converter.NewDocumentConverter(
					logger,
					config.ToolConfig{
						Command: cfg.Consumer.Converter.Binary,
						Timeout: time.Duration(cfg.Consumer.Converter.Timeout) * time.Second,
					},
					cfg.Consumer.Converter.Binary,
				)
			}
		case "textreducer":
			toolCfg := config.ToolConfig{
				Command: cfg.Enricher.TextReducer.Engine,
				Timeout: time.Duration(cfg.Enricher.TextReducer.Timeout) * time.Second,
			}
			r.textReducer, _ = textreducer.NewTextReducer(logger, toolCfg)
		case "contentanalyzer":
			ca, caErr := contentanalyzer.NewContentAnalyzer(logger, config.ToolConfig{Timeout: time.Duration(cfg.Enricher.ContentAnalyzer.Timeout) * time.Second}, &cfg.Enricher.ContentAnalyzer.Llm, cfg.Enricher.ContentAnalyzer.PromptTemplate, reg)
			if caErr != nil {
				logger.Error(nil, "create content analyzer: %v", caErr)
			}
			r.contentAnalyzer = ca
			for i := range cfg.Enricher.ContentAnalyzer.Fallbacks {
				fb := &cfg.Enricher.ContentAnalyzer.Fallbacks[i]
				if !fb.Enabled {
					continue
				}
				fbCa, fbErr := contentanalyzer.NewContentAnalyzer(logger, config.ToolConfig{Timeout: time.Duration(cfg.Enricher.ContentAnalyzer.Timeout) * time.Second}, &fb.Llm, cfg.Enricher.ContentAnalyzer.PromptTemplate, reg)
				if fbErr != nil {
					logger.Error(nil, "create fallback content analyzer: %v", fbErr)
					continue
				}
				r.fallbackAnalyzers = append(r.fallbackAnalyzers, fbCa)
				r.fallbackMeta = append(r.fallbackMeta, fallbackMeta{Provider: fb.Llm.Provider, Model: fb.Llm.Model})
			}
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

func (r *Runner) ExtractText(ctx context.Context, path string, mimeType string) (*TextExtractionResult, error) {
	if r.textExtractor == nil {
		return nil, fmt.Errorf("text extractor not configured")
	}
	timeout := time.Duration(r.config.Consumer.TextExtractor.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %s", path)
	}

	text, err := runWithTimeout(ctx, func() (*string, error) {
		return r.textExtractor.Extract(ctx, path, mimeType)
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
	timeout := time.Duration(r.config.Consumer.OCR.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

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

func (r *Runner) ConvertToPdf(ctx context.Context, path string, mimeType string) (*string, error) {
	if r.documentConverter == nil {
		return nil, fmt.Errorf("document converter not configured")
	}
	timeout := time.Duration(r.config.Consumer.Converter.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %s", path)
	}

	outputPath, err := runWithTimeout(ctx, func() (*string, error) {
		return r.documentConverter.Convert(ctx, path, mimeType)
	})
	if err != nil {
		return nil, fmt.Errorf("document converter: %w", err)
	}

	return outputPath, nil
}

func (r *Runner) OptimizePdf(ctx context.Context, docId, path string) (*PdfOptimizationResult, error) {
	if r.pdfOptimizer == nil {
		return nil, fmt.Errorf("PDF optimizer not configured")
	}

	timeout := time.Duration(r.config.Consumer.PdfOptimizer.Timeout) * time.Second

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %s", path)
	}

	primaryCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		primaryCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	outputPath, err := runWithTimeout(primaryCtx, func() (*string, error) {
		return r.pdfOptimizer.Optimize(primaryCtx, docId, path)
	})
	if errors.Is(err, context.DeadlineExceeded) {
		r.logger.Error(&docId, "pdf optimizer (%s) timed out after %ds — underlying call abandoned, may still be running",
			r.config.Consumer.PdfOptimizer.Engine, r.config.Consumer.PdfOptimizer.Timeout)
	}
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

		fbCtx := ctx
		if timeout > 0 {
			var fbCancel context.CancelFunc
			fbCtx, fbCancel = context.WithTimeout(ctx, timeout)
			defer fbCancel()
		}
		outputPath, err = runWithTimeout(fbCtx, func() (*string, error) {
			return fbOptimizer.Optimize(fbCtx, docId, path)
		})
		if errors.Is(err, context.DeadlineExceeded) {
			r.logger.Error(&docId, "pdf optimizer fallback (%s) timed out after %ds — underlying call abandoned, may still be running",
				r.config.Consumer.PdfOptimizer.Fallback, r.config.Consumer.PdfOptimizer.Timeout)
		}
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

func (r *Runner) GenerateThumbnail(ctx context.Context, docId, path, outputPath string) (*ThumbnailResult, error) {
	if r.thumbnailer == nil {
		return nil, fmt.Errorf("thumbnailer not configured")
	}

	timeout := time.Duration(r.config.Consumer.Thumbnail.Timeout) * time.Second

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %s", path)
	}

	primaryCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		primaryCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	type thumbDims struct {
		width, height int
	}
	dims, err := runWithTimeout(primaryCtx, func() (thumbDims, error) {
		w, h, err := r.thumbnailer.Generate(primaryCtx, docId, path, outputPath)
		return thumbDims{w, h}, err
	})
	if err != nil {
		os.Remove(outputPath)
		return nil, fmt.Errorf("thumbnail: %w", err)
	}

	return &ThumbnailResult{
		Success: true,
		Width:   dims.width,
		Height:  dims.height,
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
	timeout := time.Duration(r.config.Enricher.TextReducer.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
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

func (r *Runner) MatchTags(ctx context.Context, docId, input string) (*TagMatchResult, error) {
	if r.tagMatcher == nil {
		return nil, fmt.Errorf("tag matcher not configured")
	}
	timeout := time.Duration(r.config.Enricher.TagMatcher.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	tags, err := runWithTimeout(ctx, func() ([]string, error) {
		return r.tagMatcher.Match(ctx, docId, input)
	})
	if err != nil {
		return nil, fmt.Errorf("tag matcher: %w", err)
	}
	return &TagMatchResult{Tags: tags}, nil
}

func (r *Runner) ConsolidateTags(ctx context.Context, docId string, queries []string) ([]string, error) {
	if r.tagMatcher == nil {
		return nil, fmt.Errorf("tag matcher not configured")
	}
	timeout := time.Duration(r.config.Enricher.TagMatcher.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	tags, err := runWithTimeout(ctx, func() ([]string, error) {
		return r.tagMatcher.Consolidate(ctx, docId, queries)
	})
	if err != nil {
		return nil, fmt.Errorf("tag consolidation: %w", err)
	}
	return tags, nil
}

func isProviderError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ctle *contentanalyzer.ContentTooLargeError
	var tokErr *contentanalyzer.TokenLimitError
	return !errors.As(err, &ctle) && !errors.As(err, &tokErr)
}

func (r *Runner) AnalyzeContent(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*ContentAnalysisResult, error) {
	if r.contentAnalyzer == nil {
		return nil, fmt.Errorf("content analyzer not configured")
	}
	timeout := time.Duration(r.config.Enricher.ContentAnalyzer.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	result, err := runWithTimeout(ctx, func() (*contentanalyzer.AnalysisResult, error) {
		return r.contentAnalyzer.Analyze(ctx, text, docTypes, peopleTypes, tagSuggestions)
	})
	if err != nil && isProviderError(err) {
		var reqID *string
		if v, ok := ctx.Value("reqid").(string); ok && v != "" {
			reqID = &v
		}
		r.logger.Warn(reqID, "primary analyzer (%s/%s) failed: %v",
			r.config.Enricher.ContentAnalyzer.Llm.Provider, r.config.Enricher.ContentAnalyzer.Llm.Model, err)
		for i, fb := range r.fallbackAnalyzers {
			fbProvider, fbModel := "?", "?"
			if i < len(r.fallbackMeta) {
				fbProvider, fbModel = r.fallbackMeta[i].Provider, r.fallbackMeta[i].Model
			}
			r.logger.Info(reqID, "trying fallback analyzer (%s/%s)", fbProvider, fbModel)
			result, err = runWithTimeout(ctx, func() (*contentanalyzer.AnalysisResult, error) {
				return fb.Analyze(ctx, text, docTypes, peopleTypes, tagSuggestions)
			})
			if err == nil {
				break
			}
			r.logger.Warn(reqID, "fallback analyzer (%s/%s) failed: %v", fbProvider, fbModel, err)
			if !isProviderError(err) {
				break
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("content analyzer: %w", err)
	}

	return &ContentAnalysisResult{
		Title:       result.Title,
		DocType:     result.DocType,
		Tags:        result.Tags,
		People:      result.People,
		Language:    result.Language,
		Stats:       result.Stats,
		Prompt:      result.Prompt,
		PassContext: result.PassContext,
	}, nil
}

func (r *Runner) AnalyzeDocType(ctx context.Context, prevResult *ContentAnalysisResult, headTailText string, docTypes []database.DocumentType, metadata contentanalyzer.DocMetadata) (string, error) {
	if r.contentAnalyzer == nil {
		return "", fmt.Errorf("content analyzer not configured")
	}
	timeout := time.Duration(r.config.Enricher.ContentAnalyzer.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	prev := &contentanalyzer.AnalysisResult{
		PassContext: prevResult.PassContext,
		Title:       prevResult.Title,
		DocType:     prevResult.DocType,
		Tags:        prevResult.Tags,
		People:      prevResult.People,
		Language:    prevResult.Language,
	}
	result, err := runWithTimeout(ctx, func() (string, error) {
		return r.contentAnalyzer.AnalyzeDocType(ctx, prev, headTailText, docTypes, metadata)
	})
	if err != nil && isProviderError(err) {
		var reqID *string
		if v, ok := ctx.Value("reqid").(string); ok && v != "" {
			reqID = &v
		}
		r.logger.Warn(reqID, "primary analyzer (%s/%s) failed doc-type refinement: %v",
			r.config.Enricher.ContentAnalyzer.Llm.Provider, r.config.Enricher.ContentAnalyzer.Llm.Model, err)
		for i, fb := range r.fallbackAnalyzers {
			fbProvider, fbModel := "?", "?"
			if i < len(r.fallbackMeta) {
				fbProvider, fbModel = r.fallbackMeta[i].Provider, r.fallbackMeta[i].Model
			}
			r.logger.Info(reqID, "trying fallback analyzer (%s/%s)", fbProvider, fbModel)
			result, err = runWithTimeout(ctx, func() (string, error) {
				return fb.AnalyzeDocType(ctx, prev, headTailText, docTypes, metadata)
			})
			if err == nil {
				break
			}
			r.logger.Warn(reqID, "fallback analyzer (%s/%s) failed: %v", fbProvider, fbModel, err)
			if !isProviderError(err) {
				break
			}
		}
	}
	if err != nil {
		return "", fmt.Errorf("doc type refinement: %w", err)
	}
	return result, nil
}

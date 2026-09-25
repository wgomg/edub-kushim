// Package tools is the runner facade over every external capability the
// pipelines need: text extraction, OCR, document conversion, PDF
// optimization, thumbnails, text reduction, tag matching, content analysis
// and the LLM tag judging endpoints. A Runner is constructed naming the
// capabilities it will use; the runner instantiates their adapters from the
// configuration, applies each tool's timeout as a context deadline, and for
// the analyzer calls walks the configured fallback chain on provider-level
// failures. A capability that was not requested has a nil adapter and its
// method fails fast.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/documents/converter"
	"github.com/wgomg/edub-kushim/internal/documents/ocr"
	"github.com/wgomg/edub-kushim/internal/documents/pdfoptimizer"
	"github.com/wgomg/edub-kushim/internal/documents/textextractor"
	"github.com/wgomg/edub-kushim/internal/documents/thumbnail"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tools/textreducer"
)

// Runner fronts the external capabilities a pipeline was built with.
// Construct it with NewRunner or NewRunnerWithMatcher naming the tools the
// pipeline needs; every method checks its capability was requested, applies
// the tool's configured timeout, and keeps the caller's context cancellation
// intact. A timeout hit abandons the underlying call, which may still be
// running; the runner logs that fact.
type Runner struct {
	logger            *log.Logger
	config            *config.Config
	textExtractor     textextractor.TextExtractor
	ocr               ocr.OCR
	pdfOptimizer      pdfoptimizer.PdfOptimizer
	thumbnailer       thumbnail.Thumbnailer
	documentConverter converter.DocumentConverter
	tagMatcher        matcher.Matcher
	contentAnalyzer   analyzer.ContentAnalyzer
	tagAdjudicator    analyzer.ContentAnalyzer
	tagQualityJudge   analyzer.ContentAnalyzer
	tagIdentityJudge  analyzer.ContentAnalyzer
	fallbackAnalyzers []analyzer.ContentAnalyzer
	fallbackMeta      []fallbackMeta
	textReducer       textreducer.TextReducer
}

type fallbackMeta struct {
	Provider string
	Model    string
}

// TextExtractionResult carries the extracted text; nil when nothing could
// be extracted.
type TextExtractionResult struct {
	Text *string
}

// OCRResult reports a run of OCR over a document: Success when it ran,
// TmpPath pointing at the generated searchable PDF (nil when no OCR was
// needed or it failed), and Confidence, the mean recognition confidence.
type OCRResult struct {
	Success    bool
	TmpPath    *string
	Confidence *float64
}

// PdfOptimizationResult reports an optimization run: Success and TmpPath,
// the rewritten file.
type PdfOptimizationResult struct {
	Success bool
	TmpPath *string
}

// ThumbnailResult reports the first-page thumbnail's success and pixel
// dimensions.
type ThumbnailResult struct {
	Success bool
	Width   int
	Height  int
}

// TextReducerResult is a reduction outcome: the text (reduced or original
// when no reduction was needed), its word and character counts, and the
// target the reduction aimed at.
type TextReducerResult struct {
	Text            string
	WordCount       int
	CharCount       int
	TargetWordCount int
}

// TagMatchResult carries the existing tag names matched for a document.
type TagMatchResult struct {
	Tags []string
}

// ContentAnalysisResult is the LLM analysis of a document: title, type,
// tags with per-tag detail, people, language, raw provider stats, the
// prompt that produced it, and the pass context carried across the
// multi-pass refinement (never serialized).
type ContentAnalysisResult struct {
	Title       string                  `json:"title"`
	DocType     string                  `json:"type"`
	Tags        []string                `json:"tags"`
	TagDetails  []analyzer.TagDetail    `json:"tag_details,omitempty"`
	People      []analyzer.PeopleResult `json:"people"`
	Language    string                  `json:"language"`
	Stats       *json.RawMessage        `json:"stats"`
	Prompt      string                  `json:"prompt"`
	PassContext *json.RawMessage        `json:"-"`
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

// NewRunner instantiates the adapters for the named tools from cfg. The
// names are the capability keys below ("textextractor", "ocr",
// "converter", "pdfoptimizer", "thumbnail", "textreducer",
// "contentanalyzer", "tagmatcher"); an unknown name is logged and skipped.
// The model catalog is loaded from the config directory; a load failure is
// logged, leaving the analyzer adapters unconfigured.
func NewRunner(logger *log.Logger, cfg *config.Config, tools []string) *Runner {
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
			ocrWorkers := cfg.Consumer.OCR.OcrWorkers
			if ocrWorkers <= 0 {
				ocrWorkers = max(1, runtime.NumCPU()/
					max(cfg.Srv.MaxConcurrentBatches, 1)/max(cfg.Consumer.Workers, 1))
			}
			r.ocr, _ = ocr.NewOCR(logger, toolCfg, cfg.Consumer.PdfOptimizer.Engine, cfg.Consumer.OCR.Languages, cfg.Consumer.OCR.DataDir, ocrWorkers)
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
			ca, caErr := analyzer.NewContentAnalyzer(logger, config.ToolConfig{Timeout: time.Duration(cfg.Enricher.ContentAnalyzer.Timeout) * time.Second}, &cfg.Enricher.ContentAnalyzer.Llm, cfg.Enricher.ContentAnalyzer.PromptTemplate, cfg.Enricher.ContentAnalyzer.MinTags, cfg.Enricher.ContentAnalyzer.MaxTags, reg)
			if caErr != nil {
				logger.Error(nil, "create content analyzer: %v", caErr)
			}
			r.contentAnalyzer = ca
			if cfg.Enricher.TagAdjudicator.Enabled {
				adjLlm := cfg.ResolvedAdjudicatorLlm()
				adj, adjErr := analyzer.NewContentAnalyzer(logger, config.ToolConfig{Timeout: time.Duration(cfg.ResolvedAdjudicatorTimeout()) * time.Second}, &adjLlm, cfg.Enricher.ContentAnalyzer.PromptTemplate, 0, 0, reg)
				if adjErr != nil {
					logger.Error(nil, "create tag adjudicator: %v", adjErr)
				} else {
					r.tagAdjudicator = adj
					r.tagIdentityJudge = adj
				}
			}
			if cfg.Enricher.TagQuality.Enabled {
				qLlm := cfg.ResolvedQualityLlm()
				q, qErr := analyzer.NewContentAnalyzer(logger, config.ToolConfig{Timeout: time.Duration(cfg.ResolvedQualityTimeout()) * time.Second}, &qLlm, cfg.Enricher.ContentAnalyzer.PromptTemplate, 0, 0, reg)
				if qErr != nil {
					logger.Error(nil, "create tag quality judge: %v", qErr)
				} else {
					r.tagQualityJudge = q
				}
			}
			for i := range cfg.Enricher.ContentAnalyzer.Fallbacks {
				fb := &cfg.Enricher.ContentAnalyzer.Fallbacks[i]
				if !fb.Enabled {
					continue
				}
				fbCa, fbErr := analyzer.NewContentAnalyzer(logger, config.ToolConfig{Timeout: time.Duration(cfg.Enricher.ContentAnalyzer.Timeout) * time.Second}, &fb.Llm, cfg.Enricher.ContentAnalyzer.PromptTemplate, cfg.Enricher.ContentAnalyzer.MinTags, cfg.Enricher.ContentAnalyzer.MaxTags, reg)
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

// NewRunnerWithMatcher is NewRunner plus an explicit Matcher, for callers
// that already hold the right matcher implementation: the engine in-process
// in the daemon, or the socket client in the API process.
func NewRunnerWithMatcher(logger *log.Logger, cfg *config.Config, tools []string, matcher matcher.Matcher) *Runner {
	r := NewRunner(logger, cfg, tools)
	if matcher != nil && slices.Contains(tools, "tagmatcher") {
		r.tagMatcher = matcher
	}
	return r
}

// ExtractText extracts a document's text with the configured extractor
// chain for its MIME type. The path must exist; the returned Text pointer
// is nil when the document yields no text.
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

// OCR runs OCR over a PDF, rendering pages and writing an invisible-text
// overlay so the result is searchable. The path must exist; TmpPath in the
// result points at the generated PDF.
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

// ConvertToPdf converts an office document to PDF, returning the temp path
// of the converted file. The path must exist.
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

// OptimizePdf rewrites a PDF through the configured optimizer into a temp
// path. When the primary engine fails and a fallback is configured, the
// fallback runs on its own fresh timeout; both failures are returned
// together. The path must exist.
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

// GenerateThumbnail renders the document's first page as a thumbnail at
// outputPath, removing the output file when rendering fails. The path must
// exist.
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
		_ = os.Remove(outputPath)
		return nil, fmt.Errorf("thumbnail: %w", err)
	}

	return &ThumbnailResult{
		Success: true,
		Width:   dims.width,
		Height:  dims.height,
	}, nil
}

// ReduceContent compresses content toward targetWordCount words, chunked
// at chunkSize tokens, with the configured TextRank reducer. Content already
// at or below the target, or a zero target, is returned unchanged with a
// nil error; a missing reducer is an error only when reduction was needed.
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

// MatchTags matches a document's reduced text against the stored tag
// vocabulary.
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

// RankTags ranks candidate tag names against the stored vocabulary,
// returning per-query candidates and kept names.
func (r *Runner) RankTags(ctx context.Context, docId string, queries []string) ([]matcher.RankResult, error) {
	if r.tagMatcher == nil {
		return nil, fmt.Errorf("tag matcher not configured")
	}
	timeout := time.Duration(r.config.Enricher.TagMatcher.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	results, err := runWithTimeout(ctx, func() ([]matcher.RankResult, error) {
		return r.tagMatcher.Rank(ctx, docId, queries)
	})
	if err != nil {
		return nil, fmt.Errorf("tag consolidation: %w", err)
	}
	return results, nil
}

// HasTagAdjudicator reports whether an LLM tag adjudicator is configured;
// callers gate the judging steps on it.
func (r *Runner) HasTagAdjudicator() bool {
	return r.tagAdjudicator != nil
}

// AdjudicateTags asks the LLM adjudicator to decide a set of tag pairs
// (which of two similar tags to keep). Returns an error when no adjudicator
// was configured.
func (r *Runner) AdjudicateTags(ctx context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
	if r.tagAdjudicator == nil {
		return nil, fmt.Errorf("tag adjudicator not configured")
	}
	timeout := time.Duration(r.config.ResolvedAdjudicatorTimeout()) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return runWithTimeout(ctx, func() ([]analyzer.TagVerdictResult, error) {
		return r.tagAdjudicator.Adjudicate(ctx, pairs)
	})
}

// AdjudicateTagSplits asks the LLM adjudicator whether a compound tag
// should be split. Returns an error when no adjudicator was configured.
func (r *Runner) AdjudicateTagSplits(ctx context.Context, tags []string) ([]analyzer.TagSplitResult, error) {
	if r.tagAdjudicator == nil {
		return nil, fmt.Errorf("tag adjudicator not configured")
	}
	timeout := time.Duration(r.config.ResolvedAdjudicatorTimeout()) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return runWithTimeout(ctx, func() ([]analyzer.TagSplitResult, error) {
		return r.tagAdjudicator.AdjudicateTagSplits(ctx, tags)
	})
}

// HasTagQualityJudge reports whether a tag quality judge is configured.
func (r *Runner) HasTagQualityJudge() bool {
	return r.tagQualityJudge != nil
}

// JudgeTagQuality scores candidate tags against a document's text.
// Returns an error when no judge was configured.
func (r *Runner) JudgeTagQuality(ctx context.Context, text string, candidates []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
	if r.tagQualityJudge == nil {
		return nil, fmt.Errorf("tag quality judge not configured")
	}
	timeout := time.Duration(r.config.ResolvedQualityTimeout()) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return runWithTimeout(ctx, func() ([]analyzer.TagQualityResult, error) {
		return r.tagQualityJudge.JudgeTagQuality(ctx, text, candidates)
	})
}

// HasTagIdentityJudge reports whether a tag identity judge is configured.
func (r *Runner) HasTagIdentityJudge() bool {
	return r.tagIdentityJudge != nil
}

// JudgeTagIdentity lets the LLM judge the identity of candidate tags
// against the existing vocabulary. Returns an error when no judge was
// configured.
func (r *Runner) JudgeTagIdentity(ctx context.Context, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
	if r.tagIdentityJudge == nil {
		return nil, fmt.Errorf("tag identity judge not configured")
	}
	timeout := time.Duration(r.config.ResolvedAdjudicatorTimeout()) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return runWithTimeout(ctx, func() ([]analyzer.TagIdentityResult, error) {
		return r.tagIdentityJudge.JudgeTagIdentity(ctx, candidates)
	})
}

func isProviderError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ctle *analyzer.ContentTooLargeError
	var tokErr *analyzer.TokenLimitError
	return !errors.As(err, &ctle) && !errors.As(err, &tokErr)
}

// AnalyzeContent analyzes a document's reduced text with the primary
// analyzer: title, doc type, tags, people and language. On a provider-level
// error (auth, quota, availability) it walks the configured fallback
// analyzers, returning the last failure when none succeeds. Content-too-
// large, token-limit, cancellation and deadline errors are never retried on
// a fallback.
func (r *Runner) AnalyzeContent(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*ContentAnalysisResult, error) {
	if r.contentAnalyzer == nil {
		return nil, fmt.Errorf("content analyzer not configured")
	}
	timeout := time.Duration(r.config.Enricher.ContentAnalyzer.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	result, err := runWithTimeout(ctx, func() (*analyzer.AnalysisResult, error) {
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
			result, err = runWithTimeout(ctx, func() (*analyzer.AnalysisResult, error) {
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
		TagDetails:  result.TagDetails,
		People:      result.People,
		Language:    result.Language,
		Stats:       result.Stats,
		Prompt:      result.Prompt,
		PassContext: result.PassContext,
	}, nil
}

// AnalyzeDocType refines the document type from the head and tail of the
// text, given the previous analysis pass. It follows the same fallback
// chain rules as AnalyzeContent.
func (r *Runner) AnalyzeDocType(ctx context.Context, prevResult *ContentAnalysisResult, headTailText string, docTypes []analyzer.TypeRef, metadata analyzer.DocMetadata) (string, error) {
	if r.contentAnalyzer == nil {
		return "", fmt.Errorf("content analyzer not configured")
	}
	timeout := time.Duration(r.config.Enricher.ContentAnalyzer.Timeout) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	prev := &analyzer.AnalysisResult{
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

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/documents/converter"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tools/textreducer"
)

// mockPdfOptimizer records context behavior for each Optimize call.
type mockPdfOptimizer struct {
	optimizeFn func(ctx context.Context, docId, path string) (*string, error)
}

func (m *mockPdfOptimizer) Optimize(ctx context.Context, docId, path string) (*string, error) {
	if m.optimizeFn != nil {
		return m.optimizeFn(ctx, docId, path)
	}
	out := path + ".opt"
	return &out, nil
}

func (m *mockPdfOptimizer) Name() string { return "mock" }

func newTestPDF(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.pdf")
	if err := os.WriteFile(p, []byte("pdf"), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOptimizePdf_NoTimeout_ContextHasNoDeadline(t *testing.T) {
	var receivedCtx context.Context
	mock := &mockPdfOptimizer{
		optimizeFn: func(ctx context.Context, docId, path string) (*string, error) {
			receivedCtx = ctx
			out := path + ".opt"
			return &out, nil
		},
	}

	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Consumer: config.ConsumerConfig{
				PdfOptimizer: config.PdfOptimizerConfig{Timeout: 0},
			},
		},
		pdfOptimizer: mock,
	}

	result, err := r.OptimizePdf(context.Background(), "doc-1", newTestPDF(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatal("expected success")
	}
	if _, ok := receivedCtx.Deadline(); ok {
		t.Error("context should have no deadline when timeout=0")
	}
}

func TestOptimizePdf_WithTimeout_ContextHasDeadline(t *testing.T) {
	var receivedCtx context.Context
	mock := &mockPdfOptimizer{
		optimizeFn: func(ctx context.Context, docId, path string) (*string, error) {
			receivedCtx = ctx
			out := path + ".opt"
			return &out, nil
		},
	}

	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Consumer: config.ConsumerConfig{
				PdfOptimizer: config.PdfOptimizerConfig{Timeout: 120},
			},
		},
		pdfOptimizer: mock,
	}

	result, err := r.OptimizePdf(context.Background(), "doc-1", newTestPDF(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatal("expected success")
	}
	if _, ok := receivedCtx.Deadline(); !ok {
		t.Error("context should have a deadline when timeout>0")
	}
}

// mockContentAnalyzer records the context for each analysis call.
type mockContentAnalyzer struct {
	analyzeFn         func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error)
	analyzeDocTypeFn  func(ctx context.Context, prevResult *analyzer.AnalysisResult, headTailText string, docTypes []analyzer.TypeRef, metadata analyzer.DocMetadata) (string, error)
	adjudicateFn      func(ctx context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error)
	judgeTagPartsFn   func(ctx context.Context, values []string) ([]analyzer.TagPartResult, error)
	judgeTagQualityFn func(ctx context.Context, text string, candidates []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error)
	judgeTagAuditFn   func(ctx context.Context, text string, candidates []analyzer.TagAuditCandidate) ([]analyzer.TagAuditResult, error)
}

func (m *mockContentAnalyzer) Analyze(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
	if m.analyzeFn != nil {
		return m.analyzeFn(ctx, text, docTypes, peopleTypes, tagSuggestions)
	}
	return &analyzer.AnalysisResult{}, nil
}

func (m *mockContentAnalyzer) AnalyzeDocType(ctx context.Context, prevResult *analyzer.AnalysisResult, headTailText string, docTypes []analyzer.TypeRef, metadata analyzer.DocMetadata) (string, error) {
	if m.analyzeDocTypeFn != nil {
		return m.analyzeDocTypeFn(ctx, prevResult, headTailText, docTypes, metadata)
	}
	return "document", nil
}

func (m *mockContentAnalyzer) Endpoint() (string, string) {
	return "", ""
}

func (m *mockContentAnalyzer) Adjudicate(ctx context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
	if m.adjudicateFn != nil {
		return m.adjudicateFn(ctx, pairs)
	}
	return nil, nil
}

func (m *mockContentAnalyzer) JudgeTagParts(ctx context.Context, values []string) ([]analyzer.TagPartResult, error) {
	if m.judgeTagPartsFn != nil {
		return m.judgeTagPartsFn(ctx, values)
	}
	return nil, nil
}

func (m *mockContentAnalyzer) JudgeTagCompounds(_ context.Context, _ []string) ([]analyzer.TagCompoundResult, error) {
	return nil, nil
}

func (m *mockContentAnalyzer) JudgeTagConflicts(_ context.Context, _ []analyzer.TagConflictCandidate) ([]analyzer.TagConflictResult, error) {
	return nil, nil
}

func (m *mockContentAnalyzer) JudgeTagQuality(ctx context.Context, text string, candidates []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
	if m.judgeTagQualityFn != nil {
		return m.judgeTagQualityFn(ctx, text, candidates)
	}
	return nil, nil
}

func (m *mockContentAnalyzer) JudgeTagIdentity(_ context.Context, _ []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
	return nil, nil
}

func (m *mockContentAnalyzer) JudgeTagRelations(_ context.Context, _ []analyzer.TagRelationCandidate) ([]analyzer.TagRelationResult, error) {
	return nil, nil
}

func (m *mockContentAnalyzer) JudgeTagTranslations(_ context.Context, _ []string) ([]analyzer.TagTranslationResult, error) {
	return nil, nil
}

func (m *mockContentAnalyzer) JudgeTagAudit(ctx context.Context, text string, candidates []analyzer.TagAuditCandidate) ([]analyzer.TagAuditResult, error) {
	if m.judgeTagAuditFn != nil {
		return m.judgeTagAuditFn(ctx, text, candidates)
	}
	return nil, nil
}

func (m *mockContentAnalyzer) JudgeTagPhraseFolds(_ context.Context, _ []analyzer.TagPhraseFoldCandidate) ([]analyzer.TagPhraseFoldResult, error) {
	return nil, nil
}

// TestJudgeTagParts_MissingAdapter covers the judged-call path the split gate's parts pass uses: a runner
// without a gate fails fast instead of returning an empty batch that would read as a judge outage.
func TestJudgeTagParts_MissingAdapter(t *testing.T) {
	t.Run("no gate is a not-configured failure", func(t *testing.T) {
		r := &Runner{
			logger: log.NewDiscardLogger(),
			config: &config.Config{},
		}
		if _, err := r.JudgeTagParts(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Errorf("error = %v, want a not-configured failure", err)
		}
		if _, err := r.JudgeTagCompounds(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "tag lexical gate not configured") {
			t.Errorf("error = %v, want the gate identity in the failure", err)
		}
		if _, err := r.JudgeTagTranslations(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "tag lexical gate not configured") {
			t.Errorf("error = %v, want the gate identity in the failure", err)
		}
	})
}

// TestJudgeTagQuality_AuditSharesTheDocumentJudge pins the audit move's contract: quality and audit call
// the same instance, while the adjudicator's pairs and identity calls keep their own.
func TestJudgeTagQuality_AuditSharesTheDocumentJudge(t *testing.T) {
	t.Run("quality and audit hit the same document judge", func(t *testing.T) {
		qualityCalls, auditCalls := 0, 0
		mock := &mockContentAnalyzer{
			judgeTagQualityFn: func(context.Context, string, []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
				qualityCalls++
				return []analyzer.TagQualityResult{}, nil
			},
			judgeTagAuditFn: func(context.Context, string, []analyzer.TagAuditCandidate) ([]analyzer.TagAuditResult, error) {
				auditCalls++
				return []analyzer.TagAuditResult{}, nil
			},
		}
		r := &Runner{
			logger:          log.NewDiscardLogger(),
			config:          &config.Config{},
			tagQualityJudge: mock,
		}

		if _, err := r.JudgeTagQuality(context.Background(), "", nil); err != nil {
			t.Fatalf("unexpected quality error: %v", err)
		}
		if _, err := r.JudgeTagAudit(context.Background(), "", nil); err != nil {
			t.Fatalf("unexpected audit error: %v", err)
		}
		if qualityCalls != 1 || auditCalls != 1 {
			t.Errorf("calls = (quality %d, audit %d), want one each on the shared judge", qualityCalls, auditCalls)
		}
	})

	t.Run("no judge fails both document calls the same way", func(t *testing.T) {
		r := &Runner{logger: log.NewDiscardLogger(), config: &config.Config{}}
		if _, err := r.JudgeTagQuality(context.Background(), "", nil); err == nil || !strings.Contains(err.Error(), "tag quality judge not configured") {
			t.Errorf("error = %v, want the document judge identity", err)
		}
		if _, err := r.JudgeTagAudit(context.Background(), "", nil); err == nil || !strings.Contains(err.Error(), "tag quality judge not configured") {
			t.Errorf("error = %v, want the document judge identity", err)
		}
	})
}

func TestOptimizePdf_FileNotFound(t *testing.T) {
	mock := &mockPdfOptimizer{}
	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Consumer: config.ConsumerConfig{
				PdfOptimizer: config.PdfOptimizerConfig{Timeout: 0},
			},
		},
		pdfOptimizer: mock,
	}

	_, err := r.OptimizePdf(context.Background(), "doc-1", "/tmp/nonexistent-test-file.pdf")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestOptimizePdf_ParentCancellation_StopsExecution(t *testing.T) {
	started := make(chan struct{})
	mock := &mockPdfOptimizer{
		optimizeFn: func(ctx context.Context, docId, path string) (*string, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Consumer: config.ConsumerConfig{
				PdfOptimizer: config.PdfOptimizerConfig{Timeout: 0},
			},
		},
		pdfOptimizer: mock,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := r.OptimizePdf(ctx, "doc-1", newTestPDF(t))
		done <- err
	}()

	<-started
	cancel()

	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestOptimizePdf_NilOptimizer(t *testing.T) {
	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Consumer: config.ConsumerConfig{
				PdfOptimizer: config.PdfOptimizerConfig{Timeout: 0},
			},
		},
	}

	_, err := r.OptimizePdf(context.Background(), "doc-1", newTestPDF(t))
	if err == nil {
		t.Fatal("expected error when optimizer is nil")
	}
}

func TestAnalyzeDocType_NilContentAnalyzer(t *testing.T) {
	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{},
	}

	_, err := r.AnalyzeDocType(context.Background(), nil, "head tail text", nil, analyzer.DocMetadata{})
	if err == nil {
		t.Fatal("expected error when content analyzer is nil")
	}
}

// mockDocumentConverter satisfies converter.DocumentConverter for testing.
type mockDocumentConverter struct {
	convertFn func(ctx context.Context, path string, mimeType string) (*string, error)
}

func (m *mockDocumentConverter) Convert(ctx context.Context, path string, mimeType string) (*string, error) {
	if m.convertFn != nil {
		return m.convertFn(ctx, path, mimeType)
	}
	out := path + ".converted.pdf"
	return &out, nil
}

func (m *mockDocumentConverter) CanHandle(mimeType string) bool { return true }
func (m *mockDocumentConverter) Name() string                   { return "mock" }

// Compile-time assertion that mockDocumentConverter satisfies the interface.
var _ converter.DocumentConverter = (*mockDocumentConverter)(nil)

func TestConvertToPdf_NilConverter(t *testing.T) {
	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Consumer: config.ConsumerConfig{
				Converter: config.DocxOdtConverterConfig{Timeout: 0},
			},
		},
	}

	_, err := r.ConvertToPdf(context.Background(), newTestPDF(t), "application/docx")
	if err == nil {
		t.Fatal("expected error when converter is nil")
	}
}

func TestConvertToPdf_FileNotFound(t *testing.T) {
	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Consumer: config.ConsumerConfig{
				Converter: config.DocxOdtConverterConfig{Timeout: 0},
			},
		},
	}

	_, err := r.ConvertToPdf(context.Background(), "/tmp/nonexistent-test-file.pdf", "application/docx")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestConvertToPdf_WithConverter(t *testing.T) {
	mock := &mockDocumentConverter{}
	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Consumer: config.ConsumerConfig{
				Converter: config.DocxOdtConverterConfig{Timeout: 0},
			},
		},
		documentConverter: mock,
	}

	result, err := r.ConvertToPdf(context.Background(), newTestPDF(t), "application/docx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestConvertToPdf_WithTimeout_ContextHasDeadline(t *testing.T) {
	var receivedCtx context.Context
	mock := &mockDocumentConverter{
		convertFn: func(ctx context.Context, path string, mimeType string) (*string, error) {
			receivedCtx = ctx
			out := path + ".converted.pdf"
			return &out, nil
		},
	}

	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Consumer: config.ConsumerConfig{
				Converter: config.DocxOdtConverterConfig{Timeout: 120},
			},
		},
		documentConverter: mock,
	}

	result, err := r.ConvertToPdf(context.Background(), newTestPDF(t), "application/docx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	if _, ok := receivedCtx.Deadline(); !ok {
		t.Error("context should have a deadline when timeout>0")
	}
}

func TestClassifyLlmError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want analyzer.LlmErrorAction
	}{
		{"generic error", errors.New("boom"), analyzer.LlmRetry},
		{"insufficient credits", &analyzer.InsufficientCreditsError{Provider: "p", HTTPStatus: 402}, analyzer.LlmNextEndpoint},
		{"wrapped insufficient credits", fmt.Errorf("wrapped: %w", &analyzer.InsufficientCreditsError{Provider: "p", HTTPStatus: 429}), analyzer.LlmNextEndpoint},
		{"content too large", &analyzer.ContentTooLargeError{EstimatedTokens: 10, MaxInputTokens: 5}, analyzer.LlmAbort},
		{"wrapped content too large", fmt.Errorf("wrapped: %w", &analyzer.ContentTooLargeError{EstimatedTokens: 10, MaxInputTokens: 5}), analyzer.LlmAbort},
		{"token limit", &analyzer.TokenLimitError{MaxTokens: 100, RequestedTokens: 200}, analyzer.LlmAbort},
		{"wrapped token limit", fmt.Errorf("wrapped: %w", &analyzer.TokenLimitError{MaxTokens: 100, RequestedTokens: 200}), analyzer.LlmAbort},
		{"context canceled", context.Canceled, analyzer.LlmAbort},
		{"wrapped context canceled", fmt.Errorf("wrapped: %w", context.Canceled), analyzer.LlmAbort},
		{"context deadline exceeded", context.DeadlineExceeded, analyzer.LlmRetry},
		{"wrapped context deadline exceeded", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), analyzer.LlmRetry},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := analyzer.ClassifyLlmError(tt.err); got != tt.want {
				t.Errorf("ClassifyLlmError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRunJudged_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	call := func(_ context.Context) ([]analyzer.TagVerdictResult, error) {
		calls++
		if calls <= 2 {
			return nil, errors.New("API error: status 500")
		}
		return []analyzer.TagVerdictResult{{Index: 0}}, nil
	}

	results, err := runJudged(context.Background(), 3, call)
	if err != nil {
		t.Fatalf("runJudged() error = %v", err)
	}
	if len(results) != 1 {
		t.Errorf("runJudged() results = %v, want one verdict", results)
	}
	if calls != 3 {
		t.Errorf("judge calls = %d, want 3 (two transient failures then the successful third attempt)", calls)
	}
}

func TestRunJudged_DeadlineIsRetried(t *testing.T) {
	calls := 0
	call := func(_ context.Context) ([]analyzer.TagVerdictResult, error) {
		calls++
		if calls <= 2 {
			return nil, context.DeadlineExceeded
		}
		return []analyzer.TagVerdictResult{{Index: 0}}, nil
	}

	results, err := runJudged(context.Background(), 3, call)
	if err != nil {
		t.Fatalf("runJudged() error = %v", err)
	}
	if len(results) != 1 {
		t.Errorf("runJudged() results = %v, want one verdict", results)
	}
	if calls != 3 {
		t.Errorf("judge calls = %d, want 3 (a timed-out attempt is retried under a fresh deadline)", calls)
	}
}

func TestRunJudged_NonRetryableStops(t *testing.T) {
	verdicts := []analyzer.TagVerdictResult{{Index: 0}}
	tests := []struct {
		name      string
		err       error
		wantCalls int
	}{
		{"credit exhaustion", &analyzer.InsufficientCreditsError{Provider: "p", HTTPStatus: 402}, 1},
		{"incomplete reply", &analyzer.IncompleteReplyError{Missing: []int{1}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			call := func(_ context.Context) ([]analyzer.TagVerdictResult, error) {
				calls++
				return verdicts, tt.err
			}

			results, err := runJudged(context.Background(), 3, call)
			if err == nil {
				t.Fatal("runJudged() error = nil, want the non-retryable failure")
			}
			if calls != tt.wantCalls {
				t.Errorf("judge calls = %d, want %d (no retry)", calls, tt.wantCalls)
			}
			if len(results) != len(verdicts) {
				t.Errorf("runJudged() results = %v, want the last attempt's entries preserved for salvage", results)
			}
		})
	}
}

func TestAnalyzeContent_RetriesPrimaryThenSucceeds(t *testing.T) {
	primaryCalls := 0
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			primaryCalls++
			if primaryCalls <= 2 {
				return nil, errors.New("provider exploded")
			}
			return &analyzer.AnalysisResult{Title: "third try"}, nil
		},
	}

	r := runnerWithFallback(primary, nil)
	r.config.Enricher.ContentAnalyzer.MaxRetries = 2
	result, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err != nil {
		t.Fatalf("AnalyzeContent() error = %v", err)
	}
	if result.Title != "third try" {
		t.Errorf("AnalyzeContent() title = %q, want the retried primary's result", result.Title)
	}
	if primaryCalls != 3 {
		t.Errorf("primary calls = %d, want 3 (two transient failures then success, no fallback involved)", primaryCalls)
	}
}

func TestAnalyzeContent_CreditSkipsRemainingRetries(t *testing.T) {
	primaryCalls := 0
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			primaryCalls++
			return nil, &analyzer.InsufficientCreditsError{Provider: "primary", HTTPStatus: 402}
		},
	}
	fallbackCalls := 0
	fallback := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			fallbackCalls++
			return &analyzer.AnalysisResult{Title: "from fallback"}, nil
		},
	}

	r := runnerWithFallback(primary, fallback)
	r.config.Enricher.ContentAnalyzer.MaxRetries = 3
	result, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err != nil {
		t.Fatalf("AnalyzeContent() error = %v", err)
	}
	if result.Title != "from fallback" {
		t.Errorf("AnalyzeContent() title = %q, want the fallback's result", result.Title)
	}
	if primaryCalls != 1 {
		t.Errorf("primary calls = %d, want 1 (a credit error skips the remaining retries)", primaryCalls)
	}
	if fallbackCalls != 1 {
		t.Errorf("fallback calls = %d, want 1 (the walk moves straight to the fallback)", fallbackCalls)
	}
}

func runnerWithFallback(primary, fallback analyzer.Analyzer) *Runner {
	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Enricher: config.EnricherConfig{
				ContentAnalyzer: config.ContentAnalyzerConfig{
					Timeout: 0,
					Llm:     config.LlmConfig{Provider: "primary", Model: "p1"},
					Fallbacks: []config.FallbackConfig{
						{
							Enabled: true,
							Llm:     config.LlmConfig{Provider: "fallback", Model: "f1"},
						},
					},
				},
			},
		},
		contentAnalyzer: primary,
	}
	if fallback != nil {
		r.fallbackAnalyzers = []analyzer.Analyzer{fallback}
	}
	return r
}

func TestAnalyzeContent_FallbackOnProviderError(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, &analyzer.InsufficientCreditsError{Provider: "primary", HTTPStatus: 402}
		},
	}
	fallback := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return &analyzer.AnalysisResult{Title: "from fallback"}, nil
		},
	}

	r := runnerWithFallback(primary, fallback)
	result, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Title != "from fallback" {
		t.Errorf("expected result from fallback analyzer, got %q", result.Title)
	}
}

func TestAnalyzeContent_NoFallbackOnContentTooLarge(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, &analyzer.ContentTooLargeError{EstimatedTokens: 10, MaxInputTokens: 5}
		},
	}
	fallbackCalled := false
	fallback := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			fallbackCalled = true
			return &analyzer.AnalysisResult{Title: "fallback"}, nil
		},
	}

	r := runnerWithFallback(primary, fallback)
	_, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error from primary")
	}
	if fallbackCalled {
		t.Error("fallback must not be called for ContentTooLargeError")
	}
	if _, ok := errors.AsType[*analyzer.ContentTooLargeError](err); !ok {
		t.Errorf("expected ContentTooLargeError through wrap, got %v", err)
	}
}

func TestAnalyzeContent_NoFallbackOnCancellation(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, context.Canceled
		},
	}
	fallbackCalled := false
	fallback := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			fallbackCalled = true
			return &analyzer.AnalysisResult{}, nil
		},
	}

	r := runnerWithFallback(primary, fallback)
	_, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error from primary")
	}
	if fallbackCalled {
		t.Error("fallback must not be called for context.Canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled through wrap, got %v", err)
	}
}

func TestAnalyzeContent_DeadlineRetriesThenFailsOver(t *testing.T) {
	newRunner := func(t *testing.T, maxRetries int) (*Runner, *int, *bool) {
		t.Helper()
		primaryCalls := 0
		primary := &mockContentAnalyzer{
			analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
				primaryCalls++
				return nil, context.DeadlineExceeded
			},
		}
		fallbackCalled := false
		fallback := &mockContentAnalyzer{
			analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
				fallbackCalled = true
				return &analyzer.AnalysisResult{Title: "from fallback"}, nil
			},
		}
		r := runnerWithFallback(primary, fallback)
		r.config.Enricher.ContentAnalyzer.MaxRetries = maxRetries
		return r, &primaryCalls, &fallbackCalled
	}

	t.Run("retries then fails over", func(t *testing.T) {
		r, primaryCalls, fallbackCalled := newRunner(t, 2)
		result, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
		if err != nil {
			t.Fatalf("AnalyzeContent() error = %v", err)
		}
		if result.Title != "from fallback" {
			t.Errorf("title = %q, want the fallback result after the retry budget", result.Title)
		}
		if *primaryCalls != 3 {
			t.Errorf("primary calls = %d, want 3 (one attempt plus two retries under fresh deadlines)", *primaryCalls)
		}
		if !*fallbackCalled {
			t.Error("fallback must be tried once the retry budget is exhausted")
		}
	})

	t.Run("budget zero walks straight to the fallback", func(t *testing.T) {
		r, primaryCalls, fallbackCalled := newRunner(t, 0)
		result, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
		if err != nil {
			t.Fatalf("AnalyzeContent() error = %v", err)
		}
		if result.Title != "from fallback" {
			t.Errorf("title = %q, want the fallback result", result.Title)
		}
		if *primaryCalls != 1 {
			t.Errorf("primary calls = %d, want 1 with the retry budget off", *primaryCalls)
		}
		if !*fallbackCalled {
			t.Error("fallback must be tried when a deadline settles the endpoint")
		}
	})
}

func TestAnalyzeContent_NoFallbackAnalyzer(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, errors.New("provider exploded")
		},
	}

	r := runnerWithFallback(primary, nil)
	_, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when no fallback is configured")
	}
}

func TestAnalyzeContent_FallbackFailurePropagates(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, errors.New("primary network failure")
		},
	}
	fallback := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, &analyzer.InsufficientCreditsError{Provider: "fallback", HTTPStatus: 402}
		},
	}

	r := runnerWithFallback(primary, fallback)
	_, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when fallback fails")
	}
	var credErr *analyzer.InsufficientCreditsError
	if !errors.As(err, &credErr) {
		t.Fatalf("expected fallback InsufficientCreditsError through wrap, got %v", err)
	}
	if credErr.Provider != "fallback" {
		t.Errorf("expected provider %q in error, got %q", "fallback", credErr.Provider)
	}
}

func TestAnalyzeDocType_FallbackOnProviderError(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeDocTypeFn: func(ctx context.Context, prevResult *analyzer.AnalysisResult, headTailText string, docTypes []analyzer.TypeRef, metadata analyzer.DocMetadata) (string, error) {
			return "", errors.New("provider timeout")
		},
	}
	fallback := &mockContentAnalyzer{
		analyzeDocTypeFn: func(ctx context.Context, prevResult *analyzer.AnalysisResult, headTailText string, docTypes []analyzer.TypeRef, metadata analyzer.DocMetadata) (string, error) {
			return "from fallback", nil
		},
	}

	r := runnerWithFallback(primary, fallback)
	got, err := r.AnalyzeDocType(context.Background(), &ContentAnalysisResult{}, "head tail", nil, analyzer.DocMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from fallback" {
		t.Errorf("expected result from fallback analyzer, got %q", got)
	}
}

func TestAnalyzeContent_FallbackChainWalksToSuccess(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, errors.New("primary timeout")
		},
	}
	fb0 := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, errors.New("fb0 timeout")
		},
	}
	fb1 := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return &analyzer.AnalysisResult{Title: "from fb1"}, nil
		},
	}

	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Enricher: config.EnricherConfig{
				ContentAnalyzer: config.ContentAnalyzerConfig{
					Timeout: 0,
					Llm:     config.LlmConfig{Provider: "primary", Model: "p1"},
					Fallbacks: []config.FallbackConfig{
						{Enabled: true, Llm: config.LlmConfig{Provider: "fb0", Model: "f0"}},
						{Enabled: true, Llm: config.LlmConfig{Provider: "fb1", Model: "f1"}},
					},
				},
			},
		},
		contentAnalyzer:   primary,
		fallbackAnalyzers: []analyzer.Analyzer{fb0, fb1},
	}

	result, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Title != "from fb1" {
		t.Errorf("expected result from fb1, got %q", result.Title)
	}
}

func TestAnalyzeContent_FallbackChainPropagatesLastFailure(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, errors.New("primary timeout")
		},
	}
	fb0 := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, errors.New("fb0 timeout")
		},
	}
	fb1 := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []analyzer.TypeRef, peopleTypes []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
			return nil, &analyzer.InsufficientCreditsError{Provider: "fb1", HTTPStatus: 402}
		},
	}

	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Enricher: config.EnricherConfig{
				ContentAnalyzer: config.ContentAnalyzerConfig{
					Timeout: 0,
					Llm:     config.LlmConfig{Provider: "primary", Model: "p1"},
					Fallbacks: []config.FallbackConfig{
						{Enabled: true, Llm: config.LlmConfig{Provider: "fb0", Model: "f0"}},
						{Enabled: true, Llm: config.LlmConfig{Provider: "fb1", Model: "f1"}},
					},
				},
			},
		},
		contentAnalyzer:   primary,
		fallbackAnalyzers: []analyzer.Analyzer{fb0, fb1},
	}

	_, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when all fallbacks fail")
	}
	var credErr *analyzer.InsufficientCreditsError
	if !errors.As(err, &credErr) {
		t.Fatalf("expected last fallback's InsufficientCreditsError, got %v", err)
	}
	if credErr.Provider != "fb1" {
		t.Errorf("expected provider %q in error, got %q", "fb1", credErr.Provider)
	}
}

// mockTagMatcher satisfies matcher.Matcher for testing.
type mockTagMatcher struct {
	rankFn func(ctx context.Context, docId string, queries []string) ([]matcher.RankResult, error)
}

func (m *mockTagMatcher) Match(ctx context.Context, docId, input string) ([]matcher.Candidate, error) {
	return nil, nil
}

func (m *mockTagMatcher) Rank(ctx context.Context, docId string, queries []string) ([]matcher.RankResult, error) {
	if m.rankFn != nil {
		return m.rankFn(ctx, docId, queries)
	}
	results := make([]matcher.RankResult, len(queries))
	for i, q := range queries {
		results[i] = matcher.RankResult{Candidates: []matcher.Candidate{{Tag: q}}}
	}
	return results, nil
}

func (m *mockTagMatcher) Close()       {}
func (m *mockTagMatcher) Name() string { return "mock" }

// Compile-time assertion that mockTagMatcher satisfies the interface.
var _ matcher.Matcher = (*mockTagMatcher)(nil)

func TestRankTags_TimeoutAppliesDeadline(t *testing.T) {
	tests := []struct {
		name         string
		timeout      int
		wantDeadline bool
	}{
		{"timeout zero disables deadline", 0, false},
		{"timeout applies deadline", 120, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedCtx context.Context
			mock := &mockTagMatcher{
				rankFn: func(ctx context.Context, docId string, queries []string) ([]matcher.RankResult, error) {
					receivedCtx = ctx
					results := make([]matcher.RankResult, len(queries))
					for i, q := range queries {
						results[i] = matcher.RankResult{Candidates: []matcher.Candidate{{Tag: q}}}
					}
					return results, nil
				},
			}
			r := &Runner{
				logger: log.NewDiscardLogger(),
				config: &config.Config{
					Enricher: config.EnricherConfig{
						TagMatcher: config.TagMatcherConfig{Timeout: tt.timeout},
					},
				},
				tagMatcher: mock,
			}

			queries := []string{"alpha", "beta"}
			results, err := r.RankTags(context.Background(), "doc-1", queries)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for i, q := range queries {
				if len(results[i].Candidates) != 1 || results[i].Candidates[0].Tag != q {
					t.Errorf("RankTags returned %v, want one candidate carrying each query %v", results, queries)
				}
			}
			if _, ok := receivedCtx.Deadline(); ok != tt.wantDeadline {
				t.Errorf("context deadline presence = %v, want %v", ok, tt.wantDeadline)
			}
		})
	}
}

func TestRankTags_HungMatcher_ReturnsDeadlineExceeded(t *testing.T) {
	mock := &mockTagMatcher{
		rankFn: func(ctx context.Context, docId string, queries []string) ([]matcher.RankResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{
			Enricher: config.EnricherConfig{
				TagMatcher: config.TagMatcherConfig{Timeout: 1},
			},
		},
		tagMatcher: mock,
	}

	start := time.Now()
	_, err := r.RankTags(context.Background(), "doc-1", []string{"alpha"})
	if err == nil {
		t.Fatal("expected error when matcher never returns")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("RankTags took %v, want bounded by the 1s timeout", elapsed)
	}
}

func TestRankTags_NilMatcher(t *testing.T) {
	r := &Runner{
		logger: log.NewDiscardLogger(),
		config: &config.Config{},
	}

	_, err := r.RankTags(context.Background(), "doc-1", nil)
	if err == nil {
		t.Fatal("expected error when matcher is nil")
	}
}

func TestNewRunner_UnknownCapabilityLoggedAndSkipped(t *testing.T) {
	var logs bytes.Buffer
	logger := log.NewLoggerWithWriter(&logs)

	r := NewRunner(logger, config.DefaultConfig(t.TempDir()), []string{"bogus"})

	if want := `unknown capability "bogus" — skipped`; !strings.Contains(logs.String(), want) {
		t.Errorf("expected log line containing %q, got %q", want, logs.String())
	}
	if r.textExtractor != nil || r.ocr != nil || r.pdfOptimizer != nil || r.thumbnailer != nil ||
		r.documentConverter != nil || r.textReducer != nil || r.contentAnalyzer != nil || r.tagMatcher != nil {
		t.Error("an unknown capability must leave every adapter nil")
	}
}

func TestNewRunner_ConstructorFailureLogsCauseAndStaysNil(t *testing.T) {
	var logs bytes.Buffer
	logger := log.NewLoggerWithWriter(&logs)

	cfg := config.DefaultConfig(t.TempDir())
	cfg.Consumer.Thumbnail.Engine = config.Thumbnail.Imagemagick

	r := NewRunner(logger, cfg, []string{"thumbnail"})

	if want := "create thumbnailer: imagemagick thumbnail engine is not implemented yet"; !strings.Contains(logs.String(), want) {
		t.Errorf("expected the constructor cause in the log, got %q", logs.String())
	}
	if r.thumbnailer != nil {
		t.Error("a failed construction must leave the adapter nil")
	}
	_, err := r.GenerateThumbnail(context.Background(), "doc", "missing.pdf", "out.jpg")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected a not-configured failure, got %v", err)
	}
}

func TestReduceContent_NeedsReducerOnlyOverTarget(t *testing.T) {
	ctx := context.Background()
	r := &Runner{config: &config.Config{}, logger: log.NewDiscardLogger()}

	const short = "content that already fits the requested target"
	got, err := r.ReduceContent(ctx, short, 1000)
	if err != nil {
		t.Fatalf("ReduceContent: content under the target must not need a reducer: %v", err)
	}
	if got.Text != short {
		t.Errorf("content under the target changed: got %q", got.Text)
	}

	long := strings.Repeat("word ", 300)
	if _, err := r.ReduceContent(ctx, long, 100); err == nil {
		t.Error("ReduceContent: content over the target without a configured reducer must return an error")
	}
}

func TestReduceContent_OverTargetReduces(t *testing.T) {
	ctx := context.Background()
	r := NewRunner(log.NewDiscardLogger(), config.DefaultConfig(t.TempDir()), []string{"textreducer"})

	const target = 2000
	words := make([]string, 0, 3000)
	for i := range 3000 {
		words = append(words, fmt.Sprintf("item%d.", i))
	}
	content := strings.Join(words, " ")
	inputWords := len(strings.Fields(content))

	got, err := r.ReduceContent(ctx, content, target)
	if err != nil {
		t.Fatalf("ReduceContent: %v", err)
	}
	if got.WordCount >= inputWords {
		t.Errorf("over-target content was not reduced: %d -> %d words", inputWords, got.WordCount)
	}
	if limit := target + 2*textreducer.ReduceChunkSize; got.WordCount > limit {
		t.Errorf("reduced content is %d words, above the bound %d for target %d",
			got.WordCount, limit, target)
	}
}

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/contentanalyzer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/converter"
	"github.com/wgomg/edub-kushim/internal/utils"
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
		logger: utils.NewDiscardLogger(),
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
		logger: utils.NewDiscardLogger(),
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
	analyzeFn        func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error)
	analyzeDocTypeFn func(ctx context.Context, prevResult *contentanalyzer.AnalysisResult, headTailText string, docTypes []database.DocumentType, metadata contentanalyzer.DocMetadata) (string, error)
}

func (m *mockContentAnalyzer) Analyze(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
	if m.analyzeFn != nil {
		return m.analyzeFn(ctx, text, docTypes, peopleTypes, tagSuggestions)
	}
	return &contentanalyzer.AnalysisResult{}, nil
}

func (m *mockContentAnalyzer) AnalyzeDocType(ctx context.Context, prevResult *contentanalyzer.AnalysisResult, headTailText string, docTypes []database.DocumentType, metadata contentanalyzer.DocMetadata) (string, error) {
	if m.analyzeDocTypeFn != nil {
		return m.analyzeDocTypeFn(ctx, prevResult, headTailText, docTypes, metadata)
	}
	return "document", nil
}

func (m *mockContentAnalyzer) Name() string { return "mock" }

func TestAnalyzeContent_TimeoutAppliesDeadline(t *testing.T) {
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
			mock := &mockContentAnalyzer{
				analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
					receivedCtx = ctx
					return &contentanalyzer.AnalysisResult{}, nil
				},
			}

			r := &Runner{
				logger: utils.NewDiscardLogger(),
				config: &config.Config{
					Enricher: config.EnricherConfig{
						ContentAnalyzer: config.ContentAnalyzerConfig{Timeout: tt.timeout},
					},
				},
				contentAnalyzer: mock,
			}

			_, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, ok := receivedCtx.Deadline(); ok != tt.wantDeadline {
				t.Errorf("deadline present = %v, want %v", ok, tt.wantDeadline)
			}
		})
	}
}

func TestAnalyzeDocType_TimeoutAppliesDeadline(t *testing.T) {
	var receivedCtx context.Context
	mock := &mockContentAnalyzer{
		analyzeDocTypeFn: func(ctx context.Context, prevResult *contentanalyzer.AnalysisResult, headTailText string, docTypes []database.DocumentType, metadata contentanalyzer.DocMetadata) (string, error) {
			receivedCtx = ctx
			return "document", nil
		},
	}

	r := &Runner{
		logger: utils.NewDiscardLogger(),
		config: &config.Config{
			Enricher: config.EnricherConfig{
				ContentAnalyzer: config.ContentAnalyzerConfig{Timeout: 120},
			},
		},
		contentAnalyzer: mock,
	}

	_, err := r.AnalyzeDocType(context.Background(), &ContentAnalysisResult{}, "head tail", nil, contentanalyzer.DocMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := receivedCtx.Deadline(); !ok {
		t.Error("context should have a deadline when timeout>0")
	}
}

func TestOptimizePdf_FileNotFound(t *testing.T) {
	mock := &mockPdfOptimizer{}
	r := &Runner{
		logger: utils.NewDiscardLogger(),
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
		logger: utils.NewDiscardLogger(),
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
		logger: utils.NewDiscardLogger(),
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
		logger: utils.NewDiscardLogger(),
		config: &config.Config{},
	}

	_, err := r.AnalyzeDocType(context.Background(), nil, "head tail text", nil, contentanalyzer.DocMetadata{})
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
		logger: utils.NewDiscardLogger(),
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
		logger: utils.NewDiscardLogger(),
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
		logger: utils.NewDiscardLogger(),
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
		logger: utils.NewDiscardLogger(),
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

func TestIsProviderError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"generic error", errors.New("boom"), true},
		{"insufficient credits", &contentanalyzer.InsufficientCreditsError{Provider: "p", HTTPStatus: 402}, true},
		{"wrapped insufficient credits", fmt.Errorf("wrapped: %w", &contentanalyzer.InsufficientCreditsError{Provider: "p", HTTPStatus: 429}), true},
		{"content too large", &contentanalyzer.ContentTooLargeError{EstimatedTokens: 10, MaxInputTokens: 5}, false},
		{"wrapped content too large", fmt.Errorf("wrapped: %w", &contentanalyzer.ContentTooLargeError{EstimatedTokens: 10, MaxInputTokens: 5}), false},
		{"token limit", &contentanalyzer.TokenLimitError{MaxTokens: 100, RequestedTokens: 200}, false},
		{"wrapped token limit", fmt.Errorf("wrapped: %w", &contentanalyzer.TokenLimitError{MaxTokens: 100, RequestedTokens: 200}), false},
		{"context canceled", context.Canceled, false},
		{"wrapped context canceled", fmt.Errorf("wrapped: %w", context.Canceled), false},
		{"context deadline exceeded", context.DeadlineExceeded, false},
		{"wrapped context deadline exceeded", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProviderError(tt.err); got != tt.want {
				t.Errorf("isProviderError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func runnerWithFallback(primary, fallback contentanalyzer.ContentAnalyzer) *Runner {
	return &Runner{
		logger: utils.NewDiscardLogger(),
		config: &config.Config{
			Enricher: config.EnricherConfig{
				ContentAnalyzer: config.ContentAnalyzerConfig{
					Timeout: 0,
					Llm:     config.LlmConfig{Provider: "primary", Model: "p1"},
					Fallback: &config.FallbackConfig{
						Enabled: true,
						Llm:     config.LlmConfig{Provider: "fallback", Model: "f1"},
					},
				},
			},
		},
		contentAnalyzer:  primary,
		fallbackAnalyzer: fallback,
	}
}

func TestAnalyzeContent_FallbackOnProviderError(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
			return nil, &contentanalyzer.InsufficientCreditsError{Provider: "primary", HTTPStatus: 402}
		},
	}
	fallback := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
			return &contentanalyzer.AnalysisResult{Title: "from fallback"}, nil
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
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
			return nil, &contentanalyzer.ContentTooLargeError{EstimatedTokens: 10, MaxInputTokens: 5}
		},
	}
	fallbackCalled := false
	fallback := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
			fallbackCalled = true
			return &contentanalyzer.AnalysisResult{Title: "fallback"}, nil
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
	var ctle *contentanalyzer.ContentTooLargeError
	if !errors.As(err, &ctle) {
		t.Errorf("expected ContentTooLargeError through wrap, got %v", err)
	}
}

func TestAnalyzeContent_NoFallbackOnCancellation(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
			return nil, context.Canceled
		},
	}
	fallbackCalled := false
	fallback := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
			fallbackCalled = true
			return &contentanalyzer.AnalysisResult{}, nil
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

func TestAnalyzeContent_NoFallbackOnDeadlineExceeded(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
			return nil, context.DeadlineExceeded
		},
	}
	fallbackCalled := false
	fallback := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
			fallbackCalled = true
			return &contentanalyzer.AnalysisResult{}, nil
		},
	}

	r := runnerWithFallback(primary, fallback)
	_, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error from primary")
	}
	if fallbackCalled {
		t.Error("fallback must not be called for context.DeadlineExceeded: the shared context is already expired and the retry is guaranteed to fail")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded through wrap, got %v", err)
	}
}

func TestAnalyzeContent_NoFallbackAnalyzer(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
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
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
			return nil, errors.New("primary network failure")
		},
	}
	fallback := &mockContentAnalyzer{
		analyzeFn: func(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*contentanalyzer.AnalysisResult, error) {
			return nil, &contentanalyzer.InsufficientCreditsError{Provider: "fallback", HTTPStatus: 402}
		},
	}

	r := runnerWithFallback(primary, fallback)
	_, err := r.AnalyzeContent(context.Background(), "text", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when fallback fails")
	}
	var credErr *contentanalyzer.InsufficientCreditsError
	if !errors.As(err, &credErr) {
		t.Fatalf("expected fallback InsufficientCreditsError through wrap, got %v", err)
	}
	if credErr.Provider != "fallback" {
		t.Errorf("expected provider %q in error, got %q", "fallback", credErr.Provider)
	}
}

func TestAnalyzeDocType_FallbackOnProviderError(t *testing.T) {
	primary := &mockContentAnalyzer{
		analyzeDocTypeFn: func(ctx context.Context, prevResult *contentanalyzer.AnalysisResult, headTailText string, docTypes []database.DocumentType, metadata contentanalyzer.DocMetadata) (string, error) {
			return "", errors.New("provider timeout")
		},
	}
	fallback := &mockContentAnalyzer{
		analyzeDocTypeFn: func(ctx context.Context, prevResult *contentanalyzer.AnalysisResult, headTailText string, docTypes []database.DocumentType, metadata contentanalyzer.DocMetadata) (string, error) {
			return "from fallback", nil
		},
	}

	r := runnerWithFallback(primary, fallback)
	got, err := r.AnalyzeDocType(context.Background(), &ContentAnalysisResult{}, "head tail", nil, contentanalyzer.DocMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from fallback" {
		t.Errorf("expected result from fallback analyzer, got %q", got)
	}
}

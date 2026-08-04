package tools

import (
	"context"
	"errors"
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

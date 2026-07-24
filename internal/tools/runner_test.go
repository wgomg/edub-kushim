package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/contentanalyzer"
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

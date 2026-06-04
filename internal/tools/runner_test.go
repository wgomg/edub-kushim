package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type mockTextExtractor struct {
	extractFunc func(ctx context.Context, path string) (*string, error)
}

func (m *mockTextExtractor) Extract(ctx context.Context, path string) (*string, error) {
	if m.extractFunc != nil {
		return m.extractFunc(ctx, path)
	}
	text := "mock text"
	return &text, nil
}
func (m *mockTextExtractor) CanHandle(string) bool { return true }
func (m *mockTextExtractor) Name() string          { return "mock-textextractor" }

type mockOCR struct {
	processFunc func(ctx context.Context, path string) (*string, error)
}

func (m *mockOCR) Process(ctx context.Context, path string) (*string, error) {
	if m.processFunc != nil {
		return m.processFunc(ctx, path)
	}
	out := "mock-ocr-output"
	return &out, nil
}
func (m *mockOCR) CanHandle(string) bool { return true }
func (m *mockOCR) Name() string          { return "mock-ocr" }

type mockPdfOptimizer struct {
	optimizeFunc func(ctx context.Context, path string) (*string, error)
}

func (m *mockPdfOptimizer) Optimize(ctx context.Context, path string) (*string, error) {
	if m.optimizeFunc != nil {
		return m.optimizeFunc(ctx, path)
	}
	out := "mock-opt-output"
	return &out, nil
}
func (m *mockPdfOptimizer) Name() string { return "mock-optimizer" }

func tempFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "testfile")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunWithTimeout_Completes(t *testing.T) {
	ctx := context.Background()
	got, err := runWithTimeout(ctx, func() (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
}

func TestRunWithTimeout_ReturnsError(t *testing.T) {
	ctx := context.Background()
	_, err := runWithTimeout(ctx, func() (string, error) {
		return "", fmt.Errorf("boom")
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected 'boom', got %v", err)
	}
}

func TestRunWithTimeout_ContextExpires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runWithTimeout(ctx, func() (string, error) {
		time.Sleep(time.Hour)
		return "too late", nil
	})
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}

func TestRunWithTimeout_TakesLongerThanContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := runWithTimeout(ctx, func() (string, error) {
		time.Sleep(time.Hour)
		return "never", nil
	})
	if err == nil {
		t.Fatal("expected context deadline exceeded, got nil")
	}
}

func TestNewRunnerWithAdapters(t *testing.T) {
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{Consumer: config.ConsumerConfig{TextExtractor: config.TextExtractorConfig{Timeout: 5}, OCR: config.OCRConfig{Timeout: 5}, PdfOptimizer: config.PdfOptimizerConfig{Timeout: 5}}},
		&mockTextExtractor{},
		&mockOCR{},
		&mockPdfOptimizer{},
		nil, nil, nil,
	)
	if r == nil {
		t.Fatal("expected non-nil Runner")
	}
}

func TestExtractText_Success(t *testing.T) {
	path := tempFile(t, "content")
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{Consumer: config.ConsumerConfig{TextExtractor: config.TextExtractorConfig{Timeout: 5}}},
		&mockTextExtractor{},
		&mockOCR{},
		&mockPdfOptimizer{},
		nil, nil, nil,
	)

	result, err := r.ExtractText(context.Background(), path)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if result.Text == nil || *result.Text != "mock text" {
		t.Errorf("Text = %v", result.Text)
	}
}

func TestExtractText_FileNotFound(t *testing.T) {
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{Consumer: config.ConsumerConfig{TextExtractor: config.TextExtractorConfig{Timeout: 5}}},
		&mockTextExtractor{},
		&mockOCR{},
		&mockPdfOptimizer{},
		nil, nil, nil,
	)

	_, err := r.ExtractText(context.Background(), "/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestExtractText_AdapterError(t *testing.T) {
	path := tempFile(t, "content")
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{Consumer: config.ConsumerConfig{TextExtractor: config.TextExtractorConfig{Timeout: 5}}},
		&mockTextExtractor{
			extractFunc: func(ctx context.Context, path string) (*string, error) {
				return nil, fmt.Errorf("adapter failure")
			},
		},
		&mockOCR{},
		&mockPdfOptimizer{},
		nil, nil, nil,
	)

	_, err := r.ExtractText(context.Background(), path)
	if err == nil {
		t.Fatal("expected adapter error, got nil")
	}
}

func TestOCR_Success(t *testing.T) {
	path := tempFile(t, "content")
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{Consumer: config.ConsumerConfig{OCR: config.OCRConfig{Timeout: 5}}},
		&mockTextExtractor{},
		&mockOCR{},
		&mockPdfOptimizer{},
		nil, nil, nil,
	)

	result, err := r.OCR(context.Background(), path)
	if err != nil {
		t.Fatalf("OCR: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
	if result.TmpPath == nil || *result.TmpPath != "mock-ocr-output" {
		t.Errorf("TmpPath = %v", result.TmpPath)
	}
}

func TestOCR_FileNotFound(t *testing.T) {
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{Consumer: config.ConsumerConfig{OCR: config.OCRConfig{Timeout: 5}}},
		&mockTextExtractor{},
		&mockOCR{},
		&mockPdfOptimizer{},
		nil, nil, nil,
	)

	_, err := r.OCR(context.Background(), "/nonexistent")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestOCR_AdapterError(t *testing.T) {
	path := tempFile(t, "content")
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{Consumer: config.ConsumerConfig{OCR: config.OCRConfig{Timeout: 5}}},
		&mockTextExtractor{},
		&mockOCR{
			processFunc: func(ctx context.Context, path string) (*string, error) {
				return nil, fmt.Errorf("ocr failure")
			},
		},
		&mockPdfOptimizer{},
		nil, nil, nil,
	)

	_, err := r.OCR(context.Background(), path)
	if err == nil {
		t.Fatal("expected ocr error, got nil")
	}
}

func TestOptimizePdf_Success(t *testing.T) {
	path := tempFile(t, "content")
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{Consumer: config.ConsumerConfig{PdfOptimizer: config.PdfOptimizerConfig{Timeout: 5}}},
		&mockTextExtractor{},
		&mockOCR{},
		&mockPdfOptimizer{},
		nil, nil, nil,
	)

	result, err := r.OptimizePdf(context.Background(), path)
	if err != nil {
		t.Fatalf("OptimizePdf: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
	if result.TmpPath == nil || *result.TmpPath != "mock-opt-output" {
		t.Errorf("TmpPath = %v", result.TmpPath)
	}
}

func TestOptimizePdf_FileNotFound(t *testing.T) {
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{Consumer: config.ConsumerConfig{PdfOptimizer: config.PdfOptimizerConfig{Timeout: 5}}},
		&mockTextExtractor{},
		&mockOCR{},
		&mockPdfOptimizer{},
		nil, nil, nil,
	)

	_, err := r.OptimizePdf(context.Background(), "/nonexistent")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestOptimizePdf_AdapterErrorNoFallback(t *testing.T) {
	path := tempFile(t, "content")
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{Consumer: config.ConsumerConfig{PdfOptimizer: config.PdfOptimizerConfig{Timeout: 5}}},
		&mockTextExtractor{},
		&mockOCR{},
		&mockPdfOptimizer{
			optimizeFunc: func(ctx context.Context, path string) (*string, error) {
				return nil, fmt.Errorf("primary failure")
			},
		},
		nil, nil, nil,
	)

	_, err := r.OptimizePdf(context.Background(), path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestOptimizePdf_FallbackUsed(t *testing.T) {
	path := tempFile(t, "content")
	r := NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.Config{
			Consumer: config.ConsumerConfig{
				PdfOptimizer: config.PdfOptimizerConfig{
					Timeout:  5,
					Fallback: "ghostscript",
				},
			},
		},
		&mockTextExtractor{},
		&mockOCR{},
		&mockPdfOptimizer{
			optimizeFunc: func(ctx context.Context, path string) (*string, error) {
				return nil, fmt.Errorf("primary failure")
			},
		},
		nil, nil, nil,
	)

	_, err := r.OptimizePdf(context.Background(), path)
	if err == nil {
		t.Fatal("expected error because fallback adapter doesn't exist")
	}
}

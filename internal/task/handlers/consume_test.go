package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/utils"

	_ "modernc.org/sqlite"
)

func makeHandler() *ConsumeTaskHandler {
	return NewConsumeTaskHandler(nil)
}

func TestHandle_InvalidPayload(t *testing.T) {
	h := makeHandler()
	ctx := context.Background()

	task := database.Task{
		TaskID:  "test-1",
		Payload: json.RawMessage(`not json`),
	}

	_, err := h.Handle(ctx, task)
	if err == nil {
		t.Fatal("Handle() expected error for invalid JSON, got nil")
	}
}

func TestHandle_MissingFilePath(t *testing.T) {
	h := makeHandler()
	ctx := context.Background()

	task := database.Task{
		TaskID:  "test-2",
		Payload: json.RawMessage(`{}`),
	}

	_, err := h.Handle(ctx, task)
	if err == nil {
		t.Fatal("Handle() expected error for missing file_path, got nil")
	}
}

func TestHandle_NonExistentFile(t *testing.T) {
	h := makeHandler()
	ctx := context.Background()

	task := database.Task{
		TaskID:  "test-3",
		Payload: json.RawMessage(`{"file_path":"/tmp/nonexistent-file-12345.pdf"}`),
	}

	_, err := h.Handle(ctx, task)
	if err == nil {
		t.Fatal("Handle() expected error for non-existent file, got nil")
	}
}

func TestHandle_Success(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE document (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			md5_checksum TEXT NOT NULL,
			sha512_checksum TEXT UNIQUE NOT NULL,
			mime_type TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			page_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			modified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			document_type_id INTEGER,
			original_path TEXT NOT NULL,
			storage_path TEXT NOT NULL,
			text_content TEXT
		);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}

	logger := utils.NewDiscardLogger()
	dir := t.TempDir()

	content := []byte("test file content for consume handler success")
	srcPath := filepath.Join(dir, "test.pdf")
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Storage: config.StorageConfig{
			StorageDir: filepath.Join(dir, "storage"),
		},
		Consumer: config.ConsumerConfig{
			TextExtractor: config.TextExtractorConfig{Timeout: 5},
			PdfOptimizer:  config.PdfOptimizerConfig{Timeout: 5},
			OCR:           config.OCRConfig{Timeout: 5},
		},
	}

	runner := tools.NewRunnerWithAdapters(
		logger,
		cfg,
		&mockTextExtractor{},
		&mockOCR{},
		&mockPdfOptimizer{},
		nil, nil, nil,
	)
	consumer, err := consumption.NewConsumerWithRunner(cfg, logger, db, runner)
	if err != nil {
		t.Fatalf("NewConsumerWithRunner: %v", err)
	}

	h := NewConsumeTaskHandler(consumer)
	ctx := context.Background()

	task := database.Task{
		TaskID:  "test-success",
		Payload: json.RawMessage(`{"file_path":"` + srcPath + `"}`),
	}

	result, err := h.Handle(ctx, task)
	if err != nil {
		t.Fatalf("Handle() unexpected error: %v", err)
	}

	var parsed struct {
		DocumentID  int64  `json:"document_id"`
		StoragePath string `json:"storage_path"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if parsed.DocumentID == 0 {
		t.Error("expected non-zero document_id")
	}
	if parsed.StoragePath == "" {
		t.Error("expected non-empty storage_path")
	}
}

func TestDedupKey_WithFilePath(t *testing.T) {
	h := makeHandler()

	payload := json.RawMessage(`{"file_path":"/home/user/docs/report.pdf"}`)
	key := h.DedupKey(payload)

	if key != "/home/user/docs/report.pdf" {
		t.Errorf("DedupKey() = %q, want %q", key, "/home/user/docs/report.pdf")
	}
}

func TestDedupKey_EmptyPayload(t *testing.T) {
	h := makeHandler()

	payload := json.RawMessage(`{}`)
	key := h.DedupKey(payload)

	if key != "" {
		t.Errorf("DedupKey() = %q, want empty string", key)
	}
}

type mockTextExtractor struct {
	extractFunc func(ctx context.Context, path string) (*string, error)
}

func (m *mockTextExtractor) Extract(ctx context.Context, path string) (*string, error) {
	if m.extractFunc != nil {
		return m.extractFunc(ctx, path)
	}
	text := "mock extracted text"
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
	return nil, nil
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
	out := path + ".optimized"
	if err := os.WriteFile(out, []byte("optimized"), 0644); err != nil {
		return nil, err
	}
	return &out, nil
}
func (m *mockPdfOptimizer) Name() string { return "mock-optimizer" }

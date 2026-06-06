package task

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
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/utils"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE task (
			id INTEGER PRIMARY KEY,
			task_id TEXT NOT NULL UNIQUE,
			task_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			batch_id TEXT,
			payload JSON,
			result JSON,
			dedup_key TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			completed_at DATETIME,
			error TEXT
		);
		CREATE INDEX idx_task_status ON task(status);
		CREATE INDEX idx_task_pending ON task(created_at) WHERE status = 'pending';
		CREATE INDEX idx_task_batch ON task(batch_id);
		CREATE UNIQUE INDEX idx_task_dedup ON task(task_type, dedup_key)
			WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL;

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
		CREATE INDEX idx_document_md5 ON document(md5_checksum);

		CREATE TABLE document_type (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE tag (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	return db
}

func makeDispatcher(db *sql.DB) *Dispatcher {
	return &Dispatcher{
		consumer: nil,
		logger:   utils.NewDiscardLogger(),
		queries:  database.NewQueries(db),
	}
}

func TestEnqueue_ValidTaskType(t *testing.T) {
	db := setupTestDB(t)
	d := makeDispatcher(db)
	ctx := context.Background()

	payload := json.RawMessage(`{"file_path":"/tmp/test.pdf"}`)
	taskID, err := d.Enqueue(ctx, "consume", "batch-1", payload, "")
	if err != nil {
		t.Fatalf("Enqueue() unexpected error: %v", err)
	}
	if taskID == "" {
		t.Fatal("Enqueue() returned empty task ID")
	}

	queries := database.NewQueries(db)
	task, err := queries.GetTaskByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTaskByTaskID() failed: %v", err)
	}
	if task.TaskType != "consume" {
		t.Errorf("task type = %q, want %q", task.TaskType, "consume")
	}
	if task.Status != "pending" {
		t.Errorf("task status = %q, want %q", task.Status, "pending")
	}
	if task.BatchID.String != "batch-1" {
		t.Errorf("task batch_id = %q, want %q", task.BatchID.String, "batch-1")
	}
}

func TestEnqueue_InvalidTaskType(t *testing.T) {
	db := setupTestDB(t)
	d := makeDispatcher(db)
	ctx := context.Background()

	payload := json.RawMessage(`{}`)
	_, err := d.Enqueue(ctx, "nonexistent", "", payload, "")
	if err == nil {
		t.Fatal("Enqueue() expected error for unknown task type, got nil")
	}

	queries := database.NewQueries(db)
	tasks, err := queries.ListTasks(ctx, database.ListTasksParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListTasks() failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestEnqueue_DedupKey(t *testing.T) {
	db := setupTestDB(t)
	d := makeDispatcher(db)
	ctx := context.Background()

	payload := json.RawMessage(`{"file_path":"/tmp/test.pdf"}`)

	id1, err := d.Enqueue(ctx, "consume", "batch-1", payload, "")
	if err != nil {
		t.Fatalf("first Enqueue() unexpected error: %v", err)
	}
	if id1 == "" {
		t.Fatal("first Enqueue() returned empty task ID")
	}

	_, err = d.Enqueue(ctx, "consume", "batch-1", payload, "")
	if err == nil {
		t.Fatal("second Enqueue() expected dedup error, got nil")
	}
}

func TestNext_NoPendingTasks(t *testing.T) {
	db := setupTestDB(t)
	d := makeDispatcher(db)
	ctx := context.Background()

	err := d.Next(ctx, "consume")
	if err != nil {
		t.Fatalf("Next() on empty DB unexpected error: %v", err)
	}
}

func TestNext_UnknownTaskType(t *testing.T) {
	db := setupTestDB(t)
	d := makeDispatcher(db)
	ctx := context.Background()

	queries := database.NewQueries(db)
	_, err := queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "test-task-1",
		TaskType: "nonexistent",
		Status:   "pending",
		Payload:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateTask() failed: %v", err)
	}

	err = d.Next(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Next() unexpected error: %v", err)
	}

	task, err := queries.GetTaskByTaskID(ctx, "test-task-1")
	if err != nil {
		t.Fatalf("GetTaskByTaskID() failed: %v", err)
	}
	if task.Status != "failed" {
		t.Errorf("task status = %q, want %q", task.Status, "failed")
	}
	if !task.Error.Valid || task.Error.String == "" {
		t.Fatal("expected a non-empty error on the failed task")
	}
}

func TestNext_ProcessesPendingTask(t *testing.T) {
	db := setupTestDB(t)
	logger := utils.NewDiscardLogger()
	dir := t.TempDir()

	content := []byte("test file content for dispatcher next test")
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

	d := &Dispatcher{
		consumer: consumer,
		logger:   logger,
		queries:  database.NewQueries(db),
	}

	ctx := context.Background()
	payload := json.RawMessage(`{"file_path":"` + srcPath + `"}`)
	taskID, err := d.Enqueue(ctx, "consume", "batch-next", payload, "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	err = d.Next(ctx, "consume")
	if err != nil {
		t.Fatalf("Next() unexpected error: %v", err)
	}

	task, err := d.queries.GetTaskByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTaskByTaskID: %v", err)
	}
	if task.Status != "completed" {
		t.Fatalf("task status = %q, want %q; error = %v", task.Status, "completed", task.Error)
	}
	if task.Result == nil {
		t.Fatal("expected non-nil result on completed task")
	}
	var result struct {
		DocumentID  int64  `json:"document_id"`
		StoragePath string `json:"storage_path"`
	}
	if err := json.Unmarshal(*task.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.DocumentID == 0 {
		t.Error("expected non-zero document_id in result")
	}
	if result.StoragePath == "" {
		t.Error("expected non-empty storage_path in result")
	}
}

func TestNext_ConsumeResolvesWaitingEnrich(t *testing.T) {
	db := setupTestDB(t)
	logger := utils.NewDiscardLogger()
	dir := t.TempDir()

	content := []byte("test file content for enrich resolution test")
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

	d := &Dispatcher{
		consumer: consumer,
		logger:   logger,
		queries:  database.NewQueries(db),
	}

	ctx := context.Background()

	// Create consume task with on_completed pointing to the enrich task.
	consumeTaskID := "consume-uuid-resolve"
	enrichTaskID := "enrich-uuid-resolve"
	batchID := "batch-resolve-test"

	consumePayload, _ := json.Marshal(map[string]any{
		"file_path":    srcPath,
		"file_index":   1,
		"on_completed": enrichTaskID,
	})
	_, err = d.Enqueue(ctx, "consume", batchID, consumePayload, consumeTaskID)
	if err != nil {
		t.Fatalf("Enqueue(consume): %v", err)
	}

	// Create waiting enrich task linked to the consume task.
	enrichPayload, _ := json.Marshal(map[string]any{
		"waiting_for": consumeTaskID,
		"file_name":   "test.pdf",
		"file_index":  1,
	})
	_, err = d.Enqueue(ctx, "enrich", batchID, enrichPayload, enrichTaskID, "waiting")
	if err != nil {
		t.Fatalf("Enqueue(enrich): %v", err)
	}

	// Process the consume task — should activate the waiting enrich.
	err = d.Next(ctx, "consume")
	if err != nil {
		t.Fatalf("Next(consume): %v", err)
	}

	// Verify the waiting enrich task was activated.
	enrichTask, err := d.queries.GetTaskByTaskID(ctx, enrichTaskID)
	if err != nil {
		t.Fatalf("GetTaskByTaskID(enrich): %v", err)
	}
	if enrichTask.Status != "pending" {
		t.Errorf("enrich task status = %q, want %q", enrichTask.Status, "pending")
	}
	if enrichTask.BatchID.String != batchID {
		t.Errorf("enrich task batch_id = %q, want %q", enrichTask.BatchID.String, batchID)
	}

	var enrichPayloadData struct {
		DocumentID int64  `json:"document_id"`
		WaitingFor string `json:"waiting_for"`
		FileName   string `json:"file_name"`
	}
	if err := json.Unmarshal(enrichTask.Payload, &enrichPayloadData); err != nil {
		t.Fatalf("unmarshal enrich payload: %v", err)
	}
	if enrichPayloadData.DocumentID == 0 {
		t.Error("expected non-zero document_id in enrich payload after activation")
	}
	if enrichPayloadData.WaitingFor != consumeTaskID {
		t.Errorf("waiting_for = %q, want %q", enrichPayloadData.WaitingFor, consumeTaskID)
	}
}

func TestNext_EnrichProcessesTask(t *testing.T) {
	db := setupTestDB(t)
	logger := utils.NewDiscardLogger()

	queries := database.NewQueries(db)
	_, err := queries.CreateDocument(context.Background(), database.CreateDocumentParams{
		Title:          "test doc",
		Md5Checksum:    "abc",
		Sha512Checksum: "def",
		MimeType:       "application/pdf",
		FileSize:       100,
		PageCount:      0,
		OriginalPath:   "/tmp/test.pdf",
		StoragePath:    "/tmp/test.pdf",
		TextContent:    sql.NullString{String: "some text", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	cfg := &config.Config{}
	enricher, err := enrichment.NewEnricher(cfg, logger, db, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	d := &Dispatcher{
		enricher: enricher,
		logger:   logger,
		queries:  queries,
	}

	ctx := context.Background()
	payload := json.RawMessage(`{"document_id":1}`)
	taskID, err := d.Enqueue(ctx, "enrich", "batch-enrich", payload, "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	err = d.Next(ctx, "enrich")
	if err != nil {
		t.Fatalf("Next(enrich): %v", err)
	}

	task, err := d.queries.GetTaskByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTaskByTaskID: %v", err)
	}
	if task.Status != "completed" {
		t.Fatalf("enrich task status = %q, want %q; error = %v", task.Status, "completed", task.Error)
	}
	if task.Result == nil {
		t.Fatal("expected non-nil result on completed enrich task")
	}
}

func TestNext_EnrichDedup(t *testing.T) {
	db := setupTestDB(t)
	logger := utils.NewDiscardLogger()

	cfg := &config.Config{}
	enricher, err := enrichment.NewEnricher(cfg, logger, db, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	d := &Dispatcher{
		enricher: enricher,
		logger:   logger,
		queries:  database.NewQueries(db),
	}

	ctx := context.Background()
	payload := json.RawMessage(`{"document_id":42}`)

	_, err = d.Enqueue(ctx, "enrich", "batch-dedup", payload, "")
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}

	_, err = d.Enqueue(ctx, "enrich", "batch-dedup", payload, "")
	if err == nil {
		t.Fatal("second Enqueue() expected dedup error, got nil")
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

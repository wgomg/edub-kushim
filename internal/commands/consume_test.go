package commands

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/utils"

	_ "modernc.org/sqlite"
)

func consumeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

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
		CREATE INDEX idx_task_batch ON task(batch_id);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func TestConsumeCancelHandler_Help(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := consumeCancelHandler(c, []string{"--help"})
	if err != nil {
		t.Fatalf("expected nil error for --help, got %v", err)
	}
}

func TestConsumeCancelHandler_ShortHelp(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := consumeCancelHandler(c, []string{"-h"})
	if err != nil {
		t.Fatalf("expected nil error for -h, got %v", err)
	}
}

func TestConsumeCancelHandler_NoArgs(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := consumeCancelHandler(c, []string{})
	if err == nil {
		t.Fatal("expected error for missing batch ID")
	}
}

func TestConsumeCancelHandler_NoRunningProcess(t *testing.T) {
	db := consumeTestDB(t)
	c := &Container{
		db:     db,
		logger: utils.NewDiscardLogger(),
	}

	err := consumeCancelHandler(c, []string{"nonexistent-batch"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConsumeHandler_Help(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := consumeHandler(c, []string{"--help"})
	if err != nil {
		t.Fatalf("expected nil error for --help, got %v", err)
	}
}

func TestConsumeHandler_BgAndBatchMutuallyExclusive(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := consumeHandler(c, []string{"--bg", "--batch", "some-id"})
	if err == nil {
		t.Fatal("expected error for --bg and --batch together")
	}
}

func TestConsumeHandler_NoFiles(t *testing.T) {
	chdirToProjectRoot(t)
	dir := t.TempDir()
	cfg := &config.Config{
		Db: config.DatabaseConfig{
			Path: filepath.Join(dir, "db"),
			Name: "test.db",
		},
		Storage: config.StorageConfig{
			ConsumptionDir: dir,
			StorageDir:     filepath.Join(dir, "storage"),
		},
		Consumer: config.ConsumerConfig{
			SupportedFiles: []string{".pdf"},
			Workers:        1,
		},
	}
	c := &Container{
		config: cfg,
		logger: utils.NewDiscardLogger(),
	}

	err := consumeHandler(c, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConsumeHandler_ResumeBatchNotFound(t *testing.T) {
	db := consumeTestDB(t)
	cfg := &config.Config{
		Consumer: config.ConsumerConfig{Workers: 1},
	}
	c := &Container{
		config: cfg,
		logger: utils.NewDiscardLogger(),
		db:     db,
	}

	err := consumeHandler(c, []string{"--batch", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent batch")
	}
}

func TestConsumeHandler_ResumeBatchAlreadyFinished(t *testing.T) {
	db := consumeTestDB(t)
	queries := database.New(db)
	ctx := context.Background()

	queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "done-1",
		TaskType: "consume",
		Status:   "completed",
		BatchID:  sql.NullString{String: "batch-done", Valid: true},
		Payload:  []byte(`{"file_path":"/tmp/a.pdf"}`),
	})

	cfg := &config.Config{
		Consumer: config.ConsumerConfig{Workers: 1},
	}
	c := &Container{
		config: cfg,
		logger: utils.NewDiscardLogger(),
		db:     db,
	}

	err := consumeHandler(c, []string{"--batch", "batch-done"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPollBatch_CompletesWhenAllDone(t *testing.T) {
	db := consumeTestDB(t)
	queries := database.New(db)
	ctx := context.Background()

	queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "p-1",
		TaskType: "consume",
		Status:   "completed",
		BatchID:  sql.NullString{String: "batch-poll", Valid: true},
		Payload:  []byte(`{"file_path":"/tmp/a.pdf"}`),
	})

	runner := &mockPoolRunner{}
	p := pool.New(utils.NewDiscardLogger(), runner, 1, time.Hour)

	pollCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := pollBatch(pollCtx, queries, p, utils.NewDiscardLogger(), "batch-poll")
	if err != nil {
		t.Fatalf("pollBatch: %v", err)
	}
}

func TestPollBatch_CancelledByContext(t *testing.T) {
	db := consumeTestDB(t)
	queries := database.New(db)
	ctx := context.Background()

	queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "p-2",
		TaskType: "consume",
		Status:   "pending",
		BatchID:  sql.NullString{String: "batch-cancel", Valid: true},
		Payload:  []byte(`{"file_path":"/tmp/b.pdf"}`),
	})

	runner := &mockPoolRunner{}
	p := pool.New(utils.NewDiscardLogger(), runner, 1, time.Hour)

	pollCtx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- pollBatch(pollCtx, queries, p, utils.NewDiscardLogger(), "batch-cancel")
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("pollBatch: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pollBatch did not return after context cancel")
	}
}

type mockPoolRunner struct {
	mu    sync.Mutex
	calls int
}

func (m *mockPoolRunner) Next(ctx context.Context) error {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return nil
}

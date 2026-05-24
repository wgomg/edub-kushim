package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"

	_ "modernc.org/sqlite"
)

func taskTestDB(t *testing.T) *sql.DB {
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
		CREATE INDEX idx_task_status ON task(status);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func TestTaskHandler_NoArgs(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := taskHandler(c, []string{})
	if err == nil {
		t.Fatal("expected error for missing subcommand")
	}
}

func TestTaskHandler_Help(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := taskHandler(c, []string{"--help"})
	if err != nil {
		t.Fatalf("expected nil error for --help, got %v", err)
	}
}

func TestTaskHandler_UnknownSubcommand(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := taskHandler(c, []string{"unknown"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

func TestTaskListHandler_Help(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := taskListHandler(c, []string{"--help"})
	if err != nil {
		t.Fatalf("expected nil error for --help, got %v", err)
	}
}

func TestTaskListHandler_Empty(t *testing.T) {
	db := taskTestDB(t)
	c := &Container{db: db, logger: utils.NewDiscardLogger()}
	err := taskListHandler(c, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTaskListHandler_WithTasks(t *testing.T) {
	db := taskTestDB(t)
	queries := database.New(db)
	ctx := context.Background()

	queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "t1",
		TaskType: "consume",
		Status:   "completed",
		BatchID:  sql.NullString{String: "batch-1", Valid: true},
		Payload:  json.RawMessage(`{"file_path":"/tmp/a.pdf"}`),
	})
	queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "t2",
		TaskType: "consume",
		Status:   "failed",
		BatchID:  sql.NullString{String: "batch-1", Valid: true},
		Payload:  json.RawMessage(`{"file_path":"/tmp/b.pdf"}`),
	})

	c := &Container{db: db, logger: utils.NewDiscardLogger()}
	err := taskListHandler(c, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTaskListHandler_FilterByBatch(t *testing.T) {
	db := taskTestDB(t)
	queries := database.New(db)
	ctx := context.Background()

	queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "t1",
		TaskType: "consume",
		Status:   "pending",
		BatchID:  sql.NullString{String: "batch-a", Valid: true},
		Payload:  json.RawMessage(`{"file_path":"/tmp/a.pdf"}`),
	})
	queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "t2",
		TaskType: "consume",
		Status:   "pending",
		BatchID:  sql.NullString{String: "batch-b", Valid: true},
		Payload:  json.RawMessage(`{"file_path":"/tmp/b.pdf"}`),
	})

	c := &Container{db: db, logger: utils.NewDiscardLogger()}
	err := taskListHandler(c, []string{"--batch", "batch-a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTaskStatusHandler_NoArgs(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := taskStatusHandler(c, []string{})
	if err == nil {
		t.Fatal("expected error for missing task ID")
	}
}

func TestTaskStatusHandler_Help(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := taskStatusHandler(c, []string{"--help"})
	if err != nil {
		t.Fatalf("expected nil error for --help, got %v", err)
	}
}

func TestTaskStatusHandler_NotFound(t *testing.T) {
	db := taskTestDB(t)
	c := &Container{db: db, logger: utils.NewDiscardLogger()}
	err := taskStatusHandler(c, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskStatusHandler_Found(t *testing.T) {
	db := taskTestDB(t)
	queries := database.New(db)
	ctx := context.Background()

	res, err := queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "t-status",
		TaskType: "consume",
		Status:   "completed",
		BatchID:  sql.NullString{String: "batch-1", Valid: true},
		Payload:  json.RawMessage(`{"file_path":"/tmp/doc.pdf"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	raw := json.RawMessage(`{"document_id":42}`)
	queries.CompleteTask(ctx, database.CompleteTaskParams{ID: id, Result: &raw})

	c := &Container{db: db, logger: utils.NewDiscardLogger()}
	err = taskStatusHandler(c, []string{"t-status"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTaskRetryHandler_NoArgs(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := taskRetryHandler(c, []string{})
	if err == nil {
		t.Fatal("expected error for missing task ID")
	}
}

func TestTaskRetryHandler_Help(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := taskRetryHandler(c, []string{"--help"})
	if err != nil {
		t.Fatalf("expected nil error for --help, got %v", err)
	}
}

func TestTaskRetryHandler_NotFound(t *testing.T) {
	db := taskTestDB(t)
	c := &Container{db: db, logger: utils.NewDiscardLogger()}
	err := taskRetryHandler(c, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskRetryHandler_Success(t *testing.T) {
	db := taskTestDB(t)
	queries := database.New(db)
	ctx := context.Background()

	queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "t-retry",
		TaskType: "consume",
		Status:   "failed",
		Payload:  json.RawMessage(`{"file_path":"/tmp/doc.pdf"}`),
	})

	c := &Container{db: db, logger: utils.NewDiscardLogger()}
	err := taskRetryHandler(c, []string{"t-retry"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

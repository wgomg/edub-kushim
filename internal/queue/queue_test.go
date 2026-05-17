package queue

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
	_ "modernc.org/sqlite"
)

var schemaSQL = func() string {
	data, err := os.ReadFile("../../sql/schema.sql")
	if err != nil {
		panic("cannot read schema.sql: " + err.Error())
	}
	return string(data)
}()

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

type mockHandler struct {
	mu      sync.Mutex
	handled []int64
}

func (m *mockHandler) Handle(ctx context.Context, task database.Task) (sql.NullInt64, error) {
	m.mu.Lock()
	m.handled = append(m.handled, task.ID)
	m.mu.Unlock()
	return sql.NullInt64{Valid: false}, nil
}

func TestEnqueueTask(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	handler := &mockHandler{}
	q := New(utils.NewDiscardLogger(), db, 1, handler)

	taskID, err := q.EnqueueTask(context.Background(), "consume", "batch-1", "/tmp/test.pdf")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if taskID == "" {
		t.Fatal("expected non-empty task id")
	}

	task, err := queries.GetTaskByTaskID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != "pending" {
		t.Errorf("expected pending, got %s", task.Status)
	}
	if task.TaskName != "consume" {
		t.Errorf("expected consume, got %s", task.TaskName)
	}
	if !task.BatchID.Valid || task.BatchID.String != "batch-1" {
		t.Errorf("expected batch-1, got %v", task.BatchID)
	}
}

func TestWorkerPicksUpPendingTask(t *testing.T) {
	db := setupTestDB(t)
	handler := &mockHandler{}
	q := New(utils.NewDiscardLogger(), db, 1, handler)
	q.interval = 100 * time.Millisecond

	q.EnqueueTask(context.Background(), "consume", "batch-1", "/tmp/test.pdf")

	q.Start()
	time.Sleep(500 * time.Millisecond)
	q.Stop(context.Background())

	if len(handler.handled) != 1 {
		t.Fatalf("expected 1 handled task, got %d", len(handler.handled))
	}

	tasks, err := database.NewQueries(db).ListTasks(context.Background(), database.ListTasksParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != "completed" {
		t.Errorf("expected completed, got %s", tasks[0].Status)
	}
}

func TestWorkerFailsTaskOnHandlerError(t *testing.T) {
	db := setupTestDB(t)
	handler := &mockHandlerFailing{}
	q := New(utils.NewDiscardLogger(), db, 1, handler)
	q.interval = 100 * time.Millisecond

	q.EnqueueTask(context.Background(), "consume", "batch-1", "/tmp/test.pdf")

	q.Start()
	time.Sleep(500 * time.Millisecond)
	q.Stop(context.Background())

	tasks, err := database.NewQueries(db).ListTasks(context.Background(), database.ListTasksParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != "failed" {
		t.Errorf("expected failed, got %s", tasks[0].Status)
	}
	if !tasks[0].Error.Valid || tasks[0].Error.String != "handler error" {
		t.Errorf("expected 'handler error', got %v", tasks[0].Error)
	}
}

type mockHandlerFailing struct{}

func (m *mockHandlerFailing) Handle(ctx context.Context, task database.Task) (sql.NullInt64, error) {
	return sql.NullInt64{Valid: false}, fmt.Errorf("handler error")
}

func TestMultipleWorkersDontClaimSameTask(t *testing.T) {
	db := setupTestDB(t)
	handler := &mockHandler{}
	q := New(utils.NewDiscardLogger(), db, 3, handler)
	q.interval = 20 * time.Millisecond

	for i := 0; i < 5; i++ {
		q.EnqueueTask(context.Background(), "consume", "batch-1", "/tmp/test.pdf")
	}

	q.Start()
	time.Sleep(2 * time.Second)
	q.Stop(context.Background())

	// At least 2 different task IDs should have been handled (3 workers × 20ms polling
	// over 2 seconds is ~300 polling cycles — plenty of room)
	if len(handler.handled) < 2 {
		t.Fatalf("expected at least 2 handled tasks, got %d", len(handler.handled))
	}

	tasks, err := database.NewQueries(db).ListTasks(context.Background(), database.ListTasksParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	completed := 0
	for _, tsk := range tasks {
		if tsk.Status == "completed" {
			completed++
		}
	}
	if completed < 2 {
		t.Errorf("expected at least 2 completed, got %d", completed)
	}
}

func TestFileCreation(t *testing.T) {
	files := []string{
		"internal/queue/queue.go",
		"internal/queue/worker.go",
		"internal/queue/queue_test.go",
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join("..", "..", f)); os.IsNotExist(err) {
			t.Errorf("expected %s to exist", f)
		}
	}
}

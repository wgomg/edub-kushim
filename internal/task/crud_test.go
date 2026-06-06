package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
)

func seedTask(t *testing.T, queries *database.Queries, overrides map[string]any) database.Task {
	t.Helper()

	taskID := uuid.New().String()
	taskType := "consume"
	status := "pending"
	var batchID sql.NullString
	payload := json.RawMessage(`{}`)

	if v, ok := overrides["task_id"]; ok {
		taskID = v.(string)
	}
	if v, ok := overrides["task_type"]; ok {
		taskType = v.(string)
	}
	if v, ok := overrides["status"]; ok {
		status = v.(string)
	}
	if v, ok := overrides["batch_id"]; ok {
		batchID = sql.NullString{String: v.(string), Valid: true}
	}
	if v, ok := overrides["payload"]; ok {
		payload = v.(json.RawMessage)
	}

	ctx := context.Background()
	result, err := queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   taskID,
		TaskType: taskType,
		Status:   status,
		BatchID:  batchID,
		Payload:  payload,
	})
	if err != nil {
		t.Fatalf("seedTask: %v", err)
	}

	id, _ := result.LastInsertId()
	task, err := queries.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("seedTask: get after insert: %v", err)
	}
	return task
}

func TestGet_ExistingTask(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	seeded := seedTask(t, queries, nil)

	task, err := Get(ctx, queries, seeded.TaskID)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if task.ID != seeded.ID {
		t.Errorf("task.ID = %d, want %d", task.ID, seeded.ID)
	}
}

func TestGet_NotFound(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	_, err := Get(ctx, queries, "nonexistent-uuid")
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Get() expected ErrTaskNotFound, got %v", err)
	}
}

func TestListFiltered_NoFilters(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	seedTask(t, queries, map[string]any{"task_id": "t1"})
	seedTask(t, queries, map[string]any{"task_id": "t2"})
	seedTask(t, queries, map[string]any{"task_id": "t3"})

	tasks, err := ListFiltered(ctx, queries, TaskFilter{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListFiltered() unexpected error: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("got %d tasks, want 3", len(tasks))
	}
}

func TestListFiltered_ByBatch(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	seedTask(t, queries, map[string]any{"task_id": "t1", "batch_id": "batch-a"})
	seedTask(t, queries, map[string]any{"task_id": "t2", "batch_id": "batch-a"})
	seedTask(t, queries, map[string]any{"task_id": "t3", "batch_id": "batch-b"})

	tasks, err := ListFiltered(ctx, queries, TaskFilter{BatchID: "batch-a", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListFiltered() unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks, want 2", len(tasks))
	}
	for _, tk := range tasks {
		if !tk.BatchID.Valid || tk.BatchID.String != "batch-a" {
			t.Errorf("task %s has batch_id=%v, want batch-a", tk.TaskID, tk.BatchID)
		}
	}
}

func TestListFiltered_ByStatus(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	seedTask(t, queries, map[string]any{"task_id": "t1", "status": "pending"})
	seedTask(t, queries, map[string]any{"task_id": "t2", "status": "processing"})
	seedTask(t, queries, map[string]any{"task_id": "t3", "status": "completed"})

	tasks, err := ListFiltered(ctx, queries, TaskFilter{Status: "pending", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListFiltered() unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("got %d tasks, want 1", len(tasks))
	}
	if len(tasks) > 0 && tasks[0].TaskID != "t1" {
		t.Errorf("task_id = %q, want t1", tasks[0].TaskID)
	}
}

func TestListFiltered_ByBatchAndStatus(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	seedTask(t, queries, map[string]any{"task_id": "t1", "batch_id": "batch-a", "status": "pending"})
	seedTask(t, queries, map[string]any{"task_id": "t2", "batch_id": "batch-a", "status": "completed"})
	seedTask(t, queries, map[string]any{"task_id": "t3", "batch_id": "batch-b", "status": "pending"})

	tasks, err := ListFiltered(ctx, queries, TaskFilter{BatchID: "batch-a", Status: "completed", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListFiltered() unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("got %d tasks, want 1", len(tasks))
	}
	if len(tasks) > 0 && tasks[0].TaskID != "t2" {
		t.Errorf("task_id = %q, want t2", tasks[0].TaskID)
	}
}

func TestRetry_FailedTask(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	seedTask(t, queries, map[string]any{
		"task_id": "retry-me",
		"status":  "failed",
	})

	err := Retry(ctx, queries, "retry-me")
	if err != nil {
		t.Fatalf("Retry() unexpected error: %v", err)
	}

	task, err := queries.GetTaskByTaskID(ctx, "retry-me")
	if err != nil {
		t.Fatalf("GetTaskByTaskID() failed: %v", err)
	}
	if task.Status != "pending" {
		t.Errorf("task status = %q, want %q", task.Status, "pending")
	}
}

func TestRetry_NotFailed(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	seedTask(t, queries, map[string]any{
		"task_id": "completed-task",
		"status":  "completed",
	})

	err := Retry(ctx, queries, "completed-task")
	if err == nil {
		t.Fatal("Retry() expected error for non-failed task, got nil")
	}
}

func TestRetry_NotFound(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	err := Retry(ctx, queries, "nonexistent-uuid")
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Retry() expected ErrTaskNotFound, got %v", err)
	}
}

func TestCountBatchStatuses(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	// batch-a: 1 waiting, 1 pending, 1 processing, 2 completed, 1 failed = 6 total
	seedTask(t, queries, map[string]any{"task_id": "a0", "batch_id": "batch-a", "status": "waiting"})
	seedTask(t, queries, map[string]any{"task_id": "a1", "batch_id": "batch-a", "status": "pending"})
	seedTask(t, queries, map[string]any{"task_id": "a2", "batch_id": "batch-a", "status": "processing"})
	seedTask(t, queries, map[string]any{"task_id": "a3", "batch_id": "batch-a", "status": "completed"})
	seedTask(t, queries, map[string]any{"task_id": "a4", "batch_id": "batch-a", "status": "completed"})
	seedTask(t, queries, map[string]any{"task_id": "a5", "batch_id": "batch-a", "status": "failed"})

	counts := CountBatchStatuses(ctx, queries, "batch-a")

	if counts.Waiting != 1 {
		t.Errorf("Waiting = %d, want 1", counts.Waiting)
	}
	if counts.Pending != 1 {
		t.Errorf("Pending = %d, want 1", counts.Pending)
	}
	if counts.Processing != 1 {
		t.Errorf("Processing = %d, want 1", counts.Processing)
	}
	if counts.Completed != 2 {
		t.Errorf("Completed = %d, want 2", counts.Completed)
	}
	if counts.Failed != 1 {
		t.Errorf("Failed = %d, want 1", counts.Failed)
	}
	if counts.Total() != 6 {
		t.Errorf("Total() = %d, want 6", counts.Total())
	}
}

func TestCountBatchStatuses_EmptyBatch(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	counts := CountBatchStatuses(ctx, queries, "nonexistent-batch")

	if counts.Total() != 0 {
		t.Errorf("Total() = %d, want 0", counts.Total())
	}
}

func TestBatchCounts_Total(t *testing.T) {
	bc := BatchCounts{Waiting: 1, Pending: 2, Processing: 1, Completed: 5, Failed: 1}
	if bc.Total() != 10 {
		t.Errorf("Total() = %d, want 10", bc.Total())
	}
}

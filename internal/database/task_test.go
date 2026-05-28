package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
)

func taskTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
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
		CREATE INDEX idx_task_batch ON task(batch_id);
		CREATE INDEX idx_task_pending ON task(created_at) WHERE status = 'pending';
		CREATE UNIQUE INDEX idx_task_dedup ON task(task_type, dedup_key)
			WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL;
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func insertTask(t *testing.T, q *Queries, overrides map[string]any) int64 {
	t.Helper()
	taskID := "uuid-task"
	taskType := "consume"
	status := "pending"
	var batchID sql.NullString
	payload := json.RawMessage(`{}`)
	var dedupKey sql.NullString

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
	if v, ok := overrides["dedup_key"]; ok {
		dedupKey = sql.NullString{String: v.(string), Valid: true}
	}

	result, err := q.CreateTask(context.Background(), CreateTaskParams{
		TaskID:   taskID,
		TaskType: taskType,
		Status:   status,
		BatchID:  batchID,
		Payload:  payload,
		DedupKey: dedupKey,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestCreateTask(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	id := insertTask(t, q, map[string]any{"task_id": "create-test"})
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
}

func TestCreateTask_DuplicateTaskID(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "dup"})
	_, err := q.CreateTask(context.Background(), CreateTaskParams{
		TaskID:   "dup",
		TaskType: "consume",
		Status:   "pending",
		Payload:  json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation on task_id, got nil")
	}
}

func TestCreateTask_DedupConstraint(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{
		"task_id":   "t1",
		"task_type": "consume",
		"status":    "pending",
		"dedup_key": "same-key",
	})

	_, err := q.CreateTask(context.Background(), CreateTaskParams{
		TaskID:   "t2",
		TaskType: "consume",
		Status:   "pending",
		Payload:  json.RawMessage(`{}`),
		DedupKey: sql.NullString{String: "same-key", Valid: true},
	})
	if err == nil {
		t.Fatal("expected dedup constraint violation, got nil")
	}
}

func TestGetTask(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	id := insertTask(t, q, map[string]any{
		"task_id":   "get-test",
		"task_type": "test-type",
		"status":    "processing",
	})

	task, err := q.GetTask(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.TaskID != "get-test" {
		t.Errorf("TaskID = %q", task.TaskID)
	}
	if task.TaskType != "test-type" {
		t.Errorf("TaskType = %q", task.TaskType)
	}
	if task.Status != "processing" {
		t.Errorf("Status = %q", task.Status)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	_, err := q.GetTask(context.Background(), 999)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetTaskByTaskID(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "by-uuid", "batch_id": "b1"})
	task, err := q.GetTaskByTaskID(context.Background(), "by-uuid")
	if err != nil {
		t.Fatalf("GetTaskByTaskID: %v", err)
	}
	if !task.BatchID.Valid || task.BatchID.String != "b1" {
		t.Errorf("BatchID = %v", task.BatchID)
	}
}

func TestGetTaskByTaskID_NotFound(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	_, err := q.GetTaskByTaskID(context.Background(), "nonexistent")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetNextPendingTask(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "older", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "newer", "status": "pending"})

	id, err := q.GetNextPendingTask(context.Background())
	if err != nil {
		t.Fatalf("GetNextPendingTask: %v", err)
	}
	task, err := q.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if task.TaskID != "older" {
		t.Errorf("expected oldest task, got %q", task.TaskID)
	}
}

func TestGetNextPendingTask_None(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	_, err := q.GetNextPendingTask(context.Background())
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetNextPendingTaskOfType(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "a1", "task_type": "type-a", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "b1", "task_type": "type-b", "status": "pending"})

	id, err := q.GetNextPendingTaskOfType(context.Background(), "type-b")
	if err != nil {
		t.Fatalf("GetNextPendingTaskOfType: %v", err)
	}
	task, err := q.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if task.TaskType != "type-b" {
		t.Errorf("TaskType = %q", task.TaskType)
	}
}

func TestClaimTask(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	id := insertTask(t, q, map[string]any{"task_id": "claim-me"})

	rows, err := q.ClaimTask(context.Background(), id)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if rows != 1 {
		t.Errorf("rows affected = %d, want 1", rows)
	}

	task, _ := q.GetTask(context.Background(), id)
	if task.Status != "processing" {
		t.Errorf("Status = %q, want 'processing'", task.Status)
	}
}

func TestClaimTask_AlreadyClaimed(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	id := insertTask(t, q, map[string]any{"task_id": "already", "status": "processing"})

	rows, err := q.ClaimTask(context.Background(), id)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if rows != 0 {
		t.Errorf("rows affected = %d, want 0", rows)
	}
}

func TestCompleteTask(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	id := insertTask(t, q, map[string]any{"task_id": "complete-me", "status": "processing"})

	result := json.RawMessage(`{"document_id": 1}`)
	err := q.CompleteTask(context.Background(), CompleteTaskParams{
		Result: &result,
		ID:     id,
	})
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	task, _ := q.GetTask(context.Background(), id)
	if task.Status != "completed" {
		t.Errorf("Status = %q, want 'completed'", task.Status)
	}
	if !task.CompletedAt.Valid {
		t.Error("expected CompletedAt to be set")
	}
}

func TestFailTask(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	id := insertTask(t, q, map[string]any{"task_id": "fail-me", "status": "processing"})

	err := q.FailTask(context.Background(), FailTaskParams{
		Error: sql.NullString{String: "something went wrong", Valid: true},
		ID:    id,
	})
	if err != nil {
		t.Fatalf("FailTask: %v", err)
	}

	task, _ := q.GetTask(context.Background(), id)
	if task.Status != "failed" {
		t.Errorf("Status = %q, want 'failed'", task.Status)
	}
	if !task.Error.Valid || task.Error.String != "something went wrong" {
		t.Errorf("Error = %v", task.Error)
	}
}

func TestRetryTask(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	id := insertTask(t, q, map[string]any{"task_id": "retry-me", "status": "failed"})

	err := q.RetryTask(context.Background(), id)
	if err != nil {
		t.Fatalf("RetryTask: %v", err)
	}

	task, _ := q.GetTask(context.Background(), id)
	if task.Status != "pending" {
		t.Errorf("Status = %q, want 'pending'", task.Status)
	}
}

func TestDeleteTask(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	id := insertTask(t, q, nil)
	err := q.DeleteTask(context.Background(), id)
	if err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	_, err = q.GetTask(context.Background(), id)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestListTasks(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "t1"})
	insertTask(t, q, map[string]any{"task_id": "t2"})

	tasks, err := q.ListTasks(context.Background(), ListTasksParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks, want 2", len(tasks))
	}
}

func TestListTasksByBatch(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "b1t1", "batch_id": "batch-x"})
	insertTask(t, q, map[string]any{"task_id": "b1t2", "batch_id": "batch-x"})
	insertTask(t, q, map[string]any{"task_id": "other", "batch_id": "batch-y"})

	tasks, err := q.ListTasksByBatch(context.Background(), sql.NullString{String: "batch-x", Valid: true})
	if err != nil {
		t.Fatalf("ListTasksByBatch: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks, want 2", len(tasks))
	}
}

func TestListTasksByStatus(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "pending-1", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "completed-1", "status": "completed"})
	insertTask(t, q, map[string]any{"task_id": "pending-2", "status": "pending"})

	tasks, err := q.ListTasksByStatus(context.Background(), ListTasksByStatusParams{
		Status: "pending",
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListTasksByStatus: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks, want 2", len(tasks))
	}
}

func TestListTasksByBatchAndStatus(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "b1p1", "batch_id": "batch-z", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "b1p2", "batch_id": "batch-z", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "b1c1", "batch_id": "batch-z", "status": "completed"})

	tasks, err := q.ListTasksByBatchAndStatus(context.Background(), ListTasksByBatchAndStatusParams{
		BatchID: sql.NullString{String: "batch-z", Valid: true},
		Status:  "pending",
	})
	if err != nil {
		t.Fatalf("ListTasksByBatchAndStatus: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks, want 2", len(tasks))
	}
}

func TestGetTaskByBatchID(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "a1", "batch_id": "batch-q"})
	insertTask(t, q, map[string]any{"task_id": "a2", "batch_id": "batch-q"})

	tasks, err := q.GetTaskByBatchID(context.Background(), sql.NullString{String: "batch-q", Valid: true})
	if err != nil {
		t.Fatalf("GetTaskByBatchID: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks, want 2", len(tasks))
	}
}

func TestCountTasksByBatchAndStatus(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "c1", "batch_id": "batch-c", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "c2", "batch_id": "batch-c", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "c3", "batch_id": "batch-c", "status": "completed"})

	count, err := q.CountTasksByBatchAndStatus(context.Background(), CountTasksByBatchAndStatusParams{
		BatchID: sql.NullString{String: "batch-c", Valid: true},
		Status:  "pending",
	})
	if err != nil {
		t.Fatalf("CountTasksByBatchAndStatus: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestCancelPendingTasksByBatch(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "pending-1", "batch_id": "batch-x", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "pending-2", "batch_id": "batch-x", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "completed-1", "batch_id": "batch-x", "status": "completed"})

	rows, err := q.CancelPendingTasksByBatch(context.Background(), sql.NullString{String: "batch-x", Valid: true})
	if err != nil {
		t.Fatalf("CancelPendingTasksByBatch: %v", err)
	}
	if rows != 2 {
		t.Errorf("rows affected = %d, want 2", rows)
	}

	task, _ := q.GetTaskByTaskID(context.Background(), "pending-1")
	if task.Status != "cancelled" {
		t.Errorf("Status = %q, want 'cancelled'", task.Status)
	}
}

func TestCancelProcessingTasksByBatch(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "proc-1", "batch_id": "batch-y", "status": "processing"})
	insertTask(t, q, map[string]any{"task_id": "proc-2", "batch_id": "batch-y", "status": "processing"})

	rows, err := q.CancelProcessingTasksByBatch(context.Background(), sql.NullString{String: "batch-y", Valid: true})
	if err != nil {
		t.Fatalf("CancelProcessingTasksByBatch: %v", err)
	}
	if rows != 2 {
		t.Errorf("rows affected = %d, want 2", rows)
	}
}

func TestCountDistinctBatches(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "d1", "batch_id": "batch-d"})
	insertTask(t, q, map[string]any{"task_id": "d2", "batch_id": "batch-d"})
	insertTask(t, q, map[string]any{"task_id": "e1", "batch_id": "batch-e"})
	insertTask(t, q, map[string]any{"task_id": "nil1"})

	count, err := q.CountDistinctBatches(context.Background())
	if err != nil {
		t.Fatalf("CountDistinctBatches: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestCountDistinctBatches_Empty(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	count, err := q.CountDistinctBatches(context.Background())
	if err != nil {
		t.Fatalf("CountDistinctBatches: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestCountAllTasks(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "f1"})
	insertTask(t, q, map[string]any{"task_id": "f2"})
	insertTask(t, q, map[string]any{"task_id": "f3"})

	count, err := q.CountAllTasks(context.Background())
	if err != nil {
		t.Fatalf("CountAllTasks: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestCountTasksByStatus(t *testing.T) {
	db := taskTestDB(t)
	q := New(db)

	insertTask(t, q, map[string]any{"task_id": "s1", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "s2", "status": "pending"})
	insertTask(t, q, map[string]any{"task_id": "s3", "status": "completed"})
	insertTask(t, q, map[string]any{"task_id": "s4", "status": "failed"})

	rows, err := q.CountTasksByStatus(context.Background())
	if err != nil {
		t.Fatalf("CountTasksByStatus: %v", err)
	}

	byStatus := map[string]int64{}
	for _, r := range rows {
		byStatus[r.Status] = r.Count
	}

	if byStatus["pending"] != 2 {
		t.Errorf("pending = %d, want 2", byStatus["pending"])
	}
	if byStatus["completed"] != 1 {
		t.Errorf("completed = %d, want 1", byStatus["completed"])
	}
	if byStatus["failed"] != 1 {
		t.Errorf("failed = %d, want 1", byStatus["failed"])
	}
}

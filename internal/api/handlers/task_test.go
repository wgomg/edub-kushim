package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
	_ "modernc.org/sqlite"
)

func taskHandlerTestDB(t *testing.T) *sql.DB {
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
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func seedTask(t *testing.T, q *database.Queries, taskID, status, batchID string) {
	t.Helper()
	var bid sql.NullString
	if batchID != "" {
		bid = sql.NullString{String: batchID, Valid: true}
	}
	payload := json.RawMessage(`{"file_path":"/tmp/` + taskID + `.pdf"}`)
	_, err := q.CreateTask(context.Background(), database.CreateTaskParams{
		TaskID:   taskID,
		TaskType: "consume",
		Status:   status,
		BatchID:  bid,
		Payload:  payload,
	})
	if err != nil {
		t.Fatalf("seedTask: %v", err)
	}
}

func makeReq(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	ctx := context.WithValue(req.Context(), "reqid", "test-req")
	req = req.WithContext(ctx)
	pb := utils.NewParamBag(req)
	req = utils.WithParamBag(req, pb)
	return req
}

func TestNewTaskHandler(t *testing.T) {
	db := taskHandlerTestDB(t)
	q := database.New(db)
	h := NewTaskHandler(q, utils.NewDiscardLogger())
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestTaskList_Empty(t *testing.T) {
	db := taskHandlerTestDB(t)
	q := database.New(db)
	h := NewTaskHandler(q, utils.NewDiscardLogger())

	w := httptest.NewRecorder()
	req := makeReq(t, "GET", "/tasks", "")
	h.ListTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp types.ListTasksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tasks) != 0 {
		t.Errorf("got %d tasks, want 0", len(resp.Tasks))
	}
}

func TestTaskList_WithTasks(t *testing.T) {
	db := taskHandlerTestDB(t)
	q := database.New(db)
	h := NewTaskHandler(q, utils.NewDiscardLogger())

	seedTask(t, q, "t1", "completed", "batch-a")
	seedTask(t, q, "t2", "failed", "batch-a")

	w := httptest.NewRecorder()
	req := makeReq(t, "GET", "/tasks", "")
	h.ListTasks(w, req)

	var resp types.ListTasksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(resp.Tasks))
	}
}

func TestTaskList_FilterByBatch(t *testing.T) {
	db := taskHandlerTestDB(t)
	q := database.New(db)
	h := NewTaskHandler(q, utils.NewDiscardLogger())

	seedTask(t, q, "b1", "pending", "batch-x")
	seedTask(t, q, "b2", "processing", "batch-x")
	seedTask(t, q, "other", "pending", "batch-y")

	w := httptest.NewRecorder()
	req := makeReq(t, "GET", "/tasks?batch=batch-x", "")
	h.ListTasks(w, req)

	var resp types.ListTasksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(resp.Tasks))
	}
	if resp.BatchID != "batch-x" {
		t.Errorf("BatchID = %q", resp.BatchID)
	}
	if resp.Summary == nil {
		t.Fatal("expected summary")
	}
	if resp.Summary.Total != 2 {
		t.Errorf("Total = %d, want 2", resp.Summary.Total)
	}
}

func TestTaskList_FilterByStatus(t *testing.T) {
	db := taskHandlerTestDB(t)
	q := database.New(db)
	h := NewTaskHandler(q, utils.NewDiscardLogger())

	seedTask(t, q, "p1", "pending", "")
	seedTask(t, q, "c1", "completed", "")
	seedTask(t, q, "p2", "pending", "")

	w := httptest.NewRecorder()
	req := makeReq(t, "GET", "/tasks?status=pending", "")
	h.ListTasks(w, req)

	var resp types.ListTasksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(resp.Tasks))
	}
}

func TestTaskGet_Success(t *testing.T) {
	db := taskHandlerTestDB(t)
	q := database.New(db)
	h := NewTaskHandler(q, utils.NewDiscardLogger())

	seedTask(t, q, "get-me", "completed", "")

	w := httptest.NewRecorder()
	req := makeReq(t, "GET", "/tasks/get-me", "")
	req.SetPathValue("id", "get-me")
	h.GetTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp types.TaskResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TaskID != "get-me" {
		t.Errorf("TaskID = %q", resp.TaskID)
	}
	if resp.Status != "completed" {
		t.Errorf("Status = %q", resp.Status)
	}
}

func TestTaskGet_NotFound(t *testing.T) {
	db := taskHandlerTestDB(t)
	q := database.New(db)
	h := NewTaskHandler(q, utils.NewDiscardLogger())

	w := httptest.NewRecorder()
	req := makeReq(t, "GET", "/tasks/nonexistent", "")
	req.SetPathValue("id", "nonexistent")
	h.GetTask(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestTaskGet_DocumentIDExtracted(t *testing.T) {
	db := taskHandlerTestDB(t)
	q := database.New(db)
	h := NewTaskHandler(q, utils.NewDiscardLogger())

	payload := json.RawMessage(`{"file_path":"/tmp/with-res.pdf"}`)
	result := json.RawMessage(`{"document_id":42}`)
	_, err := q.CreateTask(context.Background(), database.CreateTaskParams{
		TaskID:   "with-result",
		TaskType: "consume",
		Status:   "completed",
		Payload:  payload,
	})
	if err != nil {
		t.Fatal(err)
	}

	taskRow, err := q.GetTaskByTaskID(context.Background(), "with-result")
	if err != nil {
		t.Fatal(err)
	}
	err = q.CompleteTask(context.Background(), database.CompleteTaskParams{
		Result: &result,
		ID:     taskRow.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	req := makeReq(t, "GET", "/tasks/with-result", "")
	req.SetPathValue("id", "with-result")
	h.GetTask(w, req)

	var resp types.TaskResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DocumentID == nil || *resp.DocumentID != 42 {
		t.Errorf("DocumentID = %v, want 42", resp.DocumentID)
	}
}

func TestGetBatchSummary(t *testing.T) {
	db := taskHandlerTestDB(t)
	q := database.New(db)
	h := NewTaskHandler(q, utils.NewDiscardLogger())

	seedTask(t, q, "a1", "pending", "batch-sum")
	seedTask(t, q, "a2", "processing", "batch-sum")
	seedTask(t, q, "a3", "completed", "batch-sum")
	seedTask(t, q, "a4", "failed", "batch-sum")

	w := httptest.NewRecorder()
	req := makeReq(t, "GET", "/batches/batch-sum", "")
	req.SetPathValue("id", "batch-sum")
	h.GetBatchSummary(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp types.BatchSummaryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.BatchID != "batch-sum" {
		t.Errorf("BatchID = %q", resp.BatchID)
	}
	if resp.Total != 4 {
		t.Errorf("Total = %d, want 4", resp.Total)
	}
	if resp.Pending != 1 {
		t.Errorf("Pending = %d, want 1", resp.Pending)
	}
	if resp.Completed != 1 {
		t.Errorf("Completed = %d, want 1", resp.Completed)
	}
	if resp.Failed != 1 {
		t.Errorf("Failed = %d, want 1", resp.Failed)
	}
	if resp.Processing != 1 {
		t.Errorf("Processing = %d, want 1", resp.Processing)
	}
}

func TestBuildBatchSummary(t *testing.T) {
	db := taskHandlerTestDB(t)
	q := database.New(db)

	seedTask(t, q, "s1", "pending", "batch-s")
	seedTask(t, q, "s2", "completed", "batch-s")

	summary := buildBatchSummary(context.Background(), q, "batch-s")
	if summary.Total != 2 {
		t.Errorf("Total = %d, want 2", summary.Total)
	}
}

func TestTaskToResponse(t *testing.T) {
	now := time.Now()
	taskRow := database.Task{
		TaskID:    "resp-test",
		BatchID:   sql.NullString{String: "b1", Valid: true},
		Status:    "completed",
		Payload:   json.RawMessage(`{"file_path":"/tmp/report.pdf"}`),
		Result:    &json.RawMessage{},
		Error:     sql.NullString{String: "", Valid: false},
		CreatedAt: sql.NullTime{Time: now, Valid: true},
		StartedAt: sql.NullTime{Time: now, Valid: true},
	}

	resp := taskToResponse(taskRow)
	if resp.TaskID != "resp-test" {
		t.Errorf("TaskID = %q", resp.TaskID)
	}
	if resp.BatchID != "b1" {
		t.Errorf("BatchID = %q", resp.BatchID)
	}
	if resp.FileName != "report.pdf" {
		t.Errorf("FileName = %q", resp.FileName)
	}
	if resp.Status != "completed" {
		t.Errorf("Status = %q", resp.Status)
	}
}

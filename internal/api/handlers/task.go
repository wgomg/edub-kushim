package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TaskHandler struct {
	queries *database.Queries
	logger  *utils.Logger
}

func NewTaskHandler(queries *database.Queries, logger *utils.Logger) *TaskHandler {
	return &TaskHandler{
		queries: queries,
		logger:  logger,
	}
}

func (h *TaskHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	pb := utils.GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	batchID := pb.Get("batch", "")
	statusFilter := pb.Get("status", "")
	limit := pb.GetInt64("limit", 20, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 0)

	tasks, err := task.ListFiltered(ctx, h.queries, task.TaskFilter{
		BatchID: batchID,
		Status:  statusFilter,
		Limit:   limit,
		Offset:  offset,
	})

	if err != nil {
		h.logger.Error(&reqID, "list tasks: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	resp := types.ListTasksResponse{
		Tasks: make([]types.TaskResponse, 0, len(tasks)),
	}

	if batchID != "" {
		resp.BatchID = batchID
		s := buildBatchSummary(ctx, h.queries, batchID)
		resp.Summary = &s
	}

	for _, t := range tasks {
		resp.Tasks = append(resp.Tasks, taskToResponse(t))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *TaskHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	taskID := r.PathValue("id")

	t, err := task.Get(ctx, h.queries, taskID)
	if err != nil {
		if errors.Is(err, task.ErrTaskNotFound) {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		h.logger.Error(&reqID, "get task %s: %v", taskID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(taskToResponse(t))
}

func (h *TaskHandler) GetBatchSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	batchID := r.PathValue("id")

	summary := buildBatchSummary(ctx, h.queries, batchID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)

	h.logger.Debug(&reqID, "batch summary: %+v", summary)
}

func buildBatchSummary(ctx context.Context, queries *database.Queries, batchID string) types.BatchSummaryResponse {
	bc := task.CountBatchStatuses(ctx, queries, batchID)

	return types.BatchSummaryResponse{
		BatchID:    batchID,
		Total:      bc.Total(),
		Pending:    bc.Pending,
		Processing: bc.Processing,
		Completed:  bc.Completed,
		Failed:     bc.Failed,
	}
}

func taskToResponse(t database.Task) types.TaskResponse {
	var docID *int64
	if t.Result != nil {
		var r struct {
			DocumentID int64 `json:"document_id"`
		}
		json.Unmarshal(*t.Result, &r)
		if r.DocumentID != 0 {
			docID = &r.DocumentID
		}
	}

	var errStr *string
	if t.Error.Valid {
		errStr = &t.Error.String
	}

	var started *string
	if t.StartedAt.Valid {
		v := t.StartedAt.Time.Format(time.RFC3339)
		started = &v
	}

	var completed *string
	if t.CompletedAt.Valid {
		v := t.CompletedAt.Time.Format(time.RFC3339)
		completed = &v
	}

	fileName := ""
	if t.Payload != nil {
		var p struct {
			FilePath string `json:"file_path"`
		}
		json.Unmarshal(t.Payload, &p)
		fileName = filepath.Base(p.FilePath)
	}

	return types.TaskResponse{
		TaskID:      t.TaskID,
		BatchID:     t.BatchID.String,
		FileName:    fileName,
		Status:      t.Status,
		DocumentID:  docID,
		Error:       errStr,
		CreatedAt:   t.CreatedAt.Time.Format(time.RFC3339),
		StartedAt:   started,
		CompletedAt: completed,
	}
}

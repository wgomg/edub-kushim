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

func (h *TaskHandler) ListBatches(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	pb := utils.GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	statusFilter := pb.Get("status", "")
	limit := pb.GetInt64("limit", 20, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 0)

	summaries, err := task.ListBatchSummaries(ctx, h.queries, task.BatchFilter{
		Status: statusFilter,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		h.logger.Error(&reqID, "list batches: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	resp := types.ListBatchesResponse{
		Batches: make([]types.BatchSummaryResponse, 0, len(summaries)),
	}

	for _, bc := range summaries {
		resp.Batches = append(resp.Batches, types.BatchSummaryResponse{
			BatchID:    bc.BatchID,
			Total:      bc.Total(),
			Waiting:    bc.Waiting,
			Pending:    bc.Pending,
			Processing: bc.Processing,
			Completed:  bc.Completed,
			Failed:     bc.Failed,
			Cancelled:  bc.Cancelled,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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

func (h *TaskHandler) GlobalSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	totalBatches, err := h.queries.CountDistinctBatches(ctx)
	if err != nil {
		h.logger.Error(&reqID, "count distinct batches: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	totalFiles, err := h.queries.CountAllTasks(ctx)
	if err != nil {
		h.logger.Error(&reqID, "count all tasks: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	rows, err := h.queries.CountTasksByStatus(ctx)
	if err != nil {
		h.logger.Error(&reqID, "count tasks by status: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	perStatus := map[string]int64{}
	for _, row := range rows {
		perStatus[row.Status] = row.Count
	}

	totalBytes, err := h.queries.SumDocumentFileSizes(ctx)
	if err != nil {
		h.logger.Error(&reqID, "sum document file sizes: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	resp := types.GlobalSummaryResponse{
		TotalBatches: totalBatches,
		TotalFiles:   totalFiles,
		Waiting:      perStatus["waiting"],
		Pending:      perStatus["pending"],
		Processing:   perStatus["processing"],
		Completed:    perStatus["completed"],
		Failed:       perStatus["failed"],
		Cancelled:    perStatus["cancelled"],
		TotalSizeGB:  float64(totalBytes) / (1024 * 1024 * 1024),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func buildBatchSummary(ctx context.Context, queries *database.Queries, batchID string) types.BatchSummaryResponse {
	bc := task.CountBatchStatuses(ctx, queries, batchID)

	return types.BatchSummaryResponse{
		BatchID:    batchID,
		Total:      bc.Total(),
		Waiting:    bc.Waiting,
		Pending:    bc.Pending,
		Processing: bc.Processing,
		Completed:  bc.Completed,
		Failed:     bc.Failed,
		Cancelled:  bc.Cancelled,
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
	payloadDocID := ""
	if t.Payload != nil {
		var p struct {
			FilePath   string `json:"file_path"`
			FileName   string `json:"file_name"`
			DocumentID string `json:"document_id"`
		}
		json.Unmarshal(t.Payload, &p)
		if p.FilePath != "" {
			fileName = filepath.Base(p.FilePath)
		} else {
			fileName = p.FileName
		}

		if t.TaskType == "enrich" || (t.TaskType == "consume" && t.Status == "completed") {
			payloadDocID = p.DocumentID
		}
	}

	return types.TaskResponse{
		TaskID:       t.TaskID,
		BatchID:      t.BatchID.String,
		TaskType:     t.TaskType,
		FileName:     fileName,
		PayloadDocID: payloadDocID,
		Status:       t.Status,
		DocumentID:   docID,
		Error:        errStr,
		CreatedAt:    t.CreatedAt.Time.Format(time.RFC3339),
		StartedAt:    started,
		CompletedAt:  completed,
	}
}

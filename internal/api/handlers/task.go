package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	itypes "github.com/wgomg/edub-kushim/internal/types"
)

// TaskHandler is the task and batch API: listings, the batch summary and
// dashboard analytics, retries, and the two-phase batch cancel.
type TaskHandler struct {
	services  *service.CrudServices
	client    *database.Client
	queries   *database.Queries
	logger    *log.Logger
	getConfig func() *config.Config
}

// NewTaskHandler wires the task handler to the services, the database and
// the dynamic config.
func NewTaskHandler(services *service.CrudServices, client *database.Client, logger *log.Logger, getConfig func() *config.Config) *TaskHandler {
	return &TaskHandler{
		services:  services,
		client:    client,
		queries:   client.Queries,
		logger:    logger,
		getConfig: getConfig,
	}
}

// ListTasks serves a batch's tasks, or with no batch filter the global
// task list.
func (h *TaskHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	pb := GetParamBag(r)
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
		Limit:   int32(limit),
		Offset:  int32(offset),
	})

	if err != nil {
		h.logger.Error(reqID, "list tasks: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	resp := ListTasksResponse{
		Tasks: make([]TaskResponse, 0, len(tasks)),
	}

	if batchID != "" {
		resp.BatchID = batchID
		s, err := h.services.Batch.GetSummary(ctx, batchID)
		if err != nil {
			h.logger.Error(reqID, "get batch summary for %s: %v", batchID, err)
			s = &service.BatchSummary{BatchID: batchID, OwnerState: "none"}
		}
		summary := batchSummaryToResponse(s)
		resp.Summary = &summary
	}

	for _, t := range tasks {
		resp.Tasks = append(resp.Tasks, taskToResponse(t))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// GetTask serves one task's row with its progress.
func (h *TaskHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	taskID := r.PathValue("id")

	t, err := task.Get(ctx, h.queries, taskID)
	if err != nil {
		if errors.Is(err, task.ErrTaskNotFound) {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		h.logger.Error(reqID, "get task %s: %v", taskID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(taskDetailResponse(t))
}

// GetTagReconcileReport serves the latest completed reconcile task's
// report.
func (h *TaskHandler) GetTagReconcileReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	row, err := h.queries.GetLastCompletedTagReconcileTaskWithResult(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "no completed tag reconciliation run", http.StatusNotFound)
			return
		}
		h.logger.Error(reqID, "get tag reconciliation report: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	completedAt := ""
	if row.CompletedAt.Valid {
		completedAt = row.CompletedAt.Time.Format(time.RFC3339)
	}

	result := json.RawMessage(nil)
	if row.Result != nil {
		result = *row.Result
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TagReconcileReportResponse{
		TaskID:      row.TaskID,
		CompletedAt: completedAt,
		Result:      result,
	})
}

// RetryTask resets one failed task for reprocessing.
func (h *TaskHandler) RetryTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	taskID := r.PathValue("id")

	if err := task.Retry(ctx, h.client, h.logger, taskID); err != nil {
		if errors.Is(err, task.ErrTaskNotFound) {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		h.logger.Error(reqID, "retry task %s: %v", taskID, err)
		http.Error(w, "can only retry failed tasks", http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RetryBatch resets one batch's failed tasks.
func (h *TaskHandler) RetryBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	batchID := r.PathValue("id")

	retried, err := h.services.Batch.RetryFailed(ctx, batchID)
	if err != nil {
		h.logger.Error(reqID, "retry batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"retried": retried})
}

// ListBatches serves the batch summaries, optionally filtered by status.
func (h *TaskHandler) ListBatches(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	statusFilter := pb.Get("status", "")
	limit := pb.GetInt64("limit", 20, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 0)

	summaries, err := h.services.Batch.ListSummaries(ctx, task.BatchFilter{
		Status: statusFilter,
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		h.logger.Error(reqID, "list batches: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	resp := ListBatchesResponse{
		Batches: make([]BatchSummaryResponse, 0, len(summaries)),
	}

	for _, s := range summaries {
		resp.Batches = append(resp.Batches, batchSummaryToResponse(&s))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func batchSummaryToResponse(s *service.BatchSummary) BatchSummaryResponse {
	return BatchSummaryResponse{
		BatchID: s.BatchID,
		Status:  s.Status,
		Source:  s.Source,
		BatchCounts: BatchCounts{
			Total:      s.Waiting + s.Pending + s.Processing + s.Completed + s.Failed + s.Cancelled + s.Discarded,
			Waiting:    s.Waiting,
			Pending:    s.Pending,
			Processing: s.Processing,
			Completed:  s.Completed,
			Failed:     s.Failed,
			Cancelled:  s.Cancelled,
			Discarded:  s.Discarded,
			OwnerState: s.OwnerState,
			Orphaned:   s.Orphaned,
		},
		OwnerPID: s.OwnerPID,
	}
}

// GetDashboard serves the dashboard: recent batches, running tasks,
// analytics, processing health and storage usage.
func (h *TaskHandler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	items := make([]BatchOverviewItem, 0)
	overviews, err := h.services.Batch.ListOverviews(ctx, 10, 0)
	if err != nil {
		h.logger.Error(reqID, "list batch overviews: %v", err)
	} else {
		for _, ov := range overviews {
			var createdAt string
			if !ov.CreatedAt.IsZero() {
				createdAt = ov.CreatedAt.Format(time.RFC3339)
			}
			items = append(items, BatchOverviewItem{
				BatchID:   ov.BatchID,
				Status:    ov.Status,
				Source:    ov.Source,
				CreatedAt: createdAt,
				BatchCounts: BatchCounts{
					Total:      ov.Total,
					Waiting:    ov.Waiting,
					Pending:    ov.Pending,
					Processing: ov.Processing,
					Completed:  ov.Completed,
					Failed:     ov.Failed,
					Cancelled:  ov.Cancelled,
					Discarded:  ov.Discarded,
					OwnerState: ov.OwnerState,
					Orphaned:   ov.Orphaned,
				},
				DurationMs: ov.DurationMs,
			})
		}
	}

	activeRows, err := h.queries.ListActiveTasks(ctx,
		database.ListActiveTasksParams{Limit: 25, Offset: 0})
	if err != nil {
		h.logger.Error(reqID, "list active tasks: %v", err)
	}
	runningTasks := make([]TaskResponse, 0, len(activeRows))
	for _, t := range activeRows {
		runningTasks = append(runningTasks, taskToResponse(t))
	}

	analytics := h.buildDocumentAnalytics(ctx, &reqID)

	var processingHealth *ProcessingHealth
	ph, err := h.buildProcessingHealth(ctx, &reqID)
	if err != nil {
		h.logger.Error(reqID, "processing health: %v", err)
	} else {
		processingHealth = ph
	}

	totalBatches, batchErr := h.services.Batch.CountDistinct(ctx)
	if batchErr != nil {
		h.logger.Error(reqID, "count distinct batches: %v", batchErr)
	}

	agg, aggErr := h.queries.DocumentAggregates(ctx)
	var totalFiles, totalBytes, processedBytes, totalPages, totalWords int64
	if aggErr != nil {
		h.logger.Error(reqID, "document aggregates: %v", aggErr)
	} else {
		totalFiles = agg.TotalFiles
		totalBytes = agg.TotalBytes
		processedBytes = agg.ProcessedBytes
		totalPages = agg.TotalPages
		totalWords = agg.TotalWords
	}

	statusRows, statusErr := h.queries.CountTasksByStatus(ctx)
	if statusErr != nil {
		h.logger.Error(reqID, "count tasks by status: %v", statusErr)
	}
	perStatus := map[string]int64{}
	if statusErr == nil {
		for _, r := range statusRows {
			perStatus[string(r.Status)] = r.Count
		}
	}

	activeCount := perStatus["waiting"] + perStatus["pending"] + perStatus["processing"]

	inboxFiles := int64(0)
	if cfg := h.getConfig(); cfg != nil {
		n, inboxErr := storage.CountFilePaths(cfg.Storage.ConsumptionDir, cfg.Consumer.SupportedFiles)
		if inboxErr != nil {
			h.logger.Error(reqID, "count inbox files: %v", inboxErr)
		} else {
			inboxFiles = int64(n)
		}
	}

	typeBreakdown, typeErr := h.queries.OriginalTypeBreakdown(ctx)
	if typeErr != nil {
		h.logger.Error(reqID, "original type breakdown: %v", typeErr)
	}

	trendRows, trendErr := h.queries.StorageTrendDaily(ctx)
	if trendErr != nil {
		h.logger.Error(reqID, "storage trend daily: %v", trendErr)
	}

	var avgFileSize int64
	if totalFiles > 0 {
		avgFileSize = totalBytes / totalFiles
	}

	typeStats := make([]OriginalTypeStat, 0)
	if typeErr == nil {
		typeStats = make([]OriginalTypeStat, 0, len(typeBreakdown))
		for _, m := range typeBreakdown {
			typeStats = append(typeStats, OriginalTypeStat{
				OriginalType: m.OriginalType,
				Count:        m.Count,
				TotalBytes:   m.TotalBytes,
			})
		}
	}

	var cumulative int64
	trendPoints := make([]StorageTrendPoint, 0)
	if trendErr == nil {
		trendPoints = make([]StorageTrendPoint, 0, len(trendRows))
		for _, t := range trendRows {
			cumulative += t.DailyBytes
			trendPoints = append(trendPoints, StorageTrendPoint{
				Date:            t.Day,
				DailyCount:      t.Count,
				DailyBytes:      t.DailyBytes,
				CumulativeBytes: cumulative,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(DashboardResponse{
		RecentBatches: items,
		RunningTasks: RunningTasksResponse{
			Count: activeCount,
			Tasks: runningTasks,
		},
		Analytics:             analytics,
		ProcessingHealth:      processingHealth,
		TotalBatches:          totalBatches,
		TotalFiles:            totalFiles,
		InboxFiles:            inboxFiles,
		Waiting:               perStatus["waiting"],
		Pending:               perStatus["pending"],
		Processing:            perStatus["processing"],
		Completed:             perStatus["completed"],
		Failed:                perStatus["failed"],
		Cancelled:             perStatus["cancelled"],
		Discarded:             perStatus["discarded"],
		OriginalsSizeBytes:    totalBytes,
		ProcessedSizeBytes:    processedBytes,
		OriginalTypeBreakdown: typeStats,
		StorageTrend:          trendPoints,
		AvgFileSizeBytes:      avgFileSize,
		TotalPages:            totalPages,
		TotalWords:            totalWords,
	})
}

func (h *TaskHandler) buildDocumentAnalytics(ctx context.Context, reqID *string) *DocumentAnalytics {
	id := ""
	if reqID != nil {
		id = *reqID
	}
	langRows, err := h.queries.LanguageDistribution(ctx)
	if err != nil {
		h.logger.Error(id, "language distribution: %v", err)
		return nil
	}
	docTypeRows, err := h.queries.DocumentTypeDistribution(ctx)
	if err != nil {
		h.logger.Error(id, "document type distribution: %v", err)
		return nil
	}
	tagRows, err := h.queries.TagFrequency(ctx)
	if err != nil {
		h.logger.Error(id, "tag frequency: %v", err)
		return nil
	}
	missing, err := h.queries.MissingCounts(ctx)
	if err != nil {
		h.logger.Error(id, "missing counts: %v", err)
		return nil
	}

	analytics := &DocumentAnalytics{
		LanguageDistribution:     make([]DistributionItem, 0, len(langRows)),
		DocumentTypeDistribution: make([]DistributionItem, 0, len(docTypeRows)),
		TagFrequency:             make([]DistributionItem, 0, len(tagRows)),
	}

	for _, r := range langRows {
		analytics.LanguageDistribution = append(analytics.LanguageDistribution, DistributionItem{Label: r.Label, Count: r.Count})
	}
	for _, r := range docTypeRows {
		analytics.DocumentTypeDistribution = append(analytics.DocumentTypeDistribution, DistributionItem{Label: r.Label, Count: r.Count})
	}
	for _, r := range tagRows {
		analytics.TagFrequency = append(analytics.TagFrequency, DistributionItem{Label: r.Label, Count: r.Count})
	}

	analytics.MissingLanguageCount = missing.MissingLanguage
	analytics.MissingTypeCount = missing.MissingType
	analytics.MissingTagsCount = missing.MissingTags

	return analytics
}

func (h *TaskHandler) buildProcessingHealth(ctx context.Context, reqID *string) (*ProcessingHealth, error) {
	id := ""
	if reqID != nil {
		id = *reqID
	}
	successRow, err := h.queries.TaskSuccessRate(ctx)
	if err != nil {
		return nil, fmt.Errorf("task success rate: %w", err)
	}

	var successRate float64
	total := successRow.Completed + successRow.Failed
	if total > 0 {
		successRate = float64(successRow.Completed) / float64(total)
	}

	durRow, err := h.queries.AvgTaskDurationMs(ctx)
	if err != nil {
		return nil, fmt.Errorf("avg task duration: %w", err)
	}

	activeIDs, err := h.services.Batch.ActiveIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("active batch ids: %w", err)
	}

	orphanedCount, err := h.services.Batch.CountOrphaned(ctx)
	if err != nil {
		h.logger.Debug(id, "count orphaned: %v", err)
	}

	missingTools := int64(0)
	cfg := h.getConfig()
	if cfg != nil {
		missingTools = int64(len(config.MissingExternalToolErrors(cfg)))
	}

	return &ProcessingHealth{
		SuccessRate:     successRate,
		CompletedLast7d: successRow.Completed,
		FailedLast7d:    successRow.Failed,
		AvgDurationMs:   durRow.AvgDurationMs,
		ActiveBatches:   int64(len(activeIDs)),
		OrphanedBatches: orphanedCount,
		MissingTools:    missingTools,
	}, nil
}

// GetBatchSummary serves one batch's per-status counts.
func (h *TaskHandler) GetBatchSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	batchID := r.PathValue("id")

	s, err := h.services.Batch.GetSummary(ctx, batchID)
	if err != nil {
		h.logger.Error(reqID, "get batch summary %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(batchSummaryToResponse(s))

	h.logger.Debug(reqID, "batch summary: %+v", s)
}

// ResumeBatch moves a paused batch back to queued.
func (h *TaskHandler) ResumeBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	batchID := r.PathValue("id")

	hasWork, err := h.services.Batch.HasPendingWork(ctx, batchID)
	if err != nil {
		h.logger.Error(reqID, "check pending work for batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !hasWork {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"resumed": false,
			"reason":  "batch is already settled",
		})
		return
	}

	locked, err := h.services.Batch.IsLockedByLiveOwner(ctx, batchID)
	if err == nil && locked {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "batch is locked by a live owner",
		})
		return
	}

	if _, err := h.services.Batch.ResetProcessingTasksByBatch(ctx, batchID); err != nil {
		h.logger.Error(reqID, "reset processing tasks for batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.services.Batch.RequeueBatch(ctx, batchID); err != nil {
		h.logger.Error(reqID, "requeue batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Info(reqID, "enqueued batch %s for resumption", batchID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"resumed": true,
	})
}

// CancelBatch runs the two-phase cancel: pending tasks cancel, the owner
// is signalled, and processing tasks are discarded when it steps down.
func (h *TaskHandler) CancelBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	batchID := r.PathValue("id")

	pendingCancelled, ownerPID, ownerID, err := h.services.Batch.BeginCancel(ctx, batchID)
	if err != nil {
		h.logger.Error(reqID, "cancel batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	signalSent := false
	processingCancelled := int64(0)

	if ownerPID > 0 {
		killErr := syscall.Kill(int(ownerPID), syscall.SIGTERM)
		if killErr == nil {
			signalSent = true
		}
	}

	processingCancelled, err = h.services.Batch.CompleteCancel(ctx, batchID, ownerID)
	if err != nil {
		h.logger.Error(reqID, "complete cancel batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"batch_id":             batchID,
		"cancelled_pending":    pendingCancelled,
		"cancelled_processing": processingCancelled,
		"signal_sent":          signalSent,
	})
}

// Keep in sync with configtask.DedupKey(): drift here silently degrades
// config/backup labels to the bare task type.
func taskLabel(taskType itypes.TaskType, dedupKey sql.NullString) string {
	key := dedupKey.String
	switch {
	case strings.HasPrefix(key, "backup:"):
		mode := strings.TrimPrefix(key, "backup:")
		if i := strings.IndexByte(mode, ':'); i >= 0 {
			mode = mode[:i]
		}
		if mode == "" {
			return "Backup"
		}
		return "Backup (" + mode + ")"
	case strings.HasPrefix(key, "mirror:"):
		return "Mirror"
	case key == "config:migrate-db":
		return "Database migration"
	case key == "config:migrate-storage":
		return "Storage migration"
	case strings.HasPrefix(key, "config:tessdata:"):
		return "Download tessdata (" + strings.TrimPrefix(key, "config:tessdata:") + ")"
	case key == "config:hugot":
		return "Download Hugot model"
	}
	switch taskType {
	case itypes.Task.Type.Consume:
		return "Consume"
	case itypes.Task.Type.Enrich:
		return "Enrich"
	case itypes.Task.Type.Thumbnail:
		return "Thumbnail"
	case itypes.Task.Type.ThumbBackfill:
		return "Thumbnail backfill"
	default:
		return string(taskType)
	}
}

func taskToResponse(t database.Task) TaskResponse {
	return buildTaskResponse(t, false)
}

func taskDetailResponse(t database.Task) TaskResponse {
	return buildTaskResponse(t, true)
}

func buildTaskResponse(t database.Task, includeResult bool) TaskResponse {
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

	var progress *TaskProgressResponse
	if t.Progress != nil {
		var p task.ProgressSnapshot
		if json.Unmarshal(*t.Progress, &p) == nil && p.Step != "" {
			progress = &TaskProgressResponse{
				Step:      p.Step,
				Detail:    p.Detail,
				Pct:       p.Pct,
				UpdatedAt: p.UpdatedAt,
			}
		}
	}

	fileName := ""
	payloadDocID := ""
	if t.Payload != nil {
		var p struct {
			FilePath   string `json:"file_path"`
			FileName   string `json:"file_name"`
			DocumentID string `json:"document_id"`
		}
		_ = json.Unmarshal(*t.Payload, &p)
		payloadDocID = p.DocumentID
		if p.FilePath != "" {
			fileName = filepath.Base(p.FilePath)
		} else {
			fileName = p.FileName
		}
	}

	label := taskLabel(t.TaskType, t.DedupKey)
	if fileName != "" {
		switch t.TaskType {
		case itypes.Task.Type.Consume, itypes.Task.Type.Enrich, itypes.Task.Type.Thumbnail:
			label += ": " + fileName
		default:
			label = fileName
		}
	}

	var result json.RawMessage
	if includeResult && t.TaskType == itypes.Task.Type.TagReconcile && t.Result != nil {
		result = *t.Result
	}

	return TaskResponse{
		TaskID:       t.TaskID,
		BatchID:      t.BatchID.String,
		TaskType:     string(t.TaskType),
		FileName:     fileName,
		PayloadDocID: payloadDocID,
		Status:       string(t.Status),
		Error:        errStr,
		Label:        label,
		Progress:     progress,
		CreatedAt:    t.CreatedAt.Time.Format(time.RFC3339),
		StartedAt:    started,
		CompletedAt:  completed,
		Result:       result,
	}
}

// TaskProgressResponse is the persisted progress snapshot of a task.
type TaskProgressResponse struct {
	Step      string  `json:"step"`
	Detail    string  `json:"detail,omitempty"`
	Pct       float64 `json:"pct,omitempty"`
	UpdatedAt string  `json:"updated_at"`
}

// TaskResponse is one task as the API shows it: identity, status, the
// payload's file name and document id, the label, the progress snapshot,
// and the result once done.
type TaskResponse struct {
	TaskID       string                `json:"task_id"`
	BatchID      string                `json:"batch_id"`
	TaskType     string                `json:"task_type"`
	FileName     string                `json:"file_name"`
	PayloadDocID string                `json:"payload_doc_id"`
	Status       string                `json:"status"`
	Error        *string               `json:"error"`
	Label        string                `json:"label"`
	Progress     *TaskProgressResponse `json:"progress,omitempty"`
	CreatedAt    string                `json:"created_at"`
	StartedAt    *string               `json:"started_at"`
	CompletedAt  *string               `json:"completed_at"`
	Result       json.RawMessage       `json:"result,omitempty"`
}

// TagReconcileReportResponse points at the task whose result holds the
// reconcile report.
type TagReconcileReportResponse struct {
	TaskID      string          `json:"task_id"`
	CompletedAt string          `json:"completed_at"`
	Result      json.RawMessage `json:"result"`
}

// BatchCounts is the per-status task histogram of one batch, with the
// owner's liveness.
type BatchCounts struct {
	Total      int64  `json:"total"`
	Waiting    int64  `json:"waiting"`
	Pending    int64  `json:"pending"`
	Processing int64  `json:"processing"`
	Completed  int64  `json:"completed"`
	Failed     int64  `json:"failed"`
	Cancelled  int64  `json:"cancelled"`
	Discarded  int64  `json:"discarded"`
	OwnerState string `json:"owner_state,omitempty"`
	Orphaned   bool   `json:"orphaned"`
}

// BatchSummaryResponse is one batch's API view: identity, status, source
// and the counts.
type BatchSummaryResponse struct {
	BatchID string `json:"batch_id"`
	Status  string `json:"status"`
	Source  string `json:"source"`
	BatchCounts
	OwnerPID int64 `json:"owner_pid,omitempty"`
}

// ListBatchesResponse wraps the batch summaries.
type ListBatchesResponse struct {
	Batches []BatchSummaryResponse `json:"batches"`
}

// ListTasksResponse is one batch's task list, optionally with its
// summary.
type ListTasksResponse struct {
	BatchID string                `json:"batch_id,omitempty"`
	Summary *BatchSummaryResponse `json:"summary,omitempty"`
	Tasks   []TaskResponse        `json:"tasks"`
}

// OriginalTypeStat is one file-type bucket of the storage usage.
type OriginalTypeStat struct {
	OriginalType string `json:"original_type"`
	Count        int64  `json:"count"`
	TotalBytes   int64  `json:"total_bytes"`
}

// StorageTrendPoint is one day of the storage usage trend: documents and
// bytes that day, plus the running total.
type StorageTrendPoint struct {
	Date            string `json:"date"`
	DailyCount      int64  `json:"daily_count"`
	DailyBytes      int64  `json:"daily_bytes"`
	CumulativeBytes int64  `json:"cumulative_bytes"`
}

// BatchOverviewItem is one recent batch for the dashboard.
type BatchOverviewItem struct {
	BatchID   string `json:"batch_id"`
	Status    string `json:"status"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
	BatchCounts
	DurationMs *int64 `json:"duration_ms,omitempty"`
}

// DistributionItem is one labeled bucket of a distribution chart.
type DistributionItem struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// DocumentAnalytics is the dashboard's document analytics: language,
// type and tag distributions plus the missing-state counts.
type DocumentAnalytics struct {
	LanguageDistribution     []DistributionItem `json:"language_distribution"`
	DocumentTypeDistribution []DistributionItem `json:"document_type_distribution"`
	TagFrequency             []DistributionItem `json:"tag_frequency"`
	MissingLanguageCount     int64              `json:"missing_language_count"`
	MissingTypeCount         int64              `json:"missing_type_count"`
	MissingTagsCount         int64              `json:"missing_tags_count"`
}

// ProcessingHealth is the dashboard's processing health: success rate,
// last-week counts, average duration, and the batch and tool health.
type ProcessingHealth struct {
	SuccessRate     float64 `json:"success_rate"`
	CompletedLast7d int64   `json:"completed_last_7d"`
	FailedLast7d    int64   `json:"failed_last_7d"`
	AvgDurationMs   int64   `json:"avg_duration_ms"`
	ActiveBatches   int64   `json:"active_batches"`
	OrphanedBatches int64   `json:"orphaned_batches"`
	MissingTools    int64   `json:"missing_tools"`
}

// RunningTasksResponse lists the currently processing tasks.
type RunningTasksResponse struct {
	Count int64          `json:"count"`
	Tasks []TaskResponse `json:"tasks"`
}

// DashboardResponse is everything the dashboard needs in one payload.
type DashboardResponse struct {
	RecentBatches         []BatchOverviewItem  `json:"recent_batches"`
	RunningTasks          RunningTasksResponse `json:"running_tasks"`
	Analytics             *DocumentAnalytics   `json:"analytics,omitempty"`
	ProcessingHealth      *ProcessingHealth    `json:"processing_health,omitempty"`
	TotalBatches          int64                `json:"total_batches"`
	TotalFiles            int64                `json:"total_files"`
	InboxFiles            int64                `json:"inbox_files"`
	Waiting               int64                `json:"waiting"`
	Pending               int64                `json:"pending"`
	Processing            int64                `json:"processing"`
	Completed             int64                `json:"completed"`
	Failed                int64                `json:"failed"`
	Cancelled             int64                `json:"cancelled"`
	Discarded             int64                `json:"discarded"`
	OriginalsSizeBytes    int64                `json:"originals_size_bytes"`
	ProcessedSizeBytes    int64                `json:"processed_size_bytes"`
	OriginalTypeBreakdown []OriginalTypeStat   `json:"original_type_breakdown"`
	StorageTrend          []StorageTrendPoint  `json:"storage_trend"`
	AvgFileSizeBytes      int64                `json:"avg_file_size_bytes"`
	TotalPages            int64                `json:"total_pages"`
	TotalWords            int64                `json:"total_words"`
}

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
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/configtask"
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
	reqID := log.ReqIDFrom(ctx)

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

	writeJSON(w, http.StatusOK, resp)
}

// GetTask serves one task's row with its progress.
func (h *TaskHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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

	writeJSON(w, http.StatusOK, taskDetailResponse(t))
}

// GetTagReconcileReport serves the latest completed reconcile task's
// report.
func (h *TaskHandler) GetTagReconcileReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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

	writeJSON(w, http.StatusOK, TagReconcileReportResponse{
		TaskID:      row.TaskID,
		CompletedAt: completedAt,
		Result:      result,
	})
}

// RetryTask is a compatibility alias: it resolves the task's batch and resumes it with the failed tasks
// retried. Stage 8 moves the callers to the resume endpoint.
func (h *TaskHandler) RetryTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	taskID := r.PathValue("id")

	maxRetries, ok := h.reclaimMaxRetries(w)
	if !ok {
		return
	}

	if err := task.RetryTask(ctx, h.client, h.logger, taskID, maxRetries); err != nil {
		if !writeBatchLifecycleError(w, err) {
			h.logger.Error(reqID, "retry task %s: %v", taskID, err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RetryBatch is a compatibility alias: it resumes the batch with the failed tasks retried. Stage 8 moves
// the callers to the resume endpoint.
func (h *TaskHandler) RetryBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	batchID := r.PathValue("id")

	maxRetries, ok := h.reclaimMaxRetries(w)
	if !ok {
		return
	}

	result, err := task.ResumeBatch(ctx, h.client, h.logger, batchID, maxRetries, true)
	if err != nil {
		if !writeBatchLifecycleError(w, err) {
			h.logger.Error(reqID, "retry batch %s: %v", batchID, err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{"retried": result.FailedRetried})
}

// batchLifecycleError classifies a lifecycle sentinel into the HTTP status and message that every retry,
// resume and cancel surface shares, so the same failure is never reported differently by endpoint.
func batchLifecycleError(err error) (status int, message string, handled bool) {
	switch {
	case errors.Is(err, task.ErrBatchNotFound):
		return http.StatusNotFound, "batch not found", true
	case errors.Is(err, task.ErrTaskNotFound):
		return http.StatusNotFound, "task not found", true
	case errors.Is(err, task.ErrTaskNotFailed):
		return http.StatusConflict, "can only retry failed tasks", true
	case errors.Is(err, task.ErrBatchTerminal), errors.Is(err, task.ErrBatchLocked),
		errors.Is(err, task.ErrBatchNotResumable):
		return http.StatusConflict, err.Error(), true
	default:
		return http.StatusInternalServerError, "", false
	}
}

// writeBatchLifecycleError writes the classified response and reports whether one was written.
func writeBatchLifecycleError(w http.ResponseWriter, err error) bool {
	status, message, handled := batchLifecycleError(err)
	if handled {
		http.Error(w, message, status)
	}
	return handled
}

// ListBatches serves the batch summaries, optionally filtered by status.
func (h *TaskHandler) ListBatches(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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

	writeJSON(w, http.StatusOK, resp)
}

func batchSummaryToResponse(s *service.BatchSummary) BatchSummaryResponse {
	return BatchSummaryResponse{
		BatchID:  s.BatchID,
		Status:   s.Status,
		Origin:   s.Origin,
		Schedule: s.Schedule,
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
	reqID := log.ReqIDFrom(ctx)

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
				Origin:    ov.Origin,
				Schedule:  ov.Schedule,
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

	analytics := h.buildDocumentAnalytics(ctx, reqID)

	var processingHealth *ProcessingHealth
	ph, err := h.buildProcessingHealth(ctx, reqID)
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

	writeJSON(w, http.StatusOK, DashboardResponse{
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

func (h *TaskHandler) buildDocumentAnalytics(ctx context.Context, reqID string) *DocumentAnalytics {
	langRows, err := h.queries.LanguageDistribution(ctx)
	if err != nil {
		h.logger.Error(reqID, "language distribution: %v", err)
		return nil
	}
	docTypeRows, err := h.queries.DocumentTypeDistribution(ctx)
	if err != nil {
		h.logger.Error(reqID, "document type distribution: %v", err)
		return nil
	}
	tagRows, err := h.queries.TagFrequency(ctx)
	if err != nil {
		h.logger.Error(reqID, "tag frequency: %v", err)
		return nil
	}
	missing, err := h.queries.MissingCounts(ctx)
	if err != nil {
		h.logger.Error(reqID, "missing counts: %v", err)
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

func (h *TaskHandler) buildProcessingHealth(ctx context.Context, reqID string) (*ProcessingHealth, error) {
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
		h.logger.Debug(reqID, "count orphaned: %v", err)
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
	reqID := log.ReqIDFrom(ctx)

	batchID := r.PathValue("id")

	s, err := h.services.Batch.GetSummary(ctx, batchID)
	if err != nil {
		h.logger.Error(reqID, "get batch summary %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, batchSummaryToResponse(s))

	h.logger.Debug(reqID, "batch summary: %+v", s)
}

// ResumeBatch requeues a settled or paused batch, resetting its processing tasks.
func (h *TaskHandler) ResumeBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	batchID := r.PathValue("id")

	maxRetries, ok := h.reclaimMaxRetries(w)
	if !ok {
		return
	}

	result, err := task.ResumeBatch(ctx, h.client, h.logger, batchID, maxRetries, false)
	if err != nil {
		if status, message, handled := batchLifecycleError(err); handled {
			writeJSON(w, status, map[string]string{"error": message})
			return
		}
		h.logger.Error(reqID, "resume batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !result.Resumed {
		writeJSON(w, http.StatusOK, map[string]any{
			"resumed": false,
			"reason":  "batch is already settled",
		})
		return
	}

	h.logger.Info(reqID, "enqueued batch %s for resumption", batchID)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"resumed": true,
	})
}

// CancelBatch cancels a batch through the single cancel operation: pending, waiting and processing tasks
// cancel, a live executor is signalled, and the lease and locks are released.
func (h *TaskHandler) CancelBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	batchID := r.PathValue("id")

	result, err := task.CancelBatch(ctx, h.client, h.logger, batchID)
	if err != nil {
		if status, message, handled := batchLifecycleError(err); handled {
			writeJSON(w, status, map[string]string{"error": message})
			return
		}
		h.logger.Error(reqID, "cancel batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"batch_id":             batchID,
		"cancelled_pending":    result.PendingCancelled,
		"cancelled_processing": result.ProcessingCancelled,
		"signal_sent":          result.Signalled,
	})
}

// reclaimMaxRetries returns the configured reclaim retry bound the resume resets use. A missing config
// means the server is not initialized and the request is refused.
func (h *TaskHandler) reclaimMaxRetries(w http.ResponseWriter) (int32, bool) {
	cfg := h.getConfig()
	if cfg == nil {
		http.Error(w, "not initialized", http.StatusServiceUnavailable)
		return 0, false
	}
	return int32(cfg.Consumer.Reclaim.MaxRetries), true
}

// Labels derive from the task type and, for the type-specific variants, from the dedup key.
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
	case strings.HasPrefix(key, configtask.DedupKeyTessdataPrefix):
		return "Download tessdata (" + strings.TrimPrefix(key, configtask.DedupKeyTessdataPrefix) + ")"
	case key == configtask.DedupKeyHugot:
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
	case itypes.Task.Type.MigrateDB:
		return "Database migration"
	case itypes.Task.Type.MigrateStorage:
		return "Storage migration"
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

// BatchSummaryResponse is one batch's API view: identity, status, origin, schedule and the counts.
type BatchSummaryResponse struct {
	BatchID  string `json:"batch_id"`
	Status   string `json:"status"`
	Origin   string `json:"origin"`
	Schedule string `json:"schedule,omitempty"`
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
	Origin    string `json:"origin"`
	Schedule  string `json:"schedule,omitempty"`
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

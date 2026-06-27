package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TaskHandler struct {
	queries *database.Queries
	logger  *utils.Logger
	cfg     *config.Config
}

func NewTaskHandler(queries *database.Queries, logger *utils.Logger, cfg *config.Config) *TaskHandler {
	return &TaskHandler{
		queries: queries,
		logger:  logger,
		cfg:     cfg,
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

func (h *TaskHandler) RetryTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	taskID := r.PathValue("id")

	if err := task.Retry(ctx, h.queries, taskID); err != nil {
		if errors.Is(err, task.ErrTaskNotFound) {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		h.logger.Error(&reqID, "retry task %s: %v", taskID, err)
		http.Error(w, "can only retry failed tasks", http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *TaskHandler) RetryBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	batchID := r.PathValue("id")

	retried, err := task.RetryBatchFailed(ctx, h.queries, batchID)
	if err != nil {
		h.logger.Error(&reqID, "retry batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"retried": retried})
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
		ownerState, pid, orphaned := enrichOwnerState(ctx, h.queries, bc.BatchID, bc.Pending, bc.Processing)
		resp.Batches = append(resp.Batches, types.BatchSummaryResponse{
			BatchID: bc.BatchID,
			BatchCounts: types.BatchCounts{
				Total:      bc.Total(),
				Waiting:    bc.Waiting,
				Pending:    bc.Pending,
				Processing: bc.Processing,
				Completed:  bc.Completed,
				Failed:     bc.Failed,
				Cancelled:  bc.Cancelled,
				Discarded:  bc.Discarded,
				OwnerState: ownerState,
				Orphaned:   orphaned,
			},
			OwnerPID: pid,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *TaskHandler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	rows, err := h.queries.ListBatchOverviews(ctx, database.ListBatchOverviewsParams{
		Limit:  20,
		Offset: 0,
	})
	if err != nil {
		h.logger.Error(&reqID, "list batch overviews: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	items := make([]types.BatchOverviewItem, 0, len(rows))
	for _, row := range rows {
		pending := toInt64(row.Pending)
		processing := toInt64(row.Processing)
		waiting := toInt64(row.Waiting)

		var durationMs *int64
		if pending == 0 && processing == 0 && waiting == 0 {
			firstStarted := toNullTime(row.FirstStartedAt)
			lastCompleted := toNullTime(row.LastCompletedAt)
			if firstStarted.Valid && lastCompleted.Valid {
				v := lastCompleted.Time.Sub(firstStarted.Time).Milliseconds()
				durationMs = &v
			}
		}

		var createdAt string
		if row.BatchCreatedAt.Valid {
			createdAt = row.BatchCreatedAt.Time.Format(time.RFC3339)
		}

		items = append(items, types.BatchOverviewItem{
			BatchID:   row.BatchID,
			Source:    row.Source,
			CreatedAt: createdAt,
			BatchCounts: types.BatchCounts{
				Total:      row.Total,
				Waiting:    waiting,
				Pending:    pending,
				Processing: processing,
				Completed:  toInt64(row.Completed),
				Failed:     toInt64(row.Failed),
				Cancelled:  toInt64(row.Cancelled),
				Discarded:  toInt64(row.Discarded),
				OwnerState: deriveOwnerState(row.OwnerLastHeartbeat).String(),
				Orphaned:   deriveIsOrphaned(row.OwnerLastHeartbeat, pending, processing),
			},
			DurationMs: durationMs,
		})
	}

	activityRows, err := h.queries.ListActivityTimeline(ctx)
	if err != nil {
		h.logger.Error(&reqID, "list activity timeline: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	activity := make([]types.ActivityEvent, 0, len(activityRows))
	for _, row := range activityRows {
		title := strings.TrimSpace(row.Title)
		if title == "" && row.PayloadFilePath != "" {
			title = filepath.Base(strings.TrimSpace(row.PayloadFilePath))
		}
		if title == "" {
			title = row.TaskID
		}

		var link string
		switch row.EventType {
		case "document_uploaded":
			link = "/documents/" + row.RefID
		case "task_completed", "task_failed":
			link = "/tasks/" + row.TaskID
		case "batch_created":
			link = "/tasks?batch=" + row.BatchID
		}

		var timestamp string
		if t, err := time.Parse("2006-01-02T15:04:05Z", row.EventTime); err == nil {
			timestamp = t.Format(time.RFC3339)
		} else if t, err := time.Parse("2006-01-02 15:04:05", row.EventTime); err == nil {
			timestamp = t.Format(time.RFC3339)
		} else {
			timestamp = row.EventTime
		}

		activity = append(activity, types.ActivityEvent{
			EventType: row.EventType,
			Title:     title,
			Timestamp: timestamp,
			Link:      link,
		})
	}

	analytics := h.buildDocumentAnalytics(ctx, &reqID)

	var processingHealth *types.ProcessingHealth
	ph, err := h.buildProcessingHealth(ctx, &reqID)
	if err != nil {
		h.logger.Error(&reqID, "processing health: %v", err)
	} else {
		processingHealth = ph
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(types.DashboardResponse{RecentBatches: items, Activity: activity, Analytics: analytics, ProcessingHealth: processingHealth})
}

func (h *TaskHandler) buildDocumentAnalytics(ctx context.Context, reqID *string) *types.DocumentAnalytics {
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

	analytics := &types.DocumentAnalytics{
		LanguageDistribution:     make([]types.DistributionItem, 0, len(langRows)),
		DocumentTypeDistribution: make([]types.DistributionItem, 0, len(docTypeRows)),
		TagFrequency:             make([]types.DistributionItem, 0, len(tagRows)),
	}

	for _, r := range langRows {
		analytics.LanguageDistribution = append(analytics.LanguageDistribution, types.DistributionItem{Label: r.Label, Count: r.Count})
	}
	for _, r := range docTypeRows {
		analytics.DocumentTypeDistribution = append(analytics.DocumentTypeDistribution, types.DistributionItem{Label: r.Label, Count: r.Count})
	}
	for _, r := range tagRows {
		analytics.TagFrequency = append(analytics.TagFrequency, types.DistributionItem{Label: r.Label, Count: r.Count})
	}

	analytics.MissingLanguageCount = missing.MissingLanguage
	analytics.MissingTypeCount = missing.MissingType
	analytics.MissingTagsCount = missing.MissingTags

	return analytics
}

func (h *TaskHandler) buildProcessingHealth(ctx context.Context, reqID *string) (*types.ProcessingHealth, error) {
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

	activeIDs, err := h.queries.ActiveBatchIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("active batch ids: %w", err)
	}

	orphanedCount := int64(0)
	for _, batchID := range activeIDs {
		state, _, err := task.BatchOwnerState(ctx, h.queries, batchID, task.StaleAfter)
		if err != nil {
			h.logger.Debug(reqID, "orphan check batch %s: %v", batchID, err)
			continue
		}
		if state != task.OwnerLive {
			orphanedCount++
		}
	}

	missingTools := int64(0)
	if h.cfg != nil {
		missingTools = int64(len(config.MissingExternalToolErrors(h.cfg)))
	}

	return &types.ProcessingHealth{
		SuccessRate:     successRate,
		CompletedLast7d: successRow.Completed,
		FailedLast7d:    successRow.Failed,
		AvgDurationMs:   durRow.AvgDurationMs,
		ActiveBatches:   int64(len(activeIDs)),
		OrphanedBatches: orphanedCount,
		MissingTools:    missingTools,
	}, nil
}

func deriveOwnerState(hb sql.NullTime) task.OwnerState {
	if !hb.Valid {
		return task.OwnerNone
	}
	if time.Since(hb.Time) > task.StaleAfter {
		return task.OwnerStale
	}
	return task.OwnerLive
}

func deriveIsOrphaned(hb sql.NullTime, pending, processing int64) bool {
	state := deriveOwnerState(hb)
	return task.IsOrphaned(state, pending, processing)
}

func enrichOwnerState(ctx context.Context, queries *database.Queries, batchID string, pending, processing int64) (string, int64, bool) {
	state, pid, err := task.BatchOwnerState(ctx, queries, batchID, task.StaleAfter)
	if err != nil {
		return "none", 0, false
	}
	return state.String(), pid, task.IsOrphaned(state, pending, processing)
}

func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func toNullTime(v interface{}) sql.NullTime {
	switch t := v.(type) {
	case string:
		parsed, err := time.Parse("2006-01-02T15:04:05Z", t)
		if err != nil {
			parsed, err = time.Parse("2006-01-02 15:04:05", t)
			if err != nil {
				return sql.NullTime{}
			}
		}
		return sql.NullTime{Time: parsed, Valid: true}
	case time.Time:
		return sql.NullTime{Time: t, Valid: true}
	default:
		return sql.NullTime{}
	}
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

	agg, err := h.queries.DocumentAggregates(ctx)
	if err != nil {
		h.logger.Error(&reqID, "document aggregates: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	totalFiles := agg.TotalFiles
	totalBytes := agg.TotalBytes

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

	mimeBreakdown, err := h.queries.MimeTypeBreakdown(ctx)
	if err != nil {
		h.logger.Error(&reqID, "mime type breakdown: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	trendRows, err := h.queries.StorageTrendDaily(ctx)
	if err != nil {
		h.logger.Error(&reqID, "storage trend daily: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var avgFileSize int64
	if totalFiles > 0 {
		avgFileSize = totalBytes / totalFiles
	}

	mimeStats := make([]types.MimeTypeStat, 0, len(mimeBreakdown))
	for _, m := range mimeBreakdown {
		mimeStats = append(mimeStats, types.MimeTypeStat{
			MimeType:   m.MimeType,
			Count:      m.Count,
			TotalBytes: m.TotalBytes,
		})
	}

	var cumulative int64
	trendPoints := make([]types.StorageTrendPoint, 0, len(trendRows))
	for _, t := range trendRows {
		cumulative += t.DailyBytes
		trendPoints = append(trendPoints, types.StorageTrendPoint{
			Date:            t.Day,
			DailyCount:      t.Count,
			DailyBytes:      t.DailyBytes,
			CumulativeBytes: cumulative,
		})
	}

	resp := types.GlobalSummaryResponse{
		TotalBatches:     totalBatches,
		TotalFiles:       totalFiles,
		Waiting:          perStatus["waiting"],
		Pending:          perStatus["pending"],
		Processing:       perStatus["processing"],
		Completed:        perStatus["completed"],
		Failed:           perStatus["failed"],
		Cancelled:        perStatus["cancelled"],
		Discarded:        perStatus["discarded"],
		TotalSizeGB:      float64(totalBytes) / (1024 * 1024 * 1024),
		MimeTypeBreakdown: mimeStats,
		StorageTrend:      trendPoints,
		AvgFileSizeBytes:  avgFileSize,
		TotalPages:        agg.TotalPages,
		TotalWords:        agg.TotalWords,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func buildBatchSummary(ctx context.Context, queries *database.Queries, batchID string) types.BatchSummaryResponse {
	bc := task.CountBatchStatuses(ctx, queries, batchID)
	ownerState, pid, orphaned := enrichOwnerState(ctx, queries, batchID, bc.Pending, bc.Processing)

	return types.BatchSummaryResponse{
		BatchID: batchID,
		BatchCounts: types.BatchCounts{
			Total:      bc.Total(),
			Waiting:    bc.Waiting,
			Pending:    bc.Pending,
			Processing: bc.Processing,
			Completed:  bc.Completed,
			Failed:     bc.Failed,
			Cancelled:  bc.Cancelled,
			Discarded:  bc.Discarded,
			OwnerState: ownerState,
			Orphaned:   orphaned,
		},
		OwnerPID: pid,
	}
}

func (h *TaskHandler) ResumeBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	batchID := r.PathValue("id")

	bc := task.CountBatchStatuses(ctx, h.queries, batchID)
	if bc.Pending == 0 && bc.Processing == 0 && bc.Waiting == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"resumed": false,
			"reason":  "batch is already settled",
		})
		return
	}

	state, _, err := task.BatchOwnerState(ctx, h.queries, batchID, task.StaleAfter)
	if err == nil && state == task.OwnerLive {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "batch is locked by a live owner",
		})
		return
	}

	kushimPath, err := kushimBinaryPath()
	if err != nil {
		h.logger.Error(&reqID, "kushim binary not found: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	cmd := exec.Command(kushimPath, "consume", "--batch", batchID, "--force")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		h.logger.Error(&reqID, "fork worker for resume batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	go func() {
		err := cmd.Wait()
		if err != nil {
			h.logger.Error(nil, "resume worker for batch %s exited: %v", batchID, err)
		} else {
			h.logger.Info(nil, "resume worker for batch %s completed", batchID)
		}
	}()

	h.logger.Info(&reqID, "forked resume worker for batch %s", batchID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"resumed": true,
	})
}

func (h *TaskHandler) CancelBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	batchID := r.PathValue("id")

	pendingCancelled, err := h.queries.CancelPendingTasksByBatch(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		h.logger.Error(&reqID, "cancel pending tasks for batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	bo, err := h.queries.GetBatchOwner(ctx, batchID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"batch_id":            batchID,
				"cancelled_pending":   pendingCancelled,
				"cancelled_processing": 0,
				"signal_sent":         false,
			})
			return
		}
		h.logger.Error(&reqID, "get batch owner for %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	signalSent := false
	processingCancelled := int64(0)

	if bo.Pid > 0 {
		killErr := syscall.Kill(int(bo.Pid), syscall.SIGTERM)
		if killErr == nil {
			processingCancelled, err = h.queries.CancelProcessingTasksByBatch(ctx, sql.NullString{String: batchID, Valid: true})
			if err != nil {
				h.logger.Error(&reqID, "cancel processing tasks for batch %s: %v", batchID, err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			signalSent = true
		}
	}

	h.queries.ReleaseBatchOwner(ctx, database.ReleaseBatchOwnerParams{
		BatchID: batchID,
		OwnerID: bo.OwnerID,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"batch_id":             batchID,
		"cancelled_pending":    pendingCancelled,
		"cancelled_processing": processingCancelled,
		"signal_sent":          signalSent,
	})
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

		if t.TaskType == "enrich" {
			if t.Status == "discarded" {
				var enrichPayload struct {
					WaitingFor string `json:"waiting_for"`
				}
				json.Unmarshal(t.Payload, &enrichPayload)
				payloadDocID = enrichPayload.WaitingFor
			} else {
				payloadDocID = p.DocumentID
			}
		} else if t.TaskType == "consume" && t.Status == "completed" {
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

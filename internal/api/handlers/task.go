package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	itypes "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TaskHandler struct {
	services  *itypes.CrudServices
	queries   *database.Queries
	logger    *utils.Logger
	getConfig func() *config.Config
}

func NewTaskHandler(services *itypes.CrudServices, queries *database.Queries, logger *utils.Logger, getConfig func() *config.Config) *TaskHandler {
	return &TaskHandler{
		services:  services,
		queries:   queries,
		logger:    logger,
		getConfig: getConfig,
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
		s, err := h.services.Batch.GetSummary(ctx, batchID)
		if err != nil {
			h.logger.Error(&reqID, "get batch summary for %s: %v", batchID, err)
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

	retried, err := h.services.Batch.RetryFailed(ctx, batchID)
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

	summaries, err := h.services.Batch.ListSummaries(ctx, task.BatchFilter{
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

	for _, s := range summaries {
		resp.Batches = append(resp.Batches, batchSummaryToResponse(&s))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func batchSummaryToResponse(s *service.BatchSummary) types.BatchSummaryResponse {
	return types.BatchSummaryResponse{
		BatchID: s.BatchID,
		Status:  s.Status,
		BatchCounts: types.BatchCounts{
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

func (h *TaskHandler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	items := make([]types.BatchOverviewItem, 0)
	overviews, err := h.services.Batch.ListOverviews(ctx, 10, 0)
	if err != nil {
		h.logger.Error(&reqID, "list batch overviews: %v", err)
	} else {
		for _, ov := range overviews {
			var createdAt string
			if !ov.CreatedAt.IsZero() {
				createdAt = ov.CreatedAt.Format(time.RFC3339)
			}
			items = append(items, types.BatchOverviewItem{
				BatchID:   ov.BatchID,
				Status:    ov.Status,
				Source:    ov.Source,
				CreatedAt: createdAt,
				BatchCounts: types.BatchCounts{
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

	totalBatches, batchErr := h.services.Batch.CountDistinct(ctx)
	if batchErr != nil {
		h.logger.Error(&reqID, "count distinct batches: %v", batchErr)
	}

	agg, aggErr := h.queries.DocumentAggregates(ctx)
	var totalFiles, totalBytes, totalPages, totalWords int64
	if aggErr != nil {
		h.logger.Error(&reqID, "document aggregates: %v", aggErr)
	} else {
		totalFiles = agg.TotalFiles
		totalBytes = agg.TotalBytes
		totalPages = agg.TotalPages
		totalWords = agg.TotalWords
	}

	statusRows, statusErr := h.queries.CountTasksByStatus(ctx)
	if statusErr != nil {
		h.logger.Error(&reqID, "count tasks by status: %v", statusErr)
	}
	perStatus := map[string]int64{}
	if statusErr == nil {
		for _, r := range statusRows {
			perStatus[r.Status] = r.Count
		}
	}

	mimeBreakdown, mimeErr := h.queries.MimeTypeBreakdown(ctx)
	if mimeErr != nil {
		h.logger.Error(&reqID, "mime type breakdown: %v", mimeErr)
	}

	trendRows, trendErr := h.queries.StorageTrendDaily(ctx)
	if trendErr != nil {
		h.logger.Error(&reqID, "storage trend daily: %v", trendErr)
	}

	var avgFileSize int64
	if totalFiles > 0 {
		avgFileSize = totalBytes / totalFiles
	}

	mimeStats := make([]types.MimeTypeStat, 0)
	if mimeErr == nil {
		mimeStats = make([]types.MimeTypeStat, 0, len(mimeBreakdown))
		for _, m := range mimeBreakdown {
			mimeStats = append(mimeStats, types.MimeTypeStat{
				MimeType:   m.MimeType,
				Count:      m.Count,
				TotalBytes: m.TotalBytes,
			})
		}
	}

	var cumulative int64
	trendPoints := make([]types.StorageTrendPoint, 0)
	if trendErr == nil {
		trendPoints = make([]types.StorageTrendPoint, 0, len(trendRows))
		for _, t := range trendRows {
			cumulative += t.DailyBytes
			trendPoints = append(trendPoints, types.StorageTrendPoint{
				Date:            t.Day,
				DailyCount:      t.Count,
				DailyBytes:      t.DailyBytes,
				CumulativeBytes: cumulative,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(types.DashboardResponse{
		RecentBatches:     items,
		Activity:          activity,
		Analytics:         analytics,
		ProcessingHealth:  processingHealth,
		TotalBatches:      totalBatches,
		TotalFiles:        totalFiles,
		Waiting:           perStatus["waiting"],
		Pending:           perStatus["pending"],
		Processing:        perStatus["processing"],
		Completed:         perStatus["completed"],
		Failed:            perStatus["failed"],
		Cancelled:         perStatus["cancelled"],
		Discarded:         perStatus["discarded"],
		TotalSizeGB:       float64(totalBytes) / (1024 * 1024 * 1024),
		MimeTypeBreakdown: mimeStats,
		StorageTrend:      trendPoints,
		AvgFileSizeBytes:  avgFileSize,
		TotalPages:        totalPages,
		TotalWords:        totalWords,
	})
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

func (h *TaskHandler) GetBatchSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	batchID := r.PathValue("id")

	s, err := h.services.Batch.GetSummary(ctx, batchID)
	if err != nil {
		h.logger.Error(&reqID, "get batch summary %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(batchSummaryToResponse(s))

	h.logger.Debug(&reqID, "batch summary: %+v", s)
}

func (h *TaskHandler) ResumeBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	batchID := r.PathValue("id")

	hasWork, err := h.services.Batch.HasPendingWork(ctx, batchID)
	if err != nil {
		h.logger.Error(&reqID, "check pending work for batch %s: %v", batchID, err)
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

	kushimPath, err := utils.KushimBinaryPath()
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

	pendingCancelled, ownerPID, ownerID, err := h.services.Batch.BeginCancel(ctx, batchID)
	if err != nil {
		h.logger.Error(&reqID, "cancel batch %s: %v", batchID, err)
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
		h.logger.Error(&reqID, "complete cancel batch %s: %v", batchID, err)
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

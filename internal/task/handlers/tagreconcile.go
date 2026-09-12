package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/enrichment/tagreconcile"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

var _ task.Dedupable = (*TagReconcileTaskHandler)(nil)

type TagReconcileTaskHandler struct {
	reconciler *tagreconcile.Reconciler
	queries    *database.Queries
	logger     *utils.Logger
}

func NewTagReconcileTaskHandler(r *tagreconcile.Reconciler, queries *database.Queries, logger *utils.Logger) *TagReconcileTaskHandler {
	return &TagReconcileTaskHandler{
		reconciler: r,
		queries:    queries,
		logger:     logger,
	}
}

func (h *TagReconcileTaskHandler) DedupKey(payload json.RawMessage) string {
	return fmt.Sprintf("tagreconcile:%s", time.Now().UTC().Format("2006-01-02"))
}

func (h *TagReconcileTaskHandler) Handle(ctx context.Context, t task.Task) (out json.RawMessage, err error) {
	progress := task.NewProgressTracker(h.queries, t.TaskID)
	defer func() {
		task.FinalizeBatchStatus(ctx, h.queries, t.BatchID, err != nil)
	}()

	task.MarkBatchProcessing(ctx, h.queries, t.BatchID)

	report, err := h.reconciler.Run(ctx, progress.Set)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal tag reconcile report: %w", err)
	}
	return raw, nil
}
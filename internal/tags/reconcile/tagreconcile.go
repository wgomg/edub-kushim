package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
)

var _ task.Dedupable = (*TagReconcileTaskHandler)(nil)

// TagReconcileTaskHandler runs the scheduled reconciliation as a blocking
// task; its dedup key is the UTC day, so at most one sweep runs per day.
type TagReconcileTaskHandler struct {
	reconciler *Reconciler
	queries    *database.Queries
	logger     *log.Logger
}

// NewTagReconcileTaskHandler wires the handler to the reconciler, the
// queries and the logger.
func NewTagReconcileTaskHandler(r *Reconciler, queries *database.Queries, logger *log.Logger) *TagReconcileTaskHandler {
	return &TagReconcileTaskHandler{
		reconciler: r,
		queries:    queries,
		logger:     logger,
	}
}

// DedupKey is the UTC day, collapsing concurrent triggers into one sweep
// per calendar day.
func (h *TagReconcileTaskHandler) DedupKey(payload json.RawMessage) string {
	return fmt.Sprintf("tagreconcile:%s", time.Now().UTC().Format("2006-01-02"))
}

// Handle runs one reconcile pass and returns its report as the task
// result.
func (h *TagReconcileTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	return task.RunBlocking(ctx, h.queries, h.logger, t, "tag reconcile", func(progress *task.ProgressTracker) (json.RawMessage, error) {
		report, err := h.reconciler.Run(ctx, progress.Set)
		if err != nil {
			return nil, err
		}

		raw, err := json.Marshal(report)
		if err != nil {
			return nil, fmt.Errorf("marshal tag reconcile report: %w", err)
		}
		return raw, nil
	})
}

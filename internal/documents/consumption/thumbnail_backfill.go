package consumption

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
)

// ThumbnailBackfillTaskHandler backfills thumbnails in bulk: it takes the
// maintenance lock, waits for in-flight tasks to drain, and then processes
// the payload through the thumbnail handler.
type ThumbnailBackfillTaskHandler struct {
	inner   *ThumbnailTaskHandler
	queries *database.Queries
	logger  *log.Logger
}

// NewThumbnailBackfillTaskHandler wraps the plain thumbnail handler with
// the backfill preconditions.
func NewThumbnailBackfillTaskHandler(inner *ThumbnailTaskHandler, queries *database.Queries, logger *log.Logger) *ThumbnailBackfillTaskHandler {
	return &ThumbnailBackfillTaskHandler{inner: inner, queries: queries, logger: logger}
}

// Handle takes the maintenance lock, waits for the queues to drain so the
// backfill does not fight live tasks, and then runs the payload through
// the thumbnail handler.
func (h *ThumbnailBackfillTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	progress := task.NewProgressTracker(h.queries, t.TaskID)

	stopHeartbeat := database.StartMaintenanceLockHeartbeat(ctx, h.queries, h.logger, 5*time.Minute,
		uuid.NullUUID{UUID: t.ClaimToken, Valid: true})
	defer stopHeartbeat()

	if err := database.WaitForTaskDrain(ctx, h.queries, h.logger, "thumbnail backfill", func(count int64) {
		progress.Set("drain-wait", fmt.Sprintf("%d in-flight", count), 0)
	}); err != nil {
		return nil, err
	}

	return h.inner.Handle(ctx, t)
}

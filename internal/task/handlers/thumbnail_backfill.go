package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type ThumbnailBackfillTaskHandler struct {
	inner   *ThumbnailTaskHandler
	queries *database.Queries
	logger  *utils.Logger
}

func NewThumbnailBackfillTaskHandler(inner *ThumbnailTaskHandler, queries *database.Queries, logger *utils.Logger) *ThumbnailBackfillTaskHandler {
	return &ThumbnailBackfillTaskHandler{inner: inner, queries: queries, logger: logger}
}

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

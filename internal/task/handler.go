package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
)

type Handler interface {
	Handle(ctx context.Context, task Task) (json.RawMessage, error)
}

type Dedupable interface {
	DedupKey(payload json.RawMessage) string
}

// RunBlocking runs a blocking task body under the maintenance lock protocol:
// the batch is marked processing, the claim token's maintenance lock is held
// alive for the duration, lock-gated tasks are drained first, and the batch is
// finalized from the body's error. The label names the task type in the drain
// log.
func RunBlocking(
	ctx context.Context,
	queries *database.Queries,
	logger *log.Logger,
	t Task,
	label string,
	body func(progress *ProgressTracker) (json.RawMessage, error),
) (out json.RawMessage, err error) {
	progress := NewProgressTracker(queries, t.TaskID)
	defer func() {
		FinalizeBatchStatus(ctx, queries, t.BatchID, err != nil)
	}()

	MarkBatchProcessing(ctx, queries, t.BatchID)

	stopHeartbeat := database.StartMaintenanceLockHeartbeat(ctx, queries, logger, 5*time.Minute,
		uuid.NullUUID{UUID: t.ClaimToken, Valid: true})
	defer stopHeartbeat()

	if err := database.WaitForTaskDrain(ctx, queries, logger, label, func(count int64) {
		progress.Set("drain-wait", fmt.Sprintf("%d in-flight", count), 0)
	}); err != nil {
		return nil, err
	}

	return body(progress)
}

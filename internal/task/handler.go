// Package task is the task system: the lifecycle store, the claim and
// complete protocol with claim tokens, batch ownership with heartbeats, the
// registry of task-type handlers, the runner that drives claims, the worker
// pool, the dispatcher that enqueues, and the progress tracker. The
// lifecycle, batch-ownership and queue-semantics contracts are documented
// in docs/developer-guide/task-system.md; this package implements them.
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

// Handler is the per-task-type seam: the domain packages implement it and
// the composition root registers the implementations on the Registry.
// Handle receives one claimed task and returns the result stored on the
// task row; an error fails the task, and a wrapped *Error adds a request id
// for log correlation and, with PauseBatch, pauses the whole batch.
type Handler interface {
	// Handle performs the task's work. The runner fails the task on panic,
	// so handlers should report through the returned error instead.
	Handle(ctx context.Context, task Task) (json.RawMessage, error)
}

// Dedupable is the optional second face of a Handler: a task type that
// wants enqueue-time deduplication states how to derive its dedup key from
// the payload.
type Dedupable interface {
	// DedupKey derives the key that rejects duplicate enqueues of the same
	// work; empty disables dedup for the type.
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

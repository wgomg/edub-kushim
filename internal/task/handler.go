// Package task is the task system: the lifecycle store, the claim and complete protocol with claim tokens,
// batch ownership with heartbeats, the registry of task-type handlers, the runner that drives claims, the
// worker pool, the transactional enqueue, the task type definition model and its enqueue contract, and
// the progress tracker. The lifecycle, batch-ownership and queue-semantics contracts are documented in
// docs/developer-guide/task-system.md; this package implements them.
package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
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

// RunBlocking runs a lock-holding task body: the batch is marked processing, the claim token's locks
// are kept fresh for the duration, the tasks that conflict with those locks drain first, and the batch
// is finalized from the body's error. The claim that admitted the task already acquired the locks; this
// only heartbeats them and drains. The label names the task type in the drain log.
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
		FinalizeBatchStatus(ctx, queries, logger, t.BatchID, err != nil)
	}()

	MarkBatchProcessing(ctx, queries, logger, t.BatchID)

	stop, err := HoldClaimed(ctx, queries, logger, t, label, func(count int64) {
		progress.Set("drain-wait", fmt.Sprintf("%d in-flight", count), 0)
	})
	if err != nil {
		return nil, err
	}
	defer stop()

	return body(progress)
}

// HoldClaimed keeps a claimed execution's declared locks fresh and waits for the in-flight tasks that
// conflict with them to drain. The returned stop function ends the heartbeat.
func HoldClaimed(
	ctx context.Context,
	queries *database.Queries,
	logger *log.Logger,
	t Task,
	label string,
	onWait func(count int64),
) (func(), error) {
	holds := types.LockDeclarationFor(t.TaskType).Holds
	stop := database.StartLockHeartbeat(ctx, queries, logger, LockHeartbeatInterval, holds,
		uuid.NullUUID{UUID: t.ClaimToken, Valid: true})
	if err := database.WaitForTaskDrain(ctx, queries, logger, label, types.TaskTypesWaitingOn(holds...), onWait); err != nil {
		stop()
		return nil, err
	}
	return stop, nil
}

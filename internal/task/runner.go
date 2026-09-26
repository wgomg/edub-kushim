package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Runner drives the claim-run-complete loop for the task types it is
// registered for. Next claims the next pending task, dispatches to the
// registered handler, recovers handler panics into task failures, retries
// the completion write against transient contention, and pauses the batch
// when a handler error carries PauseBatch. Safe to call from several pool
// workers.
type Runner struct {
	store    *Store
	registry *Registry
	logger   *log.Logger
}

// NewRunner wires the claim loop to the store and the handler registry.
func NewRunner(store *Store, registry *Registry, logger *log.Logger) *Runner {
	return &Runner{
		store:    store,
		registry: registry,
		logger:   logger,
	}
}

// Next claims and runs at most one task of the given type. It returns nil
// when there was nothing to claim or the maintenance lock was busy, so a
// polling loop treats it as an idle round.
func (r *Runner) Next(ctx context.Context, taskType types.TaskType) (err error) {
	task, token, err := r.store.ClaimNextPending(ctx, taskType)
	if err != nil {
		if err == sql.ErrNoRows || errors.Is(err, ErrLockBusy) {
			return nil
		}
		return fmt.Errorf("claim next pending task: %w", err)
	}

	blocking := taskType.IsBlocking()

	defer func() {
		if rcv := recover(); rcv != nil {
			failCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			reqID := task.TaskID
			if failErr := r.failTask(failCtx, task, token, blocking, fmt.Sprintf("panic: %v", rcv)); failErr != nil {
				r.logger.Error(reqID, "task %s panicked and fail-write failed: %v (stale sweep will reclaim)", task.TaskID, failErr)
			}
			r.logger.Error(reqID, "task %s panicked and was failed: %v", task.TaskID, rcv)
			err = fmt.Errorf("task %s panic: %v", task.TaskID, rcv)
		}
	}()

	if task.Payload == nil {
		if failErr := r.failTask(ctx, task, token, blocking, "task has nil payload"); failErr != nil {
			r.logger.Error(task.TaskID, "task %s has nil payload and fail-write failed: %v (stale sweep will reclaim)", task.TaskID, failErr)
		}
		r.logger.Error(task.TaskID, "task %s has nil payload", task.TaskID)
		return nil
	}

	h, err := r.registry.Get(task.TaskType)
	if err != nil {
		_ = r.failTask(ctx, task, token, blocking, err.Error())
		return nil
	}

	result, err := h.Handle(ctx, Task{
		ID:         task.ID,
		TaskID:     task.TaskID,
		TaskType:   task.TaskType,
		BatchID:    task.BatchID.String,
		Payload:    *task.Payload,
		ClaimToken: token,
	})
	if err != nil {
		_ = r.failTask(ctx, task, token, blocking, err.Error())
		reqID := ""
		if tErr, ok := errors.AsType[*Error](err); ok {
			reqID = tErr.ReqID
			if tErr.PauseBatch && task.BatchID.Valid {
				if pauseErr := r.store.PauseBatch(ctx, task.BatchID.String); pauseErr != nil {
					r.logger.Error(tErr.ReqID, "failed to pause batch %s after credit error: %v", task.BatchID.String, pauseErr)
				}
			}
		}
		r.logger.Error(reqID, "task %s failed: %v", task.TaskID, err)
		return nil
	}

	if err := r.completeTaskWithRetry(ctx, task, token, blocking, result); err != nil {
		// The handler's real work already succeeded.  Marking failed makes the
		// task visible and retryable rather than stuck in processing forever.
		failMsg := fmt.Sprintf("complete task failed after retries: %v", err)
		if failErr := r.failTask(ctx, task, token, blocking, failMsg); failErr != nil {
			return fmt.Errorf("complete task %d (and fail fallback): %v / %w", task.ID, failErr, err)
		}
		reqID := ""
		if tErr, ok := errors.AsType[*Error](err); ok {
			reqID = tErr.ReqID
		}
		r.logger.Error(reqID, "task %s completed handler but CompleteTask failed after retries — task failed instead of stuck", task.TaskID)
		return nil
	}

	return nil
}

func (r *Runner) failTask(ctx context.Context, task database.Task, token uuid.UUID, blocking bool, msg string) error {
	if blocking {
		return r.store.FailBlockingTask(ctx, task.ID, msg, token)
	}
	return r.store.FailTask(ctx, task.ID, msg)
}

// completeTaskWithRetry calls CompleteTask with bounded retries and backoff.
// The failure class is transient write contention, so a few retries cover
// the common case without infinite looping.
func (r *Runner) completeTaskWithRetry(ctx context.Context, task database.Task, token uuid.UUID, blocking bool, result json.RawMessage) error {
	const maxAttempts = 3
	backoff := 50 * time.Millisecond

	for attempt := 1; ; attempt++ {
		var rows int64
		var err error
		if blocking {
			rows, err = r.store.CompleteBlockingTask(ctx, task.ID, result, token)
		} else {
			rows, err = r.store.CompleteTask(ctx, task.ID, result)
		}
		if err == nil && rows > 0 {
			return nil
		}
		if err == nil {
			// rows == 0: the task was already transitioned (e.g. by the
			// stale-task sweep).  The handler's work is done; nothing more
			// to record.
			return nil
		}

		if attempt >= maxAttempts {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}
}

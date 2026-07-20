package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/utils"
)

type Runner struct {
	store    *Store
	registry *Registry
	logger   *utils.Logger
}

func NewRunner(store *Store, registry *Registry, logger *utils.Logger) *Runner {
	return &Runner{
		store:    store,
		registry: registry,
		logger:   logger,
	}
}

func (r *Runner) Next(ctx context.Context, taskType string) error {
	task, err := r.store.ClaimNextPending(ctx, taskType)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("claim next pending task: %w", err)
	}

	if task.Payload == nil {
		_ = r.store.FailTask(ctx, task.ID, "task has nil payload")
		var tErr *Error
		reqID := (*string)(nil)
		if errors.As(err, &tErr) {
			reqID = &tErr.ReqID
		}
		r.logger.Error(reqID, "task %s has nil payload", task.TaskID)
		return nil
	}

	h, err := r.registry.Get(task.TaskType)
	if err != nil {
		_ = r.store.FailTask(ctx, task.ID, err.Error())
		return nil
	}

	result, err := h.Handle(ctx, Task{
		ID:       task.ID,
		TaskID:   task.TaskID,
		TaskType: task.TaskType,
		Payload:  *task.Payload,
	})
	if err != nil {
		_ = r.store.FailTask(ctx, task.ID, err.Error())
		var tErr *Error
		reqID := (*string)(nil)
		if errors.As(err, &tErr) {
			reqID = &tErr.ReqID
		}
		r.logger.Error(reqID, "task %s failed: %v", task.TaskID, err)
		return nil
	}

	if err := r.completeTaskWithRetry(ctx, task.ID, result); err != nil {
		// The handler's real work already succeeded.  Marking failed makes the
		// task visible and retryable rather than stuck in processing forever.
		failMsg := fmt.Sprintf("complete task failed after retries: %v", err)
		if failErr := r.store.FailTask(ctx, task.ID, failMsg); failErr != nil {
			return fmt.Errorf("complete task %d (and fail fallback): %v / %w", task.ID, failErr, err)
		}
		var tErr *Error
		reqID := (*string)(nil)
		if errors.As(err, &tErr) {
			reqID = &tErr.ReqID
		}
		r.logger.Error(reqID, "task %s completed handler but CompleteTask failed after retries — task failed instead of stuck", task.TaskID)
		return nil
	}

	return nil
}

// completeTaskWithRetry calls CompleteTask with bounded retries and backoff.
// The failure class is transient write contention (SQLITE_BUSY), so a few
// retries cover the common case without infinite looping.
func (r *Runner) completeTaskWithRetry(ctx context.Context, id int64, result json.RawMessage) error {
	const maxAttempts = 3
	backoff := 50 * time.Millisecond

	for attempt := 1; ; attempt++ {
		rows, err := r.store.CompleteTask(ctx, id, result)
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

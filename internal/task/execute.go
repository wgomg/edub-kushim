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

// Runner drives the claim-run-settle loop for the task types it is registered for. Next claims the next
// pending task under its execution guard, drains the in-flight tasks that conflict with the locks the
// claim holds, dispatches to the registered handler, recovers handler panics into task failures, and
// settles the batch after every transition through SettleBatch. Handlers are pure work functions: the
// runner owns the locks, the drain, the transitions and the lifecycle. Safe to call from several pool
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

// Next claims and runs at most one task of the given type. It returns nil when there was nothing to
// claim, when the batch left its claimable states, or when a lock the claim needs was busy, so a polling
// loop treats each of them as an idle round.
func (r *Runner) Next(ctx context.Context, taskType types.TaskType) (err error) {
	task, guard, err := r.store.ClaimNextPending(ctx, taskType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrLockBusy) {
			return nil
		}
		return fmt.Errorf("claim next pending task: %w", err)
	}

	exec := Execution{BatchID: task.BatchID.String, Guard: guard, Leased: r.store.ownerID != ""}

	defer func() {
		if rcv := recover(); rcv != nil {
			failCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			msg := fmt.Sprintf("panic: %v", rcv)
			if failErr := r.failAndSettle(failCtx, task, exec, msg); failErr != nil {
				r.logger.Error(task.TaskID, "task %s panicked and fail-write failed: %v (stale sweep will reclaim)", task.TaskID, failErr)
			}
			r.logger.Error(task.TaskID, "task %s panicked and was failed: %v", task.TaskID, rcv)
			err = fmt.Errorf("task %s panic: %v", task.TaskID, rcv)
		}
	}()

	if task.Payload == nil {
		if failErr := r.failAndSettle(ctx, task, exec, "task has nil payload"); failErr != nil {
			r.logger.Error(task.TaskID, "task %s has nil payload and fail-write failed: %v (stale sweep will reclaim)", task.TaskID, failErr)
		}
		r.logger.Error(task.TaskID, "task %s has nil payload", task.TaskID)
		return nil
	}

	h, err := r.registry.Get(task.TaskType)
	if err != nil {
		_ = r.failAndSettle(ctx, task, exec, err.Error())
		return nil
	}

	stop, err := r.holdLocks(ctx, taskType, task, guard)
	if err != nil {
		_ = r.failAndSettle(ctx, task, exec, err.Error())
		return nil
	}
	defer stop()

	result, err := h.Handle(ctx, Task{
		ID:       task.ID,
		TaskID:   task.TaskID,
		TaskType: task.TaskType,
		BatchID:  task.BatchID.String,
		Payload:  *task.Payload,
	})
	if err != nil {
		tErr, isTaskErr := errors.AsType[*Error](err)
		reqID := ""
		if isTaskErr {
			reqID = tErr.ReqID
		}

		if failErr := r.failTask(ctx, task, exec, err.Error()); failErr != nil {
			r.logger.Error(reqID, "fail task %s: %v (stale sweep will reclaim)", task.TaskID, failErr)
		}

		if isTaskErr && tErr.PauseBatch && task.BatchID.Valid {
			// the pause must land before any terminal write: settling first would mark the batch failed and the
			// guarded pause would then refuse a batch that is no longer processing
			if pauseErr := PauseBatch(ctx, r.store.client, r.logger, exec); pauseErr != nil {
				r.logger.Error(reqID, "failed to pause batch %s after credit error: %v", task.BatchID.String, pauseErr)
			}
		} else {
			r.settle(ctx, task, exec)
		}

		r.logger.Error(reqID, "task %s failed: %v", task.TaskID, err)
		return nil
	}

	rows, err := r.completeTaskWithRetry(ctx, task, exec, result)
	if err != nil {
		// the handler's real work already succeeded; marking failed keeps the task visible and retryable rather
		// than stuck in processing forever
		failMsg := fmt.Sprintf("complete task failed after retries: %v", err)
		if failErr := r.failAndSettle(ctx, task, exec, failMsg); failErr != nil {
			return fmt.Errorf("complete task %d (and fail fallback): %v / %w", task.ID, failErr, err)
		}
		reqID := ""
		if tErr, ok := errors.AsType[*Error](err); ok {
			reqID = tErr.ReqID
		}
		r.logger.Error(reqID, "task %s completed handler but CompleteTask failed after retries — task failed instead of stuck", task.TaskID)
		return nil
	}
	if rows == 0 {
		// the task was already transitioned (cancelled, reset by the stale sweep or reclaimed), so the settle
		// may find an active task; SettleBatch's guards refuse that write and stay a no-op
		r.logger.Info(task.TaskID, "task %s was already transitioned — settling is safe", task.TaskID)
	}

	r.settle(ctx, task, exec)
	return nil
}

// holdLocks keeps the locks the claim acquired fresh for the run and waits for the in-flight tasks that
// conflict with them to drain. It is a no-op for a type that holds no locks and for an execution whose
// batch already holds them. The returned stop function ends the heartbeat.
func (r *Runner) holdLocks(ctx context.Context, taskType types.TaskType, task database.Task, guard uuid.UUID) (func(), error) {
	holds := types.LockDeclarationFor(taskType).Holds
	if len(holds) == 0 || r.store.batchHoldsAcquired {
		return func() {}, nil
	}
	stop := database.StartLockHeartbeat(ctx, r.store.queries, r.logger, LockHeartbeatInterval, holds,
		uuid.NullUUID{UUID: guard, Valid: true})
	progress := NewProgressTracker(r.store.queries, task.TaskID)
	err := database.WaitForTaskDrain(ctx, r.store.queries, r.logger, string(taskType), types.TaskTypesWaitingOn(holds...), func(count int64) {
		progress.Set("drain-wait", fmt.Sprintf("%d in-flight", count), 0)
	})
	if err != nil {
		stop()
		return nil, err
	}
	return stop, nil
}

// failTask fails the claimed task under its guard; a fail that lands zero rows means the claim was already
// taken. It deliberately does not settle, so a caller that requests a pause can pause before any terminal
// write.
func (r *Runner) failTask(ctx context.Context, task database.Task, exec Execution, msg string) error {
	_, err := r.store.FailTask(ctx, task.TaskType, task.ID, msg, exec.Guard)
	return err
}

// failAndSettle fails the claimed task under its guard and settles the batch afterwards. SettleBatch's
// guards make the settle a no-op when the claim was already taken, so the attempt is safe and still closes a
// batch whose task was completed by a response the caller never saw.
func (r *Runner) failAndSettle(ctx context.Context, task database.Task, exec Execution, msg string) error {
	if err := r.failTask(ctx, task, exec, msg); err != nil {
		return err
	}
	r.settle(ctx, task, exec)
	return nil
}

// settle writes the batch's terminal status once the run's work is over. It is best effort: a failed write is
// logged, and the guards inside SettleBatch make the call safe for an executor whose claim was already taken.
// For a pool batch the settle is the only terminal writer, so it runs after every task.
func (r *Runner) settle(ctx context.Context, task database.Task, exec Execution) {
	if exec.BatchID == "" {
		return
	}
	if _, err := SettleBatch(ctx, r.store.client, r.logger, exec); err != nil {
		r.logger.Error(task.TaskID, "settle batch %s: %v", exec.BatchID, err)
	}
}

// completeTaskWithRetry calls CompleteTask with bounded retries and backoff; the failure class is transient
// write contention, so a few retries cover the common case without infinite looping.
func (r *Runner) completeTaskWithRetry(ctx context.Context, task database.Task, exec Execution, result json.RawMessage) (int64, error) {
	const maxAttempts = 3
	backoff := 50 * time.Millisecond

	for attempt := 1; ; attempt++ {
		rows, err := r.store.CompleteTask(ctx, task.TaskType, task.ID, result, exec.Guard)
		if err == nil {
			return rows, nil
		}
		if attempt >= maxAttempts {
			return 0, err
		}

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}
}

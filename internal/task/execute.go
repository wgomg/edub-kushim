package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Runner drives the claim-run-settle loop for the task types it is registered for. Next claims the next
// pending task of the batch an execution owns, verifies the waits its type declares, dispatches to the
// registered handler, recovers handler panics into task failures, activates the completed task's waiting
// successors under the keys their type definitions derive, discards a failed task's whole waiting branch,
// and settles the batch after every transition through SettleBatch. The batch's locks, drain and heartbeat
// belong to RunBatch. Handlers are pure work functions: the runner owns the transitions and the lifecycle.
// Safe to call from several workers.
type Runner struct {
	store    *Store
	registry *Registry
	catalog  TypeCatalog
	logger   *log.Logger
}

// NewRunner wires the claim loop to the store, the handler registry and the type catalog that successor
// activation derives dedup keys from.
func NewRunner(store *Store, registry *Registry, catalog TypeCatalog, logger *log.Logger) *Runner {
	return &Runner{
		store:    store,
		registry: registry,
		catalog:  catalog,
		logger:   logger,
	}
}

// Next claims and runs at most one task of the given type. It returns nil when there was nothing to
// claim, when the batch left its claimable states, or when the task's waits are held (the claim selection
// defers it), so a polling loop treats each of them as an idle round.
func (r *Runner) Next(ctx context.Context, taskType types.TaskType) (err error) {
	task, guard, err := r.store.ClaimNextPending(ctx, taskType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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

	successors, err := r.buildSuccessorPlans(ctx, task, result)
	if err != nil {
		failMsg := fmt.Sprintf("plan waiting successors: %v", err)
		if failErr := r.failAndSettle(ctx, task, exec, failMsg); failErr != nil {
			return fmt.Errorf("plan successors of task %s (and fail fallback): %v / %w", task.TaskID, failErr, err)
		}
		r.logger.Error(task.TaskID, "task %s completed its work but planning its waiting successors failed — task failed instead", task.TaskID)
		return nil
	}

	rows, outcomes, err := r.completeTaskWithRetry(ctx, task, exec, result, successors)
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
	r.logSuccessorOutcomes(task, outcomes)
	if rows == 0 {
		// the task was already transitioned (cancelled, reset by the stale sweep or reclaimed), so the settle
		// may find an active task; SettleBatch's guards refuse that write and stay a no-op
		r.logger.Info(task.TaskID, "task %s was already transitioned — settling is safe", task.TaskID)
	}

	r.settle(ctx, task, exec)
	return nil
}

// buildSuccessorPlans reads the task's waiting successors and assembles their activation plans from the
// result the handler returned. A read failure fails the task rather than completing it with successors left
// armed behind the caller's back.
func (r *Runner) buildSuccessorPlans(ctx context.Context, task database.Task, result json.RawMessage) ([]SuccessorPlan, error) {
	children, err := r.store.WaitingSuccessors(ctx, task.TaskID)
	if err != nil {
		return nil, err
	}
	if len(children) == 0 {
		return nil, nil
	}
	return planSuccessors(children, result, r.catalog), nil
}

// logSuccessorOutcomes reports every successor whose activation did not simply go pending; an unplannable
// successor is an error because its payload contract and the type wiring disagree.
func (r *Runner) logSuccessorOutcomes(task database.Task, outcomes []SuccessorOutcome) {
	for _, outcome := range outcomes {
		if outcome.Activation == Activated {
			continue
		}
		switch outcome.Activation {
		case DiscardedDuplicate:
			r.logger.Info(task.TaskID, "successor task %s discarded: live work already covers its dedup key", outcome.TaskID)
		case DiscardedInvalid:
			r.logger.Error(task.TaskID, "successor task %s discarded: %v", outcome.TaskID, outcome.Err)
		case AlreadyTransitioned:
			r.logger.Info(task.TaskID, "successor task %s was already transitioned — left as is", outcome.TaskID)
		}
	}
}

// failTask fails the claimed task under its guard and discards its waiting branch in the same write; a fail
// that lands zero rows means the claim was already taken, so nothing is discarded either. It deliberately
// does not settle, so a caller that requests a pause can pause before any terminal write.
func (r *Runner) failTask(ctx context.Context, task database.Task, exec Execution, msg string) error {
	rows, discarded, err := r.store.FailTask(ctx, task.ID, task.TaskID, msg, exec.Guard)
	if err != nil {
		return err
	}
	if rows > 0 && discarded > 0 {
		r.logger.Info(task.TaskID, "task %s failed; discarded %d waiting successors", task.TaskID, discarded)
	}
	return nil
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
// It is the only terminal writer, and it runs after every task.
func (r *Runner) settle(ctx context.Context, task database.Task, exec Execution) {
	if exec.BatchID == "" {
		return
	}
	if _, err := SettleBatch(ctx, r.store.client, r.logger, r.catalog, exec); err != nil {
		r.logger.Error(task.TaskID, "settle batch %s: %v", exec.BatchID, err)
	}
}

// completeTaskWithRetry calls CompleteTask with bounded retries and backoff; the failure class is transient
// write contention, so a few retries cover the common case without infinite looping.
func (r *Runner) completeTaskWithRetry(ctx context.Context, task database.Task, exec Execution, result json.RawMessage, successors []SuccessorPlan) (int64, []SuccessorOutcome, error) {
	const maxAttempts = 3
	backoff := 50 * time.Millisecond

	for attempt := 1; ; attempt++ {
		rows, outcomes, err := r.store.CompleteTask(ctx, task.ID, result, exec.Guard, successors)
		if err == nil {
			return rows, outcomes, nil
		}
		if attempt >= maxAttempts {
			return 0, nil, err
		}

		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}
}

// Execution cadence of a leased batch execution: the owner heartbeat interval, the per-worker claim
// cadence and the monitor tick that observes pause, cancellation and completion.
const (
	batchHeartbeatInterval = 5 * time.Second
	batchClaimCadence      = time.Second
	batchMonitorInterval   = 500 * time.Millisecond
	batchShutdownTimeout   = 5 * time.Second
)

// BatchRunDeps carries the dependencies of one leased batch execution: the database client and logger,
// the type catalog that declares per-type worker counts, the store and registry the runner dispatches
// through, the retry budget entry repair applies, and the host-provided file quarantine hook. The hook
// keeps this package free of the documents package: the command layer supplies the consume-side cleanup.
// Client, Logger, Catalog, Store and Registry are required; a nil Quarantine disables file quarantine.
// InProcess marks a run the hosting process executes itself (the setup wizard): the lease keeps pid 0, so
// cancel and the reaper never signal the host process, and interruption arrives through the batch-status
// monitor. A forked CLI child and `kushim batch run` leave it unset and keep a signalable pid.
type BatchRunDeps struct {
	Client     *database.Client
	Logger     *log.Logger
	Catalog    TypeCatalog
	Store      *Store
	Registry   *Registry
	MaxRetries int32
	Quarantine func(ctx context.Context, taskIDs []string) error
	InProcess  bool
}

// BatchRunResult reports how a leased batch execution ended: the batch status the settle wrote or found,
// and whether the run stopped because the batch was paused.
type BatchRunResult struct {
	Status types.BatchStatus
	Paused bool
}

// adoptPID is the pid the execution records on its lease: its own for a CLI child or a foreground `kushim
// batch run`, and 0 for a run the hosting process executes itself, so cancel and the reaper never signal
// the API server, the wizard or the daemon.
func adoptPID(deps BatchRunDeps) int {
	if deps.InProcess {
		return 0
	}
	return os.Getpid()
}

// RunBatch is the leased task-CLI child. It adopts the execution lease the launcher claimed under token and
// heartbeats it from adoption on, repairs the batch's entry state (processing-task reset and the host's file
// quarantine), keeps the batch's locks fresh and drains their waiters, runs the batch's pending tasks through
// the registered handlers — one worker set per task type, sized by that type's declared concurrency and
// settling the batch after every transition — then sweeps failed consume files, settles the batch and
// releases the lease. A batch paused before or during the run returns Paused without settling. An aborted
// run — failed entry repair, failed drain, interrupted context or failed settle — releases its heartbeats,
// lease and locks, then requeues the batch, because releasing the lease removes the reaper's handle on it.
// A settle refused with tasks still active is logged and left to recovery. The lease is always released.
func RunBatch(ctx context.Context, deps BatchRunDeps, batchID string, token uuid.UUID) (BatchRunResult, error) {
	if deps.Client == nil || deps.Logger == nil || deps.Catalog == nil || deps.Store == nil || deps.Registry == nil {
		return BatchRunResult{}, fmt.Errorf("run batch %s: incomplete dependencies", batchID)
	}

	batch, err := deps.Client.Queries.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BatchRunResult{}, fmt.Errorf("batch %s not found", batchID)
		}
		return BatchRunResult{}, fmt.Errorf("get batch %s: %w", batchID, err)
	}
	switch {
	case batch.Status == types.Batch.Status.Paused:
		return BatchRunResult{Paused: true}, nil
	case batch.Status != types.Batch.Status.Processing:
		return BatchRunResult{Status: batch.Status}, fmt.Errorf("batch %s is %s, not a claimed processing batch", batchID, batch.Status)
	}

	typesPresent, err := deps.Client.Queries.ListBatchTaskTypes(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return BatchRunResult{}, fmt.Errorf("list batch task types: %w", err)
	}
	if len(typesPresent) == 0 {
		return BatchRunResult{}, fmt.Errorf("batch %s has no task types", batchID)
	}
	taskTypes := make([]types.TaskType, 0, len(typesPresent))
	for _, value := range typesPresent {
		taskTypes = append(taskTypes, types.TaskType(value))
	}

	// the batch's held locks, computed before adoption so the teardown can release them on every path
	var held []types.LockName
	for _, taskType := range taskTypes {
		held = UnionLockNames(held, types.LockDeclarationFor(taskType).Holds)
	}
	holdsHeld := len(held) > 0
	lockToken := uuid.NullUUID{UUID: token, Valid: true}
	heldNames := lockNameStrings(held)

	owner := NewOwner(deps.Client, token.String(), adoptPID(deps))
	if err := owner.Adopt(ctx, batchID); err != nil {
		return BatchRunResult{}, err
	}

	// the lease is heartbeated from adoption on: entry repair, quarantine and the drain can outlast the 15s
	// lease window, and a stale lease makes the reaper release the batch and signal this still-live child
	ownerHeartbeat := NewHeartbeat(owner, batchHeartbeatInterval, deps.Logger)
	ownerHeartbeat.Start(ctx)
	stopHeldHeartbeat := func() {}
	if holdsHeld {
		stopHeldHeartbeat = database.StartLockHeartbeat(ctx, deps.Client.Queries, deps.Logger,
			LockHeartbeatInterval, held, lockToken)
	}

	requeue := false
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), batchShutdownTimeout)
		defer cancel()
		ownerHeartbeat.Stop()
		stopHeldHeartbeat()
		if err := owner.Release(releaseCtx, batchID); err != nil {
			deps.Logger.Error("", "release batch %s: %v", batchID, err)
		}
		if err := releaseLocksForToken(releaseCtx, deps.Client.Queries, lockToken, heldNames); err != nil {
			deps.Logger.Error("", "release batch %s locks: %v", batchID, err)
		}
		// a released lease is invisible to the reaper, so an aborted run hands the batch back to the queue
		// itself; the guard refuses a batch a settle, pause or cancel already moved out of processing
		if !requeue {
			return
		}
		if rows, err := deps.Client.Queries.RequeueProcessingBatch(releaseCtx, batchID); err != nil {
			deps.Logger.Error("", "requeue batch %s: %v", batchID, err)
		} else if rows > 0 {
			deps.Logger.Info("", "batch %s: requeued after an aborted execution", batchID)
		}
	}()

	reset, err := ResetProcessingTasksForClaim(ctx, deps.Client, batchID, deps.MaxRetries)
	if err != nil {
		requeue = true
		return BatchRunResult{}, fmt.Errorf("reset processing tasks for batch %s: %w", batchID, err)
	}
	if reset.Reset > 0 {
		deps.Logger.Info("", "batch %s: reset %d orphaned processing tasks", batchID, reset.Reset)
	}
	if deps.Quarantine != nil {
		if err := deps.Quarantine(ctx, reset.ConsumeTaskIDs()); err != nil {
			deps.Logger.Error("", "quarantine files for batch %s: %v", batchID, err)
		}
	}

	// the claim acquired the batch's holds: keep them fresh and drain the in-flight tasks waiting on them;
	// an ingest batch holds nothing and skips all of it
	if holdsHeld {
		if err := database.WaitForTaskDrain(ctx, deps.Client.Queries, deps.Logger, "batch "+batchID,
			types.TaskTypesWaitingOn(held...), nil); err != nil {
			requeue = true
			return BatchRunResult{}, fmt.Errorf("hold batch %s locks: %w", batchID, err)
		}
	}

	deps.Store.SetOwnerID(token.String())

	runCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()

	// one worker set per task type; the declared count is read once, when the set is sized
	runner := NewRunner(deps.Store, deps.Registry, deps.Catalog, deps.Logger)
	var workers sync.WaitGroup
	for _, taskType := range taskTypes {
		workersForType := 1
		if def, lookupErr := deps.Catalog.Definition(taskType); lookupErr == nil && def.Concurrency > 1 {
			workersForType = def.Concurrency
		}
		for range workersForType {
			workers.Go(func() {
				for {
					select {
					case <-runCtx.Done():
						return
					case <-time.After(batchClaimCadence):
					}
					if err := runner.Next(runCtx, taskType); err != nil {
						deps.Logger.Error("", "%s worker: %v", taskType, err)
					}
				}
			})
		}
	}

	stopAndWait := func(op string) {
		stopWorkers()
		done := make(chan struct{})
		go func() { workers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(batchShutdownTimeout):
			deps.Logger.Info("", "batch %s: %s worker shutdown timed out", batchID, op)
		}
	}

	ticker := time.NewTicker(batchMonitorInterval)
	defer ticker.Stop()

	paused := false
monitor:
	for {
		select {
		case <-ctx.Done():
			stopAndWait("interrupted")
			requeue = true
			return BatchRunResult{}, ctx.Err()
		case <-ticker.C:
			current, readErr := deps.Client.Queries.GetBatch(ctx, batchID)
			if readErr != nil {
				continue
			}
			switch {
			case current.Status == types.Batch.Status.Paused:
				stopAndWait("paused")
				paused = true
				break monitor
			case current.Status != types.Batch.Status.Processing:
				stopAndWait("settled")
				break monitor
			}
			counts, countErr := deps.Client.Queries.BatchTaskStatusCounts(ctx, sql.NullString{String: batchID, Valid: true})
			if countErr != nil {
				continue
			}
			if counts.Pending+counts.Processing == 0 {
				stopAndWait("completed")
				break monitor
			}
		}
	}
	if paused {
		return BatchRunResult{Paused: true}, nil
	}

	// the closing sweep covers the files of consumes that exhausted their attempts and the ones a run that
	// died before its own quarantine left behind, before the terminal write
	if deps.Quarantine != nil {
		if err := deps.Quarantine(ctx, nil); err != nil {
			deps.Logger.Error("", "quarantine files for batch %s: %v", batchID, err)
		}
	}
	// the repair net runs for every batch type, not only the ones whose host supplies a quarantine hook: a
	// waiting successor whose parent ended without activating it would otherwise refuse the settle
	if discarded, err := deps.Client.Queries.DiscardOrphanedWaitingSuccessors(ctx, batchID); err != nil {
		deps.Logger.Error("", "sweep orphaned waiting successors for batch %s: %v", batchID, err)
	} else if discarded > 0 {
		deps.Logger.Info("", "batch %s: discarded %d orphaned waiting successors before settling", batchID, discarded)
	}
	exec := Execution{BatchID: batchID, Guard: token, Leased: true}
	status, err := SettleBatch(ctx, deps.Client, deps.Logger, deps.Catalog, exec)
	if err != nil {
		deps.Logger.Error("", "settle batch %s: %v", batchID, err)
		requeue = true
	} else if status == types.Batch.Status.Processing {
		// a refused settle means tasks are still active: report which ones and in what state their parents are,
		// because no retry would change it and the batch is left to operator recovery
		deps.Logger.Error("", "batch %s: settle refused — tasks remain active; left to recovery", batchID)
		logActiveTasksAtRefusedSettle(ctx, deps, batchID)
	}
	return BatchRunResult{Status: status}, nil
}

// logActiveTasksAtRefusedSettle reports the status breakdown and, for every waiting successor that blocked
// the settle, its parent's state: the pair is what lets an operator see why the batch never settled.
func logActiveTasksAtRefusedSettle(ctx context.Context, deps BatchRunDeps, batchID string) {
	counts, err := deps.Client.Queries.BatchTaskStatusCounts(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		deps.Logger.Error("", "batch %s: count tasks at refused settle: %v", batchID, err)
	} else {
		deps.Logger.Error("", "batch %s: %d waiting, %d pending, %d processing remain at refused settle",
			batchID, counts.Waiting, counts.Pending, counts.Processing)
	}

	waiting, err := deps.Client.Queries.ListWaitingSuccessorsWithParentStatus(ctx, batchID)
	if err != nil {
		deps.Logger.Error("", "batch %s: list waiting successors at refused settle: %v", batchID, err)
		return
	}
	for _, row := range waiting {
		deps.Logger.Error("", "batch %s: waiting %s task %s awaits parent %s in state %s",
			batchID, row.TaskType, row.TaskID, row.ParentTaskID, row.ParentStatus)
	}
}

// Package dispatch turns queued batches into running task-CLI children: the daemon uses it to claim
// eligible batches and fork their executors, and `kushim batch run` uses the same claim-and-fork path to
// run one batch in the foreground and wait for it. Both hosts share one implementation of the claim,
// launch, failure and requeue rules.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// InProcessRegistration names the task types one process executes in this process and the runner it uses
// for them. It is the setup wizard's execution path: the wizard serves migrations before the queue daemon
// exists and cannot fork kushim, so it runs their handlers itself. A batch is eligible only when every one
// of its task types is listed and every one declares this host in its definition; anything else takes the
// fork path. Run executes the claimed batch to settlement and must recover a run it could not complete:
// release the claim and requeue the batch. It reports that error; the dispatcher only logs it.
type InProcessRegistration struct {
	Types []types.TaskType
	Run   func(ctx context.Context, client *database.Client, batchID string, token uuid.UUID) error
}

// Dispatcher claims queued batches eligible for this host and forks each one's task CLI in batch mode. It
// is the daemon's only queue claimant: status, lease and holds commit together, and a batch whose locks
// are busy is skipped without blocking the candidates behind it. Its identity travels on every lease it
// claims, so the slot budget counts only this process's executions. The budget itself is supplied by a
// callback the dispatcher evaluates on every sweep, so a process that reloads its configuration enforces
// an edited value without being re-created.
type Dispatcher struct {
	catalog      task.TypeCatalog
	logger       *log.Logger
	host         types.Host
	dispatcherID uuid.UUID
	budget       func() int
	lastBudget   int
	failures     *launchBackoff
	wake         func()
	inProcess    *InProcessRegistration
	runs         sync.WaitGroup
}

// NewDispatcher wires the dispatcher to the task catalog and logger of the calling boundary, the host it
// dispatches for, the slot budget supplier and the wake channel poked once a backoff window expires. The
// supplier is read on every sweep, so a value edited at runtime reaches the next dispatch without a
// restart; a value below one means the default budget of two. inProcess names the task types this process
// executes itself; nil makes the dispatcher fork-only. The dispatcher identity is minted here and lasts
// for the process lifetime.
func NewDispatcher(catalog task.TypeCatalog, logger *log.Logger, host types.Host, budget func() int, wake func(), inProcess *InProcessRegistration) *Dispatcher {
	d := &Dispatcher{
		catalog:      catalog,
		logger:       logger,
		host:         host,
		dispatcherID: uuid.New(),
		budget:       budget,
		failures:     newLaunchBackoff(),
		wake:         wake,
		inProcess:    inProcess,
	}
	d.lastBudget = EffectiveBudget(d.budget())
	d.logger.Info("", "dispatcher budget: %d on host %s", d.lastBudget, host)
	return d
}

// EffectiveBudget resolves a configured slot budget to the value a sweep enforces: a configured value
// below one falls back to two, so a zero or negative setting cannot stop dispatch entirely. The queue
// daemon reads the same rule when it sizes its pool to the concurrency the budget admits.
func EffectiveBudget(configured int) int {
	if configured > 0 {
		return configured
	}
	return 2
}

// Dispatch claims queued batches eligible for this host. The slot budget limits only batches that acquire no
// locks: a lock-holding batch is serialized by its locks and is claimed regardless of the budget, so it can
// never wait behind no-holds work. The budget is re-read here, so a configuration reload takes effect on
// the next sweep, and a changed value is logged once.
func (d *Dispatcher) Dispatch(ctx context.Context, client *database.Client, batchSvc *service.Batch) error {
	live, err := batchSvc.CountLiveNoHoldsBatches(ctx, d.dispatcherID)
	if err != nil {
		return fmt.Errorf("count live batches: %w", err)
	}
	candidates, err := batchSvc.ListQueuedBatchesByAge(ctx)
	if err != nil {
		return fmt.Errorf("list queued batches: %w", err)
	}
	budget := EffectiveBudget(d.budget())
	if budget != d.lastBudget {
		d.logger.Info("", "dispatcher budget: %d -> %d", d.lastBudget, budget)
		d.lastBudget = budget
	}
	d.sweep(ctx, client, candidates, live, budget)
	return nil
}

// sweep tries the candidates oldest first and claims every one the budget admits. A batch this process
// executes in-process is claimed and handed to its runner on a goroutine, so the sweep keeps dispatching.
// A launch failure stops the sweep, so a broken fork does not march through the whole queue in one wake.
func (d *Dispatcher) sweep(ctx context.Context, client *database.Client, candidates []database.ListQueuedBatchesByAgeRow, live int64, budget int) {
	now := time.Now()
	for _, candidate := range candidates {
		if d.failures.cooling(candidate.BatchID, now) {
			continue
		}
		taskTypes := make([]types.TaskType, len(candidate.TaskTypes))
		for i, value := range candidate.TaskTypes {
			taskTypes[i] = types.TaskType(value)
		}
		holds, waits := batchLockSets(taskTypes)

		if inProcessEligible(d.catalog, d.host, d.inProcess, taskTypes) {
			if len(holds) == 0 && live >= int64(budget) {
				continue
			}
			token := uuid.New()
			if !d.claimCandidate(ctx, client, candidate.BatchID, token, holds, waits) {
				continue
			}
			if len(holds) == 0 {
				live++
			}
			d.logger.Info("", "dispatched batch %s in-process on host %s", candidate.BatchID, d.host)
			d.runs.Add(1)
			go func(batchID string, execToken uuid.UUID) {
				defer d.runs.Done()
				if err := d.inProcess.Run(ctx, client, batchID, execToken); err != nil {
					d.logger.Error("", "in-process batch %s: %v", batchID, err)
				}
			}(candidate.BatchID, token)
			continue
		}

		target, err := resolveLaunchTarget(d.catalog, candidate.BatchID, taskTypes, d.host)
		if err != nil {
			d.logger.Debug("", "batch %s is not dispatchable here: %v", candidate.BatchID, err)
			continue
		}
		if len(holds) == 0 && live >= int64(budget) {
			continue
		}
		token := uuid.New()
		if !d.claimCandidate(ctx, client, candidate.BatchID, token, holds, waits) {
			continue
		}
		cmd, err := launchBatch(target, token, false, func(exitErr error) {
			d.childExited(client, candidate.BatchID, token, exitErr)
		})
		if err != nil {
			d.handleLaunchFailure(ctx, client, candidate.BatchID, token, err)
			return
		}
		if len(holds) == 0 {
			live++
		}
		d.logger.Info("", "dispatched batch %s to kushim %s %s (PID %d)",
			candidate.BatchID, target.cli, target.selector, cmd.Process.Pid)
	}
}

// claimCandidate claims one queued candidate under this dispatcher's identity. A lock-busy batch, a batch
// that is no longer queued and a lease collision are normal skips; any other error is logged. It reports
// whether the claim landed.
func (d *Dispatcher) claimCandidate(ctx context.Context, client *database.Client, batchID string, token uuid.UUID, holds, waits []types.LockName) bool {
	claim := task.ClaimRequest{
		BatchID: batchID, ExecToken: token, Holds: holds, Waits: waits,
		DispatcherID: uuid.NullUUID{UUID: d.dispatcherID, Valid: true},
	}
	if err := task.ClaimBatch(ctx, client, claim); err != nil {
		switch {
		case errors.Is(err, task.ErrBatchLocked):
			// a queued batch carrying a lease is abnormal because every requeue path releases the lease row before
			// it can ever fail, so surface the stuck state instead of silently skipping the whole batch on every
			// dispatch wake
			d.logger.Error("", "batch %s is queued but still carries a lease — leaving it to the reaper", batchID)
		case !errors.Is(err, task.ErrLockBusy) && !errors.Is(err, task.ErrBatchNotQueued):
			d.logger.Error("", "claim batch %s: %v", batchID, err)
		}
		return false
	}
	return true
}

// WaitInProcess blocks until every in-process execution this dispatcher started has returned, or ctx
// expires. Runs outlive their dispatch sweep, so a host that closes shared resources at shutdown drains
// them first; each run releases its own claim on the way out.
func (d *Dispatcher) WaitInProcess(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		d.runs.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// inProcessEligible reports whether this process executes the whole batch itself: the registration must
// cover every task type and every type must name this host. A partially covered batch takes the fork path
// and fails loudly there, so a mixed batch never runs half of its work in a host without handlers for it.
func inProcessEligible(catalog task.TypeCatalog, host types.Host, registration *InProcessRegistration, taskTypes []types.TaskType) bool {
	if registration == nil || len(taskTypes) == 0 {
		return false
	}
	for _, taskType := range taskTypes {
		if !slices.Contains(registration.Types, taskType) {
			return false
		}
		def, err := catalog.Definition(taskType)
		if err != nil || !slices.Contains(def.Hosts, host) {
			return false
		}
	}
	return true
}

// childExited handles a forked child's exit. A non-zero exit while the lease still carries this token
// with pid 0 means the child died before adoption: no task can have been claimed, so it is a launch
// failure. Once the child has adopted the lease, its outcome is its own — it settled, or the reaper
// will reclaim it.
func (d *Dispatcher) childExited(client *database.Client, batchID string, token uuid.UUID, exitErr error) {
	if exitErr == nil {
		d.failures.clear(batchID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner, err := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
		BatchID:           batchID,
		StaleAfterSeconds: task.LeaseStaleSeconds,
	})
	if err != nil || owner.OwnerID != token.String() || owner.Pid != 0 {
		// the child adopted the lease or the batch was cancelled or reclaimed, so launching itself clearly works
		// and the failure streak ends here
		d.failures.clear(batchID)
		return
	}
	d.handleLaunchFailure(ctx, client, batchID, token, exitErr)
}

// handleLaunchFailure applies the bounded launch-failure policy: release and requeue with backoff, or
// fail the batch once maxLaunchFailures consecutive attempts failed.
func (d *Dispatcher) handleLaunchFailure(ctx context.Context, client *database.Client, batchID string, token uuid.UUID, cause error) {
	count, delay := d.failures.record(batchID)
	if count >= maxLaunchFailures {
		applied, err := task.FailUnstartedBatch(ctx, client, batchID, token)
		switch {
		case err != nil:
			d.logger.Error("", "fail launch-failed batch %s: %v (leaving it to the reaper)", batchID, err)
		case !applied:
			d.logger.Info("", "batch %s changed hands (owner no longer ours) — skipping stale fail", batchID)
		default:
			d.logger.Error("", "batch %s failed after %d consecutive launch failures: %v",
				batchID, maxLaunchFailures, cause)
		}
		return
	}
	requeueAfterLaunchFailure(ctx, client, d.logger, batchID, token)
	d.logger.Error("", "launch batch %s failed (%d/%d): %v — retrying in %s",
		batchID, count, maxLaunchFailures, cause, delay)
	// the requeue writes no notification, and the safety poll alone would make the retry needlessly slow
	time.AfterFunc(delay, d.wake)
}

// batchLockSets accumulates a batch claim's lock sets: the union of every selected type's holds and
// the waits declared by the types that hold locks. A type that holds nothing does not make the batch
// wait — its tasks verify their own waits at claim. The execution library drains the types waiting on
// the held locks after adoption, so the narrower claim rule here is deliberate.
func batchLockSets(taskTypes []types.TaskType) (holds, waits []types.LockName) {
	for _, taskType := range taskTypes {
		declaration := types.LockDeclarationFor(taskType)
		holds = task.UnionLockNames(holds, declaration.Holds)
		if len(declaration.Holds) > 0 {
			waits = task.UnionLockNames(waits, declaration.WaitsFor)
		}
	}
	return holds, waits
}

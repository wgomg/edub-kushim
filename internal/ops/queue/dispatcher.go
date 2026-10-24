package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// batchDispatcher claims queued batches eligible for this host and forks each one's task CLI in batch
// mode. It is the daemon's only queue claimant: status, lease and holds commit together, and a batch whose
// locks are busy is skipped without blocking the candidates behind it. Its identity travels on every lease
// it claims, so the slot budget counts only this process's executions.
type batchDispatcher struct {
	c             *container.Container
	host          types.Host
	dispatcherID  uuid.UUID
	maxConcurrent int
	failures      *launchBackoff
	wake          func()
}

// newBatchDispatcher wires the dispatcher to the container, the host it dispatches for, the batch slot
// budget and the wake channel poked once a backoff window expires. The dispatcher identity is minted here
// and lasts for the process lifetime.
func newBatchDispatcher(c *container.Container, host types.Host, maxConcurrent int, wake func()) *batchDispatcher {
	return &batchDispatcher{
		c:             c,
		host:          host,
		dispatcherID:  uuid.New(),
		maxConcurrent: maxConcurrent,
		failures:      newLaunchBackoff(),
		wake:          wake,
	}
}

// dispatch claims queued batches eligible for this host. The slot budget limits only batches that acquire
// no locks: a lock-holding batch is serialized by its locks and is claimed regardless of the budget, so it
// can never wait behind no-holds work.
func (d *batchDispatcher) dispatch(ctx context.Context, client *database.Client, batchSvc *service.Batch) error {
	catalog, err := d.c.GetCatalog()
	if err != nil {
		return fmt.Errorf("get task catalog: %w", err)
	}
	live, err := batchSvc.CountLiveNoHoldsBatches(ctx, d.dispatcherID)
	if err != nil {
		return fmt.Errorf("count live batches: %w", err)
	}
	candidates, err := batchSvc.ListQueuedBatchesByAge(ctx)
	if err != nil {
		return fmt.Errorf("list queued batches: %w", err)
	}
	d.sweep(ctx, client, catalog, candidates, live)
	return nil
}

// sweep tries the candidates oldest first and claims every one the budget admits. A launch failure stops
// the sweep, so a broken fork does not march through the whole queue in one wake.
func (d *batchDispatcher) sweep(ctx context.Context, client *database.Client, catalog task.TypeCatalog, candidates []database.ListQueuedBatchesByAgeRow, live int64) {
	now := time.Now()
	for _, candidate := range candidates {
		if d.failures.cooling(candidate.BatchID, now) {
			continue
		}
		taskTypes := make([]types.TaskType, len(candidate.TaskTypes))
		for i, value := range candidate.TaskTypes {
			taskTypes[i] = types.TaskType(value)
		}
		target, err := resolveLaunchTarget(catalog, candidate.BatchID, taskTypes, d.host)
		if err != nil {
			d.c.GetLogger().Debug("", "batch %s is not dispatchable here: %v", candidate.BatchID, err)
			continue
		}
		holds, waits := batchLockSets(taskTypes)
		if len(holds) == 0 && live >= int64(d.maxConcurrent) {
			continue
		}
		token := uuid.New()
		claim := task.ClaimRequest{
			BatchID: candidate.BatchID, ExecToken: token, Holds: holds, Waits: waits,
			DispatcherID: uuid.NullUUID{UUID: d.dispatcherID, Valid: true},
		}
		if err := task.ClaimBatch(ctx, client, claim); err != nil {
			switch {
			case errors.Is(err, task.ErrBatchLocked):
				// a queued batch carrying a lease is abnormal because every requeue path releases the lease row
				// before it can ever fail, so surface the stuck state instead of silently skipping the whole batch
				// on every dispatch wake
				d.c.GetLogger().Error("", "batch %s is queued but still carries a lease — leaving it to the reaper",
					candidate.BatchID)
			case !errors.Is(err, task.ErrLockBusy) && !errors.Is(err, task.ErrBatchNotQueued):
				d.c.GetLogger().Error("", "claim batch %s: %v", candidate.BatchID, err)
			}
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
		d.c.GetLogger().Info("", "dispatched batch %s to kushim %s %s (PID %d)",
			candidate.BatchID, target.cli, target.selector, cmd.Process.Pid)
	}
}

// childExited handles a forked child's exit. A non-zero exit while the lease still carries this token with
// pid 0 means the child died before adoption: no task can have been claimed, so it is a launch failure.
// Once the child has adopted the lease, its outcome is its own — it settled, or the reaper will reclaim it.
func (d *batchDispatcher) childExited(client *database.Client, batchID string, token uuid.UUID, exitErr error) {
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
		// the child adopted the lease or the batch was cancelled or reclaimed, so launching itself clearly
		// works and the failure streak ends here
		d.failures.clear(batchID)
		return
	}
	d.handleLaunchFailure(ctx, client, batchID, token, exitErr)
}

// handleLaunchFailure applies the bounded launch-failure policy: release and requeue with backoff, or fail
// the batch once maxLaunchFailures consecutive attempts failed.
func (d *batchDispatcher) handleLaunchFailure(ctx context.Context, client *database.Client, batchID string, token uuid.UUID, cause error) {
	count, delay := d.failures.record(batchID)
	if count >= maxLaunchFailures {
		applied, err := task.FailUnstartedBatch(ctx, client, batchID, token)
		switch {
		case err != nil:
			d.c.GetLogger().Error("", "fail launch-failed batch %s: %v (leaving it to the reaper)", batchID, err)
		case !applied:
			d.c.GetLogger().Info("", "batch %s changed hands (owner no longer ours) — skipping stale fail", batchID)
		default:
			d.c.GetLogger().Error("", "batch %s failed after %d consecutive launch failures: %v",
				batchID, maxLaunchFailures, cause)
		}
		return
	}
	requeueAfterLaunchFailure(ctx, client, d.c.GetLogger(), batchID, token)
	d.c.GetLogger().Error("", "launch batch %s failed (%d/%d): %v — retrying in %s",
		batchID, count, maxLaunchFailures, cause, delay)
	// the requeue writes no notification, and the safety poll alone would make the retry needlessly slow
	time.AfterFunc(delay, d.wake)
}

// batchLockSets accumulates a batch claim's lock sets: the union of every selected type's holds and the
// waits declared by the types that hold locks. A type that holds nothing does not make the batch wait —
// its tasks verify their own waits at claim. The consume child's batchHolds applies the wider all-types
// wait union when it self-claims and drains, so the narrower claim rule here is deliberate.
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

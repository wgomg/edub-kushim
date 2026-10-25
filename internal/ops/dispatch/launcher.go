package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// launchTarget is the resolved executor of one claimed batch: the command that runs it in batch mode, its
// batch selector and the batch id.
type launchTarget struct {
	batchID  string
	cli      string
	selector string
}

// maxLaunchFailures bounds the consecutive launch failures of one batch before it is failed instead of
// retried.
const maxLaunchFailures = 3

// batchExecutorPrecedence orders the task types whose batches may mix: an ingest batch carries consume
// tasks and their enrich and thumbnail children, so the command that owns the batch's root work executes
// it. Later stages extend the list as more types gain batch-mode CLIs.
var batchExecutorPrecedence = []types.TaskType{
	types.Task.Type.Consume,
	types.Task.Type.Enrich,
	types.Task.Type.Thumbnail,
	types.Task.Type.ThumbBackfill,
}

// resolveLaunchTarget checks that every task type in the batch is dispatchable on host and resolves the
// task CLI that owns the batch. A type is dispatchable when its definition declares the CLI command and
// the batch selector and lists the host. A pure batch resolves to its own type's command; a mixed batch
// resolves by batchExecutorPrecedence, because its child roots are the owning type's tasks and one execution
// must settle the whole batch. Types outside the precedence list that disagree fail loudly.
func resolveLaunchTarget(catalog task.TypeCatalog, batchID string, taskTypes []types.TaskType, host types.Host) (launchTarget, error) {
	if len(taskTypes) == 0 {
		return launchTarget{}, fmt.Errorf("batch %s has no task types", batchID)
	}

	resolved := make(map[types.TaskType]task.TypeDefinition, len(taskTypes))
	for _, taskType := range taskTypes {
		def, err := catalog.Definition(taskType)
		if err != nil {
			return launchTarget{}, err
		}
		if def.CLI == "" || def.BatchSelector == "" {
			return launchTarget{}, fmt.Errorf("batch %s: task type %s has no batch-mode CLI yet", batchID, taskType)
		}
		if !slices.Contains(def.Hosts, host) {
			return launchTarget{}, fmt.Errorf("batch %s: task type %s is not dispatchable on host %s", batchID, taskType, host)
		}
		resolved[taskType] = def
	}

	for _, preferred := range batchExecutorPrecedence {
		if def, ok := resolved[preferred]; ok {
			return launchTarget{batchID: batchID, cli: def.CLI, selector: def.BatchSelector}, nil
		}
	}

	first := resolved[taskTypes[0]]
	for _, taskType := range taskTypes[1:] {
		def := resolved[taskType]
		if def.CLI != first.CLI || def.BatchSelector != first.BatchSelector {
			return launchTarget{}, fmt.Errorf("batch %s: task types disagree on the executor (%s %s vs %s %s)",
				batchID, first.CLI, first.BatchSelector, def.CLI, def.BatchSelector)
		}
	}
	return launchTarget{batchID: batchID, cli: first.CLI, selector: first.BatchSelector}, nil
}

// launchBatch forks the target's task CLI in batch mode with the execution token. A non-nil onExit receives
// the child's exit error from a wait goroutine; nil leaves the waiting to the caller and returns the command
// for it. attachStdio forwards the child's streams to this process, which the interactive launcher uses.
func launchBatch(target launchTarget, token uuid.UUID, attachStdio bool, onExit func(error)) (*exec.Cmd, error) {
	cmd := exec.Command(os.Args[0], target.cli, target.selector, target.batchID, "--exec-token", token.String())
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if attachStdio {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("fork %s %s %s: %w", target.cli, target.selector, target.batchID, err)
	}
	if onExit != nil {
		go func() { onExit(cmd.Wait()) }()
	}
	return cmd, nil
}

// requeueAfterLaunchFailure releases a batch whose child never started — its lease and locks — and hands it
// back to the dispatcher. A failed release rolls back: the batch stays processing with its lease, and the
// reaper recovers it once the lease goes stale.
func requeueAfterLaunchFailure(ctx context.Context, client *database.Client, logger *log.Logger, batchID string, token uuid.UUID) {
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		logger.Error("", "begin launch-failure requeue for batch %s: %v", batchID, err)
		return
	}
	defer tx.Rollback()

	txQ := client.Queries.WithTx(tx)
	// the ownership decision must hold inside the transaction, so a batch reclaimed in the meantime is
	// never requeued or stripped of locks by this stale path
	released, err := txQ.ReleaseBatchOwner(ctx, database.ReleaseBatchOwnerParams{
		BatchID: batchID,
		OwnerID: token.String(),
	})
	if err != nil {
		logger.Error("", "release lease for batch %s after launch failure: %v (leaving it to the reaper)", batchID, err)
		return
	}
	if released == 0 {
		logger.Info("", "batch %s changed hands (owner no longer ours) — skipping stale requeue", batchID)
		return
	}
	if err := task.ReleaseLocksForBatch(ctx, txQ, batchID); err != nil {
		logger.Error("", "release locks for batch %s after launch failure: %v (leaving it to the reaper)", batchID, err)
		return
	}
	if _, err := txQ.RequeueProcessingBatch(ctx, batchID); err != nil {
		logger.Error("", "requeue batch %s after launch failure: %v", batchID, err)
		return
	}
	if err := tx.Commit(); err != nil {
		logger.Error("", "commit launch-failure requeue for batch %s: %v", batchID, err)
		return
	}
	logger.Info("", "batch %s requeued after its launch failed", batchID)
}

// LaunchQueuedBatch is the manual launcher behind `kushim batch run`: it claims the queued batch, forks
// its task CLI in batch mode with the execution token and waits for the child, propagating a non-zero exit.
// Only a queued batch with no lease is claimable; a paused batch points at resume, a processing one at its
// holder (a stale lease is the reaper's), and a terminal one refuses. It performs no slot check, because
// the dispatcher budget governs automatic dispatch only.
func LaunchQueuedBatch(ctx context.Context, client *database.Client, logger *log.Logger, catalog task.TypeCatalog, batchID string) error {
	batch, err := client.Queries.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("batch %q not found", batchID)
		}
		return fmt.Errorf("get batch %s: %w", batchID, err)
	}
	switch {
	case batch.Status.Terminal():
		return fmt.Errorf("batch %s is %s and cannot run", batchID, batch.Status)
	case batch.Status == types.Batch.Status.Paused:
		return fmt.Errorf("batch %s is paused; run 'kushim batch resume %s' first", batchID, batchID)
	case batch.Status == types.Batch.Status.Processing:
		return processingRunRefusal(ctx, client, batchID)
	}

	taskTypes, err := client.Queries.ListBatchTaskTypes(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return fmt.Errorf("list task types for batch %s: %w", batchID, err)
	}
	target, err := resolveLaunchTarget(catalog, batchID, taskTypes, types.HostQueue)
	if err != nil {
		return err
	}

	holds, waits := batchLockSets(taskTypes)
	token := uuid.New()
	claim := task.ClaimRequest{BatchID: batchID, ExecToken: token, Holds: holds, Waits: waits}
	if err := task.ClaimBatch(ctx, client, claim); err != nil {
		return err
	}

	cmd, err := launchBatch(target, token, true, nil)
	if err != nil {
		requeueAfterLaunchFailure(ctx, client, logger, batchID, token)
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("batch %s exited with error: %w", batchID, err)
	}
	return nil
}

// processingRunRefusal explains why a processing batch cannot be started and what recovers it. A leased
// batch is reclaimed by the reaper once its lease is stale; a lease-free batch is either running through
// the queue pools, which never take a lease, or has lost its execution, and lease recovery cannot see it.
func processingRunRefusal(ctx context.Context, client *database.Client, batchID string) error {
	owner, err := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
		BatchID:           batchID,
		StaleAfterSeconds: task.LeaseStaleSeconds,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("batch %s is processing without a lease: queue-pool batches run lease-free by design, "+
			"and otherwise no execution owns it, so lease recovery cannot reclaim it", batchID)
	}
	if err != nil {
		return fmt.Errorf("get batch owner for %s: %w", batchID, err)
	}
	switch {
	case owner.Stale:
		return fmt.Errorf("batch %s is processing under a stale lease; the reaper requeues it", batchID)
	case owner.Pid != 0 && task.IsAlive(owner.Pid):
		return fmt.Errorf("batch %s is being processed by PID %d", batchID, owner.Pid)
	case owner.Pid != 0:
		return fmt.Errorf("batch %s is processing under a lease held by PID %d, which is no longer running; "+
			"the reaper requeues it once the lease goes stale", batchID, owner.Pid)
	default:
		return fmt.Errorf("batch %s is processing under a fresh claim whose child has not adopted it yet; "+
			"the reaper requeues it once the lease goes stale", batchID)
	}
}

// launchFailure is the backoff state of one batch's consecutive launch failures.
type launchFailure struct {
	count       int
	nextAttempt time.Time
}

// launchBackoff tracks consecutive launch failures per batch and the earliest retry time. Its state is in
// memory only: a daemon restart clears it, which at worst grants the batch the same bounded attempts again.
type launchBackoff struct {
	mu       sync.Mutex
	failures map[string]launchFailure
}

func newLaunchBackoff() *launchBackoff {
	return &launchBackoff{failures: make(map[string]launchFailure)}
}

// cooling reports whether batchID is still inside a backoff window.
func (b *launchBackoff) cooling(batchID string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	failure, ok := b.failures[batchID]
	return ok && now.Before(failure.nextAttempt)
}

// record registers one more consecutive failure and returns the running count and the retry delay. A batch
// that reached maxLaunchFailures is failed instead of retried, so no delay is returned for it.
func (b *launchBackoff) record(batchID string) (int, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	failure := b.failures[batchID]
	failure.count++
	if failure.count >= maxLaunchFailures {
		delete(b.failures, batchID)
		return failure.count, 0
	}
	delay := 5 * time.Second << (failure.count - 1)
	failure.nextAttempt = time.Now().Add(delay)
	b.failures[batchID] = failure
	return failure.count, delay
}

// clear forgets a batch after a launch that ran.
func (b *launchBackoff) clear(batchID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.failures, batchID)
}

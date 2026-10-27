package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"syscall"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// LeaseStaleSeconds is the lease staleness window every lease-liveness query compares against, evaluated by
// the database clock.
const LeaseStaleSeconds int32 = 15

// ErrBatchLocked reports a batch owner collision with a live owner.
var ErrBatchLocked = errors.New("batch is locked by a live owner")

// ErrBatchTerminal reports a lifecycle operation that a terminal batch cannot accept.
var ErrBatchTerminal = errors.New("batch is terminal")

// ErrBatchNotResumable reports a resume request on a batch that is queued or processing.
var ErrBatchNotResumable = errors.New("batch is not resumable")

// ErrBatchNotQueued reports a claim attempt on a batch that is not queued. The dispatcher skips the
// candidate while batch run surfaces the refusal, so neither caller treats it as a failed claim.
var ErrBatchNotQueued = errors.New("batch is not queued")

// ErrBatchNotFound reports a lifecycle operation on a batch that does not exist.
var ErrBatchNotFound = errors.New("batch not found")

// OwnerState is the liveness of a batch's owner: none, live, or stale.
type OwnerState int

// The owner states, in liveness order.
const (
	OwnerNone OwnerState = iota
	OwnerLive
	OwnerStale
)

func (s OwnerState) String() string {
	switch s {
	case OwnerNone:
		return "none"
	case OwnerLive:
		return "live"
	case OwnerStale:
		return "stale"
	default:
		return "unknown"
	}
}

// Execution authorizes the lifecycle writes of one run through the batch lease: the guard is the lease
// owner. Settle, pause and the task transitions refuse a write whose batch is no longer in the state the
// guard expects, so a stale executor cannot overwrite the run that replaced it. The reaper's settle passes
// no guard after releasing the lease, which the queries accept when no owner row exists.
type Execution struct {
	BatchID string
	Guard   uuid.UUID
	Leased  bool
}

// TaskIdentity names one task by its public id and type, the durable identity the recovery paths use
// instead of error-text matching.
type TaskIdentity struct {
	TaskID   string
	TaskType types.TaskType
}

// ResetResult reports one batch reset: the processing tasks requeued and the tasks the reset quarantined
// (failed because their attempts were exhausted).
type ResetResult struct {
	Reset       int64
	Quarantined []TaskIdentity
}

// ConsumeTaskIDs returns the quarantined consume task ids, the only identities whose inbox file a quarantine
// has to move.
func (r ResetResult) ConsumeTaskIDs() []string {
	ids := make([]string, 0, len(r.Quarantined))
	for _, identity := range r.Quarantined {
		if identity.TaskType == types.Task.Type.Consume {
			ids = append(ids, identity.TaskID)
		}
	}
	return ids
}

// CancelResult reports one cancel: the tasks cancelled by status and what became of the live executor.
// PendingCancelled includes the waiting tasks, which a cancel takes with their parent. OwnerPID and
// OwnerAlive describe the lease holder the cancel found; OwnerPID is zero when the batch carries no lease.
type CancelResult struct {
	PendingCancelled    int64
	ProcessingCancelled int64
	OwnerPID            int64
	OwnerAlive          bool
	Signalled           bool
}

// ResumeResult reports one resume: whether the batch was requeued, the processing tasks reset and the
// failed tasks retried.
type ResumeResult struct {
	Resumed       bool
	Reset         int64
	FailedRetried int64
}

// Owner holds a claimed batch lease: Adopt takes the row over under the dispatcher's execution token,
// Heartbeat keeps it alive, and Release gives it up.
type Owner struct {
	queries *database.Queries
	ownerID string
	pid     int
}

// NewOwner prepares a batch owner for the given execution token.
func NewOwner(c *database.Client, ownerID string, pid int) *Owner {
	return &Owner{queries: c.Queries, ownerID: ownerID, pid: pid}
}

// Adopt takes over the lease the dispatcher claimed under this owner token, recording the executing
// child's pid and refreshing the heartbeat. Zero rows means the token no longer owns the batch — it was
// cancelled, requeued or reclaimed — so the child must not run its tasks.
func (o *Owner) Adopt(ctx context.Context, batchID string) error {
	rows, err := o.queries.AdoptBatchOwner(ctx, database.AdoptBatchOwnerParams{
		BatchID: batchID,
		OwnerID: o.ownerID,
		Pid:     int64(o.pid),
	})
	if err != nil {
		return fmt.Errorf("adopt batch owner: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("adopt batch %s: execution token %s no longer owns it (cancelled, requeued or reclaimed)", batchID, o.ownerID)
	}
	return nil
}

// Heartbeat renews the owner's timestamp, returning the still-live owner rows it saw (a zero row count
// means this owner's row is gone).
func (o *Owner) Heartbeat(ctx context.Context) (int64, error) {
	rows, err := o.queries.HeartbeatBatchOwner(ctx, o.ownerID)
	if err != nil {
		return rows, fmt.Errorf("heartbeat: %w", err)
	}
	return rows, nil
}

// Release gives the batch's ownership up.
func (o *Owner) Release(ctx context.Context, batchID string) error {
	_, err := o.queries.ReleaseBatchOwner(ctx, database.ReleaseBatchOwnerParams{
		BatchID: batchID,
		OwnerID: o.ownerID,
	})
	if err != nil {
		return fmt.Errorf("release batch owner: %w", err)
	}
	return nil
}

// ClaimRequest carries one batch claim: the batch, the execution token the forked child adopts, the lock
// sets the claim acquires and verifies, and the dispatcher that owns the resulting lease. DispatcherID is
// zero for a manual run, which therefore never charges a dispatcher's slot budget.
type ClaimRequest struct {
	BatchID      string
	ExecToken    uuid.UUID
	Holds        []types.LockName
	Waits        []types.LockName
	DispatcherID uuid.NullUUID
}

// ClaimBatch is the guarded batch claim of one execution: in one transaction it locks the batch row,
// requires it to be queued, inserts the execution lease under the token with pid 0, acquires the batch's
// holds all-or-nothing while verifying the waits its holding types declare, and flips the batch to the
// processing state. The caller passes the token to the forked child, which adopts the lease with its own
// pid; pid 0 is never alive, so the pre-adoption window cannot make the reaper signal a process. A busy
// lock reports ErrLockBusy and leaves nothing acquired, a non-queued batch ErrBatchNotQueued, a missing
// batch ErrBatchNotFound and a batch that already carries a lease ErrBatchLocked.
func ClaimBatch(ctx context.Context, client *database.Client, claim ClaimRequest) error {
	batchID := claim.BatchID
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin claim transaction for batch %s: %w", batchID, err)
	}
	defer tx.Rollback()

	txQ := client.Queries.WithTx(tx)

	status, err := txQ.LockBatchForClaim(ctx, batchID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrBatchNotFound, batchID)
		}
		return fmt.Errorf("lock batch %s for claim: %w", batchID, err)
	}
	if status != types.Batch.Status.Queued {
		return fmt.Errorf("%w: batch %s is %s", ErrBatchNotQueued, batchID, status)
	}

	inserted, err := txQ.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
		BatchID:      batchID,
		OwnerID:      claim.ExecToken.String(),
		Pid:          0,
		DispatcherID: claim.DispatcherID,
	})
	if err != nil {
		return fmt.Errorf("insert lease for batch %s: %w", batchID, err)
	}
	if inserted == 0 {
		return fmt.Errorf("%w: batch %s already carries a lease", ErrBatchLocked, batchID)
	}

	token := uuid.NullUUID{UUID: claim.ExecToken, Valid: true}
	if err := acquireLocks(ctx, txQ, claim.Holds, claim.Waits, token, batchID); err != nil {
		return err
	}

	claimed, err := txQ.MarkBatchClaimed(ctx, batchID)
	if err != nil {
		return fmt.Errorf("mark batch %s claimed: %w", batchID, err)
	}
	if claimed == 0 {
		return fmt.Errorf("mark batch %s claimed: guard refused under the row lock", batchID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit claim transaction for batch %s: %w", batchID, err)
	}
	return nil
}

// ResetProcessingTasksByBatch re-queues a batch's processing tasks, releases its locks and reports what
// the reset quarantined. It is the takeover path: the caller is not holding the batch's locks.
func ResetProcessingTasksByBatch(ctx context.Context, client *database.Client, batchID string, maxRetries int32) (ResetResult, error) {
	return resetProcessing(ctx, client, batchID, maxRetries, true)
}

// ResetProcessingTasksForClaim re-queues a batch's processing tasks for the execution that just claimed it.
// Unlike ResetProcessingTasksByBatch it leaves the batch's locks alone: the claim acquired them and the
// execution holds them for the whole run, so releasing them here would drop a hold the run depends on.
func ResetProcessingTasksForClaim(ctx context.Context, client *database.Client, batchID string, maxRetries int32) (ResetResult, error) {
	return resetProcessing(ctx, client, batchID, maxRetries, false)
}

func resetProcessing(ctx context.Context, client *database.Client, batchID string, maxRetries int32, releaseLocks bool) (ResetResult, error) {
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return ResetResult{}, fmt.Errorf("begin transaction for reset processing tasks by batch: %w", err)
	}
	defer tx.Rollback()

	result, err := resetProcessingTx(ctx, client.Queries.WithTx(tx), batchID, maxRetries, releaseLocks)
	if err != nil {
		return ResetResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return ResetResult{}, fmt.Errorf("commit transaction for reset processing tasks by batch: %w", err)
	}
	return result, nil
}

// IsAlive reports whether a PID belongs to a live process; a non-positive pid is never alive, since kill(0)
// targets the caller's own process group and a negative pid targets another group.
func IsAlive(pid int64) bool {
	return pid > 0 && syscall.Kill(int(pid), 0) == nil
}

// SettleBatch writes a batch's terminal status once its work is over and its execution guard still authorizes
// it. The write itself refuses while any pending, processing or waiting task remains, and settles failed when
// any task failed, otherwise completed. A terminal batch is immutable and returns its stored status without a
// write. The settle transaction also releases the batch's locks; the execution releases its own lease on
// teardown, and the reaper releases a stale one before settling. logger must be non-nil.
func SettleBatch(ctx context.Context, client *database.Client, logger *log.Logger, exec Execution) (types.BatchStatus, error) {
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin settle transaction for batch %s: %w", exec.BatchID, err)
	}
	defer tx.Rollback()

	txQ := client.Queries.WithTx(tx)
	status, err := txQ.SettleBatch(ctx, database.SettleBatchParams{
		ID:      exec.BatchID,
		OwnerID: executionOwner(exec),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			current, readErr := txQ.GetBatch(ctx, exec.BatchID)
			if readErr != nil {
				return "", fmt.Errorf("read batch %s after refused settle: %w", exec.BatchID, readErr)
			}
			return current.Status, nil
		}
		return "", fmt.Errorf("settle batch %s: %w", exec.BatchID, err)
	}

	if err := ReleaseLocksForBatch(ctx, txQ, exec.BatchID); err != nil {
		return status, err
	}
	if err := tx.Commit(); err != nil {
		return status, fmt.Errorf("commit settle transaction for batch %s: %w", exec.BatchID, err)
	}

	counts, err := client.Queries.BatchTaskStatusCounts(ctx, sql.NullString{String: exec.BatchID, Valid: true})
	if err != nil {
		logger.Error("", "settle batch %s: count tasks: %v", exec.BatchID, err)
		return status, nil
	}
	logSettled(logger, exec.BatchID, status, counts)
	return status, nil
}

// PauseBatch moves a processing batch to paused and releases its locks and lease. It authorizes through the
// lease: the execution must still own it, so a stale executor cannot pause the run that replaced it. A
// refused pause (another owner, a terminal batch or a batch that is no longer processing) leaves everything
// untouched and reports nil. The leased child's poll loop stops on the paused status, and with the lease
// gone its owner-scoped claims find nothing. logger must be non-nil.
func PauseBatch(ctx context.Context, client *database.Client, logger *log.Logger, exec Execution) error {
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pause transaction for batch %s: %w", exec.BatchID, err)
	}
	defer tx.Rollback()

	txQ := client.Queries.WithTx(tx)
	rows, err := txQ.SetBatchPaused(ctx, database.SetBatchPausedParams{
		ID:      exec.BatchID,
		OwnerID: executionOwner(exec),
	})
	if err != nil {
		return fmt.Errorf("pause batch %s: %w", exec.BatchID, err)
	}
	if rows == 0 {
		return nil
	}
	if err := ReleaseLocksForBatch(ctx, txQ, exec.BatchID); err != nil {
		return err
	}
	if err := releaseOwnerRow(ctx, txQ, exec); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pause transaction for batch %s: %w", exec.BatchID, err)
	}
	logger.Info("", "batch %s paused: lease and locks released, resume requeues it", exec.BatchID)
	return nil
}

// CancelBatch is the single cancel operation: it signals a live leased execution, cancels the batch's
// pending, waiting and processing tasks, releases its locks and lease, and writes cancelled. A missing
// batch reports ErrBatchNotFound and a terminal one ErrBatchTerminal. A host-run lease carries pid 0, so
// nothing is signalled; that executor observes the cancellation through its batch-status monitor and its
// guarded transitions landing zero rows. logger must be non-nil.
func CancelBatch(ctx context.Context, client *database.Client, logger *log.Logger, batchID string) (CancelResult, error) {
	batch, err := client.Queries.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CancelResult{}, fmt.Errorf("%w: %s", ErrBatchNotFound, batchID)
		}
		return CancelResult{}, fmt.Errorf("get batch %s: %w", batchID, err)
	}
	if batch.Status.Terminal() {
		return CancelResult{}, fmt.Errorf("%w: batch %s is %s", ErrBatchTerminal, batchID, batch.Status)
	}

	var result CancelResult

	owner, ownerErr := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
		BatchID:           batchID,
		StaleAfterSeconds: LeaseStaleSeconds,
	})
	switch {
	case ownerErr == nil:
		result.OwnerPID = owner.Pid
		result.OwnerAlive = IsAlive(owner.Pid)
		if result.OwnerAlive {
			if err := syscall.Kill(int(owner.Pid), syscall.SIGTERM); err == nil {
				result.Signalled = true
			} else {
				logger.Error("", "signal SIGTERM to PID %d for batch %s: %v", owner.Pid, batchID, err)
			}
		}
	case !errors.Is(ownerErr, sql.ErrNoRows):
		return CancelResult{}, fmt.Errorf("get batch owner for %s: %w", batchID, ownerErr)
	}

	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return CancelResult{}, fmt.Errorf("begin cancel transaction for batch %s: %w", batchID, err)
	}
	defer tx.Rollback()

	txQ := client.Queries.WithTx(tx)
	bid := sql.NullString{String: batchID, Valid: true}

	result.PendingCancelled, err = txQ.CancelPendingTasksByBatch(ctx, bid)
	if err != nil {
		return CancelResult{}, fmt.Errorf("cancel pending tasks for batch %s: %w", batchID, err)
	}
	result.ProcessingCancelled, err = txQ.CancelProcessingTasksByBatch(ctx, bid)
	if err != nil {
		return CancelResult{}, fmt.Errorf("cancel processing tasks for batch %s: %w", batchID, err)
	}
	if err := ReleaseLocksForBatch(ctx, txQ, batchID); err != nil {
		return CancelResult{}, err
	}
	rows, err := txQ.SetBatchCancelled(ctx, batchID)
	if err != nil {
		return CancelResult{}, fmt.Errorf("cancel batch %s: %w", batchID, err)
	}
	if rows == 0 {
		return CancelResult{}, fmt.Errorf("%w: batch %s left its cancellable states", ErrBatchTerminal, batchID)
	}
	if ownerErr == nil {
		if _, err := txQ.ReleaseBatchOwner(ctx, database.ReleaseBatchOwnerParams{
			BatchID: batchID,
			OwnerID: owner.OwnerID,
		}); err != nil {
			return CancelResult{}, fmt.Errorf("release batch owner for %s: %w", batchID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CancelResult{}, fmt.Errorf("commit cancel transaction for batch %s: %w", batchID, err)
	}

	logger.Info("", "batch %s cancelled: %d pending/waiting and %d processing tasks cancelled (signalled=%t)",
		batchID, result.PendingCancelled, result.ProcessingCancelled, result.Signalled)
	return result, nil
}

// FailUnstartedBatch is the launch-failure terminal write: under the execution token's lease fence it
// cancels the tasks that never ran, releases the batch's locks and writes failed in one transaction.
// applied is false when the lease no longer belongs to execToken — the batch changed hands since the launch
// decision — and nothing is written. It is the only failure terminal write besides SettleBatch.
func FailUnstartedBatch(ctx context.Context, client *database.Client, batchID string, execToken uuid.UUID) (applied bool, err error) {
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin fail-unstarted transaction for batch %s: %w", batchID, err)
	}
	defer tx.Rollback()

	txQ := client.Queries.WithTx(tx)
	released, err := txQ.ReleaseBatchOwner(ctx, database.ReleaseBatchOwnerParams{
		BatchID: batchID,
		OwnerID: execToken.String(),
	})
	if err != nil {
		return false, fmt.Errorf("release lease for batch %s: %w", batchID, err)
	}
	if released == 0 {
		return false, nil
	}

	bid := sql.NullString{String: batchID, Valid: true}
	if _, err := txQ.CancelPendingTasksByBatch(ctx, bid); err != nil {
		return false, fmt.Errorf("cancel pending tasks for batch %s: %w", batchID, err)
	}
	if _, err := txQ.CancelProcessingTasksByBatch(ctx, bid); err != nil {
		return false, fmt.Errorf("cancel processing tasks for batch %s: %w", batchID, err)
	}
	if err := ReleaseLocksForBatch(ctx, txQ, batchID); err != nil {
		return false, err
	}
	if _, err := txQ.SetBatchFailed(ctx, batchID); err != nil {
		return false, fmt.Errorf("fail batch %s: %w", batchID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit fail-unstarted transaction for batch %s: %w", batchID, err)
	}
	return true, nil
}

// ResumeBatch is the single requeue operation: from failed, completed, cancelled or paused it requeues the
// batch, resets its processing tasks and, when includeFailed is set, retries its failed tasks together
// with the waiting children their consumes had. A missing batch reports ErrBatchNotFound, a batch with a
// live lease ErrBatchLocked and a queued or processing batch ErrBatchNotResumable; when the resets leave
// no active work the batch is left untouched and Resumed is false. logger must be non-nil.
func ResumeBatch(ctx context.Context, client *database.Client, logger *log.Logger, batchID string, maxRetries int32, includeFailed bool) (ResumeResult, error) {
	batch, err := client.Queries.GetBatch(ctx, batchID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResumeResult{}, fmt.Errorf("%w: %s", ErrBatchNotFound, batchID)
		}
		return ResumeResult{}, fmt.Errorf("get batch %s: %w", batchID, err)
	}
	if batch.Status != types.Batch.Status.Failed && batch.Status != types.Batch.Status.Completed &&
		batch.Status != types.Batch.Status.Cancelled && batch.Status != types.Batch.Status.Paused {
		return ResumeResult{}, fmt.Errorf("%w: batch %s is %s", ErrBatchNotResumable, batchID, batch.Status)
	}

	owner, ownerErr := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
		BatchID:           batchID,
		StaleAfterSeconds: LeaseStaleSeconds,
	})
	if ownerErr != nil && !errors.Is(ownerErr, sql.ErrNoRows) {
		return ResumeResult{}, fmt.Errorf("get batch owner for %s: %w", batchID, ownerErr)
	}
	if ownerErr == nil && !owner.Stale {
		return ResumeResult{}, ErrBatchLocked
	}

	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return ResumeResult{}, fmt.Errorf("begin resume transaction for batch %s: %w", batchID, err)
	}
	defer tx.Rollback()

	txQ := client.Queries.WithTx(tx)
	bid := sql.NullString{String: batchID, Valid: true}

	reset, err := resetProcessingTx(ctx, txQ, batchID, maxRetries, true)
	if err != nil {
		return ResumeResult{}, err
	}

	result := ResumeResult{Reset: reset.Reset}
	if includeFailed {
		result.FailedRetried, err = txQ.RetryFailedTasksByBatch(ctx, bid)
		if err != nil {
			return ResumeResult{}, fmt.Errorf("retry failed tasks for batch %s: %w", batchID, err)
		}
		// the retried tasks are pending now, so their discarded successors are restored in this same transaction
		// before the requeue: it must never expose a successor whose waiting branch still looks dead
		if _, err := txQ.RestoreDiscardedSuccessorsByBatch(ctx, batchID); err != nil {
			return ResumeResult{}, fmt.Errorf("restore successors of retried tasks for batch %s: %w", batchID, err)
		}
	}

	counts, err := txQ.BatchTaskStatusCounts(ctx, bid)
	if err != nil {
		return ResumeResult{}, fmt.Errorf("count tasks for batch %s: %w", batchID, err)
	}
	if counts.Pending+counts.Waiting == 0 {
		return result, nil
	}

	rows, err := txQ.ResumeBatch(ctx, batchID)
	if err != nil {
		return ResumeResult{}, fmt.Errorf("resume batch %s: %w", batchID, err)
	}
	if rows == 0 {
		return result, nil
	}
	if ownerErr == nil {
		// a stale owner row left behind would let the reaper act on the batch now that it is queued again
		if _, err := txQ.ReleaseBatchOwner(ctx, database.ReleaseBatchOwnerParams{
			BatchID: batchID,
			OwnerID: owner.OwnerID,
		}); err != nil {
			return ResumeResult{}, fmt.Errorf("release stale batch owner for %s: %w", batchID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return ResumeResult{}, fmt.Errorf("commit resume transaction for batch %s: %w", batchID, err)
	}

	result.Resumed = true
	logger.Info("", "batch %s resumed: %d processing tasks reset, %d failed tasks retried", batchID, result.Reset, result.FailedRetried)
	return result, nil
}

func executionOwner(exec Execution) sql.NullString {
	if !exec.Leased {
		return sql.NullString{}
	}
	return sql.NullString{String: exec.Guard.String(), Valid: true}
}

func releaseOwnerRow(ctx context.Context, queries *database.Queries, exec Execution) error {
	if !exec.Leased {
		return nil
	}
	if _, err := queries.ReleaseBatchOwner(ctx, database.ReleaseBatchOwnerParams{
		BatchID: exec.BatchID,
		OwnerID: exec.Guard.String(),
	}); err != nil {
		return fmt.Errorf("release batch owner for %s: %w", exec.BatchID, err)
	}
	return nil
}

func logSettled(logger *log.Logger, batchID string, status types.BatchStatus, counts database.BatchTaskStatusCountsRow) {
	logger.Info("", "batch %s settled as %s: %d completed, %d failed, %d cancelled",
		batchID, status, counts.Completed, counts.Failed, counts.Cancelled)
}

func resetProcessingTx(ctx context.Context, queries *database.Queries, batchID string, maxRetries int32, releaseLocks bool) (ResetResult, error) {
	bid := sql.NullString{String: batchID, Valid: true}

	if releaseLocks {
		if err := ReleaseLocksForBatch(ctx, queries, batchID); err != nil {
			return ResetResult{}, err
		}
	}

	quarantined, err := queries.QuarantineProcessingTasksByBatch(ctx, database.QuarantineProcessingTasksByBatchParams{
		BatchID:  bid,
		Attempts: maxRetries,
	})
	if err != nil {
		return ResetResult{}, fmt.Errorf("quarantine processing tasks by batch: %w", err)
	}

	reset, err := queries.ResetProcessingTasksByBatch(ctx, database.ResetProcessingTasksByBatchParams{
		BatchID:  bid,
		Attempts: maxRetries,
	})
	if err != nil {
		return ResetResult{}, fmt.Errorf("reset processing tasks by batch: %w", err)
	}

	if _, err := queries.RestoreDiscardedSuccessorsByBatch(ctx, batchID); err != nil {
		return ResetResult{}, fmt.Errorf("restore discarded successors by batch: %w", err)
	}

	result := ResetResult{Reset: reset}
	for _, row := range quarantined {
		result.Quarantined = append(result.Quarantined, TaskIdentity{TaskID: row.TaskID, TaskType: row.TaskType})
	}
	return result, nil
}

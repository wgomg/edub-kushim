package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// StaleAfter is how long a batch owner may go without a heartbeat before
// its ownership counts as stale.
const StaleAfter = 15 * time.Second

// ErrBatchLocked reports a batch owner collision with a live owner.
var ErrBatchLocked = errors.New("batch is locked by a live owner")

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

// Owner claims and holds batch ownership: Acquire takes the row or reports
// it live or stale, Heartbeat keeps it alive, and Release gives it up. The
// consume driver and the blocking-task protocol use it.
type Owner struct {
	queries    *database.Queries
	ownerID    string
	pid        int
	logger     *log.Logger
	client     *database.Client
	maxRetries int32
}

// NewOwner prepares a batch owner; maxRetries bounds claim contention
// retries. logger must be non-nil: every log site dereferences it without
// a guard.
func NewOwner(c *database.Client, ownerID string, pid int, logger *log.Logger, maxRetries int) *Owner {
	return &Owner{queries: c.Queries, ownerID: ownerID, pid: pid, logger: logger, client: c, maxRetries: int32(maxRetries)}
}

// Acquire takes a batch's ownership row, taking over one whose heartbeat is
// older than StaleAfter and refusing a live one with ErrBatchLocked.
func (o *Owner) Acquire(ctx context.Context, batchID string) error {
	rows, err := o.queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
		BatchID: batchID,
		OwnerID: o.ownerID,
		Pid:     int64(o.pid),
	})
	if err != nil {
		return fmt.Errorf("insert batch owner: %w", err)
	}
	if rows > 0 {
		return nil
	}

	cutoff := time.Now().Add(-StaleAfter)
	rows, err = o.queries.UpdateBatchOwnerIfStale(ctx, database.UpdateBatchOwnerIfStaleParams{
		OwnerID:       o.ownerID,
		OwnerID_2:     o.ownerID,
		Pid:           int64(o.pid),
		BatchID:       batchID,
		LastHeartbeat: cutoff,
	})
	if err != nil {
		return fmt.Errorf("update stale batch owner: %w", err)
	}
	if rows == 0 {
		o.logger.Error("", "acquire batch %s failed: live owner holds it", batchID)
		return ErrBatchLocked
	}
	return nil
}

// AcquireForce takes the ownership row regardless of who holds it, for
// the takeover paths the cancel protocol drives.
func (o *Owner) AcquireForce(ctx context.Context, batchID string) error {
	rows, err := o.queries.AcquireBatchOwnerForce(ctx, database.AcquireBatchOwnerForceParams{
		BatchID: batchID,
		OwnerID: o.ownerID,
		Pid:     int64(o.pid),
	})
	if err != nil {
		return fmt.Errorf("force acquire batch owner: %w", err)
	}
	if rows == 0 {
		o.logger.Error("", "AcquireForce for batch %s returned 0 rows — batch_owner row not written (ownerID=%s, pid=%d)", batchID, o.ownerID, o.pid)
	}
	return nil
}

// Heartbeat renews the owner's timestamp, returning the still-live
// owner rows it saw (a zero row count means this owner's row is gone).
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

// ResetProcessingByBatch re-queues a batch's processing tasks.
func (o *Owner) ResetProcessingByBatch(ctx context.Context, batchID string) (int64, error) {
	tx, err := o.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction for reset processing tasks by batch: %w", err)
	}
	defer tx.Rollback()

	bid := sql.NullString{String: batchID, Valid: true}
	txQ := o.client.Queries.WithTx(tx)

	if _, err := txQ.ReleaseMaintenanceLockForBatch(ctx, database.ReleaseMaintenanceLockForBatchParams{
		BatchID: bid,
		Types:   types.BlockingTaskTypes(),
	}); err != nil {
		return 0, fmt.Errorf("release maintenance lock for batch: %w", err)
	}

	quarantined, err := txQ.QuarantineProcessingTasksByBatch(ctx, database.QuarantineProcessingTasksByBatchParams{
		BatchID:  bid,
		Attempts: o.maxRetries,
	})
	if err != nil {
		return 0, fmt.Errorf("quarantine processing tasks by batch: %w", err)
	}

	reset, err := txQ.ResetProcessingTasksByBatch(ctx, database.ResetProcessingTasksByBatchParams{
		BatchID:  bid,
		Attempts: o.maxRetries,
	})
	if err != nil {
		return 0, fmt.Errorf("reset processing tasks by batch: %w", err)
	}

	if _, err := txQ.RestoreDiscardedEnrichTasksByBatch(ctx, bid); err != nil {
		return 0, fmt.Errorf("restore discarded enrich tasks by batch: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit transaction for reset processing tasks by batch: %w", err)
	}

	return quarantined + reset, nil
}

// CleanupCompleted removes the ownership rows of completed batches.
func (o *Owner) CleanupCompleted(ctx context.Context) error {
	_, err := o.queries.CleanupCompletedBatches(ctx)
	if err != nil {
		return fmt.Errorf("cleanup completed batches: %w", err)
	}
	return nil
}

// IsOrphaned decides whether a batch needs takeover: the owner state and
// the pending and processing counts together say whether a dead owner left
// work behind.
func IsOrphaned(state OwnerState, pending, processing int64) bool {
	return state != OwnerLive && (pending > 0 || processing > 0)
}

// MarkBatchProcessing moves a batch to processing, ignoring rows that no
// longer exist; a failed write is logged, not returned. It and
// FinalizeBatchStatus are the best-effort bookends of the blocking-task
// protocol, called from defers with a non-nil logger.
func MarkBatchProcessing(ctx context.Context, queries *database.Queries, logger *log.Logger, batchID string) {
	if batchID == "" {
		return
	}
	if err := queries.SetBatchProcessing(ctx, batchID); err != nil {
		logger.Error("", "mark batch %s processing failed: %v", batchID, err)
	}
}

// FinalizeBatchStatus settles a batch as completed or failed based on the
// body's error, once no sibling task is still pending or waiting, and logs
// one INFO summary line with the settle status and the
// completed/failed/cancelled task counts. A failed count query or status
// write is logged, not returned, and never blocks the settle; logger must
// be non-nil.
func FinalizeBatchStatus(ctx context.Context, queries *database.Queries, logger *log.Logger, batchID string, failed bool) {
	if batchID == "" {
		return
	}
	bid := sql.NullString{String: batchID, Valid: true}
	for _, status := range []types.TaskStatus{types.Task.Status.Pending, types.Task.Status.Waiting} {
		count, err := queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
			BatchID: bid,
			Status:  status,
		})
		if err != nil {
			logger.Error("", "finalize batch %s: count %s tasks: %v", batchID, status, err)
			return
		}
		if count > 0 {
			return
		}
	}
	status := types.Batch.Status.Completed
	if failed {
		status = types.Batch.Status.Failed
		if err := queries.SetBatchFailed(ctx, batchID); err != nil {
			logger.Error("", "finalize batch %s as failed: %v", batchID, err)
		}
	} else if err := queries.SetBatchCompleted(ctx, batchID); err != nil {
		logger.Error("", "finalize batch %s as completed: %v", batchID, err)
	}

	counts := make([]string, 0, 3)
	for _, taskStatus := range []types.TaskStatus{types.Task.Status.Completed, types.Task.Status.Failed, types.Task.Status.Cancelled} {
		count, err := queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
			BatchID: bid,
			Status:  taskStatus,
		})
		if err != nil {
			logger.Error("", "finalize batch %s: count %s tasks: %v", batchID, taskStatus, err)
			return
		}
		counts = append(counts, fmt.Sprintf("%d %s", count, taskStatus))
	}
	logger.Info("", "batch %s settled as %s: %s", batchID, status, strings.Join(counts, ", "))
}

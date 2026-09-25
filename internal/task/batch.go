package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	Queries    *database.Queries
	OwnerID    string
	PID        int
	Logger     *log.Logger
	client     *database.Client
	maxRetries int32
}

// NewOwner prepares a batch owner; maxRetries bounds claim contention
// retries.
func NewOwner(c *database.Client, ownerID string, pid int, logger *log.Logger, maxRetries int) *Owner {
	return &Owner{Queries: c.Queries, OwnerID: ownerID, PID: pid, Logger: logger, client: c, maxRetries: int32(maxRetries)}
}

// Acquire takes a batch's ownership row, taking over a stale one and
// refusing a live one with ErrBatchLocked.
func (o *Owner) Acquire(ctx context.Context, batchID string, staleAfter time.Duration) error {
	rows, err := o.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
		BatchID: batchID,
		OwnerID: o.OwnerID,
		Pid:     int64(o.PID),
	})
	if err != nil {
		return fmt.Errorf("insert batch owner: %w", err)
	}
	if rows > 0 {
		return nil
	}

	cutoff := time.Now().Add(-staleAfter)
	rows, err = o.Queries.UpdateBatchOwnerIfStale(ctx, database.UpdateBatchOwnerIfStaleParams{
		OwnerID:       o.OwnerID,
		OwnerID_2:     o.OwnerID,
		Pid:           int64(o.PID),
		BatchID:       batchID,
		LastHeartbeat: cutoff,
	})
	if err != nil {
		return fmt.Errorf("update stale batch owner: %w", err)
	}
	if rows == 0 {
		if o.Logger != nil {
			o.Logger.Error("", "acquire batch %s failed: live owner holds it", batchID)
		}
		return ErrBatchLocked
	}
	return nil
}

// AcquireForce takes the ownership row regardless of who holds it, for
// the takeover paths the cancel protocol drives.
func (o *Owner) AcquireForce(ctx context.Context, batchID string) error {
	rows, err := o.Queries.AcquireBatchOwnerForce(ctx, database.AcquireBatchOwnerForceParams{
		BatchID: batchID,
		OwnerID: o.OwnerID,
		Pid:     int64(o.PID),
	})
	if err != nil {
		return fmt.Errorf("force acquire batch owner: %w", err)
	}
	if rows == 0 && o.Logger != nil {
		o.Logger.Error("", "AcquireForce for batch %s returned 0 rows — batch_owner row not written (ownerID=%s, pid=%d)", batchID, o.OwnerID, o.PID)
	}
	return nil
}

// Heartbeat renews the owner's timestamp, returning the still-live
// owner rows it saw (a zero row count means this owner's row is gone).
func (o *Owner) Heartbeat(ctx context.Context) (int64, error) {
	rows, err := o.Queries.HeartbeatBatchOwner(ctx, o.OwnerID)
	if err != nil {
		return rows, fmt.Errorf("heartbeat: %w", err)
	}
	return rows, nil
}

// Release gives the batch's ownership up.
func (o *Owner) Release(ctx context.Context, batchID string) error {
	_, err := o.Queries.ReleaseBatchOwner(ctx, database.ReleaseBatchOwnerParams{
		BatchID: batchID,
		OwnerID: o.OwnerID,
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
	_, err := o.Queries.CleanupCompletedBatches(ctx)
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
// longer exist. It and FinalizeBatchStatus are the bookends of the
// blocking-task protocol.
func MarkBatchProcessing(ctx context.Context, queries *database.Queries, batchID string) {
	if batchID == "" {
		return
	}
	_ = queries.SetBatchProcessing(ctx, batchID)
}

// FinalizeBatchStatus settles a batch as completed or failed based on the
// body's error.
func FinalizeBatchStatus(ctx context.Context, queries *database.Queries, batchID string, failed bool) {
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
			return
		}
		if count > 0 {
			return
		}
	}
	if failed {
		_ = queries.SetBatchFailed(ctx, batchID)
	} else {
		_ = queries.SetBatchCompleted(ctx, batchID)
	}
}

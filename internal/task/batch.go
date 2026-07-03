package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const StaleAfter = 15 * time.Second

var ErrBatchLocked = errors.New("batch is locked by a live owner")

type OwnerState int

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

type Owner struct {
	Queries    *database.Queries
	OwnerID    string
	PID        int
	Logger     *utils.Logger
	client     *database.Client
	maxRetries int64
}

func NewOwner(c *database.Client, ownerID string, pid int, logger *utils.Logger, maxRetries int) *Owner {
	return &Owner{Queries: c.Queries, OwnerID: ownerID, PID: pid, Logger: logger, client: c, maxRetries: int64(maxRetries)}
}

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
			o.Logger.Error(nil, "acquire batch %s failed: live owner holds it", batchID)
		}
		return ErrBatchLocked
	}
	return nil
}

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
		o.Logger.Error(nil, "AcquireForce for batch %s returned 0 rows — batch_owner row not written (ownerID=%s, pid=%d)", batchID, o.OwnerID, o.PID)
	}
	return nil
}

func (o *Owner) Heartbeat(ctx context.Context) (int64, error) {
	rows, err := o.Queries.HeartbeatBatchOwner(ctx, o.OwnerID)
	if err != nil {
		return rows, fmt.Errorf("heartbeat: %w", err)
	}
	return rows, nil
}

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

func (o *Owner) ResetProcessingByBatch(ctx context.Context, batchID string) (int64, error) {
	tx, err := o.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction for reset processing tasks by batch: %w", err)
	}
	defer tx.Rollback()

	bid := sql.NullString{String: batchID, Valid: true}
	txQ := o.client.Queries.WithTx(tx)

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

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit transaction for reset processing tasks by batch: %w", err)
	}

	return quarantined + reset, nil
}

func (o *Owner) CleanupCompleted(ctx context.Context) error {
	_, err := o.Queries.CleanupCompletedBatches(ctx)
	if err != nil {
		return fmt.Errorf("cleanup completed batches: %w", err)
	}
	return nil
}

func IsOrphaned(state OwnerState, pending, processing int64) bool {
	return state != OwnerLive && (pending > 0 || processing > 0)
}

package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/types"
)

// ErrLockBusy reports that a lock a claim or acquisition needs is held by another execution. A claim attempt
// treats it as an idle round rather than an error.
var ErrLockBusy = errors.New("lock busy")

// LockHeartbeatInterval is how often a holder refreshes its locks' staleness timestamp. It must stay well
// under the staleness window the lock queries use (lock_is_held, 30 minutes) or a live holder becomes
// takeover-eligible mid-run.
const LockHeartbeatInterval = 5 * time.Minute

// AcquireLocks acquires the holds all-or-nothing for one execution, verifying that none of the waits is
// held, in one transaction that row-locks every involved lock in canonical order. A conflict returns
// ErrLockBusy and leaves nothing acquired; other errors are database failures.
func AcquireLocks(ctx context.Context, client *database.Client, holds, waits []types.LockName, token uuid.NullUUID, batchID string) error {
	if len(holds) == 0 {
		return nil
	}

	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin lock transaction: %w", err)
	}
	defer tx.Rollback()

	if err := acquireLocks(ctx, client.Queries.WithTx(tx), holds, waits, token, batchID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit lock transaction: %w", err)
	}
	return nil
}

// acquireLocks is AcquireLocks inside an existing claim transaction. The row locks are taken in the
// canonical name order every acquirer follows, so two executions can never hold one lock while waiting
// for another.
func acquireLocks(ctx context.Context, queries *database.Queries, holds, waits []types.LockName, token uuid.NullUUID, batchID string) error {
	if _, err := queries.LockRowsForUpdate(ctx, lockNameStrings(UnionLockNames(holds, waits))); err != nil {
		return fmt.Errorf("lock rows: %w", err)
	}

	if len(waits) > 0 {
		held, err := queries.CountHeldLocks(ctx, lockNameStrings(waits))
		if err != nil {
			return fmt.Errorf("check waited locks: %w", err)
		}
		if held > 0 {
			return ErrLockBusy
		}
	}

	rows, err := queries.AcquireLocks(ctx, database.AcquireLocksParams{
		OwnerToken: token,
		BatchID:    sql.NullString{String: batchID, Valid: batchID != ""},
		LockNames:  lockNameStrings(holds),
	})
	if err != nil {
		return fmt.Errorf("acquire locks: %w", err)
	}
	if rows != int64(len(holds)) {
		return ErrLockBusy
	}
	return nil
}

// ReleaseLocksForToken releases every lock held under a unique token and notifies the waiters; it is safe
// to call when nothing is held. Manual holders share the NULL token and use the scoped variant below.
func ReleaseLocksForToken(ctx context.Context, queries *database.Queries, token uuid.NullUUID) error {
	return releaseLocksForToken(ctx, queries, token, types.LockNameValues())
}

// releaseLocksForToken releases the named locks the token holds and notifies their waiters. A manual
// holder shares the NULL token with every other manual run, so it must release only the names its own
// policy acquired: releasing every name would drop a concurrent manual run's distinct lock.
func releaseLocksForToken(ctx context.Context, queries *database.Queries, token uuid.NullUUID, names []string) error {
	released, err := queries.ReleaseLocksForToken(ctx, database.ReleaseLocksForTokenParams{
		LockNames:  names,
		OwnerToken: token,
	})
	if err != nil {
		return fmt.Errorf("release locks: %w", err)
	}
	return notifyLocksReleased(ctx, queries, released)
}

// ReleaseLocksForBatch releases every lock the batch's execution holds and notifies the waiters. It
// covers both task-held locks and a child-held lock, because every acquisition records its batch.
func ReleaseLocksForBatch(ctx context.Context, queries *database.Queries, batchID string) error {
	released, err := queries.ReleaseLocksForBatch(ctx, database.ReleaseLocksForBatchParams{
		LockNames: types.LockNameValues(),
		BatchID:   batchID,
	})
	if err != nil {
		return fmt.Errorf("release locks for batch %s: %w", batchID, err)
	}
	return notifyLocksReleased(ctx, queries, released)
}

// ReleaseLocksForStaleHolders releases the aged locks of processing tasks of the given holding types
// and notifies the waiters.
func ReleaseLocksForStaleHolders(ctx context.Context, queries *database.Queries, holdingTypes []types.TaskType) error {
	released, err := queries.ReleaseLocksForStaleHolders(ctx, database.ReleaseLocksForStaleHoldersParams{
		LockNames: types.LockNameValues(),
		Types:     holdingTypes,
	})
	if err != nil {
		return fmt.Errorf("release locks for stale holders: %w", err)
	}
	return notifyLocksReleased(ctx, queries, released)
}

func notifyLocksReleased(ctx context.Context, queries *database.Queries, released []string) error {
	if len(released) == 0 {
		return nil
	}
	if err := queries.NotifyLocksReleased(ctx, released); err != nil {
		return fmt.Errorf("notify lock release: %w", err)
	}
	return nil
}

// UnionLockNames returns the deduplicated union of two lock-name sets, holds first. Callers use it to
// accumulate the holds or the waits of a batch's task types.
func UnionLockNames(holds, waits []types.LockName) []types.LockName {
	names := make([]types.LockName, 0, len(holds)+len(waits))
	names = append(names, holds...)
	for _, wait := range waits {
		if !slices.Contains(names, wait) {
			names = append(names, wait)
		}
	}
	return names
}

func lockNameStrings(names []types.LockName) []string {
	values := make([]string, len(names))
	for i, name := range names {
		values[i] = string(name)
	}
	return values
}

package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/log"
)

// TestHoldMaintenanceLock pins the helper contract the blocking-task
// lifecycle, the tagmeasure CLI and both migration paths share: TryHold
// refuses a held lock instead of waiting, a waiting acquire ends with the
// context, and a claim token's lock stays with its owner.
func TestHoldMaintenanceLock(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()
	logger := log.NewDiscardLogger()

	t.Run("try acquires a free lock and release frees it", func(t *testing.T) {
		defer q.ReleaseBackupLock(ctx, uuid.NullUUID{})

		release, err := TryHoldMaintenanceLock(ctx, q, logger, "try-lock", uuid.NullUUID{}, nil)
		assertNoError(t, err, "try hold")
		locked, err := q.IsMaintenanceLocked(ctx)
		assertNoError(t, err, "is locked")
		assertEqual(t, locked, int32(1), "lock held while held")

		release()
		locked, err = q.IsMaintenanceLocked(ctx)
		assertNoError(t, err, "is locked after release")
		assertEqual(t, locked, int32(0), "lock released")
	})

	t.Run("try refuses a lock another holder owns", func(t *testing.T) {
		defer q.ReleaseBackupLock(ctx, uuid.NullUUID{})

		rows, err := q.AcquireBackupLock(ctx)
		assertNoError(t, err, "pre-acquire")
		assertEqual(t, rows, int64(1), "pre-acquire")

		release, err := TryHoldMaintenanceLock(ctx, q, logger, "try-lock", uuid.NullUUID{}, nil)
		if err == nil {
			release()
			t.Fatal("TryHoldMaintenanceLock accepted a lock held by another process")
		}
		if !strings.Contains(err.Error(), "held by another process") {
			t.Errorf("error = %q, want it to report the held lock", err)
		}
		locked, err := q.IsMaintenanceLocked(ctx)
		assertNoError(t, err, "is locked")
		assertEqual(t, locked, int32(1), "the original holder keeps the lock")
	})

	t.Run("the waiting acquire ends with the context", func(t *testing.T) {
		defer q.ReleaseBackupLock(ctx, uuid.NullUUID{})

		_, err := q.AcquireBackupLock(ctx)
		assertNoError(t, err, "pre-acquire")

		waitCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		release, err := HoldMaintenanceLock(waitCtx, q, logger, "wait-lock", uuid.NullUUID{}, nil)
		if release != nil {
			t.Error("release returned although the lock was never acquired")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want the context deadline", err)
		}
	})

	t.Run("a claim token's lock is left for its owner", func(t *testing.T) {
		token := uuid.NullUUID{UUID: uuid.New(), Valid: true}
		defer q.ReleaseMaintenanceLockForTask(ctx, token)

		rows, err := q.AcquireMaintenanceLockForTask(ctx, token)
		assertNoError(t, err, "claim acquire")
		assertEqual(t, rows, int64(1), "claim acquire")

		release, err := HoldMaintenanceLock(ctx, q, logger, "claim-lock", token, nil)
		assertNoError(t, err, "hold under the claim token")
		release()

		locked, err := q.IsMaintenanceLocked(ctx)
		assertNoError(t, err, "is locked")
		assertEqual(t, locked, int32(1), "the helper must not release the claim token's lock")
	})
}

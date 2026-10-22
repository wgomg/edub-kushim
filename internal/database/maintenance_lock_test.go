package database

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TestLockQueries pins the lock-row contract the claims, the child batch acquisition and the manual
// holder path share: acquisition is guarded by the holder token, a held lock refuses another holder, a
// waited lock blocks, release reports the released names, and an aged holder is taken over.
func TestLockQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()
	maintenance := []string{string(types.LockMaintenance)}

	t.Cleanup(func() {
		_, _ = q.ReleaseLocksForToken(ctx, ReleaseLocksForTokenParams{
			LockNames:  types.LockNameValues(),
			OwnerToken: uuid.NullUUID{},
		})
	})

	t.Run("acquire holds the lock until its token releases it", func(t *testing.T) {
		token := uuid.NullUUID{UUID: uuid.New(), Valid: true}
		rows, err := q.AcquireLocks(ctx, AcquireLocksParams{OwnerToken: token, LockNames: maintenance})
		assertNoError(t, err, "acquire")
		assertEqual(t, rows, int64(1), "acquire")

		held, err := q.IsLockHeld(ctx, string(types.LockMaintenance))
		assertNoError(t, err, "is held")
		assertEqual(t, held, int32(1), "held after acquire")

		released, err := q.ReleaseLocksForToken(ctx, ReleaseLocksForTokenParams{
			LockNames:  maintenance,
			OwnerToken: token,
		})
		assertNoError(t, err, "release")
		assertEqual(t, len(released), 1, "release reports the lock")

		held, err = q.IsLockHeld(ctx, string(types.LockMaintenance))
		assertNoError(t, err, "is held after release")
		assertEqual(t, held, int32(0), "released")
	})

	t.Run("a held lock refuses another holder and a waited lock blocks", func(t *testing.T) {
		token := uuid.NullUUID{UUID: uuid.New(), Valid: true}
		rows, err := q.AcquireLocks(ctx, AcquireLocksParams{OwnerToken: token, LockNames: maintenance})
		assertNoError(t, err, "pre-acquire")
		assertEqual(t, rows, int64(1), "pre-acquire")

		rows, err = q.AcquireLocks(ctx, AcquireLocksParams{
			OwnerToken: uuid.NullUUID{UUID: uuid.New(), Valid: true},
			LockNames:  maintenance,
		})
		assertNoError(t, err, "contended acquire")
		assertEqual(t, rows, int64(0), "contended acquire changes nothing")

		held, err := q.CountHeldLocks(ctx, maintenance)
		assertNoError(t, err, "count held")
		assertEqual(t, held, int64(1), "the waited lock is held")

		if _, err := q.ReleaseLocksForToken(ctx, ReleaseLocksForTokenParams{
			LockNames:  maintenance,
			OwnerToken: token,
		}); err != nil {
			t.Fatalf("release: %v", err)
		}
	})

	t.Run("an aged holder is taken over", func(t *testing.T) {
		token := uuid.NullUUID{UUID: uuid.New(), Valid: true}
		rows, err := q.AcquireLocks(ctx, AcquireLocksParams{OwnerToken: token, LockNames: maintenance})
		assertNoError(t, err, "pre-acquire")
		assertEqual(t, rows, int64(1), "pre-acquire")

		if _, err := db.ExecContext(ctx,
			"UPDATE system_signal SET data = jsonb_set(data, '{started_at}', to_jsonb(NOW() - INTERVAL '31 minutes')) WHERE name = $1",
			string(types.LockMaintenance)); err != nil {
			t.Fatalf("age the lock: %v", err)
		}

		held, err := q.IsLockHeld(ctx, string(types.LockMaintenance))
		assertNoError(t, err, "is held")
		assertEqual(t, held, int32(0), "an aged lock is not held")

		successor := uuid.NullUUID{UUID: uuid.New(), Valid: true}
		rows, err = q.AcquireLocks(ctx, AcquireLocksParams{OwnerToken: successor, LockNames: maintenance})
		assertNoError(t, err, "takeover")
		assertEqual(t, rows, int64(1), "takeover acquires")

		if _, err := q.ReleaseLocksForToken(ctx, ReleaseLocksForTokenParams{
			LockNames:  maintenance,
			OwnerToken: successor,
		}); err != nil {
			t.Fatalf("release: %v", err)
		}
	})
}

package task

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

func heldLocks(t *testing.T, client *database.Client, names []types.LockName) int64 {
	t.Helper()
	count, err := client.Queries.CountHeldLocks(context.Background(), lockNameStrings(names))
	testutil.AssertNoError(t, err, "count held locks")
	return count
}

func TestAcquireLocks_VerifiesWaitsAndIsAllOrNothing(t *testing.T) {
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	maintenance := []types.LockName{types.LockMaintenance}
	storage := []types.LockName{types.LockStorage}
	both := []types.LockName{types.LockMaintenance, types.LockStorage}

	other := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	t.Cleanup(func() {
		_ = ReleaseLocksForToken(context.Background(), client.Queries, other)
		_ = ReleaseLocksForToken(context.Background(), client.Queries, uuid.NullUUID{})
	})

	t.Run("a held wait refuses the acquisition and holds nothing", func(t *testing.T) {
		if err := AcquireLocks(ctx, client, storage, nil, other, ""); err != nil {
			t.Fatalf("hold the waited lock: %v", err)
		}

		err := AcquireLocks(ctx, client, maintenance, storage, uuid.NullUUID{}, "")
		if !errors.Is(err, ErrLockBusy) {
			t.Fatalf("acquire with a held wait: want ErrLockBusy, got %v", err)
		}
		if held := heldLocks(t, client, maintenance); held != 0 {
			t.Fatalf("maintenance held=%d after a refused acquisition, want 0", held)
		}

		if err := ReleaseLocksForToken(ctx, client.Queries, other); err != nil {
			t.Fatalf("release the waited lock: %v", err)
		}
		if err := AcquireLocks(ctx, client, maintenance, storage, uuid.NullUUID{}, ""); err != nil {
			t.Fatalf("acquire after the wait freed: %v", err)
		}
		if held := heldLocks(t, client, maintenance); held != 1 {
			t.Fatalf("maintenance held=%d after acquisition, want 1", held)
		}
		if err := ReleaseLocksForToken(ctx, client.Queries, uuid.NullUUID{}); err != nil {
			t.Fatalf("release the acquired lock: %v", err)
		}
	})

	t.Run("a held lock refuses the whole set instead of holding part of it", func(t *testing.T) {
		if err := AcquireLocks(ctx, client, storage, nil, other, ""); err != nil {
			t.Fatalf("hold one of the set: %v", err)
		}

		err := AcquireLocks(ctx, client, both, nil, uuid.NullUUID{}, "")
		if !errors.Is(err, ErrLockBusy) {
			t.Fatalf("acquire both with one held: want ErrLockBusy, got %v", err)
		}
		if held := heldLocks(t, client, maintenance); held != 0 {
			t.Fatalf("maintenance held=%d after a partial-conflict acquisition, want 0", held)
		}

		if err := ReleaseLocksForToken(ctx, client.Queries, other); err != nil {
			t.Fatalf("release: %v", err)
		}
	})
}

func TestReleaseLocksForBatch_ReleasesChildHeldLock(t *testing.T) {
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	owner := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	storage := []types.LockName{types.LockStorage}

	if err := AcquireLocks(ctx, client, storage, nil, owner, "rlock-b1"); err != nil {
		t.Fatalf("child holds storage: %v", err)
	}

	if err := ReleaseLocksForBatch(ctx, client.Queries, "rlock-other"); err != nil {
		t.Fatalf("release for another batch: %v", err)
	}
	if held := heldLocks(t, client, storage); held != 1 {
		t.Fatalf("storage held=%d after another batch's release, want 1", held)
	}

	if err := ReleaseLocksForBatch(ctx, client.Queries, "rlock-b1"); err != nil {
		t.Fatalf("release for the holding batch: %v", err)
	}
	if held := heldLocks(t, client, storage); held != 0 {
		t.Fatalf("storage held=%d after its batch's release, want 0", held)
	}
}

func TestRunDirect_RunsBodyAndReleases(t *testing.T) {
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	policy := LockPolicy{Holds: []types.LockName{types.LockMaintenance}}

	ran := false
	err := RunDirect(ctx, client, log.NewDiscardLogger(), "test-run", policy, false, nil, func() error {
		ran = true
		if held := heldLocks(t, client, policy.Holds); held != 1 {
			t.Fatalf("held=%d inside the body, want the lock held", held)
		}
		return nil
	})
	testutil.AssertNoError(t, err, "RunDirect")
	if !ran {
		t.Fatal("body did not run")
	}
	if held := heldLocks(t, client, policy.Holds); held != 0 {
		t.Fatalf("held=%d after RunDirect, want the lock released", held)
	}
}

// TestRunDirect_ReleasesOnlyItsOwnHolds pins the manual-holder release scope: every direct run shares the
// NULL owner token, so a run may release only the names its own policy acquired — releasing every
// NULL-token lock would drop a concurrent manual run's lock and let a third process in while both still
// believe they hold theirs exclusively.
func TestRunDirect_ReleasesOnlyItsOwnHolds(t *testing.T) {
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	storage := []types.LockName{types.LockStorage}
	maintenance := []types.LockName{types.LockMaintenance}
	policy := LockPolicy{Holds: storage, WaitsFor: maintenance}
	t.Cleanup(func() { _ = ReleaseLocksForToken(context.Background(), client.Queries, uuid.NullUUID{}) })

	err := RunDirect(ctx, client, log.NewDiscardLogger(), "thumb-first", policy, false, nil, func() error {
		// a second manual run starts under the same NULL token while this one holds storage
		if err := AcquireLocks(ctx, client, maintenance, nil, uuid.NullUUID{}, ""); err != nil {
			t.Fatalf("the concurrent manual run must acquire maintenance: %v", err)
		}
		if held := heldLocks(t, client, policy.Holds); held != 1 {
			t.Fatalf("held=%d for our storage lock inside the body, want 1", held)
		}
		if held := heldLocks(t, client, maintenance); held != 1 {
			t.Fatalf("held=%d for the concurrent run's lock inside the body, want 1", held)
		}
		return nil
	})
	testutil.AssertNoError(t, err, "RunDirect")

	if held := heldLocks(t, client, storage); held != 0 {
		t.Fatalf("held=%d after our release, want 0 (our storage lock released)", held)
	}
	if held := heldLocks(t, client, maintenance); held != 1 {
		t.Fatalf("held=%d after our release, want 1 (the concurrent run's lock must survive)", held)
	}
}

func TestRunDirect_NoWaitRefusesBusyLock(t *testing.T) {
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	policy := LockPolicy{Holds: []types.LockName{types.LockMaintenance}}

	other := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	if err := AcquireLocks(ctx, client, policy.Holds, nil, other, ""); err != nil {
		t.Fatalf("hold the lock: %v", err)
	}
	t.Cleanup(func() { _ = ReleaseLocksForToken(context.Background(), client.Queries, other) })

	ran := false
	err := RunDirect(ctx, client, log.NewDiscardLogger(), "test-busy", policy, false, nil, func() error {
		ran = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "a required lock is held") {
		t.Fatalf("RunDirect with a busy lock: want a busy-lock error, got %v", err)
	}
	if ran {
		t.Fatal("body must not run when --no-wait finds the lock busy")
	}
	if held := heldLocks(t, client, policy.Holds); held != 1 {
		t.Fatalf("held=%d after a refused run, want the other holder's lock intact", held)
	}
}

func TestRunDirect_NoWaitRefusesInFlightWork(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	policy := LockPolicy{Holds: []types.LockName{types.LockMaintenance}}

	if _, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Consume, "",
		json.RawMessage(`{}`), "run-direct-flight", types.Task.Status.Processing, ""); err != nil {
		t.Fatalf("create processing task: %v", err)
	}

	ran := false
	err := RunDirect(ctx, client, log.NewDiscardLogger(), "test-drain", policy, false, nil, func() error {
		ran = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "conflict with the run") {
		t.Fatalf("RunDirect with in-flight work: want a conflict error, got %v", err)
	}
	if ran {
		t.Fatal("body must not run when --no-wait finds in-flight work")
	}
	if held := heldLocks(t, client, policy.Holds); held != 0 {
		t.Fatalf("held=%d after a refused run, want the lock released", held)
	}
}

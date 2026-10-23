package task

import (
	"context"
	"database/sql"
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

func TestStoreClaimBlocking_VerifiesWaitedStorage(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	createBackupTask(t, store, "bk-waits-storage")

	other := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	t.Cleanup(func() { _ = ReleaseLocksForToken(context.Background(), client.Queries, other) })
	if err := AcquireLocks(ctx, client, []types.LockName{types.LockStorage}, nil, other, ""); err != nil {
		t.Fatalf("hold storage: %v", err)
	}

	_, _, err := store.ClaimNextPending(ctx, types.Task.Type.Backup)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("claim while storage is held: want sql.ErrNoRows (idle deferral), got %v", err)
	}
	if lockHeld(t, client) {
		t.Fatal("a deferred claim must not hold the maintenance lock")
	}

	if err := ReleaseLocksForToken(ctx, client.Queries, other); err != nil {
		t.Fatalf("release storage: %v", err)
	}

	_, token, err := store.ClaimNextPending(ctx, types.Task.Type.Backup)
	testutil.AssertNoError(t, err, "claim after storage freed")
	if !lockHeld(t, client) {
		t.Fatal("the claim must acquire maintenance once storage is free")
	}
	if err := ReleaseLocksForToken(ctx, client.Queries, uuid.NullUUID{UUID: token, Valid: true}); err != nil {
		t.Fatalf("release the claim token: %v", err)
	}
}

func TestStoreClaimBlocking_BatchHoldsAcquiredSkipsAcquisition(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()

	if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID: "bf-batch-1", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Processing,
	}); err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if _, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.ThumbBackfill, "bf-batch-1",
		json.RawMessage(`{}`), "bf-1", "", ""); err != nil {
		t.Fatalf("create thumbbackfill task: %v", err)
	}

	childToken := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	if err := AcquireLocks(ctx, client, []types.LockName{types.LockStorage}, nil, childToken, "bf-batch-1"); err != nil {
		t.Fatalf("child acquires storage: %v", err)
	}

	store.SetBatchHoldsAcquired(true)
	_, token, err := store.ClaimNextPending(ctx, types.Task.Type.ThumbBackfill)
	testutil.AssertNoError(t, err, "claim under the batch hold")

	released, err := client.Queries.ReleaseLocksForToken(ctx, database.ReleaseLocksForTokenParams{
		LockNames:  types.LockNameValues(),
		OwnerToken: uuid.NullUUID{UUID: token, Valid: true},
	})
	testutil.AssertNoError(t, err, "release by claim token")
	if len(released) != 0 {
		t.Fatalf("claim token released %v, want none (the child holds the lock)", released)
	}
	if held := heldLocks(t, client, []types.LockName{types.LockStorage}); held != 1 {
		t.Fatalf("storage held=%d, want the child's lock intact", held)
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

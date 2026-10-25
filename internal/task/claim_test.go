package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// setupClaimTest seeds a queued thumbbackfill batch (holds storage, waits maintenance) and frees
// both resource locks so every case starts from a known lock state.
func setupClaimTest(t *testing.T, batchID string) *database.Client {
	t.Helper()
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	if _, err := client.DB().ExecContext(ctx, `UPDATE system_signal SET value = 'false', data = NULL`); err != nil {
		t.Fatalf("free resource locks: %v", err)
	}
	t.Cleanup(func() {
		_ = ReleaseLocksForBatch(context.Background(), client.Queries, batchID)
	})
	if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID: batchID, Origin: types.Batch.Origin.Schedule, Status: types.Batch.Status.Queued,
	}); err != nil {
		t.Fatalf("create batch: %v", err)
	}
	payload := json.RawMessage(`{}`)
	if _, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID: batchID + "-t", TaskType: types.Task.Type.ThumbBackfill,
		Status: types.Task.Status.Pending, Payload: &payload,
		BatchID: sql.NullString{String: batchID, Valid: true},
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return client
}

// TestClaimBatch pins the atomic claim: one transaction takes status, the pid-0 lease and the
// batch's holds together, and any obstruction leaves the batch queued with nothing acquired.
func TestClaimBatch(t *testing.T) {
	holds := []types.LockName{types.LockStorage}
	waits := []types.LockName{types.LockMaintenance}

	t.Run("one claim wins and the loser's transaction holds nothing", func(t *testing.T) {
		client := setupClaimTest(t, "claim-win")
		ctx := context.Background()
		token := uuid.New()

		if err := ClaimBatch(ctx, client, ClaimRequest{BatchID: "claim-win", ExecToken: token, Holds: holds, Waits: waits}); err != nil {
			t.Fatalf("claim: %v", err)
		}
		batch, err := client.Queries.GetBatch(ctx, "claim-win")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Processing, "status flipped")
		owner, err := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
			BatchID: "claim-win", StaleAfterSeconds: LeaseStaleSeconds,
		})
		testutil.AssertNoError(t, err, "read lease")
		testutil.AssertEqual(t, owner.OwnerID, token.String(), "lease carries the execution token")
		testutil.AssertEqual(t, owner.Pid, int64(0), "lease stays unadopted until the child adopts")
		if held := heldLocks(t, client, holds); held != 1 {
			t.Fatalf("storage held=%d after claim, want 1", held)
		}

		loser := uuid.New()
		err = ClaimBatch(ctx, client, ClaimRequest{BatchID: "claim-win", ExecToken: loser, Holds: holds, Waits: waits})
		if !errors.Is(err, ErrBatchNotQueued) {
			t.Fatalf("second claim: want ErrBatchNotQueued, got %v", err)
		}
		owner, err = client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
			BatchID: "claim-win", StaleAfterSeconds: LeaseStaleSeconds,
		})
		testutil.AssertNoError(t, err, "read lease after loser")
		testutil.AssertEqual(t, owner.OwnerID, token.String(), "loser's lease rolled back")
	})

	t.Run("a missing batch reports ErrBatchNotFound", func(t *testing.T) {
		client := setupClaimTest(t, "claim-missing-setup")
		err := ClaimBatch(context.Background(), client, ClaimRequest{BatchID: "claim-missing", ExecToken: uuid.New()})
		if !errors.Is(err, ErrBatchNotFound) {
			t.Fatalf("want ErrBatchNotFound, got %v", err)
		}
	})

	t.Run("a queued batch that already carries a lease refuses and stays queued", func(t *testing.T) {
		client := setupClaimTest(t, "claim-lease")
		ctx := context.Background()
		_, err := client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "claim-lease", OwnerID: "live-owner", Pid: 42,
		})
		testutil.AssertNoError(t, err, "insert live lease")

		err = ClaimBatch(ctx, client, ClaimRequest{BatchID: "claim-lease", ExecToken: uuid.New(), Holds: holds, Waits: waits})
		if !errors.Is(err, ErrBatchLocked) {
			t.Fatalf("want ErrBatchLocked, got %v", err)
		}
		batch, err := client.Queries.GetBatch(ctx, "claim-lease")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Queued, "refused claim must not flip the batch")
	})

	t.Run("a busy wait refuses the claim and leaves no lease or lock behind", func(t *testing.T) {
		client := setupClaimTest(t, "claim-busy")
		ctx := context.Background()
		maintenance := uuid.NullUUID{UUID: uuid.New(), Valid: true}
		t.Cleanup(func() {
			_ = ReleaseLocksForToken(context.Background(), client.Queries, maintenance)
		})
		if err := AcquireLocks(ctx, client, []types.LockName{types.LockMaintenance}, nil, maintenance, ""); err != nil {
			t.Fatalf("hold maintenance: %v", err)
		}

		token := uuid.New()
		err := ClaimBatch(ctx, client, ClaimRequest{BatchID: "claim-busy", ExecToken: token, Holds: holds, Waits: waits})
		if !errors.Is(err, ErrLockBusy) {
			t.Fatalf("want ErrLockBusy, got %v", err)
		}
		batch, batchErr := client.Queries.GetBatch(ctx, "claim-busy")
		testutil.AssertNoError(t, batchErr, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Queued, "refused claim must not flip the batch")
		if _, leaseErr := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
			BatchID: "claim-busy", StaleAfterSeconds: LeaseStaleSeconds,
		}); leaseErr == nil {
			t.Fatal("refused claim must not leave a lease row")
		}
		if held := heldLocks(t, client, holds); held != 0 {
			t.Fatalf("storage held=%d after refused claim, want 0", held)
		}
	})
}

// TestOwnerAdopt pins adoption as a one-time hand-off: only the claiming token may adopt, only
// while the lease is unadopted, and a released lease refuses adoption entirely.
func TestOwnerAdopt(t *testing.T) {
	client := setupClaimTest(t, "adopt-batch")
	ctx := context.Background()
	token := uuid.New()
	if err := ClaimBatch(ctx, client, ClaimRequest{
		BatchID: "adopt-batch", ExecToken: token,
		Holds: []types.LockName{types.LockStorage}, Waits: []types.LockName{types.LockMaintenance},
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}

	adopt := func(ownerToken string) error {
		owner := NewOwner(client, ownerToken, 4321)
		return owner.Adopt(ctx, "adopt-batch")
	}

	if err := adopt(uuid.New().String()); err == nil {
		t.Fatal("a foreign token must not adopt the lease")
	}

	if err := adopt(token.String()); err != nil {
		t.Fatalf("first adoption: %v", err)
	}
	owner, err := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
		BatchID: "adopt-batch", StaleAfterSeconds: LeaseStaleSeconds,
	})
	testutil.AssertNoError(t, err, "read lease")
	testutil.AssertEqual(t, owner.Pid, int64(4321), "adoption records the child pid")

	if err := adopt(token.String()); err == nil {
		t.Fatal("adoption must be one-time: an adopted lease refuses a second adoption")
	}

	_, err = client.Queries.ReleaseBatchOwner(ctx, database.ReleaseBatchOwnerParams{
		BatchID: "adopt-batch", OwnerID: token.String(),
	})
	testutil.AssertNoError(t, err, "release lease")
	if err := adopt(token.String()); err == nil {
		t.Fatal("a released lease must refuse adoption")
	}
}

// TestFailUnstartedBatch pins the launch-failure terminal write: under the lease fence it cancels the
// tasks that never ran, releases the batch's locks and writes failed; a lease that changed hands is
// untouched.
func TestFailUnstartedBatch(t *testing.T) {
	ctx := context.Background()
	holds := []types.LockName{types.LockStorage}

	t.Run("cancels the never-run tasks, releases the locks and fails the batch", func(t *testing.T) {
		client := setupClaimTest(t, "fail-unstarted")
		token := uuid.New()
		if err := ClaimBatch(ctx, client, ClaimRequest{
			BatchID: "fail-unstarted", ExecToken: token,
			Holds: holds, Waits: []types.LockName{types.LockMaintenance},
		}); err != nil {
			t.Fatalf("claim: %v", err)
		}

		applied, err := FailUnstartedBatch(ctx, client, "fail-unstarted", token)
		testutil.AssertNoError(t, err, "fail unstarted")
		testutil.AssertEqual(t, applied, true, "the leased batch is failed")
		batch, err := client.Queries.GetBatch(ctx, "fail-unstarted")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Failed, "batch failed")
		task, err := client.Queries.GetTaskByTaskID(ctx, "fail-unstarted-t")
		testutil.AssertNoError(t, err, "read task")
		testutil.AssertEqual(t, task.Status, types.Task.Status.Cancelled, "never-run task cancelled")
		if _, err := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
			BatchID: "fail-unstarted", StaleAfterSeconds: LeaseStaleSeconds,
		}); err == nil {
			t.Fatal("the lease must be released")
		}
		if held := heldLocks(t, client, holds); held != 0 {
			t.Fatalf("storage held=%d after the failure write, want the claim's hold released", held)
		}
	})

	t.Run("a lease that changed hands is left alone", func(t *testing.T) {
		client := setupClaimTest(t, "fail-foreign")
		if err := ClaimBatch(ctx, client, ClaimRequest{
			BatchID: "fail-foreign", ExecToken: uuid.New(),
			Holds: holds, Waits: []types.LockName{types.LockMaintenance},
		}); err != nil {
			t.Fatalf("claim: %v", err)
		}

		applied, err := FailUnstartedBatch(ctx, client, "fail-foreign", uuid.New())
		testutil.AssertNoError(t, err, "foreign fail attempt")
		testutil.AssertEqual(t, applied, false, "a foreign token writes nothing")
		batch, err := client.Queries.GetBatch(ctx, "fail-foreign")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Processing, "batch untouched")
		task, err := client.Queries.GetTaskByTaskID(ctx, "fail-foreign-t")
		testutil.AssertNoError(t, err, "read task")
		testutil.AssertEqual(t, task.Status, types.Task.Status.Pending, "task untouched")
		if held := heldLocks(t, client, holds); held != 1 {
			t.Fatalf("storage held=%d, want the foreign claim's hold kept", held)
		}
	})
}

// TestResetProcessingTasksForClaimKeepsLocks pins the claim-path entry repair: unlike the takeover
// variant, it must not release the holds the claim acquired for the whole run.
func TestResetProcessingTasksForClaimKeepsLocks(t *testing.T) {
	client := setupClaimTest(t, "reset-claim")
	ctx := context.Background()
	holds := []types.LockName{types.LockStorage}
	token := uuid.New()
	if err := ClaimBatch(ctx, client, ClaimRequest{
		BatchID: "reset-claim", ExecToken: token,
		Holds: holds, Waits: []types.LockName{types.LockMaintenance},
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	payload := json.RawMessage(`{}`)
	if _, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "reset-claim-p", TaskType: types.Task.Type.Consume, Status: types.Task.Status.Processing,
		Payload: &payload, BatchID: sql.NullString{String: "reset-claim", Valid: true},
	}); err != nil {
		t.Fatalf("create processing task: %v", err)
	}

	if _, err := ResetProcessingTasksForClaim(ctx, client, "reset-claim", 3); err != nil {
		t.Fatalf("reset for claim: %v", err)
	}
	if held := heldLocks(t, client, holds); held != 1 {
		t.Fatalf("storage held=%d after claim-path reset, want the claim's hold kept", held)
	}
	task, err := client.Queries.GetTaskByTaskID(ctx, "reset-claim-p")
	testutil.AssertNoError(t, err, "read task")
	testutil.AssertEqual(t, task.Status, types.Task.Status.Pending, "processing task reset for the run")

	if _, err := ResetProcessingTasksByBatch(ctx, client, "reset-claim", 3); err != nil {
		t.Fatalf("takeover reset: %v", err)
	}
	if held := heldLocks(t, client, holds); held != 0 {
		t.Fatalf("storage held=%d after takeover reset, want its lock release kept", held)
	}
}

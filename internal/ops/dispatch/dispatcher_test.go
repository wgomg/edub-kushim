package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// waitFor polls cond until it holds and fails the test after the deadline.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func batchIsQueuedWithoutLease(t *testing.T, client *database.Client, batchID string) bool {
	t.Helper()
	batch, err := client.Queries.GetBatch(context.Background(), batchID)
	if err != nil || batch.Status != types.Batch.Status.Queued {
		return false
	}
	return !hasLeaseRow(context.Background(), t, client, batchID)
}

// TestDispatchHonorsBudgetAndSkipsUnknownTypes pins the two dispatch gates: a batch whose task
// types have no batch-mode CLI is never claimed, and a full slot budget stops the sweep before any
// claim.
func TestDispatchHonorsBudgetAndSkipsUnknownTypes(t *testing.T) {
	client, cfg := setupLauncherTest(t)
	ctx := context.Background()
	batchSvc := service.NewBatch(client, cfg.Consumer.Reclaim.MaxRetries)
	catalog := dispatchCatalog(t)
	logger := testutil.NewTestLogger()

	t.Run("a type without a batch-mode CLI stays queued", func(t *testing.T) {
		seedBatch(t, client, "dispatch-backup", types.Task.Type.Backup)
		d := NewDispatcher(catalog, logger, types.HostQueue, 4, func() {})
		testutil.AssertNoError(t, d.Dispatch(ctx, client, batchSvc), "dispatch over a backup-only queue")
		if !batchIsQueuedWithoutLease(t, client, "dispatch-backup") {
			t.Fatal("a backup batch must never be claimed by the dispatcher")
		}
	})

	t.Run("a full budget stops the sweep before any no-holds claim", func(t *testing.T) {
		d := NewDispatcher(catalog, logger, types.HostQueue, 1, func() {})
		seedBatch(t, client, "dispatch-running", types.Task.Type.Consume)
		if _, err := client.Queries.MarkBatchClaimed(ctx, "dispatch-running"); err != nil {
			t.Fatalf("mark the filler claimed: %v", err)
		}
		if _, err := client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "dispatch-running", OwnerID: uuid.New().String(), Pid: 999999999,
			DispatcherID: uuid.NullUUID{UUID: d.dispatcherID, Valid: true},
		}); err != nil {
			t.Fatalf("charge the slot to this dispatcher: %v", err)
		}
		seedBatch(t, client, "dispatch-waiting", types.Task.Type.Consume)
		testutil.AssertNoError(t, d.Dispatch(ctx, client, batchSvc), "dispatch at budget")

		if !batchIsQueuedWithoutLease(t, client, "dispatch-waiting") {
			t.Fatal("the one slot is taken: the waiting batch must stay queued")
		}
		batch, err := client.Queries.GetBatch(ctx, "dispatch-running")
		testutil.AssertNoError(t, err, "read running batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Processing, "the occupying batch keeps running")
	})
}

// TestDispatchClaimsHoldingBatchOutsideTheBudget pins decision #25: a lock-holding batch does not consume
// the slot budget and is never gated by it. With the budget full and the ingest candidate older, the
// sweep must skip the no-holds candidate and still claim the holding one.
func TestDispatchClaimsHoldingBatchOutsideTheBudget(t *testing.T) {
	client, cfg := setupLauncherTest(t)
	ctx := context.Background()
	batchSvc := service.NewBatch(client, cfg.Consumer.Reclaim.MaxRetries)
	catalog := dispatchCatalog(t)
	t.Setenv(testChildExitEnv, "7")

	d := NewDispatcher(catalog, testutil.NewTestLogger(), types.HostQueue, 1, func() {})
	seedBatch(t, client, "bypass-filler", types.Task.Type.Consume)
	if _, err := client.Queries.MarkBatchClaimed(ctx, "bypass-filler"); err != nil {
		t.Fatalf("mark the filler claimed: %v", err)
	}
	if _, err := client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
		BatchID: "bypass-filler", OwnerID: uuid.New().String(), Pid: 999999999,
		DispatcherID: uuid.NullUUID{UUID: d.dispatcherID, Valid: true},
	}); err != nil {
		t.Fatalf("charge the slot to this dispatcher: %v", err)
	}
	seedBatch(t, client, "bypass-ingest", types.Task.Type.Consume)
	seedBatch(t, client, "bypass-holding", types.Task.Type.ThumbBackfill)

	testutil.AssertNoError(t, d.Dispatch(ctx, client, batchSvc), "dispatch over a full budget")

	waitFor(t, "the holding batch to be claimed and its failed launch requeued", func() bool {
		return batchIsQueuedWithoutLease(t, client, "bypass-holding") &&
			d.failures.cooling("bypass-holding", time.Now())
	})
	if held, err := client.Queries.IsLockHeld(ctx, string(types.LockStorage)); err != nil || held != 0 {
		t.Fatalf("storage held=%d after the requeue, want the launch failure to release it (%v)", held, err)
	}
	if !batchIsQueuedWithoutLease(t, client, "bypass-ingest") {
		t.Fatal("a no-holds batch must stay queued while the budget is full")
	}
}

// TestDispatchBoundsLaunchFailures walks the launch-failure policy end to end: every wake claims
// the oldest claimable batch and forks it, the forked child dies before adopting, the lease and
// locks are released and the batch is requeued to cool down, and the third consecutive failure
// fails the batch instead of looping. The wake is driven manually so each claim happens after the
// recorded backoff window, exactly like the daemon's safety poll.
func TestDispatchBoundsLaunchFailures(t *testing.T) {
	client, cfg := setupLauncherTest(t)
	ctx := context.Background()
	batchSvc := service.NewBatch(client, cfg.Consumer.Reclaim.MaxRetries)
	catalog := dispatchCatalog(t)
	t.Setenv(testChildExitEnv, "7")

	const batchID = "dispatch-failing"
	seedBatch(t, client, batchID, types.Task.Type.Consume)
	seedBatch(t, client, "dispatch-untouchable", types.Task.Type.Backup)

	d := NewDispatcher(catalog, testutil.NewTestLogger(), types.HostQueue, 1, func() {})
	waitRequeued := func(what string) {
		waitFor(t, what, func() bool { return batchIsQueuedWithoutLease(t, client, batchID) })
	}
	waitCooled := func() {
		waitFor(t, "the backoff window to expire", func() bool {
			return !d.failures.cooling(batchID, time.Now())
		})
	}

	for attempt := 1; attempt <= maxLaunchFailures; attempt++ {
		testutil.AssertNoError(t, d.Dispatch(ctx, client, batchSvc), "dispatch attempt")
		if attempt == maxLaunchFailures {
			break
		}
		waitRequeued("the failed launch to be requeued")
		waitCooled()
	}

	waitFor(t, "the batch to fail after the bounded retries", func() bool {
		batch, err := client.Queries.GetBatch(ctx, batchID)
		if err != nil || batch.Status != types.Batch.Status.Failed {
			return false
		}
		queuedTask, err := client.Queries.GetTaskByTaskID(ctx, batchID+"-t")
		return err == nil && queuedTask.Status == types.Task.Status.Cancelled
	})
	if hasLeaseRow(ctx, t, client, batchID) {
		t.Fatal("the failed batch must not keep a lease")
	}
	if !batchIsQueuedWithoutLease(t, client, "dispatch-untouchable") {
		t.Fatal("an undispatchable batch must stay queued through every wake")
	}
}

// TestChildExitedClassification pins what counts as a launch failure: a child that adopted the
// lease owns its outcome, a foreign token means the batch moved on, and a clean exit forgets the
// failure streak.
func TestChildExitedClassification(t *testing.T) {
	client, _ := setupLauncherTest(t)
	ctx := context.Background()
	catalog := dispatchCatalog(t)
	d := NewDispatcher(catalog, testutil.NewTestLogger(), types.HostQueue, 4, func() {})
	exitErr := errors.New("exit status 7")

	t.Run("an adopted lease keeps its batch running", func(t *testing.T) {
		token := uuid.New()
		seedClaimedBatch(t, client, "exit-adopted", token.String(), 4321, types.Task.Type.Consume)
		d.childExited(client, "exit-adopted", token, exitErr)

		batch, err := client.Queries.GetBatch(ctx, "exit-adopted")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Processing, "the adopted child owns its outcome")
		owner, ownerErr := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
			BatchID: "exit-adopted", StaleAfterSeconds: task.LeaseStaleSeconds,
		})
		testutil.AssertNoError(t, ownerErr, "lease still present")
		testutil.AssertEqual(t, owner.Pid, int64(4321), "adopted pid kept")
	})

	t.Run("a foreign token means the batch moved on", func(t *testing.T) {
		seedClaimedBatch(t, client, "exit-foreign", uuid.New().String(), 0, types.Task.Type.Consume)
		d.childExited(client, "exit-foreign", uuid.New(), exitErr)

		batch, err := client.Queries.GetBatch(ctx, "exit-foreign")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Processing, "no teardown for someone else's lease")
		if !hasLeaseRow(ctx, t, client, "exit-foreign") {
			t.Fatal("the foreign lease must be kept")
		}
	})

	t.Run("a clean exit forgets the streak", func(t *testing.T) {
		d.failures.record("exit-clean")
		if !d.failures.cooling("exit-clean", time.Now()) {
			t.Fatal("precondition: the batch is cooling")
		}
		d.childExited(client, "exit-clean", uuid.New(), nil)
		if d.failures.cooling("exit-clean", time.Now()) {
			t.Fatal("a launch that ran must clear the failure streak")
		}
	})
}

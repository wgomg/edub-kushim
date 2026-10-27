package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// testStoreOwner is the execution identity the task-package test stores claim under; batches must carry its
// lease for their tasks to be claimable.
const testStoreOwner = "00000000-0000-0000-0000-000000000001"

func createBatchRow(ctx context.Context, t *testing.T, q *database.Queries, id string, status types.BatchStatus) {
	t.Helper()
	err := q.CreateBatch(ctx, database.CreateBatchParams{ID: id, Origin: types.Batch.Origin.CLI, Status: status})
	testutil.AssertNoError(t, err, "create batch "+id)
}

// leasedBatch creates a batch and its lease under the store's owner: the state every claim requires now that
// the batch claim owns the locks and the task claim only runs under an owned batch.
func leasedBatch(ctx context.Context, t *testing.T, q *database.Queries, id string, status types.BatchStatus) {
	t.Helper()
	createBatchRow(ctx, t, q, id, status)
	insertLease(ctx, t, q, id, testStoreOwner)
}

func batchStatus(ctx context.Context, t *testing.T, q *database.Queries, id string) types.BatchStatus {
	t.Helper()
	b, err := q.GetBatch(ctx, id)
	testutil.AssertNoError(t, err, "read batch "+id)
	return b.Status
}

// insertLease creates a fresh ownership row; Pid 999999999 exceeds pid_max so no process can be signalled.
func insertLease(ctx context.Context, t *testing.T, q *database.Queries, batchID, ownerID string) {
	t.Helper()
	_, err := q.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
		BatchID: batchID, OwnerID: ownerID, Pid: 999999999,
	})
	testutil.AssertNoError(t, err, "insert lease for "+batchID)
}

func hasLease(ctx context.Context, t *testing.T, q *database.Queries, batchID string) bool {
	t.Helper()
	_, err := q.GetBatchOwner(ctx, database.GetBatchOwnerParams{
		BatchID: batchID, StaleAfterSeconds: LeaseStaleSeconds,
	})
	return err == nil
}

// TestSettleBatch pins the single terminal writer: only it may move a
// processing batch to a terminal status, and its guards decide every write.
func TestSettleBatch(t *testing.T) {
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	logger := testutil.NewTestLogger()
	q := client.Queries

	t.Run("settles completed when every task is terminal", func(t *testing.T) {
		createBatchRow(ctx, t, q, "settle-done", types.Batch.Status.Processing)
		createTaskRow(ctx, t, q, types.Task.Type.Consume, "settle-done", json.RawMessage(`{}`), "", types.Task.Status.Completed, "")
		createTaskRow(ctx, t, q, types.Task.Type.Consume, "settle-done", json.RawMessage(`{}`), "", types.Task.Status.Cancelled, "")

		status, err := SettleBatch(ctx, client, logger, Execution{BatchID: "settle-done"})
		testutil.AssertNoError(t, err, "settle")
		testutil.AssertEqual(t, status, types.Batch.Status.Completed, "settled status")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "settle-done"), types.Batch.Status.Completed, "batch row")
	})

	t.Run("settles failed when any task failed", func(t *testing.T) {
		createBatchRow(ctx, t, q, "settle-fail", types.Batch.Status.Processing)
		createTaskRow(ctx, t, q, types.Task.Type.Consume, "settle-fail", json.RawMessage(`{}`), "", types.Task.Status.Completed, "")
		createTaskRow(ctx, t, q, types.Task.Type.Consume, "settle-fail", json.RawMessage(`{}`), "", types.Task.Status.Failed, "")

		status, err := SettleBatch(ctx, client, logger, Execution{BatchID: "settle-fail"})
		testutil.AssertNoError(t, err, "settle")
		testutil.AssertEqual(t, status, types.Batch.Status.Failed, "settled status")
	})

	t.Run("refuses while any task is still active", func(t *testing.T) {
		createBatchRow(ctx, t, q, "settle-active", types.Batch.Status.Processing)
		createTaskRow(ctx, t, q, types.Task.Type.Consume, "settle-active", json.RawMessage(`{}`), "", types.Task.Status.Completed, "")
		createTaskRow(ctx, t, q, types.Task.Type.Consume, "settle-active", json.RawMessage(`{}`), "", types.Task.Status.Pending, "")

		status, err := SettleBatch(ctx, client, logger, Execution{BatchID: "settle-active"})
		testutil.AssertNoError(t, err, "refusal is not an error")
		testutil.AssertEqual(t, status, types.Batch.Status.Processing, "returned status")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "settle-active"), types.Batch.Status.Processing, "batch untouched")
	})

	t.Run("refuses a lease that belongs to another execution", func(t *testing.T) {
		createBatchRow(ctx, t, q, "settle-foreign", types.Batch.Status.Processing)
		insertLease(ctx, t, q, "settle-foreign", "other-owner")

		status, err := SettleBatch(ctx, client, logger, Execution{BatchID: "settle-foreign", Guard: uuid.New(), Leased: true})
		testutil.AssertNoError(t, err, "refusal is not an error")
		testutil.AssertEqual(t, status, types.Batch.Status.Processing, "returned status")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "settle-foreign"), types.Batch.Status.Processing, "batch untouched")
	})

	t.Run("keeps a terminal batch immutable", func(t *testing.T) {
		createBatchRow(ctx, t, q, "settle-immutable", types.Batch.Status.Completed)
		createTaskRow(ctx, t, q, types.Task.Type.Consume, "settle-immutable", json.RawMessage(`{}`), "", types.Task.Status.Failed, "")

		status, err := SettleBatch(ctx, client, logger, Execution{BatchID: "settle-immutable"})
		testutil.AssertNoError(t, err, "idempotent settle")
		testutil.AssertEqual(t, status, types.Batch.Status.Completed, "returned status")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "settle-immutable"), types.Batch.Status.Completed, "failed task must not rewrite a settled batch")
	})
}

// pauseRequestHandler reports a credit error the way the enricher does: the
// task fails and the whole batch must pause instead of settling.
type pauseRequestHandler struct{ handled int }

func (h *pauseRequestHandler) Handle(ctx context.Context, _ Task) (json.RawMessage, error) {
	h.handled++
	return nil, &Error{ReqID: "req-credit", Err: errMockFail, PauseBatch: true}
}

// TestRunnerPauseRequestPausesInsteadOfSettling contrasts the two terminal
// outcomes of a failing handler: a plain error settles the batch failed, while
// a requested pause must land first or the guarded pause would refuse a
// batch that already went terminal.
func TestRunnerPauseRequestPausesInsteadOfSettling(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testSuccessorCatalog(t), testutil.NewTestLogger())
	ctx := context.Background()
	q := store.client.Queries

	t.Run("plain handler failure settles the batch failed", func(t *testing.T) {
		handler := newMockHandler()
		handler.fail = true
		registry.Register(types.Task.Type.Consume, handler)

		createBatchRow(ctx, t, q, "runner-fail-batch", types.Batch.Status.Queued)
		insertLease(ctx, t, q, "runner-fail-batch", testStoreOwner)
		createTaskRow(ctx, t, q, types.Task.Type.Consume, "runner-fail-batch", json.RawMessage(`{}`), "", "", "")

		testutil.AssertNoError(t, runner.Next(ctx, types.Task.Type.Consume), "run")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "runner-fail-batch"), types.Batch.Status.Failed, "batch settles failed")
	})

	t.Run("pause request pauses the batch instead of settling it", func(t *testing.T) {
		handler := &pauseRequestHandler{}
		registry.Register(types.Task.Type.Enrich, handler)

		createBatchRow(ctx, t, q, "runner-pause-batch", types.Batch.Status.Queued)
		insertLease(ctx, t, q, "runner-pause-batch", testStoreOwner)
		taskID, err := createTaskRow(ctx, t, q, types.Task.Type.Enrich, "runner-pause-batch", json.RawMessage(`{}`), "", "", "")
		testutil.AssertNoError(t, err, "create enrich task")

		testutil.AssertNoError(t, runner.Next(ctx, types.Task.Type.Enrich), "run")
		testutil.AssertEqual(t, handler.handled, 1, "handler called")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "runner-pause-batch"), types.Batch.Status.Paused, "batch pauses, not fails")

		row, err := q.GetTaskByTaskID(ctx, taskID)
		testutil.AssertNoError(t, err, "read task")
		testutil.AssertEqual(t, string(row.Status), string(types.Task.Status.Failed), "the failing task is still recorded")
	})
}

// TestPauseBatchAuthorization pins the pause guard: only the execution that still owns the lease may pause.
func TestPauseBatchAuthorization(t *testing.T) {
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	logger := testutil.NewTestLogger()
	q := client.Queries

	t.Run("lease holder pauses and releases its lease", func(t *testing.T) {
		createBatchRow(ctx, t, q, "pause-lease", types.Batch.Status.Processing)
		ownerGuard := uuid.New()
		insertLease(ctx, t, q, "pause-lease", ownerGuard.String())

		testutil.AssertNoError(t, PauseBatch(ctx, client, logger, Execution{BatchID: "pause-lease", Guard: ownerGuard, Leased: true}), "pause")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "pause-lease"), types.Batch.Status.Paused, "batch paused")
		if hasLease(ctx, t, q, "pause-lease") {
			t.Fatal("pause must release the lease it owns")
		}
	})

	t.Run("stale leased execution cannot pause the new owner's batch", func(t *testing.T) {
		createBatchRow(ctx, t, q, "pause-lease-foreign", types.Batch.Status.Processing)
		insertLease(ctx, t, q, "pause-lease-foreign", "lease-owner-b")

		testutil.AssertNoError(t, PauseBatch(ctx, client, logger, Execution{BatchID: "pause-lease-foreign", Guard: uuid.New(), Leased: true}), "refusal is not an error")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "pause-lease-foreign"), types.Batch.Status.Processing, "batch untouched")
		if !hasLease(ctx, t, q, "pause-lease-foreign") {
			t.Fatal("a refused pause must leave the current lease in place")
		}
	})
}

// TestClaimNextPendingSkipsUnclaimableBatch guards against head-of-line
// stalls: a pending task whose batch is not owned by this execution (a terminal
// batch carries no lease) must neither be claimed nor block the next claimable
// task of the same type.
func TestClaimNextPendingSkipsUnclaimableBatch(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	q := client.Queries

	createBatchRow(ctx, t, q, "claim-old-failed", types.Batch.Status.Failed)
	leasedBatch(ctx, t, q, "claim-new-queued", types.Batch.Status.Queued)

	olderID, err := createTaskRow(ctx, t, q, types.Task.Type.Consume, "claim-old-failed", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create older task")
	if _, err := client.DB().ExecContext(ctx,
		"UPDATE task SET created_at = created_at - interval '10 minutes' WHERE task_id = $1", olderID); err != nil {
		t.Fatalf("backdate older task: %v", err)
	}
	newerID, err := createTaskRow(ctx, t, q, types.Task.Type.Consume, "claim-new-queued", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create newer task")

	claimed, _, err := store.ClaimNextPending(ctx, types.Task.Type.Consume)
	testutil.AssertNoError(t, err, "claim skips the terminal batch")
	testutil.AssertEqual(t, claimed.TaskID, newerID, "claimable task wins over the older unclaimable one")
	testutil.AssertEqual(t, batchStatus(ctx, t, q, "claim-new-queued"), types.Batch.Status.Processing, "claim flips the claimed batch")
	testutil.AssertEqual(t, batchStatus(ctx, t, q, "claim-old-failed"), types.Batch.Status.Failed, "unclaimable batch untouched")

	_, err = createTaskRow(ctx, t, q, types.Task.Type.Consume, "claim-old-failed", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create second unclaimable task")
	_, _, err = store.ClaimNextPending(ctx, types.Task.Type.Consume)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("claim with only unclaimable work: err = %v, want sql.ErrNoRows", err)
	}
}

// TestResumeBatch pins the resume contract: the retried consumes and their
// restored children become visible atomically with the requeue, and the
// guarded refusals leave the batch untouched.
func TestResumeBatch(t *testing.T) {
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	logger := testutil.NewTestLogger()
	q := client.Queries

	t.Run("retries failed tasks and restores their successors in one call", func(t *testing.T) {
		createBatchRow(ctx, t, q, "resume-retry", types.Batch.Status.Failed)

		consumeID := uuid.New().String()
		childID := createSuccessorRow(t, q, consumeID, types.Task.Type.Enrich, json.RawMessage(`{}`), types.Task.Status.Discarded)
		_, err := createTaskRow(ctx, t, q, types.Task.Type.Consume, "resume-retry", json.RawMessage(`{}`), consumeID, types.Task.Status.Failed, "")
		testutil.AssertNoError(t, err, "create failed consume")

		result, err := ResumeBatch(ctx, client, logger, "resume-retry", 3, true)
		testutil.AssertNoError(t, err, "resume")
		testutil.AssertEqual(t, result.Resumed, true, "resumed")
		testutil.AssertEqual(t, result.FailedRetried, int64(1), "failed retried")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "resume-retry"), types.Batch.Status.Queued, "requeued")

		child, err := q.GetTaskByTaskID(ctx, childID)
		testutil.AssertNoError(t, err, "read child")
		testutil.AssertEqual(t, string(child.Status), string(types.Task.Status.Waiting), "child restored before the batch is visible as queued")
	})

	t.Run("refuses a batch held by a live owner", func(t *testing.T) {
		createBatchRow(ctx, t, q, "resume-locked", types.Batch.Status.Failed)
		insertLease(ctx, t, q, "resume-locked", "resume-owner")

		_, err := ResumeBatch(ctx, client, logger, "resume-locked", 3, false)
		if !errors.Is(err, ErrBatchLocked) {
			t.Fatalf("err = %v, want ErrBatchLocked", err)
		}
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "resume-locked"), types.Batch.Status.Failed, "batch untouched")
	})

	t.Run("refuses a batch that is still processing", func(t *testing.T) {
		createBatchRow(ctx, t, q, "resume-running", types.Batch.Status.Processing)

		_, err := ResumeBatch(ctx, client, logger, "resume-running", 3, false)
		if !errors.Is(err, ErrBatchNotResumable) {
			t.Fatalf("err = %v, want ErrBatchNotResumable", err)
		}
	})

	t.Run("reports nothing to resume when no task can become active", func(t *testing.T) {
		createBatchRow(ctx, t, q, "resume-nothing", types.Batch.Status.Failed)
		createTaskRow(ctx, t, q, types.Task.Type.Consume, "resume-nothing", json.RawMessage(`{}`), "", types.Task.Status.Cancelled, "")

		result, err := ResumeBatch(ctx, client, logger, "resume-nothing", 3, false)
		testutil.AssertNoError(t, err, "resume")
		testutil.AssertEqual(t, result.Resumed, false, "nothing to resume")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "resume-nothing"), types.Batch.Status.Failed, "batch stays failed")
	})
}

// TestSuccessorActivation covers the completion and failure contract of waiting successors: a completed
// parent activates them under their derived keys and merges the fields they declare, a duplicate key
// discards the successor with its branch instead of failing the parent, an unplannable successor is
// discarded with its reason, a failed parent discards the whole branch, a completion that lost its claim
// arms nothing, and a malformed parent result only fails the plan of a successor that declares inheritance.
func TestSuccessorActivation(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	q := client.Queries
	catalog := testSuccessorCatalog(t)

	t.Run("activates successors under their keys and merges inherited fields", func(t *testing.T) {
		parent, token := claimParentInBatch(ctx, t, store, q, "succ-activate", types.Task.Type.Consume, json.RawMessage(`{}`))
		enrichID := createSuccessorRow(t, q, parent.TaskID, types.Task.Type.Enrich, json.RawMessage(`{"document_id":"doc-a"}`), types.Task.Status.Waiting)
		thumbnailID := createSuccessorRow(t, q, parent.TaskID, types.Task.Type.Thumbnail,
			json.RawMessage(`{"document_id":"doc-a","inherit":["storage_path"]}`), types.Task.Status.Waiting)

		outcomes := completeWithSuccessors(t, store, catalog, parent, token, json.RawMessage(`{"storage_path":"/store/a.pdf"}`))
		testutil.AssertEqual(t, len(outcomes), 2, "one outcome per successor")
		for _, outcome := range outcomes {
			testutil.AssertEqual(t, outcome.Activation, Activated, "successor activated")
		}

		enrich, err := q.GetTaskByTaskID(ctx, enrichID)
		testutil.AssertNoError(t, err, "read enrich")
		testutil.AssertEqual(t, string(enrich.Status), string(types.Task.Status.Pending), "enrich pending")
		if !enrich.DedupKey.Valid || enrich.DedupKey.String != "enrich:doc:doc-a" {
			t.Fatalf("enrich dedup_key = %v, want enrich:doc:doc-a", enrich.DedupKey)
		}

		thumbnail, err := q.GetTaskByTaskID(ctx, thumbnailID)
		testutil.AssertNoError(t, err, "read thumbnail")
		testutil.AssertEqual(t, string(thumbnail.Status), string(types.Task.Status.Pending), "thumbnail pending")
		if !thumbnail.DedupKey.Valid || thumbnail.DedupKey.String != "thumbnail:doc:doc-a" {
			t.Fatalf("thumbnail dedup_key = %v, want thumbnail:doc:doc-a", thumbnail.DedupKey)
		}
		var thumbnailPayload map[string]any
		testutil.AssertNoError(t, json.Unmarshal(*thumbnail.Payload, &thumbnailPayload), "parse thumbnail payload")
		testutil.AssertEqual(t, thumbnailPayload["storage_path"], "/store/a.pdf", "storage path inherited")
	})

	t.Run("discards a duplicate and its branch instead of failing the parent", func(t *testing.T) {
		_, err := createTaskRow(ctx, t, q, types.Task.Type.Enrich, "", json.RawMessage(`{}`), "", types.Task.Status.Pending, "enrich:doc:doc-dup")
		testutil.AssertNoError(t, err, "create live duplicate")

		parent, token := claimParentInBatch(ctx, t, store, q, "succ-duplicate", types.Task.Type.Consume, json.RawMessage(`{}`))
		childID := createSuccessorRow(t, q, parent.TaskID, types.Task.Type.Enrich, json.RawMessage(`{"document_id":"doc-dup"}`), types.Task.Status.Waiting)
		grandchildID := createSuccessorRow(t, q, childID, types.Task.Type.TagIdentity, json.RawMessage(`{}`), types.Task.Status.Waiting)

		outcomes := completeWithSuccessors(t, store, catalog, parent, token, json.RawMessage(`{}`))
		testutil.AssertEqual(t, len(outcomes), 1, "one outcome for the direct successor")
		testutil.AssertEqual(t, outcomes[0].Activation, DiscardedDuplicate, "duplicate discarded")

		child, err := q.GetTaskByTaskID(ctx, childID)
		testutil.AssertNoError(t, err, "read child")
		testutil.AssertEqual(t, string(child.Status), string(types.Task.Status.Discarded), "duplicate child discarded")
		if !strings.Contains(child.Error.String, "duplicate of live work") {
			t.Fatalf("error = %q, want the duplicate reason", child.Error.String)
		}
		grandchild, err := q.GetTaskByTaskID(ctx, grandchildID)
		testutil.AssertNoError(t, err, "read grandchild")
		testutil.AssertEqual(t, string(grandchild.Status), string(types.Task.Status.Discarded), "awaiting branch discarded with it")

		completed, err := q.GetTask(ctx, parent.ID)
		testutil.AssertNoError(t, err, "read parent")
		testutil.AssertEqual(t, string(completed.Status), string(types.Task.Status.Completed), "parent completes despite the duplicate")
	})

	t.Run("discards an unplannable successor with its reason", func(t *testing.T) {
		parent, token := claimParentInBatch(ctx, t, store, q, "succ-unplannable", types.Task.Type.Consume, json.RawMessage(`{}`))
		childID := createSuccessorRow(t, q, parent.TaskID, types.Task.Type.Enrich, json.RawMessage(`{}`), types.Task.Status.Waiting)

		outcomes := completeWithSuccessors(t, store, catalog, parent, token, json.RawMessage(`{}`))
		testutil.AssertEqual(t, len(outcomes), 1, "one outcome")
		testutil.AssertEqual(t, outcomes[0].Activation, DiscardedInvalid, "unplannable child discarded")
		testutil.AssertError(t, outcomes[0].Err, "the reason travels with the outcome")

		child, err := q.GetTaskByTaskID(ctx, childID)
		testutil.AssertNoError(t, err, "read child")
		testutil.AssertEqual(t, string(child.Status), string(types.Task.Status.Discarded), "child discarded")
		if !strings.Contains(child.Error.String, "empty dedup key") {
			t.Fatalf("error = %q, want the empty-key reason", child.Error.String)
		}
		if child.DedupKey.Valid {
			t.Fatal("a discarded successor must not carry a key")
		}

		completed, err := q.GetTask(ctx, parent.ID)
		testutil.AssertNoError(t, err, "read parent")
		testutil.AssertEqual(t, string(completed.Status), string(types.Task.Status.Completed), "parent completes")
	})

	t.Run("a failed parent discards its whole waiting branch", func(t *testing.T) {
		parent, token := claimParentInBatch(ctx, t, store, q, "succ-cascade", types.Task.Type.Consume, json.RawMessage(`{}`))
		childID := createSuccessorRow(t, q, parent.TaskID, types.Task.Type.Enrich, json.RawMessage(`{"document_id":"doc-c"}`), types.Task.Status.Waiting)
		grandchildID := createSuccessorRow(t, q, childID, types.Task.Type.TagIdentity, json.RawMessage(`{}`), types.Task.Status.Waiting)

		rows, discarded, err := store.FailTask(ctx, parent.ID, parent.TaskID, "boom", token)
		testutil.AssertNoError(t, err, "fail")
		testutil.AssertEqual(t, rows, int64(1), "parent failed")
		testutil.AssertEqual(t, discarded, int64(2), "whole branch discarded")

		for _, id := range []string{childID, grandchildID} {
			row, err := q.GetTaskByTaskID(ctx, id)
			testutil.AssertNoError(t, err, "read successor "+id)
			testutil.AssertEqual(t, string(row.Status), string(types.Task.Status.Discarded), "successor discarded")
			testutil.AssertEqual(t, row.Error.String, "boom", "failure text copied")
		}
	})

	t.Run("a completion that lost its claim arms nothing", func(t *testing.T) {
		parent, _ := claimParentInBatch(ctx, t, store, q, "succ-refused", types.Task.Type.Consume, json.RawMessage(`{}`))
		childID := createSuccessorRow(t, q, parent.TaskID, types.Task.Type.Enrich, json.RawMessage(`{"document_id":"doc-r"}`), types.Task.Status.Waiting)

		children, err := store.WaitingSuccessors(ctx, parent.TaskID)
		testutil.AssertNoError(t, err, "read successors")
		plans := planSuccessors(children, json.RawMessage(`{}`), catalog)
		rows, outcomes, err := store.CompleteTask(ctx, parent.ID, json.RawMessage(`{}`), uuid.New(), plans)
		testutil.AssertNoError(t, err, "refused completion is not an error")
		testutil.AssertEqual(t, rows, int64(0), "completion refused")
		testutil.AssertEqual(t, len(outcomes), 0, "no successor activated")

		child, err := q.GetTaskByTaskID(ctx, childID)
		testutil.AssertNoError(t, err, "read child")
		testutil.AssertEqual(t, string(child.Status), string(types.Task.Status.Waiting), "child stays waiting")
	})

	t.Run("a malformed parent result only fails the plan of a successor that declares inheritance", func(t *testing.T) {
		enrichID := createSuccessorRow(t, q, "succ-malformed-parent", types.Task.Type.Enrich, json.RawMessage(`{"document_id":"doc-m"}`), types.Task.Status.Waiting)
		thumbnailID := createSuccessorRow(t, q, "succ-malformed-parent", types.Task.Type.Thumbnail,
			json.RawMessage(`{"document_id":"doc-m","inherit":["storage_path"]}`), types.Task.Status.Waiting)

		children, err := store.WaitingSuccessors(ctx, "succ-malformed-parent")
		testutil.AssertNoError(t, err, "read successors")
		plans := planSuccessors(children, json.RawMessage(`{"storage_path":`), catalog)
		testutil.AssertEqual(t, len(plans), 2, "one plan per successor")
		if plans[0].Err != nil {
			t.Fatalf("a successor declaring nothing must ignore the result: %v", plans[0].Err)
		}
		testutil.AssertError(t, plans[1].Err, "an inherit-declaring successor cannot merge a malformed result")
		if !strings.Contains(plans[1].Err.Error(), "not valid JSON") {
			t.Fatalf("plan error = %q, want the malformed-result reason", plans[1].Err)
		}

		for _, id := range []string{enrichID, thumbnailID} {
			row, err := q.GetTaskByTaskID(ctx, id)
			testutil.AssertNoError(t, err, "read "+id)
			testutil.AssertEqual(t, string(row.Status), string(types.Task.Status.Waiting), id+" stays waiting until a completion executes its plan")
		}
	})
}

// testSuccessorCatalog wires the dedup hooks successor activation derives keys with.
func testSuccessorCatalog(t *testing.T) *Catalog {
	t.Helper()
	catalog, err := NewCatalog(map[types.TaskType]TypeWiring{
		types.Task.Type.Enrich:       {DedupKey: payloadDocumentKey("enrich:doc:")},
		types.Task.Type.Thumbnail:    {DedupKey: payloadDocumentKey("thumbnail:doc:")},
		types.Task.Type.TagIdentity:  {DedupKey: ConstantDedupKey("tagidentity")},
		types.Task.Type.TagReconcile: {DedupKey: ConstantDedupKey("tagreconcile")},
		types.Task.Type.TagAudit:     {DedupKey: ConstantDedupKey("tagaudit")},
	})
	testutil.AssertNoError(t, err, "build successor catalog")
	return catalog
}

// payloadDocumentKey derives "<prefix><document_id>", empty when the payload names no document.
func payloadDocumentKey(prefix string) DedupKeyFunc {
	return func(payload json.RawMessage) string {
		var p struct {
			DocumentID string `json:"document_id"`
		}
		if err := json.Unmarshal(payload, &p); err != nil || p.DocumentID == "" {
			return ""
		}
		return prefix + p.DocumentID
	}
}

// claimParentInBatch creates a leased batch, a pending parent inside it and claims the parent, returning the
// claimed row and its guard: the state every completion or failure requires.
func claimParentInBatch(ctx context.Context, t *testing.T, store *Store, q *database.Queries, batchID string, parentType types.TaskType, payload json.RawMessage) (database.Task, uuid.UUID) {
	t.Helper()
	leasedBatch(ctx, t, q, batchID, types.Batch.Status.Queued)
	createTaskRow(ctx, t, q, parentType, batchID, payload, "", "", "")
	parent, token, err := store.ClaimNextPending(ctx, parentType)
	testutil.AssertNoError(t, err, "claim parent")
	return parent, token
}

// createSuccessorRow inserts a successor of parentTaskID in the given status.
func createSuccessorRow(t *testing.T, q *database.Queries, parentTaskID string, taskType types.TaskType, payload json.RawMessage, status types.TaskStatus) string {
	t.Helper()
	taskID := uuid.New().String()
	body := payload
	_, err := q.CreateTask(context.Background(), database.CreateTaskParams{
		TaskID:       taskID,
		TaskType:     taskType,
		Status:       status,
		Payload:      &body,
		ParentTaskID: sql.NullString{String: parentTaskID, Valid: true},
	})
	testutil.AssertNoError(t, err, "create "+string(status)+" "+string(taskType))
	return taskID
}

// completeWithSuccessors reads the parent's waiting successors, plans them from the result and completes the
// parent with them: the runner's own sequence, driven directly by the store-level tests.
func completeWithSuccessors(t *testing.T, store *Store, catalog TypeCatalog, parent database.Task, token uuid.UUID, result json.RawMessage) []SuccessorOutcome {
	t.Helper()
	children, err := store.WaitingSuccessors(context.Background(), parent.TaskID)
	testutil.AssertNoError(t, err, "read waiting successors")
	plans := planSuccessors(children, result, catalog)
	rows, outcomes, err := store.CompleteTask(context.Background(), parent.ID, result, token, plans)
	testutil.AssertNoError(t, err, "complete with successors")
	testutil.AssertEqual(t, rows, int64(1), "parent completed")
	return outcomes
}

// TestCancelBatch pins the single cancel operation: one call takes every
// non-terminal task plus the lease, and a terminal batch refuses.
func TestCancelBatch(t *testing.T) {
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	logger := testutil.NewTestLogger()
	q := client.Queries

	t.Run("cancels every active task and releases the lease", func(t *testing.T) {
		createBatchRow(ctx, t, q, "cancel-run", types.Batch.Status.Processing)
		insertLease(ctx, t, q, "cancel-run", "cancel-owner")
		createTaskRow(ctx, t, q, types.Task.Type.Consume, "cancel-run", json.RawMessage(`{}`), "", types.Task.Status.Pending, "")
		createTaskRow(ctx, t, q, types.Task.Type.Enrich, "cancel-run", json.RawMessage(`{}`), "", types.Task.Status.Waiting, "")
		createTaskRow(ctx, t, q, types.Task.Type.Thumbnail, "cancel-run", json.RawMessage(`{}`), "", types.Task.Status.Processing, "")

		result, err := CancelBatch(ctx, client, logger, "cancel-run")
		testutil.AssertNoError(t, err, "cancel")
		testutil.AssertEqual(t, result.PendingCancelled, int64(2), "pending and waiting cancelled together")
		testutil.AssertEqual(t, result.ProcessingCancelled, int64(1), "processing cancelled")
		testutil.AssertEqual(t, result.OwnerAlive, false, "dead owner")
		testutil.AssertEqual(t, result.Signalled, false, "no signal for a dead owner")
		testutil.AssertEqual(t, batchStatus(ctx, t, q, "cancel-run"), types.Batch.Status.Cancelled, "batch cancelled")
		if hasLease(ctx, t, q, "cancel-run") {
			t.Fatal("cancel must release the lease")
		}
	})

	t.Run("refuses a terminal batch", func(t *testing.T) {
		createBatchRow(ctx, t, q, "cancel-terminal", types.Batch.Status.Completed)

		_, err := CancelBatch(ctx, client, logger, "cancel-terminal")
		if !errors.Is(err, ErrBatchTerminal) {
			t.Fatalf("err = %v, want ErrBatchTerminal", err)
		}
	})
}

// TestIsAlive pins the process check the cancel and reclamation paths signal
// through: only a positive pid of a live process counts, so kill(0) can never
// target the caller's own group.
func TestIsAlive(t *testing.T) {
	if IsAlive(0) {
		t.Fatal("pid 0 targets the caller's process group and must not count as alive")
	}
	if IsAlive(-1) {
		t.Fatal("a negative pid targets another process group and must not count as alive")
	}
	if !IsAlive(int64(os.Getpid())) {
		t.Fatal("the current process must be alive")
	}
}

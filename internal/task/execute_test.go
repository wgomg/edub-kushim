package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// runBatchCatalog answers Definition for the one type RunBatch's tests drive, carrying that type's worker
// count so the library's concurrency read is exercised.
type runBatchCatalog struct{}

func (runBatchCatalog) Definition(taskType types.TaskType) (TypeDefinition, error) {
	if taskType != types.Task.Type.Consume {
		return TypeDefinition{}, fmt.Errorf("unknown task type: %q", taskType)
	}
	return TypeDefinition{Type: taskType, Concurrency: 1}, nil
}

// seedClaimedBatch creates a processing batch with one pending task and the dispatcher's lease row under
// token — the state a forked child finds after a successful claim — and returns the task id.
func seedClaimedBatch(t *testing.T, client *database.Client, batchID string, taskType types.TaskType, token string) string {
	t.Helper()
	ctx := context.Background()
	if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID:     batchID,
		Origin: types.Batch.Origin.CLI,
		Status: types.Batch.Status.Processing,
	}); err != nil {
		t.Fatalf("create batch %s: %v", batchID, err)
	}
	taskID, err := createTaskRow(ctx, t, client.Queries, taskType, batchID, json.RawMessage(`{}`), "", "", "")
	if err != nil {
		t.Fatalf("create task for %s: %v", batchID, err)
	}
	rows, err := client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
		BatchID: batchID,
		OwnerID: token,
		Pid:     0,
	})
	if err != nil || rows != 1 {
		t.Fatalf("seed lease for %s: rows=%d err=%v", batchID, rows, err)
	}
	return taskID
}

func runBatchDeps(store *Store, registry *Registry, client *database.Client) BatchRunDeps {
	return BatchRunDeps{
		Client:   client,
		Logger:   log.NewDiscardLogger(),
		Catalog:  runBatchCatalog{},
		Store:    store,
		Registry: registry,
	}
}

func leaseCount(t *testing.T, client *database.Client, batchID string) int {
	t.Helper()
	var leases int
	if err := client.DB().QueryRowContext(context.Background(),
		"SELECT count(*) FROM batch_owner WHERE batch_id = $1", batchID).Scan(&leases); err != nil {
		t.Fatalf("count leases for %s: %v", batchID, err)
	}
	return leases
}

// TestRunBatch_ClaimedBatchRunsTasksAndSettles pins the leased child's dispatch path end to end: the
// dispatcher's claim is adopted, the batch's pending tasks run through their registered handlers, the batch
// settles as completed and the lease is released — Stage 5's acceptance for forked batch execution.
func TestRunBatch_ClaimedBatchRunsTasksAndSettles(t *testing.T) {
	store, registry, client := setupTaskTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	handler := newMockHandler()
	registry.Register(types.Task.Type.Consume, handler)

	token := uuid.New()
	batchID := "runbatch-settle"
	taskID := seedClaimedBatch(t, client, batchID, types.Task.Type.Consume, token.String())

	result, err := RunBatch(ctx, runBatchDeps(store, registry, client), batchID, token)
	testutil.AssertNoError(t, err, "RunBatch")
	if result.Paused {
		t.Fatal("a completed run must not report paused")
	}
	if result.Status != types.Batch.Status.Completed {
		t.Fatalf("result.Status = %q, want %q", result.Status, types.Batch.Status.Completed)
	}
	if handled := handler.HandledCount(); handled != 1 {
		t.Fatalf("handled = %d, want 1 (the batch's single task)", handled)
	}

	batch, err := client.Queries.GetBatch(ctx, batchID)
	testutil.AssertNoError(t, err, "read the settled batch")
	if batch.Status != types.Batch.Status.Completed {
		t.Fatalf("batch status = %q, want %q", batch.Status, types.Batch.Status.Completed)
	}

	var taskStatus string
	if err := client.DB().QueryRowContext(ctx,
		"SELECT status FROM task WHERE task_id = $1", taskID).Scan(&taskStatus); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if taskStatus != string(types.Task.Status.Completed) {
		t.Fatalf("task status = %q, want %q", taskStatus, types.Task.Status.Completed)
	}
	if leases := leaseCount(t, client, batchID); leases != 0 {
		t.Fatalf("lease rows = %d after the run, want 0 (the run released it)", leases)
	}
}

// TestRunBatch_RefusedSettleLeavesBatchForRecovery pins the post-run contract when tasks still block the
// settle: a waiting child nobody clears leaves the batch processing, the teardown releases the lease, and
// the batch is not requeued — no retry would clear the child, so a requeue would loop.
func TestRunBatch_RefusedSettleLeavesBatchForRecovery(t *testing.T) {
	store, registry, client := setupTaskTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token := uuid.New()
	batchID := "runbatch-refused"
	if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID:     batchID,
		Origin: types.Batch.Origin.CLI,
		Status: types.Batch.Status.Processing,
	}); err != nil {
		t.Fatalf("create batch %s: %v", batchID, err)
	}
	if _, err := createTaskRow(ctx, t, client.Queries, types.Task.Type.Enrich, batchID,
		json.RawMessage(`{}`), "", "waiting", ""); err != nil {
		t.Fatalf("create waiting task: %v", err)
	}
	rows, err := client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
		BatchID: batchID,
		OwnerID: token.String(),
		Pid:     0,
	})
	if err != nil || rows != 1 {
		t.Fatalf("seed lease: rows=%d err=%v", rows, err)
	}

	result, err := RunBatch(ctx, runBatchDeps(store, registry, client), batchID, token)
	testutil.AssertNoError(t, err, "RunBatch")
	if result.Paused {
		t.Fatal("a refused settle must not report paused")
	}
	if result.Status != types.Batch.Status.Processing {
		t.Fatalf("result.Status = %q, want processing (settle refused)", result.Status)
	}

	batch, err := client.Queries.GetBatch(ctx, batchID)
	testutil.AssertNoError(t, err, "read the refused batch")
	if batch.Status != types.Batch.Status.Processing {
		t.Fatalf("batch status = %q, want processing (a refused settle is not requeued)", batch.Status)
	}
	if leases := leaseCount(t, client, batchID); leases != 0 {
		t.Fatalf("lease rows = %d after a refused settle, want 0 (the teardown releases it)", leases)
	}
}

// TestRunBatch_PreAdoptionChecks pins the two refusals that must fire before the child touches any task:
// a paused batch returns the paused result without adopting, and a batch nobody claimed is refused.
func TestRunBatch_PreAdoptionChecks(t *testing.T) {
	store, registry, client := setupTaskTestClient(t)
	ctx := context.Background()

	t.Run("a paused batch returns paused without adopting the lease", func(t *testing.T) {
		batchID := "runbatch-paused"
		if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID:     batchID,
			Origin: types.Batch.Origin.CLI,
			Status: types.Batch.Status.Paused,
		}); err != nil {
			t.Fatalf("create batch %s: %v", batchID, err)
		}

		result, err := RunBatch(ctx, runBatchDeps(store, registry, client), batchID, uuid.New())
		testutil.AssertNoError(t, err, "RunBatch on a paused batch")
		if !result.Paused {
			t.Fatal("a paused batch must report paused")
		}
		if leases := leaseCount(t, client, batchID); leases != 0 {
			t.Fatalf("lease rows = %d after the paused pre-check, want 0 (no adoption)", leases)
		}
	})

	t.Run("a batch nobody claimed is refused", func(t *testing.T) {
		batchID := "runbatch-unclaimed"
		if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID:     batchID,
			Origin: types.Batch.Origin.CLI,
			Status: types.Batch.Status.Queued,
		}); err != nil {
			t.Fatalf("create batch %s: %v", batchID, err)
		}

		_, err := RunBatch(ctx, runBatchDeps(store, registry, client), batchID, uuid.New())
		if err == nil || !strings.Contains(err.Error(), "not a claimed processing batch") {
			t.Fatalf("want the unclaimed-batch refusal, got %v", err)
		}
		if leases := leaseCount(t, client, batchID); leases != 0 {
			t.Fatalf("lease rows = %d after a refused run, want 0", leases)
		}
	})
}

// TestBatchConnBudget pins the forked executor's pool formula: one worker per catalog-declared
// concurrency above one, one for types the catalog does not know, plus the fixed auxiliary count.
func TestBatchConnBudget(t *testing.T) {
	catalog := budgetCatalog{
		types.Task.Type.Consume:   {Concurrency: 1},
		types.Task.Type.Enrich:    {Concurrency: 3},
		types.Task.Type.Thumbnail: {Concurrency: 0},
	}
	cases := []struct {
		name      string
		taskTypes []types.TaskType
		want      int
	}{
		{"a type the catalog does not know counts one", []types.TaskType{types.Task.Type.Backup}, 1 + batchAuxConns},
		{"Concurrency above one contributes its value", []types.TaskType{types.Task.Type.Enrich}, 3 + batchAuxConns},
		{"Concurrency at or below one counts one", []types.TaskType{types.Task.Type.Consume, types.Task.Type.Thumbnail}, 2 + batchAuxConns},
		{"mixed types sum their workers", []types.TaskType{types.Task.Type.Consume, types.Task.Type.Enrich, types.Task.Type.Backup}, 5 + batchAuxConns},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.AssertEqual(t, batchConnBudget(tc.taskTypes, catalog), tc.want, "connection budget")
		})
	}
}

// budgetCatalog is a map-backed TypeCatalog: it answers the per-type worker counts the budget test
// drives and the not-found error the one-worker floor must cover.
type budgetCatalog map[types.TaskType]TypeDefinition

func (c budgetCatalog) Definition(taskType types.TaskType) (TypeDefinition, error) {
	def, ok := c[taskType]
	if !ok {
		return TypeDefinition{}, fmt.Errorf("unknown task type: %q", taskType)
	}
	def.Type = taskType
	return def, nil
}

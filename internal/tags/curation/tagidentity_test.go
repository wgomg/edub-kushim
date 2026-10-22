package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// curationTestCatalog is the task type table the enqueue tests need: only
// identity and the fixed dedup key of each sweep.
type curationTestCatalog struct {
	defs map[types.TaskType]task.TypeDefinition
}

func (c curationTestCatalog) Definition(taskType types.TaskType) (task.TypeDefinition, error) {
	def, ok := c.defs[taskType]
	if !ok {
		return task.TypeDefinition{}, sql.ErrNoRows
	}
	return def, nil
}

func newCurationEnqueuer(client *database.Client) *task.Enqueuer {
	key := func(key string) func(json.RawMessage) string {
		return func(json.RawMessage) string { return key }
	}
	catalog := curationTestCatalog{defs: map[types.TaskType]task.TypeDefinition{
		types.Task.Type.TagReconcile: {Type: types.Task.Type.TagReconcile, DedupKey: key(TagReconcileDedupKey)},
		types.Task.Type.TagMeasure:   {Type: types.Task.Type.TagMeasure, DedupKey: key("tagmeasure")},
		types.Task.Type.TagIdentity:  {Type: types.Task.Type.TagIdentity, DedupKey: key(TagIdentityDedupKey)},
		types.Task.Type.TagAudit:     {Type: types.Task.Type.TagAudit, DedupKey: key(TagAuditDedupKey)},
	}}
	return task.NewEnqueuer(client, catalog)
}

func newEnqueueTestClient(t *testing.T) *database.Client {
	t.Helper()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })
	return client
}

func curationBatchSpec(t *testing.T, origin types.BatchOrigin) task.BatchSpec {
	t.Helper()
	spec, err := task.NewBatchSpec(origin, "")
	if err != nil {
		t.Fatalf("build spec: %v", err)
	}
	return spec
}

// seedActiveTagBatch creates a queued batch holding one pending task of the
// type, which is what the active-batch count keys on.
func seedActiveTagBatch(t *testing.T, client *database.Client, taskType types.TaskType, dedupKey string) {
	t.Helper()
	ctx := context.Background()
	batchID := "active-" + string(taskType)
	payload := json.RawMessage(`{}`)
	testutil.AssertNoError(t, client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID:     batchID,
		Origin: types.Batch.Origin.CLI,
		Status: types.Batch.Status.Queued,
	}), "seed batch")
	_, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "active-task-" + string(taskType),
		TaskType: taskType,
		Status:   types.Task.Status.Pending,
		BatchID:  sql.NullString{String: batchID, Valid: true},
		Payload:  &payload,
		DedupKey: sql.NullString{String: dedupKey, Valid: true},
	})
	testutil.AssertNoError(t, err, "seed task")
}

// seedPendingTagTask creates a task with no batch, which only the dedup
// index sees.
func seedPendingTagTask(t *testing.T, client *database.Client, taskType types.TaskType, dedupKey string) {
	t.Helper()
	payload := json.RawMessage(`{}`)
	_, err := client.Queries.CreateTask(context.Background(), database.CreateTaskParams{
		TaskID:   "pending-task-" + string(taskType),
		TaskType: taskType,
		Status:   types.Task.Status.Pending,
		Payload:  &payload,
		DedupKey: sql.NullString{String: dedupKey, Valid: true},
	})
	testutil.AssertNoError(t, err, "seed pending task")
}

func TestTagIdentityEnqueue(t *testing.T) {
	ctx := context.Background()

	t.Run("creates one blocking task and a queued batch", func(t *testing.T) {
		client := newEnqueueTestClient(t)
		svc := NewTagIdentity(client.Queries, newCurationEnqueuer(client))

		batchID, alreadyQueued, err := svc.Enqueue(ctx, curationBatchSpec(t, types.Batch.Origin.CLI))
		testutil.AssertNoError(t, err, "enqueue")
		if batchID == "" {
			t.Fatal("expected non-empty batch ID")
		}
		testutil.AssertEqual(t, alreadyQueued, false, "first call is not already queued")

		tasks, err := client.Queries.ListTasksByBatch(ctx, database.ListTasksByBatchParams{
			BatchID: sql.NullString{String: batchID, Valid: true},
			Limit:   10,
		})
		testutil.AssertNoError(t, err, "list tasks")
		testutil.AssertEqual(t, len(tasks), 1, "one task created")
		testutil.AssertEqual(t, string(tasks[0].TaskType), "tagidentity", "task type")
		testutil.AssertEqual(t, string(tasks[0].Status), "pending", "task status")
		testutil.AssertEqual(t, tasks[0].DedupKey.String, TagIdentityDedupKey, "dedup key")
		testutil.AssertEqual(t, tasks[0].BatchID.String, batchID, "task belongs to the batch")

		batch, err := client.Queries.GetBatch(ctx, batchID)
		testutil.AssertNoError(t, err, "get batch")
		testutil.AssertEqual(t, string(batch.Origin), "cli", "batch origin")
		testutil.AssertEqual(t, string(batch.Status), "queued", "batch status")
	})

	t.Run("an active batch reports already queued", func(t *testing.T) {
		client := newEnqueueTestClient(t)
		seedActiveTagBatch(t, client, types.Task.Type.TagIdentity, TagIdentityDedupKey)
		svc := NewTagIdentity(client.Queries, newCurationEnqueuer(client))

		batchID, alreadyQueued, err := svc.Enqueue(ctx, curationBatchSpec(t, types.Batch.Origin.CLI))
		testutil.AssertNoError(t, err, "enqueue while active")
		testutil.AssertEqual(t, alreadyQueued, true, "already queued")
		testutil.AssertEqual(t, batchID, "", "no batch ID when already queued")
	})

	t.Run("a duplicate pending task reports already queued without a batch", func(t *testing.T) {
		client := newEnqueueTestClient(t)
		seedPendingTagTask(t, client, types.Task.Type.TagIdentity, TagIdentityDedupKey)
		svc := NewTagIdentity(client.Queries, newCurationEnqueuer(client))

		batchID, alreadyQueued, err := svc.Enqueue(ctx, curationBatchSpec(t, types.Batch.Origin.CLI))
		testutil.AssertNoError(t, err, "enqueue")
		testutil.AssertEqual(t, alreadyQueued, true, "a rejected task insert means the work already exists")
		testutil.AssertEqual(t, batchID, "", "no batch without a task")
	})
}

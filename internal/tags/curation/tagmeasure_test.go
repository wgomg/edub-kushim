package curation

import (
	"context"
	"database/sql"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TestTagMeasureEnqueue pins the manual measurement trigger: one blocking
// task plus one queued batch, and the active/dedup guards that make a second
// trigger a no-op.
func TestTagMeasureEnqueue(t *testing.T) {
	ctx := context.Background()

	t.Run("creates one blocking task and a queued batch", func(t *testing.T) {
		client := newEnqueueTestClient(t)
		svc := NewTagMeasure(client.Queries, newCurationEnqueuer(client))

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
		testutil.AssertEqual(t, string(tasks[0].TaskType), "tagmeasure", "task type")
		testutil.AssertEqual(t, string(tasks[0].Status), "pending", "task status")
		testutil.AssertEqual(t, tasks[0].DedupKey.String, "tagmeasure", "dedup key")
		testutil.AssertEqual(t, tasks[0].BatchID.String, batchID, "task belongs to the batch")

		batch, err := client.Queries.GetBatch(ctx, batchID)
		testutil.AssertNoError(t, err, "get batch")
		testutil.AssertEqual(t, string(batch.Origin), "cli", "batch origin")
		testutil.AssertEqual(t, string(batch.Status), "queued", "batch status")
	})

	t.Run("an active batch reports already queued", func(t *testing.T) {
		client := newEnqueueTestClient(t)
		seedActiveTagBatch(t, client, types.Task.Type.TagMeasure, "tagmeasure")
		svc := NewTagMeasure(client.Queries, newCurationEnqueuer(client))

		batchID, alreadyQueued, err := svc.Enqueue(ctx, curationBatchSpec(t, types.Batch.Origin.CLI))
		testutil.AssertNoError(t, err, "enqueue while active")
		testutil.AssertEqual(t, alreadyQueued, true, "already queued")
		testutil.AssertEqual(t, batchID, "", "no batch ID when already queued")
	})

	t.Run("a duplicate pending task reports already queued without a batch", func(t *testing.T) {
		client := newEnqueueTestClient(t)
		seedPendingTagTask(t, client, types.Task.Type.TagMeasure, "tagmeasure")
		svc := NewTagMeasure(client.Queries, newCurationEnqueuer(client))

		batchID, alreadyQueued, err := svc.Enqueue(ctx, curationBatchSpec(t, types.Batch.Origin.CLI))
		testutil.AssertNoError(t, err, "enqueue")
		testutil.AssertEqual(t, alreadyQueued, true, "a rejected task insert means the work already exists")
		testutil.AssertEqual(t, batchID, "", "no batch without a task")
	})
}

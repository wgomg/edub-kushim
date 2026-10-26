package database

import (
	"context"
	"testing"

	"github.com/wgomg/edub-kushim/internal/types"
)

// TestTagMeasureQueuedBatchListing pins the batch composition the dispatcher
// reads: a queued tagmeasure batch surfaces its task type in the listing.
func TestTagMeasureQueuedBatchListing(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO batch (id, origin, status) VALUES ('tm-b1', 'schedule', 'queued')"); err != nil {
		t.Fatalf("insert batch: %v", err)
	}
	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO task (task_id, task_type, status, batch_id, dedup_key) VALUES ('tm-t1', 'tagmeasure', 'pending', 'tm-b1', 'tagmeasure')"); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	batches, err := q.ListQueuedBatchesByAge(ctx)
	if err != nil {
		t.Fatalf("list queued batches: %v", err)
	}
	if len(batches) != 1 || len(batches[0].TaskTypes) != 1 || batches[0].TaskTypes[0] != string(types.Task.Type.TagMeasure) {
		t.Errorf("queued listing must surface the tagmeasure task type for the dispatcher filter, got %+v", batches)
	}
}

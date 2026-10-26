package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/wgomg/edub-kushim/internal/types"
)

func TestTagMeasureBatchAndTaskQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	if n, err := q.CountActiveTagMeasureBatches(ctx); err != nil || n != 0 {
		t.Fatalf("initial active batches = %d, %v", n, err)
	}
	if _, err := q.GetLastTagMeasureBatchCreatedAt(ctx); err != sql.ErrNoRows {
		t.Fatalf("initial GetLastTagMeasureBatchCreatedAt: want ErrNoRows, got %v", err)
	}

	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO batch (id, origin, status) VALUES ('tm-b1', 'schedule', 'queued')"); err != nil {
		t.Fatalf("insert batch: %v", err)
	}
	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO task (task_id, task_type, status, batch_id, dedup_key) VALUES ('tm-t1', 'tagmeasure', 'pending', 'tm-b1', 'tagmeasure')"); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	if n, err := q.CountActiveTagMeasureBatches(ctx); err != nil || n != 1 {
		t.Fatalf("active batches = %d, %v", n, err)
	}
	if _, err := q.GetLastTagMeasureBatchCreatedAt(ctx); err != sql.ErrNoRows {
		t.Errorf("queued batch must not anchor the schedule: want ErrNoRows, got %v", err)
	}

	batches, err := q.ListQueuedBatchesByAge(ctx)
	if err != nil {
		t.Fatalf("list queued batches: %v", err)
	}
	if len(batches) != 1 || len(batches[0].TaskTypes) != 1 || batches[0].TaskTypes[0] != string(types.Task.Type.TagMeasure) {
		t.Errorf("queued listing must surface the tagmeasure task type for the dispatcher filter, got %+v", batches)
	}

	if _, err := q.db.ExecContext(ctx,
		"UPDATE batch SET status = 'completed' WHERE id = 'tm-b1'"); err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if _, err := q.db.ExecContext(ctx,
		"UPDATE task SET status = 'completed', completed_at = now() WHERE task_id = 'tm-t1'"); err != nil {
		t.Fatalf("update task: %v", err)
	}
	if n, err := q.CountActiveTagMeasureBatches(ctx); err != nil || n != 0 {
		t.Errorf("active batches after completion = %d, %v", n, err)
	}
	if ts, err := q.GetLastTagMeasureBatchCreatedAt(ctx); err != nil || !ts.Valid {
		t.Errorf("completed batch must anchor the schedule: %v, %v", ts, err)
	}
}

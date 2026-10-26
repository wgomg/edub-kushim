package database

import (
	"context"
	"testing"
)

// TestTagIdentityActiveBatchCount pins the identity schedule's active-run gate:
// the counter the daemon and the review surface consult before enqueueing counts
// only queued or processing tagidentity batches, so a foreign, failed or
// completed batch never defers the next daily pass.
func TestTagIdentityActiveBatchCount(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	if n, err := q.CountActiveTagIdentityBatches(ctx); err != nil || n != 0 {
		t.Fatalf("initial active batches = %d, %v", n, err)
	}

	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO batch (id, origin, status) VALUES ('foreign-b1', 'schedule', 'completed')"); err != nil {
		t.Fatalf("insert foreign batch: %v", err)
	}
	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO task (task_id, task_type, status, batch_id, completed_at) VALUES ('foreign-t1', 'tagmeasure', 'completed', 'foreign-b1', now())"); err != nil {
		t.Fatalf("insert foreign task: %v", err)
	}
	if n, err := q.CountActiveTagIdentityBatches(ctx); err != nil || n != 0 {
		t.Errorf("another source's batch counted as an active identity run: %d, %v", n, err)
	}

	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO batch (id, origin, status) VALUES ('ti-b1', 'schedule', 'queued')"); err != nil {
		t.Fatalf("insert batch: %v", err)
	}
	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO task (task_id, task_type, status, batch_id, dedup_key) VALUES ('ti-t1', 'tagidentity', 'pending', 'ti-b1', 'tagidentity')"); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if n, err := q.CountActiveTagIdentityBatches(ctx); err != nil || n != 1 {
		t.Fatalf("active batches = %d, %v", n, err)
	}

	if _, err := q.db.ExecContext(ctx,
		"UPDATE batch SET status = 'failed' WHERE id = 'ti-b1'"); err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if n, err := q.CountActiveTagIdentityBatches(ctx); err != nil || n != 0 {
		t.Errorf("a failed batch is no longer an active run: %d, %v", n, err)
	}

	if _, err := q.db.ExecContext(ctx,
		"UPDATE batch SET status = 'completed' WHERE id = 'ti-b1'"); err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if n, err := q.CountActiveTagIdentityBatches(ctx); err != nil || n != 0 {
		t.Errorf("a completed batch is no longer an active run: %d, %v", n, err)
	}
}

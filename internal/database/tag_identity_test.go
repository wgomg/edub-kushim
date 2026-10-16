package database

import (
	"context"
	"database/sql"
	"testing"
)

// TestTagIdentityBatchQueries pins the identity schedule's two gates: the
// active-run counter the daemon consults before enqueueing, and an anchor only a
// completed tagidentity batch may move — so a queued, failed, or foreign batch
// never defers the next daily pass.
func TestTagIdentityBatchQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	if n, err := q.CountActiveTagIdentityBatches(ctx); err != nil || n != 0 {
		t.Fatalf("initial active batches = %d, %v", n, err)
	}
	if _, err := q.GetLastTagIdentityBatchCreatedAt(ctx); err != sql.ErrNoRows {
		t.Fatalf("initial GetLastTagIdentityBatchCreatedAt: want ErrNoRows, got %v", err)
	}

	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO batch (id, source, status) VALUES ('foreign-b1', 'tagmeasure', 'completed')"); err != nil {
		t.Fatalf("insert foreign batch: %v", err)
	}
	if n, err := q.CountActiveTagIdentityBatches(ctx); err != nil || n != 0 {
		t.Errorf("another source's batch counted as an active identity run: %d, %v", n, err)
	}
	if _, err := q.GetLastTagIdentityBatchCreatedAt(ctx); err != sql.ErrNoRows {
		t.Errorf("another source's completed batch anchored identity: want ErrNoRows, got %v", err)
	}

	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO batch (id, source, status) VALUES ('ti-b1', 'tagidentity', 'queued')"); err != nil {
		t.Fatalf("insert batch: %v", err)
	}
	if n, err := q.CountActiveTagIdentityBatches(ctx); err != nil || n != 1 {
		t.Fatalf("active batches = %d, %v", n, err)
	}
	if _, err := q.GetLastTagIdentityBatchCreatedAt(ctx); err != sql.ErrNoRows {
		t.Errorf("queued batch must not anchor the schedule: want ErrNoRows, got %v", err)
	}

	if _, err := q.db.ExecContext(ctx,
		"UPDATE batch SET status = 'failed' WHERE id = 'ti-b1'"); err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if n, err := q.CountActiveTagIdentityBatches(ctx); err != nil || n != 0 {
		t.Errorf("a failed batch is no longer an active run: %d, %v", n, err)
	}
	if _, err := q.GetLastTagIdentityBatchCreatedAt(ctx); err != sql.ErrNoRows {
		t.Errorf("failed batch must not defer the next pass: want ErrNoRows, got %v", err)
	}

	if _, err := q.db.ExecContext(ctx,
		"UPDATE batch SET status = 'completed' WHERE id = 'ti-b1'"); err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if ts, err := q.GetLastTagIdentityBatchCreatedAt(ctx); err != nil || !ts.Valid {
		t.Errorf("completed batch must anchor the schedule: %v, %v", ts, err)
	}
}

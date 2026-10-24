package database

import (
	"context"
	"testing"

	"github.com/wgomg/edub-kushim/internal/types"
)

// assertRows checks a row count and reports the got/want pair when they differ.
func assertRows(t *testing.T, what string, got, want int64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %d rows, want %d", what, got, want)
	}
}

// TestReleaseStaleBatchOwner pins the reaper's identity fence: the release must match the row its
// snapshot described — same pid and still stale — so a lease adopted or heartbeated since survives.
func TestReleaseStaleBatchOwner(t *testing.T) {
	q, db := NewTestQueries(t)
	ResetTestDatabase(db)
	ctx := context.Background()
	stale := int32(15)

	seed := func(batchID, ownerID string, pid int64) {
		t.Helper()
		if err := q.CreateBatch(ctx, CreateBatchParams{
			ID: batchID, Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Processing,
		}); err != nil {
			t.Fatalf("create batch: %v", err)
		}
		if _, err := q.TryInsertBatchOwner(ctx, TryInsertBatchOwnerParams{
			BatchID: batchID, OwnerID: ownerID, Pid: pid,
		}); err != nil {
			t.Fatalf("insert lease: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			"UPDATE batch_owner SET last_heartbeat = NOW() - INTERVAL '5 minutes' WHERE batch_id = $1",
			batchID); err != nil {
			t.Fatalf("backdate heartbeat: %v", err)
		}
	}

	t.Run("releases the stale row the snapshot listed", func(t *testing.T) {
		seed("fence-stale", "owner-a", 0)
		rows, err := q.ReleaseStaleBatchOwner(ctx, ReleaseStaleBatchOwnerParams{
			BatchID: "fence-stale", OwnerID: "owner-a", Pid: 0, StaleAfterSeconds: stale,
		})
		if err != nil {
			t.Fatalf("release stale lease: %v", err)
		}
		assertRows(t, "stale lease released", rows, 1)
	})

	t.Run("keeps a lease whose pid changed after the snapshot", func(t *testing.T) {
		seed("fence-adopted", "owner-b", 0)
		if _, err := q.AdoptBatchOwner(ctx, AdoptBatchOwnerParams{
			BatchID: "fence-adopted", OwnerID: "owner-b", Pid: 4321,
		}); err != nil {
			t.Fatalf("adopt: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			"UPDATE batch_owner SET last_heartbeat = NOW() - INTERVAL '5 minutes' WHERE batch_id = $1",
			"fence-adopted"); err != nil {
			t.Fatalf("stale again after the snapshot: %v", err)
		}
		rows, err := q.ReleaseStaleBatchOwner(ctx, ReleaseStaleBatchOwnerParams{
			BatchID: "fence-adopted", OwnerID: "owner-b", Pid: 0, StaleAfterSeconds: stale,
		})
		if err != nil {
			t.Fatalf("release with the listed pid: %v", err)
		}
		assertRows(t, "an adopted lease must survive the listed-pid release", rows, 0)
		if _, err := q.GetBatchOwner(ctx, GetBatchOwnerParams{BatchID: "fence-adopted", StaleAfterSeconds: stale}); err != nil {
			t.Fatalf("adopted lease must still exist: %v", err)
		}
	})

	t.Run("keeps a lease heartbeated after the snapshot", func(t *testing.T) {
		seed("fence-fresh", "owner-c", 0)
		if _, err := q.HeartbeatBatchOwner(ctx, "owner-c"); err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
		rows, err := q.ReleaseStaleBatchOwner(ctx, ReleaseStaleBatchOwnerParams{
			BatchID: "fence-fresh", OwnerID: "owner-c", Pid: 0, StaleAfterSeconds: stale,
		})
		if err != nil {
			t.Fatalf("release with the listed staleness: %v", err)
		}
		assertRows(t, "a refreshed lease must survive the listed-staleness release", rows, 0)
	})
}

// TestSetBatchFailed pins the launch-failure exit write: it only lands on batches the dispatcher
// could still own (queued or processing) and never rewrites a settled batch.
func TestSetBatchFailed(t *testing.T) {
	q, db := NewTestQueries(t)
	ResetTestDatabase(db)
	ctx := context.Background()

	seed := func(batchID string, status types.BatchStatus) {
		t.Helper()
		if err := q.CreateBatch(ctx, CreateBatchParams{
			ID: batchID, Origin: types.Batch.Origin.CLI, Status: status,
		}); err != nil {
			t.Fatalf("create batch: %v", err)
		}
	}

	t.Run("fails a processing batch", func(t *testing.T) {
		seed("failable", types.Batch.Status.Processing)
		rows, err := q.SetBatchFailed(ctx, "failable")
		if err != nil {
			t.Fatalf("fail batch: %v", err)
		}
		assertRows(t, "processing batch failed", rows, 1)
		batch, err := q.GetBatch(ctx, "failable")
		if err != nil {
			t.Fatalf("read batch: %v", err)
		}
		if batch.Status != types.Batch.Status.Failed {
			t.Fatalf("status = %s, want failed", batch.Status)
		}
	})

	t.Run("never rewrites a settled batch", func(t *testing.T) {
		seed("settled", types.Batch.Status.Completed)
		rows, err := q.SetBatchFailed(ctx, "settled")
		if err != nil {
			t.Fatalf("fail attempt on a settled batch: %v", err)
		}
		assertRows(t, "settled batch untouched", rows, 0)
		batch, err := q.GetBatch(ctx, "settled")
		if err != nil {
			t.Fatalf("read batch: %v", err)
		}
		if batch.Status != types.Batch.Status.Completed {
			t.Fatalf("status = %s, want completed", batch.Status)
		}
	})
}

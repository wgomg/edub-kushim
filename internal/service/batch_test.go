package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func newTestBatch(t *testing.T) (*Batch, *database.Client) {
	t.Helper()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	return NewBatch(client.Queries), client
}

func TestBatch_Create(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	t.Run("creates batch with valid id and source", func(t *testing.T) {
		err := svc.Create(ctx, "batch-1", "api")
		testutil.AssertNoError(t, err, "create batch")
		tasks, err := client.Queries.ListAllTasksByBatch(ctx, sql.NullString{String: "batch-1", Valid: true})
		testutil.AssertNoError(t, err, "verify batch exists via tasks")
		testutil.AssertEqual(t, len(tasks), 0, "no tasks in new batch")
	})

	t.Run("rejects empty id", func(t *testing.T) {
		err := svc.Create(ctx, "", "api")
		testutil.AssertError(t, err, "empty id should fail")
	})
}

func TestBatch_GetSummary_OwnerState(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	t.Run("no owner with pending tasks is orphaned", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "no-owner", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "no-owner-task", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "no-owner", Valid: true},
		})
		testutil.AssertNoError(t, err, "create task")

		summary, err := svc.GetSummary(ctx, "no-owner")
		testutil.AssertNoError(t, err, "get summary")
		testutil.AssertEqual(t, summary.OwnerState, "none", "owner state")
		testutil.AssertEqual(t, summary.Orphaned, true, "orphaned")
		testutil.AssertEqual(t, summary.Pending, int64(1), "pending count")
	})

	t.Run("no owner with only completed tasks is not orphaned", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "no-owner-done", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "done-task", TaskType: "consume", Status: "completed",
			BatchID: sql.NullString{String: "no-owner-done", Valid: true},
		})
		testutil.AssertNoError(t, err, "create task")

		summary, err := svc.GetSummary(ctx, "no-owner-done")
		testutil.AssertNoError(t, err, "get summary")
		testutil.AssertEqual(t, summary.OwnerState, "none", "owner state")
		testutil.AssertEqual(t, summary.Orphaned, false, "not orphaned when no active work")
		testutil.AssertEqual(t, summary.Completed, int64(1), "completed count")
	})

	t.Run("live owner returns state live", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "live-owner", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "live-owner", OwnerID: "owner-1", Pid: 1,
		})
		testutil.AssertNoError(t, err, "insert owner")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "live-task", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "live-owner", Valid: true},
		})
		testutil.AssertNoError(t, err, "create task")

		summary, err := svc.GetSummary(ctx, "live-owner")
		testutil.AssertNoError(t, err, "get summary")
		testutil.AssertEqual(t, summary.OwnerState, "live", "owner state")
		testutil.AssertEqual(t, summary.Orphaned, false, "orphaned with live owner")
	})

	t.Run("stale owner returns state stale and orphaned when tasks pending", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "stale-owner", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "stale-owner", OwnerID: "owner-2", Pid: 2,
		})
		testutil.AssertNoError(t, err, "insert owner")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "stale-task", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "stale-owner", Valid: true},
		})
		testutil.AssertNoError(t, err, "create task")
		staleTime := time.Now().Add(-2 * time.Minute)
		_, err = client.DB().ExecContext(ctx,
			"UPDATE batch_owner SET last_heartbeat = ? WHERE batch_id = ?",
			staleTime, "stale-owner",
		)
		testutil.AssertNoError(t, err, "backdate heartbeat")

		summary, err := svc.GetSummary(ctx, "stale-owner")
		testutil.AssertNoError(t, err, "get summary")
		testutil.AssertEqual(t, summary.OwnerState, "stale", "owner state")
		testutil.AssertEqual(t, summary.Orphaned, true, "orphaned with stale owner")
	})
}

func TestBatch_HasPendingWork(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	t.Run("returns false when no tasks exist", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "empty-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")

		hasWork, err := svc.HasPendingWork(ctx, "empty-batch")
		testutil.AssertNoError(t, err, "has pending work")
		testutil.AssertEqual(t, hasWork, false, "should have no work")
	})

	t.Run("returns true when pending tasks exist", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "pending-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "pending-t1", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "pending-batch", Valid: true},
		})
		testutil.AssertNoError(t, err, "create task")

		hasWork, err := svc.HasPendingWork(ctx, "pending-batch")
		testutil.AssertNoError(t, err, "has pending work")
		testutil.AssertEqual(t, hasWork, true, "should have work")
	})

	t.Run("returns true when processing tasks exist", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "proc-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "proc-t1", TaskType: "consume", Status: "processing",
			BatchID: sql.NullString{String: "proc-batch", Valid: true},
		})
		testutil.AssertNoError(t, err, "create task")

		hasWork, err := svc.HasPendingWork(ctx, "proc-batch")
		testutil.AssertNoError(t, err, "has pending work")
		testutil.AssertEqual(t, hasWork, true, "should have work")
	})

	t.Run("returns false when only completed tasks", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "done-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "done-t1", TaskType: "consume", Status: "completed",
			BatchID: sql.NullString{String: "done-batch", Valid: true},
		})
		testutil.AssertNoError(t, err, "create task")

		hasWork, err := svc.HasPendingWork(ctx, "done-batch")
		testutil.AssertNoError(t, err, "has pending work")
		testutil.AssertEqual(t, hasWork, false, "should have no work")
	})
}

func TestBatch_IsLockedByLiveOwner(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	t.Run("returns false when no owner", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "no-owner-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")

		locked, err := svc.IsLockedByLiveOwner(ctx, "no-owner-batch")
		testutil.AssertNoError(t, err, "check lock")
		testutil.AssertEqual(t, locked, false, "should not be locked")
	})

	t.Run("returns true when live owner exists", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "locked-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "locked-batch", OwnerID: "owner-3", Pid: 3,
		})
		testutil.AssertNoError(t, err, "insert owner")

		locked, err := svc.IsLockedByLiveOwner(ctx, "locked-batch")
		testutil.AssertNoError(t, err, "check lock")
		testutil.AssertEqual(t, locked, true, "should be locked")
	})

	t.Run("returns false when owner is stale", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "stale-lock-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "stale-lock-batch", OwnerID: "owner-4", Pid: 4,
		})
		testutil.AssertNoError(t, err, "insert owner")
		staleTime := time.Now().Add(-2 * time.Minute)
		_, err = client.DB().ExecContext(ctx,
			"UPDATE batch_owner SET last_heartbeat = ? WHERE batch_id = ?",
			staleTime, "stale-lock-batch",
		)
		testutil.AssertNoError(t, err, "backdate heartbeat")

		locked, err := svc.IsLockedByLiveOwner(ctx, "stale-lock-batch")
		testutil.AssertNoError(t, err, "check lock")
		testutil.AssertEqual(t, locked, false, "stale owner should not lock")
	})
}

func TestBatch_CountOrphaned(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	t.Run("counts batches with stale owners", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "live-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create live batch")
		_, err = client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "live-batch", OwnerID: "owner-live", Pid: 100,
		})
		testutil.AssertNoError(t, err, "insert live owner")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "live-t1", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "live-batch", Valid: true},
		})
		testutil.AssertNoError(t, err, "create pending task")

		_, err = client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "orphan-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create orphan batch")
		_, err = client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "orphan-batch", OwnerID: "owner-stale", Pid: 200,
		})
		testutil.AssertNoError(t, err, "insert stale owner")
		staleTime := time.Now().Add(-2 * time.Minute)
		_, err = client.DB().ExecContext(ctx,
			"UPDATE batch_owner SET last_heartbeat = ? WHERE batch_id = ?",
			staleTime, "orphan-batch",
		)
		testutil.AssertNoError(t, err, "backdate heartbeat")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "orphan-t1", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "orphan-batch", Valid: true},
		})
		testutil.AssertNoError(t, err, "create pending task")

		count, err := svc.CountOrphaned(ctx)
		testutil.AssertNoError(t, err, "count orphaned")
		testutil.AssertEqual(t, count, int64(1), "orphaned count")
	})
}

func TestBatch_ListOverviews(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	t.Run("returns batch with correct counts", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "ov-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "ov-t1", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "ov-batch", Valid: true},
		})
		testutil.AssertNoError(t, err, "create pending task")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "ov-t2", TaskType: "consume", Status: "completed",
			BatchID: sql.NullString{String: "ov-batch", Valid: true},
		})
		testutil.AssertNoError(t, err, "create completed task")

		overviews, err := svc.ListOverviews(ctx, 10, 0)
		testutil.AssertNoError(t, err, "list overviews")
		for _, ov := range overviews {
			if ov.BatchID == "ov-batch" {
				testutil.AssertEqual(t, ov.Total, int64(2), "total")
				testutil.AssertEqual(t, ov.Pending, int64(1), "pending")
				testutil.AssertEqual(t, ov.Completed, int64(1), "completed")
				testutil.AssertEqual(t, ov.Source, "test", "source")
				return
			}
		}
		t.Fatal("batch not found in overviews")
	})

	t.Run("duration nil when batch has pending tasks", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "dur-pending", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "dur-t1", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "dur-pending", Valid: true},
		})
		testutil.AssertNoError(t, err, "create task")

		overviews, err := svc.ListOverviews(ctx, 10, 0)
		testutil.AssertNoError(t, err, "list overviews")
		for _, ov := range overviews {
			if ov.BatchID == "dur-pending" {
				if ov.DurationMs != nil {
					t.Fatalf("expected nil duration for batch with pending tasks, got %d", *ov.DurationMs)
				}
				return
			}
		}
		t.Fatal("batch not found in overviews")
	})
}

func TestBatch_BeginCancel(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	t.Run("returns cancelled count and no owner for batch without owner", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "cancel-no-owner", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "cancel-t1", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "cancel-no-owner", Valid: true},
		})
		testutil.AssertNoError(t, err, "create task")

		pending, pid, ownerID, err := svc.BeginCancel(ctx, "cancel-no-owner")
		testutil.AssertNoError(t, err, "begin cancel")
		testutil.AssertEqual(t, pending, int64(1), "pending cancelled")
		testutil.AssertEqual(t, pid, int64(0), "no owner pid")
		testutil.AssertEqual(t, ownerID, "", "no owner id")
	})

	t.Run("returns owner info when owner exists", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "cancel-with-owner", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "cancel-with-owner", OwnerID: "cancel-owner", Pid: 42,
		})
		testutil.AssertNoError(t, err, "insert owner")

		_, pid, ownerID, err := svc.BeginCancel(ctx, "cancel-with-owner")
		testutil.AssertNoError(t, err, "begin cancel")
		testutil.AssertEqual(t, pid, int64(42), "owner pid")
		testutil.AssertEqual(t, ownerID, "cancel-owner", "owner id")
	})
}

func TestBatch_ListSummaries(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID: "summ-1", Source: "test",
	})
	testutil.AssertNoError(t, err, "create batch")
	_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "summ-t1", TaskType: "consume", Status: "completed",
		BatchID: sql.NullString{String: "summ-1", Valid: true},
	})
	testutil.AssertNoError(t, err, "create task")

	summaries, err := svc.ListSummaries(ctx, task.BatchFilter{Limit: 10, Offset: 0})
	testutil.AssertNoError(t, err, "list summaries")
	if len(summaries) < 1 {
		t.Fatal("expected at least 1 summary")
	}
	found := false
	for _, s := range summaries {
		if s.BatchID == "summ-1" {
			found = true
			testutil.AssertEqual(t, s.Completed, int64(1), "completed count")
		}
	}
	if !found {
		t.Fatal("batch summ-1 not found in summaries")
	}
}

func TestBatch_CountDistinct(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	before, err := svc.CountDistinct(ctx)
	testutil.AssertNoError(t, err, "count before")

	_, err = client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID: "distinct-1", Source: "test",
	})
	testutil.AssertNoError(t, err, "create batch")
	_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "distinct-t1", TaskType: "consume", Status: "completed",
		BatchID: sql.NullString{String: "distinct-1", Valid: true},
	})
	testutil.AssertNoError(t, err, "create task in batch")

	after, err := svc.CountDistinct(ctx)
	testutil.AssertNoError(t, err, "count after")
	testutil.AssertEqual(t, after, before+1, "count increased by 1")
}

func TestBatch_ActiveIDs(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	ids, err := svc.ActiveIDs(ctx)
	testutil.AssertNoError(t, err, "active ids empty")
	testutil.AssertEqual(t, len(ids), 0, "no active batches initially")

	_, err = client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID: "active-1", Source: "test",
	})
	testutil.AssertNoError(t, err, "create batch")
	_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "active-t1", TaskType: "consume", Status: "pending",
		BatchID: sql.NullString{String: "active-1", Valid: true},
	})
	testutil.AssertNoError(t, err, "create task")

	ids, err = svc.ActiveIDs(ctx)
	testutil.AssertNoError(t, err, "active ids")
	if len(ids) != 1 {
		t.Fatalf("expected 1 active batch, got %d", len(ids))
	}
	testutil.AssertEqual(t, ids[0], "active-1", "active batch id")
}

func TestBatch_CompleteCancel(t *testing.T) {
	svc, client := newTestBatch(t)
	ctx := context.Background()

	t.Run("cancels processing tasks and releases owner", func(t *testing.T) {
		_, err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: "cc-batch", Source: "test",
		})
		testutil.AssertNoError(t, err, "create batch")
		_, err = client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "cc-batch", OwnerID: "cc-owner", Pid: 50,
		})
		testutil.AssertNoError(t, err, "insert owner")
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "cc-t1", TaskType: "consume", Status: "processing",
			BatchID: sql.NullString{String: "cc-batch", Valid: true},
		})
		testutil.AssertNoError(t, err, "create processing task")

		cancelled, err := svc.CompleteCancel(ctx, "cc-batch", "cc-owner")
		testutil.AssertNoError(t, err, "complete cancel")
		testutil.AssertEqual(t, cancelled, int64(1), "processing cancelled")

		_, err = client.Queries.GetBatchOwner(ctx, "cc-batch")
		testutil.AssertError(t, err, "owner should be released")
	})
}

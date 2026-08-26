package task

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func TestListFilteredActiveOrdering(t *testing.T) {
	_, _, q := setupTaskTest(t)
	ctx := context.Background()

	batch := sql.NullString{String: "listf-batch", Valid: true}
	create := func(taskID, taskType, status string) int64 {
		t.Helper()
		id, err := q.CreateTask(ctx, database.CreateTaskParams{
			TaskID: taskID, TaskType: taskType, Status: status, BatchID: batch,
		})
		testutil.AssertNoError(t, err, "create task "+taskID)
		return id
	}

	// Two processing tasks, claimed in order: the earlier claim has the
	// earlier started_at and must sort first within the processing tier.
	proc1ID := create("listf-proc-1", "consume", "pending")
	proc2ID := create("listf-proc-2", "consume", "pending")
	_, err := q.ClaimTask(ctx, proc1ID)
	testutil.AssertNoError(t, err, "claim proc 1")
	_, err = q.ClaimTask(ctx, proc2ID)
	testutil.AssertNoError(t, err, "claim proc 2")
	create("listf-pending", "consume", "pending")
	create("listf-waiting", "enrich", "waiting")
	create("listf-completed", "consume", "completed")

	got, err := ListFiltered(ctx, q, TaskFilter{Status: "active", Limit: 10})
	testutil.AssertNoError(t, err, "list active")
	wantIDs := []string{"listf-proc-1", "listf-proc-2", "listf-pending", "listf-waiting"}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d active tasks, want %d (terminal statuses excluded)", len(got), len(wantIDs))
	}
	for i, want := range wantIDs {
		if got[i].TaskID != want {
			t.Fatalf("active task %d = %q, want %q", i, got[i].TaskID, want)
		}
	}
}

func TestListFilteredActiveLimit(t *testing.T) {
	_, _, q := setupTaskTest(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		taskID := fmt.Sprintf("listf-limit-%d", i)
		_, err := q.CreateTask(ctx, database.CreateTaskParams{
			TaskID: taskID, TaskType: "consume", Status: "pending",
		})
		testutil.AssertNoError(t, err, "create task "+taskID)
	}

	got, err := ListFiltered(ctx, q, TaskFilter{Status: "active", Limit: 2})
	testutil.AssertNoError(t, err, "list active with limit")
	if len(got) != 2 {
		t.Fatalf("limit 2 returned %d tasks, want 2", len(got))
	}
}

func TestListFilteredActiveWithBatchOrTypeFilter(t *testing.T) {
	_, _, q := setupTaskTest(t)
	ctx := context.Background()

	_, err := q.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "listf-other", TaskType: "consume", Status: "pending",
		BatchID: sql.NullString{String: "listf-other-batch", Valid: true},
	})
	testutil.AssertNoError(t, err, "create other-batch task")

	// The "active" case must not swallow combined filters: these requests
	// fall through to literal-status queries and match nothing, instead of
	// silently returning the global active list.
	withBatch, err := ListFiltered(ctx, q, TaskFilter{BatchID: "listf-other-batch", Status: "active", Limit: 10})
	testutil.AssertNoError(t, err, "list active with batch")
	if len(withBatch) != 0 {
		t.Fatalf("batch+active returned %d tasks, want 0 (no task has literal status 'active')", len(withBatch))
	}

	withType, err := ListFiltered(ctx, q, TaskFilter{Status: "active", TaskType: "consume", Limit: 10})
	testutil.AssertNoError(t, err, "list active with type")
	if len(withType) != 0 {
		t.Fatalf("type+active returned %d tasks, want 0 (no task has literal status 'active')", len(withType))
	}
}

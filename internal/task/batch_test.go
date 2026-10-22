package task

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// closedTestDB returns queries over a per-test handle whose database is
// closed, so every statement fails instead of reaching the shared database.
func closedTestDB(t *testing.T) *database.Queries {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	queries, db := database.NewTestQueries(t)
	// The bookends' contract is that a failed write reaches the log; a
	// closed handle injects that failure deterministically.
	db.Close()
	return queries
}

func TestMarkBatchProcessingLogsWriteFailure(t *testing.T) {
	queries := closedTestDB(t)
	var logs bytes.Buffer

	MarkBatchProcessing(context.Background(), queries, log.NewLoggerWithWriter(&logs), "batch-mark-fail")

	if !strings.Contains(logs.String(), "mark batch batch-mark-fail processing failed") {
		t.Errorf("write failure must be logged naming the batch, got:\n%s", logs.String())
	}
}

func TestFinalizeBatchStatusLogsCountFailure(t *testing.T) {
	queries := closedTestDB(t)
	var logs bytes.Buffer

	FinalizeBatchStatus(context.Background(), queries, log.NewLoggerWithWriter(&logs), "batch-finalize-fail", false)

	if !strings.Contains(logs.String(), "finalize batch batch-finalize-fail: count") {
		t.Errorf("count-query failure must be logged naming the batch, got:\n%s", logs.String())
	}
}

func TestFinalizeBatchStatusSettlesAndLogsSummary(t *testing.T) {
	_, _, queries := setupTaskTest(t)
	ctx := context.Background()

	settle := func(t *testing.T, batchID string, failed bool, statuses []types.TaskStatus, wantBatch types.BatchStatus, wantLog string) {
		t.Helper()
		testutil.AssertNoError(t, queries.CreateBatch(ctx, database.CreateBatchParams{
			ID:     batchID,
			Origin: types.Batch.Origin.CLI,
			Status: types.Batch.Status.Processing,
		}), "create batch")
		for i, status := range statuses {
			_, err := queries.CreateTask(ctx, database.CreateTaskParams{
				TaskID:   fmt.Sprintf("%s-%d", batchID, i),
				TaskType: types.Task.Type.Consume,
				Status:   status,
				BatchID:  sql.NullString{String: batchID, Valid: true},
			})
			testutil.AssertNoError(t, err, "create task")
		}

		var logs bytes.Buffer
		FinalizeBatchStatus(ctx, queries, log.NewLoggerWithWriter(&logs), batchID, failed)

		batch, err := queries.GetBatch(ctx, batchID)
		testutil.AssertNoError(t, err, "get batch")
		testutil.AssertEqual(t, batch.Status, wantBatch, "settled batch status")
		if !strings.Contains(logs.String(), wantLog) {
			t.Errorf("summary = %q, want it to contain %q", logs.String(), wantLog)
		}
	}

	t.Run("a completed settle logs the terminal task counts", func(t *testing.T) {
		settle(t, "finalize-summary-ok", false,
			[]types.TaskStatus{
				types.Task.Status.Completed,
				types.Task.Status.Completed,
				types.Task.Status.Failed,
				types.Task.Status.Cancelled,
			},
			types.Batch.Status.Completed,
			"batch finalize-summary-ok settled as completed: 2 completed, 1 failed, 1 cancelled")
	})

	t.Run("a failed settle logs the terminal task counts", func(t *testing.T) {
		settle(t, "finalize-summary-failed", true,
			[]types.TaskStatus{types.Task.Status.Failed},
			types.Batch.Status.Failed,
			"batch finalize-summary-failed settled as failed: 0 completed, 1 failed, 0 cancelled")
	})
}

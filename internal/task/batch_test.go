package task

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
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

func TestDeleteBatchTasksReturnsQueryError(t *testing.T) {
	queries := closedTestDB(t)

	if err := DeleteBatchTasks(context.Background(), queries, "batch-cleanup-fail"); err == nil {
		t.Error("a failed cleanup must surface its error to the caller, not swallow it")
	}
}

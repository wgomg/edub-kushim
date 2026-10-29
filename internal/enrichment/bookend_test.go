package enrichment

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TestLogBatchBookendAggregatesWholeBatch pins the unpaginated whole-batch read: the settle-time summary
// must count every completed enrich task of the batch, not one page of them.
func TestLogBatchBookendAggregatesWholeBatch(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })
	ctx := context.Background()

	const documents = 25
	batchID := sql.NullString{String: "bookend-batch", Valid: true}
	result := json.RawMessage(`{"counts": {"assignments": 1, "births": 1}}`)
	for i := range documents {
		taskID := fmt.Sprintf("bookend-t-%02d", i)
		_, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID: taskID, TaskType: types.Task.Type.Enrich,
			Status: types.Task.Status.Pending, BatchID: batchID,
		})
		testutil.AssertNoError(t, err, "create task "+taskID)
		_, err = client.DB().ExecContext(ctx,
			"UPDATE task SET status = 'completed', result = $1 WHERE task_id = $2",
			result, taskID,
		)
		testutil.AssertNoError(t, err, "complete task "+taskID)
	}

	var buf bytes.Buffer
	LogBatchBookend(ctx, client.Queries, log.NewLoggerWithWriter(&buf), "bookend-batch", types.Batch.Status.Completed)

	line := buf.String()
	if !strings.Contains(line, "25 documents") {
		t.Fatalf("bookend line = %q, want the whole-batch total of 25 documents", line)
	}
	if !strings.Contains(line, "25 assignments") {
		t.Fatalf("bookend line = %q, want 25 summed assignments", line)
	}
}

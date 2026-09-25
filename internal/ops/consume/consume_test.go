package consume

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

func payloadTask(taskType types.TaskType, payload string) database.Task {
	raw := json.RawMessage(payload)
	return database.Task{TaskType: taskType, Payload: &raw}
}

func assertSamePools(t *testing.T, got, want []*task.Pool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("forSource returned %d pools, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("forSource pool[%d] = %p, want %p", i, got[i], want[i])
		}
	}
}

func TestPoolSetForSource(t *testing.T) {
	consumePool := &task.Pool{}
	enrichPool := &task.Pool{}
	thumbnailPool := &task.Pool{}
	backfillPool := &task.Pool{}
	set := poolSet{
		consume:       consumePool,
		enrich:        enrichPool,
		thumbnail:     thumbnailPool,
		thumbbackfill: backfillPool,
	}

	t.Run("a thumbnail backfill batch runs on the backfill pool alone", func(t *testing.T) {
		assertSamePools(t, set.forSource(types.Batch.Source.Thumbbackfill), []*task.Pool{backfillPool})
	})

	t.Run("other sources run the consume, enrich and thumbnail pools", func(t *testing.T) {
		want := []*task.Pool{consumePool, enrichPool, thumbnailPool}
		for _, source := range []types.BatchSource{
			types.Batch.Source.CLI,
			types.Batch.Source.Polling,
			types.Batch.Source.Backup,
		} {
			assertSamePools(t, set.forSource(source), want)
		}
	})
}

func TestTotalFiles(t *testing.T) {
	cases := []struct {
		name  string
		tasks []database.Task
		want  int
	}{
		{
			name: "consume tasks report their highest file index",
			tasks: []database.Task{
				payloadTask(types.Task.Type.Consume, `{"file_index":1}`),
				payloadTask(types.Task.Type.Consume, `{"file_index":4}`),
				payloadTask(types.Task.Type.Enrich, `{"file_index":9}`),
			},
			want: 4,
		},
		{
			name: "consume tasks without an index are counted",
			tasks: []database.Task{
				payloadTask(types.Task.Type.Consume, `{"file_name":"a.pdf"}`),
				payloadTask(types.Task.Type.Consume, `{"file_name":"b.pdf"}`),
			},
			want: 2,
		},
		{
			name: "non-consume tasks are ignored",
			tasks: []database.Task{
				payloadTask(types.Task.Type.Enrich, `{"file_name":"a.pdf"}`),
			},
			want: 0,
		},
		{
			name:  "no tasks counts nothing",
			tasks: nil,
			want:  0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := totalFiles(c.tasks); got != c.want {
				t.Errorf("totalFiles() = %d, want %d", got, c.want)
			}
		})
	}
}

func TestSetBatchTerminalStatus(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	batchSvc := service.NewBatch(client, 3)

	insertBatch := func(t *testing.T, id string, status types.BatchStatus) {
		t.Helper()
		if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID:     id,
			Source: types.Batch.Source.CLI,
			Status: status,
		}); err != nil {
			t.Fatalf("create batch %s: %v", id, err)
		}
	}

	insertTask := func(t *testing.T, id, batchID string, status types.TaskStatus) {
		t.Helper()
		if _, err := client.DB().ExecContext(ctx,
			`INSERT INTO task (task_id, task_type, status, batch_id) VALUES ($1, 'consume', $2, $3)`,
			id, string(status), batchID); err != nil {
			t.Fatalf("insert task %s: %v", id, err)
		}
	}

	batchStatus := func(t *testing.T, id string) types.BatchStatus {
		t.Helper()
		batch, err := client.Queries.GetBatch(ctx, id)
		if err != nil {
			t.Fatalf("get batch %s: %v", id, err)
		}
		return batch.Status
	}

	t.Run("a failed task fails the batch", func(t *testing.T) {
		insertBatch(t, "terminal-failed", types.Batch.Status.Processing)
		insertTask(t, "terminal-failed-ok", "terminal-failed", types.Task.Status.Completed)
		insertTask(t, "terminal-failed-bad", "terminal-failed", types.Task.Status.Failed)

		if err := setBatchTerminalStatus(ctx, client.Queries, batchSvc, "terminal-failed"); err != nil {
			t.Fatalf("setBatchTerminalStatus: %v", err)
		}
		if got, want := batchStatus(t, "terminal-failed"), types.Batch.Status.Failed; got != want {
			t.Errorf("batch status = %q, want %q", got, want)
		}
	})

	t.Run("all completed tasks complete the batch", func(t *testing.T) {
		insertBatch(t, "terminal-completed", types.Batch.Status.Processing)
		insertTask(t, "terminal-completed-1", "terminal-completed", types.Task.Status.Completed)
		insertTask(t, "terminal-completed-2", "terminal-completed", types.Task.Status.Completed)

		if err := setBatchTerminalStatus(ctx, client.Queries, batchSvc, "terminal-completed"); err != nil {
			t.Fatalf("setBatchTerminalStatus: %v", err)
		}
		if got, want := batchStatus(t, "terminal-completed"), types.Batch.Status.Completed; got != want {
			t.Errorf("batch status = %q, want %q", got, want)
		}
	})

	t.Run("a batch that already settled is not rewritten", func(t *testing.T) {
		insertBatch(t, "terminal-settled", types.Batch.Status.Completed)
		insertTask(t, "terminal-settled-bad", "terminal-settled", types.Task.Status.Failed)

		if err := setBatchTerminalStatus(ctx, client.Queries, batchSvc, "terminal-settled"); err != nil {
			t.Fatalf("setBatchTerminalStatus: %v", err)
		}
		if got, want := batchStatus(t, "terminal-settled"), types.Batch.Status.Completed; got != want {
			t.Errorf("batch status = %q, want %q (a settled batch must keep its status)", got, want)
		}
	})
}

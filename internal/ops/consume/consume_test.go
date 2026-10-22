package consume

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
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
		t.Fatalf("forThumbbackfill returned %d pools, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("forThumbbackfill pool[%d] = %p, want %p", i, got[i], want[i])
		}
	}
}

func TestPoolSetThumbbackfill(t *testing.T) {
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
		assertSamePools(t, set.forThumbbackfill(true), []*task.Pool{backfillPool})
	})

	t.Run("other batches run the consume, enrich and thumbnail pools", func(t *testing.T) {
		assertSamePools(t, set.forThumbbackfill(false), []*task.Pool{consumePool, enrichPool, thumbnailPool})
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
			Origin: types.Batch.Origin.CLI,
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

		if err := setBatchTerminalStatus(ctx, client.Queries, batchSvc, log.NewDiscardLogger(), "terminal-failed"); err != nil {
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

		if err := setBatchTerminalStatus(ctx, client.Queries, batchSvc, log.NewDiscardLogger(), "terminal-completed"); err != nil {
			t.Fatalf("setBatchTerminalStatus: %v", err)
		}
		if got, want := batchStatus(t, "terminal-completed"), types.Batch.Status.Completed; got != want {
			t.Errorf("batch status = %q, want %q", got, want)
		}
	})

	t.Run("a batch that already settled is not rewritten", func(t *testing.T) {
		insertBatch(t, "terminal-settled", types.Batch.Status.Completed)
		insertTask(t, "terminal-settled-bad", "terminal-settled", types.Task.Status.Failed)

		var buf bytes.Buffer
		if err := setBatchTerminalStatus(ctx, client.Queries, batchSvc, log.NewLoggerWithWriter(&buf), "terminal-settled"); err != nil {
			t.Fatalf("setBatchTerminalStatus: %v", err)
		}
		if got, want := batchStatus(t, "terminal-settled"), types.Batch.Status.Completed; got != want {
			t.Errorf("batch status = %q, want %q (a settled batch must keep its status)", got, want)
		}
		// the settle write is skipped for a terminal batch, the bookend is not
		want := "batch terminal-settled settled as completed: 0 completed, 1 failed, 0 cancelled"
		if !strings.Contains(buf.String(), want) {
			t.Errorf("bookend line missing %q, got:\n%s", want, buf.String())
		}
	})
}

func TestLogBatchBookend(t *testing.T) {
	result := func(payload string) *json.RawMessage {
		raw := json.RawMessage(payload)
		return &raw
	}
	bookend := func(batchID string, status types.BatchStatus, tasks []database.Task) string {
		var buf bytes.Buffer
		logBatchBookend(log.NewLoggerWithWriter(&buf), batchID, status, tasks)
		return buf.String()
	}

	t.Run("aggregates completed enrich results into the line", func(t *testing.T) {
		out := bookend("b1", types.Batch.Status.Completed, []database.Task{
			{TaskType: types.Task.Type.Enrich, Status: types.Task.Status.Completed,
				Result: result(`{"stats":{"prompt_tokens":1},"counts":{"assignments":3,"births":2,"promotions":1,"rejects":1,"defers":1,"splits":1,"keeps":1,"drops":0}}`)},
			{TaskType: types.Task.Type.Enrich, Status: types.Task.Status.Completed,
				Result: result(`{"stats":{"prompt_tokens":2},"counts":{"assignments":2,"births":1,"promotions":0,"rejects":0,"defers":2,"splits":0,"keeps":2,"drops":1}}`)},
			{TaskType: types.Task.Type.Enrich, Status: types.Task.Status.Completed},
			{TaskType: types.Task.Type.Consume, Status: types.Task.Status.Completed},
			{TaskType: types.Task.Type.Consume, Status: types.Task.Status.Cancelled},
		})
		want := "batch b1 settled as completed: 4 completed, 0 failed, 1 cancelled" +
			"; enrich: 2 documents, 5 assignments, 3 births, 1 promotions, 1 rejects, 3 defers, 1 splits, 3 keeps, 1 drops"
		if !strings.Contains(out, want) {
			t.Errorf("bookend line missing %q, got:\n%s", want, out)
		}
	})

	t.Run("no enrich segment without decoded results", func(t *testing.T) {
		out := bookend("b2", types.Batch.Status.Failed, []database.Task{
			// a pre-envelope payload and a failed enrich task never count
			{TaskType: types.Task.Type.Enrich, Status: types.Task.Status.Completed,
				Result: result(`{"prompt_tokens":9}`)},
			{TaskType: types.Task.Type.Enrich, Status: types.Task.Status.Failed,
				Result: result(`{"stats":null,"counts":{"assignments":4}}`)},
			{TaskType: types.Task.Type.Consume, Status: types.Task.Status.Completed},
			{TaskType: types.Task.Type.Consume, Status: types.Task.Status.Failed},
		})
		want := "batch b2 settled as failed: 2 completed, 2 failed, 0 cancelled"
		if !strings.Contains(out, want) {
			t.Errorf("bookend line missing %q, got:\n%s", want, out)
		}
		if strings.Contains(out, "; enrich:") {
			t.Errorf("bookend must not carry enrich counts without decoded results, got:\n%s", out)
		}
	})
}

func TestBatchRunsThumbbackfill(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()

	insert := func(t *testing.T, batchID, taskType string) {
		t.Helper()
		if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID:     batchID,
			Origin: types.Batch.Origin.CLI,
			Status: types.Batch.Status.Queued,
		}); err != nil {
			t.Fatalf("create batch %s: %v", batchID, err)
		}
		if taskType == "" {
			return
		}
		if _, err := client.DB().ExecContext(ctx,
			`INSERT INTO task (task_id, task_type, status, batch_id) VALUES ($1, $2, 'pending', $3)`,
			batchID+"-t", taskType, batchID); err != nil {
			t.Fatalf("insert task for %s: %v", batchID, err)
		}
	}

	cases := []struct {
		name     string
		taskType string
		want     bool
	}{
		{"a thumbbackfill task selects the backfill pool", "thumbbackfill", true},
		{"consume tasks stay on the consume family", "consume", false},
		{"a batch without tasks runs neither", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			batchID := "probe-" + c.taskType
			insert(t, batchID, c.taskType)
			got, err := batchRunsThumbbackfill(ctx, client, batchID)
			if err != nil {
				t.Fatalf("batchRunsThumbbackfill: %v", err)
			}
			if got != c.want {
				t.Errorf("batchRunsThumbbackfill = %v, want %v", got, c.want)
			}
		})
	}
}

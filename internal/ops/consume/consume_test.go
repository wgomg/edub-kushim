package consume

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
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

// TestParseExecToken pins the executor contract: no token means the child claims the batch itself,
// a token from the dispatcher is adopted unchanged, and a malformed token is refused loudly.
func TestParseExecToken(t *testing.T) {
	t.Run("an empty value means self-claim", func(t *testing.T) {
		token, err := parseExecToken("")
		if err != nil {
			t.Fatalf("parseExecToken(\"\"): %v", err)
		}
		if token != uuid.Nil {
			t.Fatalf("token = %s, want uuid.Nil for the self-claim path", token)
		}
	})

	t.Run("a dispatcher token is adopted unchanged", func(t *testing.T) {
		want := uuid.New()
		token, err := parseExecToken(want.String())
		if err != nil {
			t.Fatalf("parseExecToken(%s): %v", want, err)
		}
		if token != want {
			t.Fatalf("token = %s, want %s", token, want)
		}
	})

	t.Run("a malformed token is refused", func(t *testing.T) {
		if _, err := parseExecToken("not-a-uuid"); err == nil {
			t.Fatal("a malformed token must be refused")
		}
	})
}

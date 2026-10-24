package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// staleLeaseFor creates an ownership row whose heartbeat is older than the
// staleness window; the pid exceeds pid_max so nothing can be signalled.
func staleLeaseFor(ctx context.Context, t *testing.T, client *database.Client, batchID string) {
	t.Helper()
	_, err := client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
		BatchID: batchID, OwnerID: "stale-owner-" + batchID, Pid: 999999999,
	})
	testutil.AssertNoError(t, err, "insert lease")
	if _, err := client.DB().ExecContext(ctx,
		"UPDATE batch_owner SET last_heartbeat = NOW() - INTERVAL '5 minutes' WHERE batch_id = $1", batchID); err != nil {
		t.Fatalf("backdate heartbeat: %v", err)
	}
}

func hasLeaseRow(ctx context.Context, t *testing.T, client *database.Client, batchID string) bool {
	t.Helper()
	_, err := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
		BatchID: batchID, StaleAfterSeconds: 15,
	})
	return err == nil
}

// TestReclaimStaleBatches pins the reaper's recovery: the listed stale lease
// is released first, live work is reset and requeued for rediscovery, and work
// already past the retry bound is quarantined and settled instead.
func TestReclaimStaleBatches(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	cfg, cleanupCfg := testutil.NewTestConfig(t)
	t.Cleanup(cleanupCfg)

	logger := testutil.NewTestLogger()
	batchSvc := service.NewBatch(client, cfg.Consumer.Reclaim.MaxRetries)
	q := client.Queries

	t.Run("requeues a stale batch that still has work", func(t *testing.T) {
		err := q.CreateBatch(ctx, database.CreateBatchParams{
			ID: "reclaim-requeue", Origin: types.Batch.Origin.Polling, Status: types.Batch.Status.Processing,
		})
		testutil.AssertNoError(t, err, "create batch")
		payload := json.RawMessage(`{}`)
		_, err = q.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "reclaim-requeue-task", TaskType: "consume", Status: "processing", Payload: &payload,
			BatchID: sql.NullString{String: "reclaim-requeue", Valid: true},
		})
		testutil.AssertNoError(t, err, "create processing task")
		staleLeaseFor(ctx, t, client, "reclaim-requeue")

		testutil.AssertNoError(t, reclaimStaleBatches(ctx, cfg, client, batchSvc, logger), "reclaim")

		batch, err := q.GetBatch(ctx, "reclaim-requeue")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Queued, "requeued for the dispatcher")
		if hasLeaseRow(ctx, t, client, "reclaim-requeue") {
			t.Fatal("the listed stale lease must be released")
		}
		task, err := q.GetTaskByTaskID(ctx, "reclaim-requeue-task")
		testutil.AssertNoError(t, err, "read task")
		testutil.AssertEqual(t, string(task.Status), string(types.Task.Status.Pending), "processing task reset")
	})

	t.Run("settles a stale batch whose tasks are already past the retry bound", func(t *testing.T) {
		err := q.CreateBatch(ctx, database.CreateBatchParams{
			ID: "reclaim-settle", Origin: types.Batch.Origin.Polling, Status: types.Batch.Status.Processing,
		})
		testutil.AssertNoError(t, err, "create batch")
		payload := json.RawMessage(`{}`)
		_, err = q.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "reclaim-settle-task", TaskType: "consume", Status: "processing", Payload: &payload,
			BatchID: sql.NullString{String: "reclaim-settle", Valid: true},
		})
		testutil.AssertNoError(t, err, "create processing task")
		if _, err := client.DB().ExecContext(ctx,
			"UPDATE task SET attempts = $2 WHERE task_id = $1",
			"reclaim-settle-task", cfg.Consumer.Reclaim.MaxRetries); err != nil {
			t.Fatalf("set attempts: %v", err)
		}
		staleLeaseFor(ctx, t, client, "reclaim-settle")

		testutil.AssertNoError(t, reclaimStaleBatches(ctx, cfg, client, batchSvc, logger), "reclaim")

		batch, err := q.GetBatch(ctx, "reclaim-settle")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Failed, "all tasks quarantined → failed")
		if hasLeaseRow(ctx, t, client, "reclaim-settle") {
			t.Fatal("the listed stale lease must be released")
		}
		task, err := q.GetTaskByTaskID(ctx, "reclaim-settle-task")
		testutil.AssertNoError(t, err, "read task")
		testutil.AssertEqual(t, string(task.Status), string(types.Task.Status.Failed), "task quarantined")
		if !strings.HasPrefix(task.Error.String, "Max retries exceeded") {
			t.Fatalf("error = %q, want a quarantine reason", task.Error.String)
		}
	})
	t.Run("reclaims a pid-0 lease whose child never adopted it", func(t *testing.T) {
		err := q.CreateBatch(ctx, database.CreateBatchParams{
			ID: "reclaim-preadopt", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Processing,
		})
		testutil.AssertNoError(t, err, "create batch")
		payload := json.RawMessage(`{}`)
		_, err = q.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "reclaim-preadopt-task", TaskType: "consume", Status: "pending", Payload: &payload,
			BatchID: sql.NullString{String: "reclaim-preadopt", Valid: true},
		})
		testutil.AssertNoError(t, err, "create pending task")
		if _, err := q.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: "reclaim-preadopt", OwnerID: "dispatch-token", Pid: 0,
		}); err != nil {
			t.Fatalf("insert claim lease: %v", err)
		}
		if _, err := client.DB().ExecContext(ctx,
			"UPDATE batch_owner SET last_heartbeat = NOW() - INTERVAL '5 minutes' WHERE batch_id = $1",
			"reclaim-preadopt"); err != nil {
			t.Fatalf("backdate heartbeat: %v", err)
		}

		testutil.AssertNoError(t, reclaimStaleBatches(ctx, cfg, client, batchSvc, logger), "reclaim")

		batch, err := q.GetBatch(ctx, "reclaim-preadopt")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Queued, "the orphaned claim is requeued")
		if hasLeaseRow(ctx, t, client, "reclaim-preadopt") {
			t.Fatal("the unadopted lease must be released")
		}
	})
}

// TestReclaimStaleTasksQuarantinesInboxFiles pins the global stale sweep's
// hand-off: the consume identities the sweep quarantines have their inbox
// files moved immediately instead of waiting for the batch's next run.
func TestReclaimStaleTasksQuarantinesInboxFiles(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	cfg, cleanupCfg := testutil.NewTestConfig(t)
	t.Cleanup(cleanupCfg)

	logger := testutil.NewTestLogger()
	batchSvc := service.NewBatch(client, cfg.Consumer.Reclaim.MaxRetries)
	q := client.Queries

	inboxFile := filepath.Join(cfg.Storage.ConsumptionDir, "stale-upload.pdf")
	if err := os.WriteFile(inboxFile, []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatalf("write inbox file: %v", err)
	}
	payloadRaw, _ := json.Marshal(map[string]any{"file_path": inboxFile})
	payload := json.RawMessage(payloadRaw)
	_, err := q.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "stale-consume-1", TaskType: "consume", Status: "processing", Payload: &payload,
	})
	testutil.AssertNoError(t, err, "create stale task")
	if _, err := client.DB().ExecContext(ctx,
		"UPDATE task SET attempts = $2, started_at = NOW() - INTERVAL '2 hours' WHERE task_id = $1",
		"stale-consume-1", cfg.Consumer.Reclaim.MaxRetries); err != nil {
		t.Fatalf("age the task past the retry bound: %v", err)
	}

	testutil.AssertNoError(t, reclaimStaleTasks(ctx, client, batchSvc, cfg, logger), "reclaim")

	task, err := q.GetTaskByTaskID(ctx, "stale-consume-1")
	testutil.AssertNoError(t, err, "read task")
	testutil.AssertEqual(t, string(task.Status), string(types.Task.Status.Failed), "task quarantined")
	if !strings.HasPrefix(task.Error.String, "Max retries exceeded") {
		t.Fatalf("error = %q, want a quarantine reason", task.Error.String)
	}
	if _, err := os.Stat(inboxFile); !os.IsNotExist(err) {
		t.Fatalf("inbox file still present (stat err = %v), quarantine must move it", err)
	}

	errorDir := filepath.Join(cfg.Storage.StorageDir, storage.DirErrors)
	entries, err := os.ReadDir(errorDir)
	testutil.AssertNoError(t, err, "read errors dir")
	moved := false
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "-stale-upload.pdf") {
			moved = true
		}
	}
	if !moved {
		t.Fatalf("quarantined file not found in %s (entries: %d)", errorDir, len(entries))
	}
}

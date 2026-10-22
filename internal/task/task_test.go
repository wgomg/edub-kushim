package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// mockHandler implements Handler for testing.
type mockHandler struct {
	mu       sync.Mutex
	handled  int
	fail     bool
	lastTask Task
	result   json.RawMessage
}

func newMockHandler() *mockHandler {
	return &mockHandler{
		result: json.RawMessage(`{"ok":true}`),
	}
}

func (h *mockHandler) Handle(ctx context.Context, t Task) (json.RawMessage, error) {
	h.mu.Lock()
	h.handled++
	h.lastTask = t
	fail := h.fail
	h.mu.Unlock()

	if fail {
		return nil, errMockFail
	}

	return h.result, nil
}

func (h *mockHandler) HandledCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.handled
}

var errMockFail = errSentinel{}

type errSentinel struct{}

func (e errSentinel) Error() string { return "mock failure" }

// createTaskRow inserts a task directly, keeping the store-level tests
// focused on claim, complete, fail and retry rather than on the enqueue
// contract.
func createTaskRow(
	ctx context.Context, t *testing.T, queries *database.Queries,
	taskType types.TaskType, batchID string, payload json.RawMessage,
	taskID string, status types.TaskStatus, dedupKey string,
) (string, error) {
	t.Helper()
	if taskID == "" {
		taskID = uuid.New().String()
	}
	if status == "" {
		status = types.Task.Status.Pending
	}
	var dedup sql.NullString
	if dedupKey != "" {
		dedup = sql.NullString{String: dedupKey, Valid: true}
	}
	body := payload
	_, err := queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   taskID,
		TaskType: taskType,
		Status:   status,
		BatchID:  sql.NullString{String: batchID, Valid: batchID != ""},
		Payload:  &body,
		DedupKey: dedup,
	})
	return taskID, err
}

func setupTaskTest(t *testing.T) (*Store, *Registry, *database.Queries) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	database.ResetTestDatabase(db)
	store := NewStore(database.NewClient(db))
	registry := NewRegistry()
	return store, registry, q
}

func TestStoreCreateAndGetTask(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	taskID, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Consume, "", json.RawMessage(`{"foo":"bar"}`), "", "", "")
	testutil.AssertNoError(t, err, "create task")
	if taskID == "" {
		t.Fatal("expected non-empty task ID")
	}

	task, err := store.GetTaskByTaskID(ctx, taskID)
	testutil.AssertNoError(t, err, "get by taskID")
	testutil.AssertEqual(t, string(task.TaskType), "consume", "type")
	testutil.AssertEqual(t, string(task.Status), "pending", "status")

	task2, err := store.GetTask(ctx, task.ID)
	testutil.AssertNoError(t, err, "get by ID")
	testutil.AssertEqual(t, task2.TaskID, taskID, "ID match")
}

func TestStoreCreateWithBatchID(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	taskID, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Consume, "batch-123", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")
	task, _ := store.GetTaskByTaskID(ctx, taskID)
	if !task.BatchID.Valid || task.BatchID.String != "batch-123" {
		t.Fatalf("expected batch-123, got %v", task.BatchID)
	}
}

func TestStoreClaimAndComplete(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	_, err := createTaskRow(ctx, t, store.client.Queries, "consume", "", json.RawMessage(`{"file":"test.pdf"}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	claimed, _, err := store.ClaimNextPending(ctx, "consume")
	testutil.AssertNoError(t, err, "claim")
	testutil.AssertEqual(t, string(claimed.Status), "processing", "after claim")

	_, err = store.CompleteTask(ctx, claimed.ID, json.RawMessage(`{"ok":true}`))
	testutil.AssertNoError(t, err, "complete")

	task, _ := store.GetTask(ctx, claimed.ID)
	testutil.AssertEqual(t, string(task.Status), "completed", "after complete")
	if task.Result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestStoreClaimAndFail(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	_, err := createTaskRow(ctx, t, store.client.Queries, "consume", "", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	claimed, _, err := store.ClaimNextPending(ctx, "consume")
	testutil.AssertNoError(t, err, "claim")

	err = store.FailTask(ctx, claimed.ID, "something went wrong")
	testutil.AssertNoError(t, err, "fail")

	task, _ := store.GetTask(ctx, claimed.ID)
	testutil.AssertEqual(t, string(task.Status), "failed", "after fail")
	testutil.AssertEqual(t, task.Error.String, "something went wrong", "error")
}

func TestStoreNoTasks(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	_, _, err := store.ClaimNextPending(ctx, "consume")
	testutil.AssertError(t, err, "no tasks")
}

func TestStoreDedupRejectsDuplicate(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	_, err := createTaskRow(ctx, t, store.client.Queries, "config", "", json.RawMessage(`{"op":"dl"}`), "", "", "download:eng")
	testutil.AssertNoError(t, err, "first")

	_, err = createTaskRow(ctx, t, store.client.Queries, "config", "", json.RawMessage(`{"op":"dl"}`), "", "", "download:eng")
	testutil.AssertError(t, err, "duplicate dedup should error")
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	r.Register("test", newMockHandler())

	h, err := r.Get("test")
	testutil.AssertNoError(t, err, "get")
	if h == nil {
		t.Fatal("expected non-nil")
	}

	_, err = r.Get("nonexistent")
	testutil.AssertError(t, err, "missing")
}

// testCatalog is the minimal task type table the enqueue tests need: only
// identity and a payload-derived dedup key.
type testCatalog struct {
	defs map[types.TaskType]TypeDefinition
}

func (c testCatalog) Definition(taskType types.TaskType) (TypeDefinition, error) {
	def, ok := c.defs[taskType]
	if !ok {
		return TypeDefinition{}, errors.New("unknown task type")
	}
	return def, nil
}

func newEnqueuerForTest(t *testing.T) (*Enqueuer, *database.Client) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	_, db := database.NewTestQueries(t)
	database.ResetTestDatabase(db)
	client := database.NewClient(db)

	keyFromPayload := func(payload json.RawMessage) string {
		var p struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(payload, &p)
		return p.Key
	}
	catalog := testCatalog{defs: map[types.TaskType]TypeDefinition{
		types.Task.Type.Consume: {Type: types.Task.Type.Consume, DedupKey: keyFromPayload},
		types.Task.Type.Enrich:  {Type: types.Task.Type.Enrich, DedupKey: keyFromPayload},
		types.Task.Type.Config:  {Type: types.Task.Type.Config, DedupKey: keyFromPayload},
	}}
	return NewEnqueuer(client, catalog), client
}

func TestEnqueuerCreatesBatchAndTask(t *testing.T) {
	enqueuer, client := newEnqueuerForTest(t)
	defer client.DB().Close()
	ctx := context.Background()

	spec, err := NewBatchSpec(types.Batch.Origin.CLI, "")
	testutil.AssertNoError(t, err, "spec")

	result, err := enqueuer.EnqueueBatch(ctx, spec, []TaskSpec{{
		Type:    types.Task.Type.Consume,
		Payload: json.RawMessage(`{"key":"a"}`),
	}})
	testutil.AssertNoError(t, err, "enqueue")
	if result.BatchID == "" || len(result.TaskIDs) != 1 {
		t.Fatalf("result = %+v, want one task in one batch", result)
	}

	task, err := client.Queries.GetTaskByTaskID(ctx, result.TaskIDs[0])
	testutil.AssertNoError(t, err, "get task")
	testutil.AssertEqual(t, task.BatchID.String, result.BatchID, "task batch")
	testutil.AssertEqual(t, string(task.Status), "pending", "task status")

	batch, err := client.Queries.GetBatch(ctx, result.BatchID)
	testutil.AssertNoError(t, err, "get batch")
	testutil.AssertEqual(t, string(batch.Origin), "cli", "batch origin")
	testutil.AssertEqual(t, string(batch.Status), "queued", "batch status")
}

func TestEnqueuerSkipsDuplicate(t *testing.T) {
	enqueuer, client := newEnqueuerForTest(t)
	defer client.DB().Close()
	ctx := context.Background()

	spec, _ := NewBatchSpec(types.Batch.Origin.CLI, "")
	first, err := enqueuer.EnqueueBatch(ctx, spec, []TaskSpec{{
		Type: types.Task.Type.Config, Payload: json.RawMessage(`{"key":"dup"}`),
	}})
	testutil.AssertNoError(t, err, "first enqueue")
	if first.BatchID == "" {
		t.Fatal("first enqueue created nothing")
	}

	second, err := enqueuer.EnqueueBatch(ctx, spec, []TaskSpec{{
		Type: types.Task.Type.Config, Payload: json.RawMessage(`{"key":"dup"}`),
	}})
	testutil.AssertNoError(t, err, "second enqueue")
	testutil.AssertEqual(t, second.BatchID, "", "no batch when every task is skipped")
	testutil.AssertEqual(t, second.Skipped, 1, "one skipped")
}

func TestEnqueuerAbortsOnTaskIDCollision(t *testing.T) {
	enqueuer, client := newEnqueuerForTest(t)
	defer client.DB().Close()
	ctx := context.Background()

	spec, _ := NewBatchSpec(types.Batch.Origin.CLI, "")
	_, err := enqueuer.EnqueueBatch(ctx, spec, []TaskSpec{{
		Type:    types.Task.Type.Config,
		TaskID:  "clash",
		Payload: json.RawMessage(`{"key":"first"}`),
	}})
	testutil.AssertNoError(t, err, "first enqueue")

	// the colliding task carries a different dedup key, so the failure can only come from the task id
	// uniqueness and must abort instead of being counted as a dedup skip
	_, err = enqueuer.EnqueueBatch(ctx, spec, []TaskSpec{{
		Type:    types.Task.Type.Config,
		TaskID:  "clash",
		Payload: json.RawMessage(`{"key":"second"}`),
	}, {
		Type:    types.Task.Type.Config,
		TaskID:  "sibling",
		Payload: json.RawMessage(`{"key":"third"}`),
	}})
	testutil.AssertError(t, err, "task id collision must abort the enqueue")

	_, err = client.Queries.GetTaskByTaskID(ctx, "sibling")
	testutil.AssertError(t, err, "aborted enqueue leaves no sibling behind")
}

func TestEnqueuerRejectsWaitingWithoutID(t *testing.T) {
	enqueuer, client := newEnqueuerForTest(t)
	defer client.DB().Close()

	spec, _ := NewBatchSpec(types.Batch.Origin.CLI, "")
	_, err := enqueuer.EnqueueBatch(context.Background(), spec, []TaskSpec{{
		Type:   types.Task.Type.Enrich,
		Status: types.Task.Status.Waiting,
	}})
	testutil.AssertError(t, err, "waiting needs an explicit id")
}

func TestEnqueuerWaitingChildCarriesNoDedupKey(t *testing.T) {
	enqueuer, client := newEnqueuerForTest(t)
	defer client.DB().Close()
	ctx := context.Background()

	spec, _ := NewBatchSpec(types.Batch.Origin.CLI, "")
	result, err := enqueuer.EnqueueBatch(ctx, spec, []TaskSpec{{
		Type:    types.Task.Type.Consume,
		TaskID:  "parent-id",
		Payload: json.RawMessage(`{"key":"shared"}`),
	}, {
		Type:         types.Task.Type.Enrich,
		TaskID:       "child-id",
		ParentTaskID: "parent-id",
		Payload:      json.RawMessage(`{"key":"shared"}`),
		Status:       types.Task.Status.Waiting,
	}})
	testutil.AssertNoError(t, err, "enqueue")
	testutil.AssertEqual(t, len(result.TaskIDs), 2, "both tasks created")

	parent, err := client.Queries.GetTaskByTaskID(ctx, "parent-id")
	testutil.AssertNoError(t, err, "get parent")
	testutil.AssertEqual(t, parent.DedupKey.String, "shared", "pending parent keeps its derived key")

	child, err := client.Queries.GetTaskByTaskID(ctx, "child-id")
	testutil.AssertNoError(t, err, "get child")
	if child.DedupKey.Valid {
		t.Errorf("waiting child carries dedup key %q: activation may then collide with a duplicate enqueued while it waited",
			child.DedupKey.String)
	}
	testutil.AssertEqual(t, string(child.Status), "waiting", "child status")
}

func TestEnqueuerRejectsUnknownType(t *testing.T) {
	enqueuer, client := newEnqueuerForTest(t)
	defer client.DB().Close()

	spec, _ := NewBatchSpec(types.Batch.Origin.CLI, "")
	_, err := enqueuer.EnqueueBatch(context.Background(), spec, []TaskSpec{{Type: "nonexistent"}})
	testutil.AssertError(t, err, "unknown type")
}

func TestEnqueuerCustomID(t *testing.T) {
	enqueuer, client := newEnqueuerForTest(t)
	defer client.DB().Close()

	spec, _ := NewBatchSpec(types.Batch.Origin.CLI, "")
	result, err := enqueuer.EnqueueBatch(context.Background(), spec, []TaskSpec{{
		Type:   types.Task.Type.Config,
		TaskID: "my-id",
	}})
	testutil.AssertNoError(t, err, "enqueue")
	testutil.AssertEqual(t, result.TaskIDs[0], "my-id", "custom id")
}

func TestRetry(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()
	logger := testutil.NewTestLogger()

	// happy path: retrying a failed consume restores its discarded enrich and thumbnail to waiting
	_, err := createTaskRow(ctx, t, store.client.Queries, "enrich", "", json.RawMessage(`{}`), "retry-e1", "discarded", "")
	testutil.AssertNoError(t, err, "create discarded enrich")
	_, err = createTaskRow(ctx, t, store.client.Queries, "thumbnail", "", json.RawMessage(`{}`), "retry-t1", "discarded", "")
	testutil.AssertNoError(t, err, "create discarded thumbnail")
	_, err = createTaskRow(ctx, t, store.client.Queries, "consume", "", json.RawMessage(`{"on_completed":"retry-e1","on_completed_thumbnail":"retry-t1"}`), "retry-c1", "failed", "")
	testutil.AssertNoError(t, err, "create failed consume")

	err = Retry(ctx, store.client, logger, "retry-c1")
	testutil.AssertNoError(t, err, "retry")
	consume, _ := store.GetTaskByTaskID(ctx, "retry-c1")
	testutil.AssertEqual(t, string(consume.Status), "pending", "consume pending after retry")
	enrich, _ := store.GetTaskByTaskID(ctx, "retry-e1")
	testutil.AssertEqual(t, string(enrich.Status), "waiting", "enrich restored to waiting")
	thumbnail, _ := store.GetTaskByTaskID(ctx, "retry-t1")
	testutil.AssertEqual(t, string(thumbnail.Status), "waiting", "thumbnail restored to waiting")

	// error path: only failed tasks can be retried
	err = Retry(ctx, store.client, logger, "retry-c1")
	testutil.AssertError(t, err, "pending task cannot be retried")

	// error path: consume without on_completed retries without touching anything
	_, err = createTaskRow(ctx, t, store.client.Queries, "enrich", "", json.RawMessage(`{}`), "retry-e2", "discarded", "")
	testutil.AssertNoError(t, err, "create second discarded enrich")
	_, err = createTaskRow(ctx, t, store.client.Queries, "consume", "", json.RawMessage(`{"file_path":"/tmp/x.pdf"}`), "retry-c2", "failed", "")
	testutil.AssertNoError(t, err, "create failed consume without on_completed")

	err = Retry(ctx, store.client, logger, "retry-c2")
	testutil.AssertNoError(t, err, "retry without on_completed")
	enrich, _ = store.GetTaskByTaskID(ctx, "retry-e2")
	testutil.AssertEqual(t, string(enrich.Status), "discarded", "unlinked enrich untouched")
}

func TestRunnerCompletesTask(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())

	handler := newMockHandler()
	registry.Register("consume", handler)

	ctx := context.Background()
	_, err := createTaskRow(ctx, t, store.client.Queries, "consume", "", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	err = runner.Next(ctx, types.Task.Type.Consume)
	testutil.AssertNoError(t, err, "run")
	testutil.AssertEqual(t, handler.HandledCount(), 1, "called")
}

func TestRunnerFailsTask(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())

	handler := newMockHandler()
	handler.fail = true
	registry.Register(types.Task.Type.Enrich, handler)

	ctx := context.Background()
	_, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Enrich, "", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	err = runner.Next(ctx, types.Task.Type.Enrich)
	testutil.AssertNoError(t, err, "run (error swallowed)")

	// The task should be marked as failed
	// Verify by checking the store
	_ = handler
}

// wrappedFailHandler returns *task.Error when failing, exercising the
// errors.As extraction path in Runner.Next.
type wrappedFailHandler struct {
	mockHandler
}

func (h *wrappedFailHandler) Handle(ctx context.Context, t Task) (json.RawMessage, error) {
	h.mu.Lock()
	h.handled++
	h.lastTask = t
	fail := h.fail
	h.mu.Unlock()

	if fail {
		return nil, &Error{ReqID: "req-from-handler", Err: errMockFail}
	}
	return h.result, nil
}

// panickingHandler panics from Handle, exercising the per-task recover in Runner.Next.
type panickingHandler struct {
	mockHandler
}

func (h *panickingHandler) Handle(ctx context.Context, t Task) (json.RawMessage, error) {
	h.mu.Lock()
	h.handled++
	h.mu.Unlock()
	panic("boom")
}

func TestRunnerExtractsReqIDFromWrappedError(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())

	handler := &wrappedFailHandler{}
	handler.fail = true
	registry.Register(types.Task.Type.Enrich, handler)

	ctx := context.Background()
	_, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Enrich, "", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	err = runner.Next(ctx, types.Task.Type.Enrich)
	testutil.AssertNoError(t, err, "run (error swallowed)")
	testutil.AssertEqual(t, handler.HandledCount(), 1, "called")

	// Failed task should not be claimable again
	_, _, err = store.ClaimNextPending(ctx, types.Task.Type.Enrich)
	testutil.AssertError(t, err, "failed task should not be claimable")
}

func TestRunnerFailsTaskOnPanic(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())

	handler := &panickingHandler{}
	registry.Register(types.Task.Type.Enrich, handler)

	ctx := context.Background()
	taskID, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Enrich, "", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	err = runner.Next(ctx, types.Task.Type.Enrich)
	testutil.AssertError(t, err, "panic should surface as error")

	task, err := store.GetTaskByTaskID(ctx, taskID)
	testutil.AssertNoError(t, err, "get task")
	testutil.AssertEqual(t, handler.HandledCount(), 1, "handler called")
	testutil.AssertEqual(t, string(task.Status), "failed", "task failed on panic")
	testutil.AssertEqual(t, task.Error.String, "panic: boom", "panic text recorded")

	// The failed task must not be stranded claimable in 'processing'
	_, _, err = store.ClaimNextPending(ctx, types.Task.Type.Enrich)
	testutil.AssertError(t, err, "failed task should not be claimable")
}

func TestRunnerNoTasks(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())
	registry.Register(types.Task.Type.Consume, newMockHandler())

	err := runner.Next(context.Background(), types.Task.Type.Consume)
	testutil.AssertNoError(t, err, "no tasks")
}

func TestRunnerNilPayload(t *testing.T) {
	store, registry, q := setupTaskTest(t)
	logger := testutil.NewTestLogger()
	runner := NewRunner(store, registry, logger)
	handler := newMockHandler()
	registry.Register("consume", handler)

	_, db := database.NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()

	_, err := db.ExecContext(ctx,
		"INSERT INTO task (task_id, task_type, status) VALUES ($1, 'consume', 'pending')",
		"nil-payload-task",
	)
	testutil.AssertNoError(t, err, "insert nil-payload task")

	err = runner.Next(ctx, types.Task.Type.Consume)
	testutil.AssertNoError(t, err, "runner should not error on nil payload")
	testutil.AssertEqual(t, handler.HandledCount(), 0, "handler should not be called")

	task, err := q.GetTaskByTaskID(ctx, "nil-payload-task")
	testutil.AssertNoError(t, err, "get task")
	testutil.AssertEqual(t, string(task.Status), "failed", "task should be failed")
	if task.Error.String != "task has nil payload" {
		t.Fatalf("expected nil payload error, got %q", task.Error.String)
	}
}

func TestClaimNextPendingGating(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	_, err := createTaskRow(ctx, t, store.client.Queries, "consume", "", json.RawMessage(`{"file":"test.pdf"}`), "", "", "")
	testutil.AssertNoError(t, err, "create consume task")

	t.Run("consume claimable when backup unlocked", func(t *testing.T) {
		claimed, _, err := store.ClaimNextPending(ctx, "consume")
		testutil.AssertNoError(t, err, "claim consume")
		testutil.AssertEqual(t, string(claimed.Status), "processing", "claimed")
	})

	// Create another consume task and lock backup
	_, err = createTaskRow(ctx, t, store.client.Queries, "consume", "", json.RawMessage(`{"file":"test2.pdf"}`), "", "", "")
	testutil.AssertNoError(t, err, "create second consume task")

	err = AcquireLocks(ctx, store.client, []types.LockName{types.LockMaintenance}, nil, uuid.NullUUID{}, "")
	testutil.AssertNoError(t, err, "acquire maintenance lock")
	defer ReleaseLocksForToken(ctx, store.client.Queries, uuid.NullUUID{})

	t.Run("consume blocked when backup locked", func(t *testing.T) {
		_, _, err := store.ClaimNextPending(ctx, "consume")
		testutil.AssertError(t, err, "consume claim should fail during backup")
	})

	t.Run("enrich blocked when backup locked", func(t *testing.T) {
		_, _, err := store.ClaimNextPending(ctx, types.Task.Type.Enrich)
		testutil.AssertError(t, err, "enrich claim should fail during backup")
	})

	t.Run("config not blocked when backup locked", func(t *testing.T) {
		_, err := createTaskRow(ctx, t, store.client.Queries, "config", "", json.RawMessage(`{"op":"test"}`), "", "", "")
		testutil.AssertNoError(t, err, "create config task")

		claimed, _, err := store.ClaimNextPending(ctx, types.Task.Type.Config)
		testutil.AssertNoError(t, err, "config claim should work during backup")
		testutil.AssertEqual(t, string(claimed.Status), "processing", "config claimed")
	})
}

func TestPoolLifecycle(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())
	registry.Register(types.Task.Type.Config, newMockHandler())

	p := NewPool(testutil.NewTestLogger(), runner, 1, 50*time.Millisecond, "config")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	p.Start(ctx)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	p.Stop(stopCtx)
}

func setupTaskTestClient(t *testing.T) (*Store, *Registry, *database.Client) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	_, db := database.NewTestQueries(t)
	database.ResetTestDatabase(db)
	store := NewStore(database.NewClient(db))
	return store, NewRegistry(), database.NewClient(db)
}

func createBackupTask(t *testing.T, store *Store, taskID string) {
	t.Helper()
	_, err := createTaskRow(context.Background(), t, store.client.Queries, types.Task.Type.Backup, "", json.RawMessage(`{}`), taskID, "", "")
	testutil.AssertNoError(t, err, "create backup task")
}

func lockHeld(t *testing.T, client *database.Client) bool {
	t.Helper()
	v, err := client.Queries.IsLockHeld(context.Background(), string(types.LockMaintenance))
	testutil.AssertNoError(t, err, "check lock")
	return v == 1
}

func TestStoreClaimBlockingTask_SetsLockAndToken(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	createBackupTask(t, store, "bk-token")

	task, token, err := store.ClaimNextPending(context.Background(), types.Task.Type.Backup)
	testutil.AssertNoError(t, err, "claim blocking")
	if token == uuid.Nil {
		t.Fatal("expected a non-nil claim token")
	}
	if !task.ClaimToken.Valid || task.ClaimToken.UUID != token {
		t.Fatalf("task claim_token=%v, want %v", task.ClaimToken.UUID, token)
	}
	if string(task.Status) != "processing" {
		t.Fatalf("status=%s, want processing", task.Status)
	}
	if !lockHeld(t, client) {
		t.Fatal("expected maintenance lock to be held after blocking claim")
	}
}

func TestStoreClaimBlockingTask_CollisionDefers(t *testing.T) {
	store, _, _ := setupTaskTestClient(t)
	ctx := context.Background()
	createBackupTask(t, store, "bk-first")
	createBackupTask(t, store, "bk-second")

	if _, _, err := store.ClaimNextPending(ctx, types.Task.Type.Backup); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	_, _, err := store.ClaimNextPending(ctx, types.Task.Type.Backup)
	if !errors.Is(err, ErrLockBusy) {
		t.Fatalf("second claim: want ErrLockBusy, got %v", err)
	}

	row, err := store.GetTaskByTaskID(ctx, "bk-second")
	testutil.AssertNoError(t, err, "get bk-second")
	if string(row.Status) != "pending" {
		t.Fatalf("bk-second status=%s, want pending (rolled back)", row.Status)
	}
}

func TestStoreCompleteBlockingTask_ReleasesLock(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	createBackupTask(t, store, "bk-complete")

	task, token, err := store.ClaimNextPending(ctx, types.Task.Type.Backup)
	testutil.AssertNoError(t, err, "claim")

	rows, err := store.CompleteBlockingTask(ctx, task.ID, json.RawMessage(`{"ok":true}`), token)
	testutil.AssertNoError(t, err, "complete")
	if rows != 1 {
		t.Fatalf("rows=%d, want 1", rows)
	}
	if lockHeld(t, client) {
		t.Fatal("lock should be released after a successful blocking complete")
	}

	row, _ := store.GetTask(ctx, task.ID)
	if string(row.Status) != "completed" {
		t.Fatalf("status=%s, want completed", row.Status)
	}
}

func TestStoreCompleteBlockingTask_WrongTokenNoop(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	createBackupTask(t, store, "bk-wrong-complete")

	task, _, err := store.ClaimNextPending(ctx, types.Task.Type.Backup)
	testutil.AssertNoError(t, err, "claim")

	rows, err := store.CompleteBlockingTask(ctx, task.ID, json.RawMessage(`{}`), uuid.New())
	testutil.AssertNoError(t, err, "complete wrong-token")
	if rows != 0 {
		t.Fatalf("rows=%d, want 0 (token guard must noop the transition)", rows)
	}
	if !lockHeld(t, client) {
		t.Fatal("lock should still be held after a wrong-token complete")
	}

	row, _ := store.GetTask(ctx, task.ID)
	if string(row.Status) != "processing" {
		t.Fatalf("status=%s, want processing", row.Status)
	}
}

func TestStoreFailBlockingTask_ReleasesLock(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	createBackupTask(t, store, "bk-fail")

	task, token, err := store.ClaimNextPending(ctx, types.Task.Type.Backup)
	testutil.AssertNoError(t, err, "claim")

	if err := store.FailBlockingTask(ctx, task.ID, "boom", token); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if lockHeld(t, client) {
		t.Fatal("lock should be released after a successful blocking fail")
	}

	row, _ := store.GetTask(ctx, task.ID)
	if string(row.Status) != "failed" {
		t.Fatalf("status=%s, want failed", row.Status)
	}
}

func TestStoreFailBlockingTask_WrongTokenNoop(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	createBackupTask(t, store, "bk-wrong-fail")

	task, _, err := store.ClaimNextPending(ctx, types.Task.Type.Backup)
	testutil.AssertNoError(t, err, "claim")

	if err := store.FailBlockingTask(ctx, task.ID, "boom", uuid.New()); err != nil {
		t.Fatalf("fail wrong-token: %v", err)
	}
	if !lockHeld(t, client) {
		t.Fatal("lock should still be held after a wrong-token fail")
	}

	row, _ := store.GetTask(ctx, task.ID)
	if string(row.Status) != "processing" {
		t.Fatalf("status=%s, want processing", row.Status)
	}
}

func TestStoreGuardAgainstStaleClaimant(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	createBackupTask(t, store, "bk-stale")

	task, token, err := store.ClaimNextPending(ctx, types.Task.Type.Backup)
	testutil.AssertNoError(t, err, "claim")

	fresh := uuid.New()
	if _, err := client.DB().ExecContext(ctx, "UPDATE system_signal SET data = jsonb_set(data, '{owner_token}', to_jsonb($1::text)) WHERE name = 'maintenance'", fresh); err != nil {
		t.Fatalf("overwrite lock token: %v", err)
	}
	if _, err := client.DB().ExecContext(ctx, "UPDATE task SET claim_token = $1 WHERE id = $2", fresh, task.ID); err != nil {
		t.Fatalf("overwrite task token: %v", err)
	}

	rows, err := store.CompleteBlockingTask(ctx, task.ID, json.RawMessage(`{}`), token)
	testutil.AssertNoError(t, err, "stale complete")
	if rows != 0 {
		t.Fatalf("stale complete rows=%d, want 0", rows)
	}
	if !lockHeld(t, client) {
		t.Fatal("fresh holder's lock was dropped by a stale claimant's complete")
	}

	rows, err = store.CompleteBlockingTask(ctx, task.ID, json.RawMessage(`{"ok":true}`), fresh)
	testutil.AssertNoError(t, err, "fresh complete")
	if rows != 1 {
		t.Fatalf("fresh complete rows=%d, want 1", rows)
	}
	if lockHeld(t, client) {
		t.Fatal("lock should be released after the fresh holder completes")
	}
}

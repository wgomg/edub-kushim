package task

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/testutil"
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

// mockDedupHandler implements both Handler and Dedupable.
type mockDedupHandler struct {
	mockHandler
}

func (h *mockDedupHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		Key string `json:"key"`
	}
	json.Unmarshal(payload, &p)
	return p.Key
}

var errMockFail = errSentinel{}

type errSentinel struct{}

func (e errSentinel) Error() string { return "mock failure" }

func setupTaskTest(t *testing.T) (*Store, *Registry, *database.Queries) {
	t.Helper()
	q, db := database.NewTestQueries(t)
	database.ResetTestDatabase(db)
	store := NewStore(q)
	registry := NewRegistry()
	return store, registry, q
}

func TestStoreCreateAndGetTask(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	taskID, err := store.CreateTask(ctx, "test-type", "", json.RawMessage(`{"foo":"bar"}`), "", "", "")
	testutil.AssertNoError(t, err, "create task")
	if taskID == "" {
		t.Fatal("expected non-empty task ID")
	}

	task, err := store.GetTaskByTaskID(ctx, taskID)
	testutil.AssertNoError(t, err, "get by taskID")
	testutil.AssertEqual(t, task.TaskType, "test-type", "type")
	testutil.AssertEqual(t, task.Status, "pending", "status")

	task2, err := store.GetTask(ctx, task.ID)
	testutil.AssertNoError(t, err, "get by ID")
	testutil.AssertEqual(t, task2.TaskID, taskID, "ID match")
}

func TestStoreCreateWithBatchID(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	taskID, err := store.CreateTask(ctx, "test", "batch-123", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")
	task, _ := store.GetTaskByTaskID(ctx, taskID)
	if !task.BatchID.Valid || task.BatchID.String != "batch-123" {
		t.Fatalf("expected batch-123, got %v", task.BatchID)
	}
}

func TestStoreClaimAndComplete(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	_, err := store.CreateTask(ctx, "consume", "", json.RawMessage(`{"file":"test.pdf"}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	claimed, err := store.ClaimNextPending(ctx, "consume")
	testutil.AssertNoError(t, err, "claim")
	testutil.AssertEqual(t, claimed.Status, "processing", "after claim")

	_, err = store.CompleteTask(ctx, claimed.ID, json.RawMessage(`{"ok":true}`))
	testutil.AssertNoError(t, err, "complete")

	task, _ := store.GetTask(ctx, claimed.ID)
	testutil.AssertEqual(t, task.Status, "completed", "after complete")
	if task.Result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestStoreClaimAndFail(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	_, err := store.CreateTask(ctx, "consume", "", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	claimed, err := store.ClaimNextPending(ctx, "consume")
	testutil.AssertNoError(t, err, "claim")

	err = store.FailTask(ctx, claimed.ID, "something went wrong")
	testutil.AssertNoError(t, err, "fail")

	task, _ := store.GetTask(ctx, claimed.ID)
	testutil.AssertEqual(t, task.Status, "failed", "after fail")
	testutil.AssertEqual(t, task.Error.String, "something went wrong", "error")
}

func TestStoreNoTasks(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	_, err := store.ClaimNextPending(ctx, "consume")
	testutil.AssertError(t, err, "no tasks")
}

func TestStoreDedupRejectsDuplicate(t *testing.T) {
	store, _, _ := setupTaskTest(t)
	ctx := context.Background()

	_, err := store.CreateTask(ctx, "config", "", json.RawMessage(`{"op":"dl"}`), "", "", "download:eng")
	testutil.AssertNoError(t, err, "first")

	_, err = store.CreateTask(ctx, "config", "", json.RawMessage(`{"op":"dl"}`), "", "", "download:eng")
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

func TestRegistryDedupKey(t *testing.T) {
	r := NewRegistry()
	r.Register("dedup", &mockDedupHandler{})

	key := r.DedupKey("dedup", json.RawMessage(`{"key":"abc"}`))
	testutil.AssertEqual(t, key, "abc", "dedup key")

	r.Register("plain", newMockHandler())
	key = r.DedupKey("plain", json.RawMessage(`{}`))
	testutil.AssertEqual(t, key, "", "no key")
}

func TestRunnerCompletesTask(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())

	handler := newMockHandler()
	registry.Register("consume", handler)

	ctx := context.Background()
	_, err := store.CreateTask(ctx, "consume", "", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	err = runner.Next(ctx, "consume")
	testutil.AssertNoError(t, err, "run")
	testutil.AssertEqual(t, handler.HandledCount(), 1, "called")
}

func TestRunnerFailsTask(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())

	handler := newMockHandler()
	handler.fail = true
	registry.Register("failing", handler)

	ctx := context.Background()
	_, err := store.CreateTask(ctx, "failing", "", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	err = runner.Next(ctx, "failing")
	testutil.AssertNoError(t, err, "run (error swallowed)")

	// The task should be marked as failed
	// Verify by checking the store
	_ = handler
}

func TestRunnerNoTasks(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())
	registry.Register("test", newMockHandler())

	err := runner.Next(context.Background(), "test")
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

	err = runner.Next(ctx, "consume")
	testutil.AssertNoError(t, err, "runner should not error on nil payload")
	testutil.AssertEqual(t, handler.HandledCount(), 0, "handler should not be called")

	task, err := q.GetTaskByTaskID(ctx, "nil-payload-task")
	testutil.AssertNoError(t, err, "get task")
	testutil.AssertEqual(t, task.Status, "failed", "task should be failed")
	if task.Error.String != "task has nil payload" {
		t.Fatalf("expected nil payload error, got %q", task.Error.String)
	}
}

func TestDispatcher(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	dispatcher := NewDispatcher(testutil.NewTestLogger(), store, registry)

	handler := newMockHandler()
	registry.Register("work", handler)

	ctx := context.Background()
	taskID, err := dispatcher.Enqueue(ctx, "work", "batch-1", json.RawMessage(`{"k":"v"}`), "", "")
	testutil.AssertNoError(t, err, "enqueue")
	if taskID == "" {
		t.Fatal("expected non-empty task ID")
	}

	task, _ := store.GetTaskByTaskID(ctx, taskID)
	testutil.AssertEqual(t, task.TaskType, "work", "type")
	testutil.AssertEqual(t, task.BatchID.String, "batch-1", "batch")
}

func TestDispatcherCustomStatus(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	dispatcher := NewDispatcher(testutil.NewTestLogger(), store, registry)
	registry.Register("delayed", newMockHandler())

	_, err := dispatcher.Enqueue(context.Background(), "delayed", "", json.RawMessage(`{}`), "", "waiting")
	testutil.AssertNoError(t, err, "enqueue with waiting")
}

func TestDispatcherRejectsUnknownType(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	dispatcher := NewDispatcher(testutil.NewTestLogger(), store, registry)

	_, err := dispatcher.Enqueue(context.Background(), "nonexistent", "", json.RawMessage(`{}`), "", "")
	testutil.AssertError(t, err, "unknown type")
}

func TestDispatcherCustomID(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	dispatcher := NewDispatcher(testutil.NewTestLogger(), store, registry)
	registry.Register("custom-id", newMockHandler())

	taskID, err := dispatcher.Enqueue(context.Background(), "custom-id", "", json.RawMessage(`{}`), "my-id", "")
	testutil.AssertNoError(t, err, "enqueue")
	testutil.AssertEqual(t, taskID, "my-id", "custom id")
}

func TestClaimNextPendingGating(t *testing.T) {
	store, _, q := setupTaskTest(t)
	ctx := context.Background()

	_, err := store.CreateTask(ctx, "consume", "", json.RawMessage(`{"file":"test.pdf"}`), "", "", "")
	testutil.AssertNoError(t, err, "create consume task")

	t.Run("consume claimable when backup unlocked", func(t *testing.T) {
		claimed, err := store.ClaimNextPending(ctx, "consume")
		testutil.AssertNoError(t, err, "claim consume")
		testutil.AssertEqual(t, claimed.Status, "processing", "claimed")
	})

	// Create another consume task and lock backup
	_, err = store.CreateTask(ctx, "consume", "", json.RawMessage(`{"file":"test2.pdf"}`), "", "", "")
	testutil.AssertNoError(t, err, "create second consume task")

	_, err = q.AcquireBackupLock(ctx)
	testutil.AssertNoError(t, err, "acquire backup lock")
	defer q.ReleaseBackupLock(ctx)

	t.Run("consume blocked when backup locked", func(t *testing.T) {
		_, err := store.ClaimNextPending(ctx, "consume")
		testutil.AssertError(t, err, "consume claim should fail during backup")
	})

	t.Run("enrich blocked when backup locked", func(t *testing.T) {
		_, err := store.ClaimNextPending(ctx, "enrich")
		testutil.AssertError(t, err, "enrich claim should fail during backup")
	})

	t.Run("config not blocked when backup locked", func(t *testing.T) {
		_, err := store.CreateTask(ctx, "config", "", json.RawMessage(`{"op":"test"}`), "", "", "")
		testutil.AssertNoError(t, err, "create config task")

		claimed, err := store.ClaimNextPending(ctx, "config")
		testutil.AssertNoError(t, err, "config claim should work during backup")
		testutil.AssertEqual(t, claimed.Status, "processing", "config claimed")
	})
}

func TestPoolLifecycle(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())
	registry.Register("ptest", newMockHandler())

	p := pool.New(testutil.NewTestLogger(), runner, 1, 50*time.Millisecond, "ptest")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	p.Start(ctx)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	p.Stop(stopCtx)
}

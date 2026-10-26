package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

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
	store.SetOwnerID(testStoreOwner)
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

	leasedBatch(ctx, t, store.client.Queries, "claim-complete-batch", types.Batch.Status.Queued)
	_, err := createTaskRow(ctx, t, store.client.Queries, "consume", "claim-complete-batch", json.RawMessage(`{"file":"test.pdf"}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	claimed, token, err := store.ClaimNextPending(ctx, "consume")
	testutil.AssertNoError(t, err, "claim")
	testutil.AssertEqual(t, string(claimed.Status), "processing", "after claim")

	_, err = store.CompleteTask(ctx, claimed.ID, json.RawMessage(`{"ok":true}`), token)
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

	leasedBatch(ctx, t, store.client.Queries, "claim-fail-batch", types.Batch.Status.Queued)
	_, err := createTaskRow(ctx, t, store.client.Queries, "consume", "claim-fail-batch", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create")

	claimed, token, err := store.ClaimNextPending(ctx, "consume")
	testutil.AssertNoError(t, err, "claim")

	if _, err := store.FailTask(ctx, claimed.ID, "something went wrong", token); err != nil {
		testutil.AssertNoError(t, err, "fail")
	}

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
		types.Task.Type.Consume:        {Type: types.Task.Type.Consume, DedupKey: keyFromPayload},
		types.Task.Type.Enrich:         {Type: types.Task.Type.Enrich, DedupKey: keyFromPayload},
		types.Task.Type.MigrateStorage: {Type: types.Task.Type.MigrateStorage, DedupKey: keyFromPayload},
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
		Type: types.Task.Type.MigrateStorage, Payload: json.RawMessage(`{"key":"dup"}`),
	}})
	testutil.AssertNoError(t, err, "first enqueue")
	if first.BatchID == "" {
		t.Fatal("first enqueue created nothing")
	}

	second, err := enqueuer.EnqueueBatch(ctx, spec, []TaskSpec{{
		Type: types.Task.Type.MigrateStorage, Payload: json.RawMessage(`{"key":"dup"}`),
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
		Type:    types.Task.Type.MigrateStorage,
		TaskID:  "clash",
		Payload: json.RawMessage(`{"key":"first"}`),
	}})
	testutil.AssertNoError(t, err, "first enqueue")

	// the colliding task carries a different dedup key, so the failure can only come from the task id
	// uniqueness and must abort instead of being counted as a dedup skip
	_, err = enqueuer.EnqueueBatch(ctx, spec, []TaskSpec{{
		Type:    types.Task.Type.MigrateStorage,
		TaskID:  "clash",
		Payload: json.RawMessage(`{"key":"second"}`),
	}, {
		Type:    types.Task.Type.MigrateStorage,
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
		Type:   types.Task.Type.MigrateStorage,
		TaskID: "my-id",
	}})
	testutil.AssertNoError(t, err, "enqueue")
	testutil.AssertEqual(t, result.TaskIDs[0], "my-id", "custom id")
}

// newHooklessEnqueuerForTest wires an enqueuer whose catalog defines one type without a dedup hook: the
// shape a boundary produces when it wires only part of its enqueue surface.
func newHooklessEnqueuerForTest(t *testing.T) (*Enqueuer, *database.Client) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	_, db := database.NewTestQueries(t)
	database.ResetTestDatabase(db)
	client := database.NewClient(db)
	catalog := testCatalog{defs: map[types.TaskType]TypeDefinition{
		types.Task.Type.Consume: {
			Type:     types.Task.Type.Consume,
			DedupKey: func(json.RawMessage) string { return "consume-key" },
		},
		types.Task.Type.Backup: {Type: types.Task.Type.Backup},
	}}
	return NewEnqueuer(client, catalog), client
}

// TestEnqueuerRefusesPendingTaskWithoutHook pins the loud-failure rule: a pending task whose type carries
// no hook fails the whole enqueue instead of creating keyless work.
func TestEnqueuerRefusesPendingTaskWithoutHook(t *testing.T) {
	enqueuer, client := newHooklessEnqueuerForTest(t)
	defer client.DB().Close()
	ctx := context.Background()

	spec, err := NewBatchSpec(types.Batch.Origin.CLI, "")
	testutil.AssertNoError(t, err, "spec")
	_, err = enqueuer.EnqueueBatch(ctx, spec, []TaskSpec{{
		Type:   types.Task.Type.Backup,
		TaskID: "hookless-pending",
	}})
	testutil.AssertError(t, err, "pending task without a hook must be refused")
	if !strings.Contains(err.Error(), "no dedup hook") {
		t.Fatalf("error = %v, want it to name the missing dedup hook", err)
	}
	if _, getErr := client.Queries.GetTaskByTaskID(ctx, "hookless-pending"); getErr == nil {
		t.Fatal("a refused enqueue must write nothing")
	}
}

// TestEnqueuerWaitingChildWithoutHookIsAllowed pins that the hook guard covers pending work only: a
// waiting child stays keyless until activation derives its key from the hook-bearing handler.
func TestEnqueuerWaitingChildWithoutHookIsAllowed(t *testing.T) {
	enqueuer, client := newHooklessEnqueuerForTest(t)
	defer client.DB().Close()
	ctx := context.Background()

	spec, err := NewBatchSpec(types.Batch.Origin.CLI, "")
	testutil.AssertNoError(t, err, "spec")
	result, err := enqueuer.EnqueueBatch(ctx, spec, []TaskSpec{{
		Type:    types.Task.Type.Consume,
		TaskID:  "hookless-parent",
		Payload: json.RawMessage(`{"key":"p"}`),
	}, {
		Type:         types.Task.Type.Backup,
		TaskID:       "hookless-child",
		ParentTaskID: "hookless-parent",
		Status:       types.Task.Status.Waiting,
	}})
	testutil.AssertNoError(t, err, "waiting child of a hookless type may be enqueued")
	testutil.AssertEqual(t, len(result.TaskIDs), 2, "both tasks created")

	child, err := client.Queries.GetTaskByTaskID(ctx, "hookless-child")
	testutil.AssertNoError(t, err, "get child")
	if child.DedupKey.Valid {
		t.Fatalf("waiting child carries dedup key %q before activation", child.DedupKey.String)
	}
}

// TestEnqueuerDedupKeyPrecheck pins the coverage pre-check the config API relies on: a hooked type derives
// its key and a hookless one errors, never an empty key that reads as no key at all.
func TestEnqueuerDedupKeyPrecheck(t *testing.T) {
	enqueuer, client := newHooklessEnqueuerForTest(t)
	defer client.DB().Close()

	key, err := enqueuer.DedupKey(types.Task.Type.Consume, json.RawMessage(`{}`))
	testutil.AssertNoError(t, err, "derive the hooked key")
	testutil.AssertEqual(t, key, "consume-key", "hooked key")

	if _, err := enqueuer.DedupKey(types.Task.Type.Backup, json.RawMessage(`{}`)); err == nil {
		t.Fatal("a hookless type must be an error, not an empty key")
	}
}

func TestRunnerCompletesTask(t *testing.T) {
	store, registry, _ := setupTaskTest(t)
	runner := NewRunner(store, registry, testutil.NewTestLogger())

	handler := newMockHandler()
	registry.Register("consume", handler)

	ctx := context.Background()
	leasedBatch(ctx, t, store.client.Queries, "runner-complete-batch", types.Batch.Status.Queued)
	_, err := createTaskRow(ctx, t, store.client.Queries, "consume", "runner-complete-batch", json.RawMessage(`{}`), "", "", "")
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
	leasedBatch(ctx, t, store.client.Queries, "runner-fail-batch", types.Batch.Status.Queued)
	_, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Enrich, "runner-fail-batch", json.RawMessage(`{}`), "", "", "")
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
	leasedBatch(ctx, t, store.client.Queries, "runner-wrapped-batch", types.Batch.Status.Queued)
	_, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Enrich, "runner-wrapped-batch", json.RawMessage(`{}`), "", "", "")
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
	leasedBatch(ctx, t, store.client.Queries, "runner-panic-batch", types.Batch.Status.Queued)
	taskID, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Enrich, "runner-panic-batch", json.RawMessage(`{}`), "", "", "")
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

	leasedBatch(ctx, t, store.client.Queries, "runner-nil-batch", types.Batch.Status.Queued)
	_, err := db.ExecContext(ctx,
		"INSERT INTO task (task_id, task_type, status, batch_id) VALUES ($1, 'consume', 'pending', $2)",
		"nil-payload-task", "runner-nil-batch",
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

	leasedBatch(ctx, t, store.client.Queries, "gate-consume-1", types.Batch.Status.Queued)
	_, err := createTaskRow(ctx, t, store.client.Queries, "consume", "gate-consume-1", json.RawMessage(`{"file":"test.pdf"}`), "", "", "")
	testutil.AssertNoError(t, err, "create consume task")

	t.Run("consume claimable when backup unlocked", func(t *testing.T) {
		claimed, _, err := store.ClaimNextPending(ctx, "consume")
		testutil.AssertNoError(t, err, "claim consume")
		testutil.AssertEqual(t, string(claimed.Status), "processing", "claimed")
	})

	// Create the later candidates before locking maintenance
	leasedBatch(ctx, t, store.client.Queries, "gate-consume-2", types.Batch.Status.Queued)
	_, err = createTaskRow(ctx, t, store.client.Queries, "consume", "gate-consume-2", json.RawMessage(`{"file":"test2.pdf"}`), "", "", "")
	testutil.AssertNoError(t, err, "create second consume task")
	leasedBatch(ctx, t, store.client.Queries, "gate-enrich", types.Batch.Status.Queued)
	_, err = createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Enrich, "gate-enrich", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create enrich task")
	leasedBatch(ctx, t, store.client.Queries, "gate-tagmeasure", types.Batch.Status.Queued)
	_, err = createTaskRow(ctx, t, store.client.Queries, types.Task.Type.TagMeasure, "gate-tagmeasure", json.RawMessage(`{}`), "", "", "")
	testutil.AssertNoError(t, err, "create tag measure task")

	err = AcquireLocks(ctx, store.client, []types.LockName{types.LockMaintenance}, nil, uuid.NullUUID{}, "")
	testutil.AssertNoError(t, err, "acquire maintenance lock")
	defer ReleaseLocksForToken(ctx, store.client.Queries, uuid.NullUUID{})

	t.Run("consume claim defers when backup locked", func(t *testing.T) {
		_, _, err := store.ClaimNextPending(ctx, "consume")
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("consume claim during maintenance: err = %v, want sql.ErrNoRows", err)
		}
	})

	t.Run("enrich claim defers when backup locked", func(t *testing.T) {
		_, _, err := store.ClaimNextPending(ctx, types.Task.Type.Enrich)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("enrich claim during maintenance: err = %v, want sql.ErrNoRows", err)
		}
	})

	t.Run("a maintenance-holding type is claimable while backup holds maintenance", func(t *testing.T) {
		claimed, _, err := store.ClaimNextPending(ctx, types.Task.Type.TagMeasure)
		testutil.AssertNoError(t, err, "tag measure claim should work during backup")
		testutil.AssertEqual(t, string(claimed.Status), "processing", "tag measure claimed")
	})
}

func setupTaskTestClient(t *testing.T) (*Store, *Registry, *database.Client) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	_, db := database.NewTestQueries(t)
	database.ResetTestDatabase(db)
	store := NewStore(database.NewClient(db))
	store.SetOwnerID(testStoreOwner)
	return store, NewRegistry(), database.NewClient(db)
}

// createBackupTask creates a backup task in a batch leased to the store's owner, the state a claim requires.
func createBackupTask(t *testing.T, store *Store, taskID string) {
	t.Helper()
	leasedBatch(context.Background(), t, store.client.Queries, taskID+"-batch", types.Batch.Status.Queued)
	_, err := createTaskRow(context.Background(), t, store.client.Queries, types.Task.Type.Backup, taskID+"-batch",
		json.RawMessage(`{}`), taskID, "", "")
	testutil.AssertNoError(t, err, "create backup task")
}

func lockHeld(t *testing.T, client *database.Client) bool {
	t.Helper()
	v, err := client.Queries.IsLockHeld(context.Background(), string(types.LockMaintenance))
	testutil.AssertNoError(t, err, "check lock")
	return v == 1
}

func TestStoreTransitionWrongOwnerNoop(t *testing.T) {
	store, _, _ := setupTaskTestClient(t)
	ctx := context.Background()
	leasedBatch(ctx, t, store.client.Queries, "guard-wrong-owner-batch", types.Batch.Status.Queued)
	_, err := createTaskRow(ctx, t, store.client.Queries, types.Task.Type.Backup, "guard-wrong-owner-batch",
		json.RawMessage(`{}`), "guard-wrong-owner", "", "")
	testutil.AssertNoError(t, err, "create backup task")

	task, _, err := store.ClaimNextPending(ctx, types.Task.Type.Backup)
	testutil.AssertNoError(t, err, "claim")

	rows, err := store.CompleteTask(ctx, task.ID, json.RawMessage(`{}`), uuid.New())
	testutil.AssertNoError(t, err, "complete under a foreign guard")
	if rows != 0 {
		t.Fatalf("rows=%d, want 0 (the lease guard must refuse the write)", rows)
	}
	row, _ := store.GetTask(ctx, task.ID)
	if string(row.Status) != "processing" {
		t.Fatalf("status=%s, want processing", row.Status)
	}

	rows, err = store.FailTask(ctx, task.ID, "boom", uuid.New())
	testutil.AssertNoError(t, err, "fail under a foreign guard")
	if rows != 0 {
		t.Fatalf("fail rows=%d, want 0 (the lease guard must refuse the write)", rows)
	}
	row, _ = store.GetTask(ctx, task.ID)
	if string(row.Status) != "processing" {
		t.Fatalf("status=%s after a refused fail, want processing", row.Status)
	}
}

func TestStoreGuardAgainstStaleClaimant(t *testing.T) {
	store, _, client := setupTaskTestClient(t)
	ctx := context.Background()
	createBackupTask(t, store, "bk-stale")

	task, token, err := store.ClaimNextPending(ctx, types.Task.Type.Backup)
	testutil.AssertNoError(t, err, "claim")

	fresh := uuid.New()
	if _, err := client.DB().ExecContext(ctx,
		"UPDATE batch_owner SET owner_id = $1 WHERE batch_id = $2", fresh.String(), "bk-stale-batch"); err != nil {
		t.Fatalf("hand the lease to a new execution: %v", err)
	}

	rows, err := store.CompleteTask(ctx, task.ID, json.RawMessage(`{}`), token)
	testutil.AssertNoError(t, err, "stale complete")
	if rows != 0 {
		t.Fatalf("stale complete rows=%d, want 0", rows)
	}

	rows, err = store.CompleteTask(ctx, task.ID, json.RawMessage(`{"ok":true}`), fresh)
	testutil.AssertNoError(t, err, "fresh complete")
	if rows != 1 {
		t.Fatalf("fresh complete rows=%d, want 1", rows)
	}
}

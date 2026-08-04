package task_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/task/handlers"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// newTestConsumeHandler builds a handler wired to a fresh test database.
// Lives here as an external test package: the consume handler's discard paths
// need a DB-backed store, and internal/task is already in the test-db matrix.
func newTestConsumeHandler(t *testing.T) (*handlers.ConsumeTaskHandler, *task.Store) {
	t.Helper()
	cfg, cleanupCfg := testutil.NewTestConfig(t)
	// keep the consumer constructible without external tools; these tests
	// never reach Process, so the real runner is never invoked
	cfg.Consumer.Converter.Enabled = false
	t.Cleanup(cleanupCfg)

	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })

	logger := utils.NewDiscardLogger()
	consumer, err := consumption.NewConsumer(cfg, logger, client)
	testutil.AssertNoError(t, err, "create consumer")

	store := task.NewStore(client.Queries)
	return handlers.NewConsumeTaskHandler(consumer, store, logger), store
}

// insertConsumeEnrichPair creates a waiting enrich linked to a pending consume
// via the consume payload's on_completed field.
func insertConsumeEnrichPair(t *testing.T, store *task.Store, filePath, waitingFor string) (consumeTaskID, enrichTaskID string) {
	t.Helper()
	ctx := context.Background()
	consumeTaskID = uuid.New().String()
	enrichTaskID = uuid.New().String()
	if waitingFor == "" {
		waitingFor = consumeTaskID
	}

	enrichPayload, _ := json.Marshal(map[string]any{"waiting_for": waitingFor})
	_, err := store.CreateTask(ctx, "enrich", "", enrichPayload, enrichTaskID, "waiting", "")
	testutil.AssertNoError(t, err, "create enrich task")

	consumePayload, _ := json.Marshal(map[string]any{"file_path": filePath, "on_completed": enrichTaskID})
	_, err = store.CreateTask(ctx, "consume", "", consumePayload, consumeTaskID, "pending", "")
	testutil.AssertNoError(t, err, "create consume task")
	return consumeTaskID, enrichTaskID
}

func consumeTaskFor(t *testing.T, store *task.Store, consumeTaskID string, payload json.RawMessage) task.Task {
	t.Helper()
	row, err := store.GetTaskByTaskID(context.Background(), consumeTaskID)
	testutil.AssertNoError(t, err, "get consume task")
	if payload == nil {
		payload = *row.Payload
	}
	return task.Task{ID: row.ID, TaskID: row.TaskID, TaskType: row.TaskType, Payload: payload}
}

func TestConsumeHandlerDiscardsEnrichOnFailure(t *testing.T) {
	handler, store := newTestConsumeHandler(t)
	ctx := context.Background()

	t.Run("payload unmarshal failure recovers on_completed and discards", func(t *testing.T) {
		consumeID, enrichID := insertConsumeEnrichPair(t, store, "", "")
		// strict unmarshal fails: file_path is an object, not a string
		payload := json.RawMessage(fmt.Sprintf(`{"file_path": {"bad": true}, "on_completed": %q}`, enrichID))

		_, err := handler.Handle(ctx, consumeTaskFor(t, store, consumeID, payload))
		if err == nil || !strings.Contains(err.Error(), "unmarshal payload") {
			t.Fatalf("expected unmarshal error, got %v", err)
		}
		enrich, err := store.GetTaskByTaskID(ctx, enrichID)
		testutil.AssertNoError(t, err, "get enrich task")
		testutil.AssertEqual(t, enrich.Status, "discarded", "enrich discarded")
	})

	t.Run("missing file_path discards", func(t *testing.T) {
		consumeID, enrichID := insertConsumeEnrichPair(t, store, "", "")

		_, err := handler.Handle(ctx, consumeTaskFor(t, store, consumeID, nil))
		if err == nil || !strings.Contains(err.Error(), "no file_path") {
			t.Fatalf("expected file_path error, got %v", err)
		}
		enrich, _ := store.GetTaskByTaskID(ctx, enrichID)
		testutil.AssertEqual(t, enrich.Status, "discarded", "enrich discarded")
	})

	t.Run("missing inbox file discards", func(t *testing.T) {
		consumeID, enrichID := insertConsumeEnrichPair(t, store, filepath.Join(t.TempDir(), "gone.pdf"), "")

		_, err := handler.Handle(ctx, consumeTaskFor(t, store, consumeID, nil))
		if err == nil || !strings.Contains(err.Error(), "build file from path") {
			t.Fatalf("expected file build error, got %v", err)
		}
		enrich, _ := store.GetTaskByTaskID(ctx, enrichID)
		testutil.AssertEqual(t, enrich.Status, "discarded", "enrich discarded")
	})

	t.Run("discard failure is surfaced in the task error", func(t *testing.T) {
		consumeID, enrichID := insertConsumeEnrichPair(t, store, "", "unrelated-consume")

		_, err := handler.Handle(ctx, consumeTaskFor(t, store, consumeID, nil))
		if err == nil || !strings.Contains(err.Error(), "additionally failed to discard enrich task") {
			t.Fatalf("expected discard failure in error, got %v", err)
		}
		enrich, _ := store.GetTaskByTaskID(ctx, enrichID)
		testutil.AssertEqual(t, enrich.Status, "waiting", "waiting_for mismatch blocks the discard")
	})
}

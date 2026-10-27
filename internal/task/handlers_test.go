package task_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// newTestConsumeHandler builds a handler wired to a fresh test database.
// Lives here as an external test package: the consumer needs a DB-backed
// config, and internal/task is already in the test-db matrix.
func newTestConsumeHandler(t *testing.T) (*consumption.ConsumeTaskHandler, *database.Client) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	cfg, cleanupCfg := testutil.NewTestConfig(t)
	// keep the consumer constructible without external tools; these tests
	// never reach Process, so the real runner is never invoked
	cfg.Consumer.Converter.Enabled = false
	t.Cleanup(cleanupCfg)

	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })

	logger := log.NewDiscardLogger()
	consumer, err := consumption.NewConsumer(cfg, logger, client)
	testutil.AssertNoError(t, err, "create consumer")

	return consumption.NewConsumeTaskHandler(consumer, client.Queries, logger), client
}

// consumeTask builds the claimed task shape a handler call receives.
func consumeTask(payload json.RawMessage) task.Task {
	return task.Task{TaskID: "consume-task", TaskType: types.Task.Type.Consume, Payload: payload}
}

// TestConsumeHandlerReportsFailures pins the handler's failure surface: the runner fails the task on these
// errors and discards its waiting branch, so the handler itself touches no successor rows.
func TestConsumeHandlerReportsFailures(t *testing.T) {
	handler, _ := newTestConsumeHandler(t)
	ctx := context.Background()

	t.Run("payload unmarshal failure", func(t *testing.T) {
		_, err := handler.Handle(ctx, consumeTask(json.RawMessage(`{"file_path": {"bad": true}}`)))
		if err == nil || !strings.Contains(err.Error(), "unmarshal payload") {
			t.Fatalf("expected unmarshal error, got %v", err)
		}
	})

	t.Run("missing file_path", func(t *testing.T) {
		_, err := handler.Handle(ctx, consumeTask(json.RawMessage(`{"document_id":"doc-1"}`)))
		if err == nil || !strings.Contains(err.Error(), "no file_path") {
			t.Fatalf("expected file_path error, got %v", err)
		}
	})

	t.Run("missing inbox file", func(t *testing.T) {
		payload := fmt.Sprintf(`{"file_path": %q}`, filepath.Join(t.TempDir(), "gone.pdf"))
		_, err := handler.Handle(ctx, consumeTask(json.RawMessage(payload)))
		if err == nil || !strings.Contains(err.Error(), "build file from path") {
			t.Fatalf("expected file build error, got %v", err)
		}
	})
}

package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TestTagReconcileEnqueue pins the manual reconcile trigger: one blocking task plus
// one queued batch, the fixed dedup key that lets an active run block the next
// trigger, and an honest failure path when either insert loses.
func TestTagReconcileEnqueue(t *testing.T) {
	t.Run("creates one blocking task and a queued batch", func(t *testing.T) {
		client := newEnqueueTestClient(t)
		taskMock := &recordingTaskCreator{}
		batchMock := &recordingBatchCreator{}
		svc := NewTagReconcile(client.Queries, taskMock, batchMock)

		batchID, alreadyQueued, err := svc.Enqueue(context.Background())
		testutil.AssertNoError(t, err, "enqueue")
		if batchID == "" {
			t.Fatal("expected non-empty batch ID")
		}
		testutil.AssertEqual(t, alreadyQueued, false, "first call is not already queued")

		testutil.AssertEqual(t, len(taskMock.calls), 1, "one task created")
		testutil.AssertEqual(t, taskMock.calls[0].TaskType, "tagreconcile", "task type")
		testutil.AssertEqual(t, taskMock.calls[0].Status, "pending", "task status")
		testutil.AssertEqual(t, taskMock.calls[0].DedupKey, "tagreconcile", "dedup key")
		testutil.AssertEqual(t, taskMock.calls[0].BatchID, batchID, "task belongs to the batch")

		testutil.AssertEqual(t, len(batchMock.calls), 1, "one batch created")
		testutil.AssertEqual(t, batchMock.calls[0].Source, "tagreconcile", "batch source")
		testutil.AssertEqual(t, batchMock.calls[0].Status, "queued", "batch status")
		testutil.AssertEqual(t, batchMock.calls[0].BatchID, batchID, "batch ID matches returned ID")
	})

	t.Run("an active batch reports already queued", func(t *testing.T) {
		client := newEnqueueTestClient(t)
		taskMock := &recordingTaskCreator{}
		batchMock := &recordingBatchCreator{}
		svc := NewTagReconcile(client.Queries, taskMock, batchMock)

		batchSvc := service.NewBatch(client, 3)
		testutil.AssertNoError(t, batchSvc.Create(context.Background(), "existing-run", types.Batch.Source.TagReconcile, types.Batch.Status.Queued), "seed active batch")

		batchID, alreadyQueued, err := svc.Enqueue(context.Background())
		testutil.AssertNoError(t, err, "enqueue while active")
		testutil.AssertEqual(t, alreadyQueued, true, "already queued")
		if batchID != "" {
			t.Errorf("batch ID = %q, want empty when already queued", batchID)
		}
		testutil.AssertEqual(t, len(taskMock.calls), 0, "no new task while a batch is active")
		testutil.AssertEqual(t, len(batchMock.calls), 0, "no new batch while a batch is active")
	})

	t.Run("a duplicate active task reports already queued without a batch", func(t *testing.T) {
		client := newEnqueueTestClient(t)
		taskMock := &recordingTaskCreator{
			createFn: func(types.TaskType, string, json.RawMessage, string, types.TaskStatus, string) (string, error) {
				// the active-dedup index rejects the second task for the slot the count above lost a race to
				return "", &pgconn.PgError{Code: "23505", Message: `duplicate key value violates unique constraint "idx_task_dedup"`}
			},
		}
		batchMock := &recordingBatchCreator{}
		svc := NewTagReconcile(client.Queries, taskMock, batchMock)

		batchID, alreadyQueued, err := svc.Enqueue(context.Background())
		testutil.AssertNoError(t, err, "enqueue")
		testutil.AssertEqual(t, alreadyQueued, true, "a rejected task insert means the work already exists")
		if batchID != "" {
			t.Errorf("batch ID = %q, want empty", batchID)
		}
		testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch without a task")
	})

	t.Run("a task creation failure is reported, not swallowed", func(t *testing.T) {
		client := newEnqueueTestClient(t)
		taskMock := &recordingTaskCreator{
			createFn: func(types.TaskType, string, json.RawMessage, string, types.TaskStatus, string) (string, error) {
				return "", errors.New("connection lost")
			},
		}
		batchMock := &recordingBatchCreator{}
		svc := NewTagReconcile(client.Queries, taskMock, batchMock)

		_, _, err := svc.Enqueue(context.Background())
		testutil.AssertError(t, err, "a real failure must not be reported as already queued")
		testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch without a task")
	})

	t.Run("batch failure deletes the orphan task", func(t *testing.T) {
		client := newEnqueueTestClient(t)

		// an orphan pending task would be claimable by the queue's tagreconcile pool with a batch that
		// never exists
		inner := &capturingTaskCreator{inner: task.NewStore(client)}
		batchMock := &recordingBatchCreator{
			createFn: func(string, types.BatchSource, types.BatchStatus) error {
				return errors.New("batch insert failed")
			},
		}
		svc := NewTagReconcile(client.Queries, inner, batchMock)

		_, _, err := svc.Enqueue(context.Background())
		if err == nil {
			t.Fatal("expected error when batch creation fails")
		}
		if len(inner.captured) == 0 {
			t.Fatal("expected the task to have been attempted first")
		}

		tasks, listErr := client.Queries.ListTasksByBatch(context.Background(), database.ListTasksByBatchParams{
			BatchID: sql.NullString{String: inner.captured[0], Valid: true},
			Limit:   10,
		})
		testutil.AssertNoError(t, listErr, "list tasks for the failed batch")
		testutil.AssertEqual(t, len(tasks), 0, "orphan task must be deleted")
	})
}

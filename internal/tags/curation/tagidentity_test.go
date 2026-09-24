package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

type mockTaskCall struct {
	TaskType string
	BatchID  string
	Payload  json.RawMessage
	TaskID   string
	Status   string
	DedupKey string
	Source   string
}

type recordingTaskCreator struct {
	calls    []mockTaskCall
	createFn func(taskType types.TaskType, batchID string, payload json.RawMessage, taskID string, status types.TaskStatus, dedupKey string) (string, error)
}

func (m *recordingTaskCreator) CreateTask(_ context.Context, taskType types.TaskType, batchID string, payload json.RawMessage, taskID string, status types.TaskStatus, dedupKey string) (string, error) {
	if m.createFn != nil {
		return m.createFn(taskType, batchID, payload, taskID, status, dedupKey)
	}
	m.calls = append(m.calls, mockTaskCall{
		TaskType: string(taskType), BatchID: batchID, Payload: payload,
		TaskID: taskID, Status: string(status), DedupKey: dedupKey,
	})
	return taskID, nil
}

type recordingBatchCreator struct {
	calls    []mockTaskCall
	createFn func(id string, source types.BatchSource, status types.BatchStatus) error
}

func (m *recordingBatchCreator) Create(_ context.Context, id string, source types.BatchSource, status types.BatchStatus) error {
	if m.createFn != nil {
		return m.createFn(id, source, status)
	}
	m.calls = append(m.calls, mockTaskCall{
		BatchID: id, Source: string(source), Status: string(status),
	})
	return nil
}

func newTagIdentityTestClient(t *testing.T) *database.Client {
	t.Helper()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })
	return client
}

func TestTagIdentityEnqueue(t *testing.T) {
	t.Run("creates one blocking task and a queued batch", func(t *testing.T) {
		client := newTagIdentityTestClient(t)
		taskMock := &recordingTaskCreator{}
		batchMock := &recordingBatchCreator{}
		svc := NewTagIdentity(client.Queries, taskMock, batchMock)

		batchID, alreadyQueued, err := svc.Enqueue(context.Background())
		testutil.AssertNoError(t, err, "enqueue")
		if batchID == "" {
			t.Fatal("expected non-empty batch ID")
		}
		testutil.AssertEqual(t, alreadyQueued, false, "first call is not already queued")

		testutil.AssertEqual(t, len(taskMock.calls), 1, "one task created")
		testutil.AssertEqual(t, taskMock.calls[0].TaskType, "tagidentity", "task type")
		testutil.AssertEqual(t, taskMock.calls[0].Status, "pending", "task status")
		testutil.AssertEqual(t, taskMock.calls[0].DedupKey, "tagidentity", "dedup key")
		testutil.AssertEqual(t, taskMock.calls[0].BatchID, batchID, "task belongs to the batch")

		testutil.AssertEqual(t, len(batchMock.calls), 1, "one batch created")
		testutil.AssertEqual(t, batchMock.calls[0].Source, "tagidentity", "batch source")
		testutil.AssertEqual(t, batchMock.calls[0].Status, "queued", "batch status")
		testutil.AssertEqual(t, batchMock.calls[0].BatchID, batchID, "batch ID matches returned ID")
	})

	t.Run("an active batch reports already queued", func(t *testing.T) {
		client := newTagIdentityTestClient(t)
		taskMock := &recordingTaskCreator{}
		batchMock := &recordingBatchCreator{}
		svc := NewTagIdentity(client.Queries, taskMock, batchMock)

		batchSvc := service.NewBatch(client, 3)
		testutil.AssertNoError(t, batchSvc.Create(context.Background(), "existing-run", types.Batch.Source.TagIdentity, types.Batch.Status.Queued), "seed active batch")

		batchID, alreadyQueued, err := svc.Enqueue(context.Background())
		testutil.AssertNoError(t, err, "enqueue while active")
		testutil.AssertEqual(t, alreadyQueued, true, "already queued")
		if batchID != "" {
			t.Errorf("batch ID = %q, want empty when already queued", batchID)
		}
		testutil.AssertEqual(t, len(taskMock.calls), 0, "no new task while a batch is active")
		testutil.AssertEqual(t, len(batchMock.calls), 0, "no new batch while a batch is active")
	})

	t.Run("task creation failure reports already queued without a batch", func(t *testing.T) {
		client := newTagIdentityTestClient(t)
		taskMock := &recordingTaskCreator{
			createFn: func(types.TaskType, string, json.RawMessage, string, types.TaskStatus, string) (string, error) {
				return "", errors.New("dedup key in use")
			},
		}
		batchMock := &recordingBatchCreator{}
		svc := NewTagIdentity(client.Queries, taskMock, batchMock)

		batchID, alreadyQueued, err := svc.Enqueue(context.Background())
		testutil.AssertNoError(t, err, "enqueue")
		testutil.AssertEqual(t, alreadyQueued, true, "a rejected task insert means the work already exists")
		if batchID != "" {
			t.Errorf("batch ID = %q, want empty", batchID)
		}
		testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch without a task")
	})

	t.Run("batch failure deletes the orphan task", func(t *testing.T) {
		client := newTagIdentityTestClient(t)

		// an orphan pending task would be claimable by the queue's
		// tagidentity pool with a batch that never exists
		inner := &capturingTaskCreator{inner: task.NewStore(client)}
		batchMock := &recordingBatchCreator{
			createFn: func(string, types.BatchSource, types.BatchStatus) error {
				return errors.New("batch insert failed")
			},
		}
		svc := NewTagIdentity(client.Queries, inner, batchMock)

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

type capturingTaskCreator struct {
	inner    service.TaskCreator
	captured []string
}

func (c *capturingTaskCreator) CreateTask(ctx context.Context, taskType types.TaskType, batchID string, payload json.RawMessage, taskID string, status types.TaskStatus, dedupKey string) (string, error) {
	c.captured = append(c.captured, batchID)
	return c.inner.CreateTask(ctx, taskType, batchID, payload, taskID, status, dedupKey)
}

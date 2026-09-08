package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

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

func TestReEnrich_Success(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "re-test.pdf")

	taskMock := &recordingTaskCreator{}
	batchMock := &recordingBatchCreator{}
	svc := NewReEnrich(client.Queries, taskMock, batchMock)

	batchID, err := svc.ReEnrich(ctx, docUUID)
	testutil.AssertNoError(t, err, "re-enrich")
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}

	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch creation called")
	testutil.AssertEqual(t, batchMock.calls[0].Source, "reenrich", "batch source")
	testutil.AssertEqual(t, batchMock.calls[0].Status, "queued", "batch status")
	testutil.AssertEqual(t, batchMock.calls[0].BatchID, batchID, "batch ID matches returned ID")

	testutil.AssertEqual(t, len(taskMock.calls), 1, "task creation called")
	testutil.AssertEqual(t, taskMock.calls[0].TaskType, "enrich", "task type")
	testutil.AssertEqual(t, taskMock.calls[0].Status, "pending", "task status")
	testutil.AssertEqual(t, taskMock.calls[0].BatchID, batchID, "task batch ID matches")
	testutil.AssertEqual(t, taskMock.calls[0].DedupKey, "enrich:doc:"+docUUID, "dedup key format")

	var payload map[string]any
	json.Unmarshal(taskMock.calls[0].Payload, &payload)
	testutil.AssertEqual(t, payload["document_id"], docUUID, "document_id in payload")
}

func TestReEnrich_DocumentNotFound(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	taskMock := &recordingTaskCreator{}
	batchMock := &recordingBatchCreator{}
	svc := NewReEnrich(client.Queries, taskMock, batchMock)

	_, err := svc.ReEnrich(ctx, "nonexistent-uuid")
	testutil.AssertError(t, err, "not found")
	testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch on not-found")
	testutil.AssertEqual(t, len(taskMock.calls), 0, "no task on not-found")
}

func TestReEnrich_TaskCreationFails(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "fail-task.pdf")

	taskMock := &recordingTaskCreator{
		createFn: func(_ types.TaskType, _ string, _ json.RawMessage, _ string, _ types.TaskStatus, _ string) (string, error) {
			return "", fmt.Errorf("create task: unique constraint")
		},
	}
	batchMock := &recordingBatchCreator{}
	svc := NewReEnrich(client.Queries, taskMock, batchMock)

	_, err := svc.ReEnrich(ctx, docUUID)
	testutil.AssertError(t, err, "task creation error")
	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch was created before task")
}

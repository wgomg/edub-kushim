package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

type recordingTaskCreator struct {
	calls    []mockTaskCall
	createFn func(taskType, batchID string, payload json.RawMessage, taskID, status, dedupKey string) (string, error)
}

func (m *recordingTaskCreator) CreateTask(_ context.Context, taskType, batchID string, payload json.RawMessage, taskID, status, dedupKey string) (string, error) {
	if m.createFn != nil {
		return m.createFn(taskType, batchID, payload, taskID, status, dedupKey)
	}
	m.calls = append(m.calls, mockTaskCall{
		TaskType: taskType, BatchID: batchID, Payload: payload,
		TaskID: taskID, Status: status, DedupKey: dedupKey,
	})
	return taskID, nil
}

type recordingBatchCreator struct {
	calls    []mockTaskCall
	createFn func(id, source, status string) error
}

func (m *recordingBatchCreator) Create(_ context.Context, id, source, status string) error {
	if m.createFn != nil {
		return m.createFn(id, source, status)
	}
	m.calls = append(m.calls, mockTaskCall{
		BatchID: id, Source: source, Status: status,
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
		createFn: func(_, _ string, _ json.RawMessage, _, _, _ string) (string, error) {
			return "", fmt.Errorf("create task: unique constraint")
		},
	}
	batchMock := &recordingBatchCreator{}
	svc := NewReEnrich(client.Queries, taskMock, batchMock)

	_, err := svc.ReEnrich(ctx, docUUID)
	testutil.AssertError(t, err, "task creation error")
	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch was created before task")
}

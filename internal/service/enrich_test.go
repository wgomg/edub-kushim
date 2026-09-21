package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// createTestDoc creates a document with unique checksums, returning its UUID.
func createTestDoc(t *testing.T, queries *database.Queries, title string) string {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	docID := "td-" + suffix
	_, err := queries.CreateDocument(ctx, database.CreateDocumentParams{
		DocumentID:     docID,
		Title:          title,
		Md5Checksum:    "md5-" + suffix,
		Sha512Checksum: "sha512-" + suffix,
		OriginalType:   "application/pdf",
		FileSize:       1024,
		OriginalPath:   "/tmp/" + title,
		StoragePath:    "/tmp/storage/" + title,
		TextContent:    sql.NullString{String: "test content", Valid: true},
		PageCount:      1, WordCount: 5, CharCount: 35, Language: "eng",
	})
	if err != nil {
		t.Fatalf("create test doc %q: %v", title, err)
	}
	return docID
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

func TestReEnrichMany_LimitCapped(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	createTestDoc(t, client.Queries, "bulk-1.pdf")
	createTestDoc(t, client.Queries, "bulk-2.pdf")

	taskMock := &recordingTaskCreator{}
	batchMock := &recordingBatchCreator{}
	svc := NewReEnrich(client.Queries, taskMock, batchMock)

	batchID, enqueued, skipped, err := svc.ReEnrichMany(ctx, 1)
	testutil.AssertNoError(t, err, "re-enrich-many")
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}
	testutil.AssertEqual(t, enqueued, 1, "enqueued")
	testutil.AssertEqual(t, skipped, 0, "skipped")
	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch created once")
	testutil.AssertEqual(t, batchMock.calls[0].Source, "reenrich", "batch source")
	testutil.AssertEqual(t, len(taskMock.calls), 1, "task created once")
	testutil.AssertEqual(t, taskMock.calls[0].TaskType, "enrich", "task type")
	testutil.AssertEqual(t, taskMock.calls[0].Status, "pending", "task status")
}

func TestReEnrichMany_All(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	createTestDoc(t, client.Queries, "bulk-1.pdf")
	createTestDoc(t, client.Queries, "bulk-2.pdf")
	createTestDoc(t, client.Queries, "bulk-3.pdf")

	taskMock := &recordingTaskCreator{}
	batchMock := &recordingBatchCreator{}
	svc := NewReEnrich(client.Queries, taskMock, batchMock)

	batchID, enqueued, skipped, err := svc.ReEnrichMany(ctx, 0)
	testutil.AssertNoError(t, err, "re-enrich-many all")
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}
	testutil.AssertEqual(t, enqueued, 3, "enqueued all")
	testutil.AssertEqual(t, skipped, 0, "skipped")
	testutil.AssertEqual(t, len(taskMock.calls), 3, "three tasks created")
}

func TestReEnrichMany_ConflictSkipped(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	createTestDoc(t, client.Queries, "conflict.pdf")

	// simulate an existing pending enrich task via the mock returning an error
	taskMock := &recordingTaskCreator{
		createFn: func(_ types.TaskType, _ string, _ json.RawMessage, _ string, _ types.TaskStatus, _ string) (string, error) {
			return "", fmt.Errorf("create task: unique constraint violation")
		},
	}
	batchMock := &recordingBatchCreator{}
	svc := NewReEnrich(client.Queries, taskMock, batchMock)

	batchID, enqueued, skipped, err := svc.ReEnrichMany(ctx, 0)
	testutil.AssertNoError(t, err, "re-enrich-many conflict")
	testutil.AssertEqual(t, batchID, "", "no batch when all skipped")
	testutil.AssertEqual(t, enqueued, 0, "none enqueued")
	testutil.AssertEqual(t, skipped, 1, "one skipped")
	testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch created")
}

func TestReEnrichMany_BatchCreateFails(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	createTestDoc(t, client.Queries, "batch-fail-1.pdf")
	createTestDoc(t, client.Queries, "batch-fail-2.pdf")

	taskMock := &recordingTaskCreator{}
	batchMock := &recordingBatchCreator{
		createFn: func(_ string, _ types.BatchSource, _ types.BatchStatus) error {
			return fmt.Errorf("create batch: connection lost")
		},
	}
	svc := NewReEnrich(client.Queries, taskMock, batchMock)

	batchID, enqueued, skipped, err := svc.ReEnrichMany(ctx, 0)
	if err == nil {
		t.Fatal("expected error from batch creation")
	}
	testutil.AssertError(t, err, "batch creation failure")
	testutil.AssertEqual(t, batchID, "", "no batch ID on failure")
	testutil.AssertEqual(t, enqueued, 2, "two tasks created before failure")
	testutil.AssertEqual(t, skipped, 0, "no conflicts")

	// verify cleanup: no tasks should remain with the orphaned batch id
	// the batch id is captured from the mock — extract it from the mock's calls
	batchIDUsed := taskMock.calls[0].BatchID
	tasks, listErr := client.Queries.ListTasksByBatch(ctx, database.ListTasksByBatchParams{
		BatchID: sql.NullString{String: batchIDUsed, Valid: true},
		Limit:   100,
		Offset:  0,
	})
	testutil.AssertNoError(t, listErr, "list tasks after cleanup")
	testutil.AssertEqual(t, len(tasks), 0, "unbatched tasks cleaned up")
}

func TestReEnrichMany_NoDocuments(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	taskMock := &recordingTaskCreator{}
	batchMock := &recordingBatchCreator{}
	svc := NewReEnrich(client.Queries, taskMock, batchMock)

	batchID, enqueued, skipped, err := svc.ReEnrichMany(ctx, 0)
	testutil.AssertNoError(t, err, "re-enrich-many empty")
	testutil.AssertEqual(t, batchID, "", "no batch when no documents")
	testutil.AssertEqual(t, enqueued, 0, "none enqueued")
	testutil.AssertEqual(t, skipped, 0, "none skipped")
	testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch created")
}

func TestReEnrichMany_BatchIDUsedInTasks(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	docUUID := createTestDoc(t, client.Queries, "verify-bid.pdf")

	taskMock := &recordingTaskCreator{}
	batchMock := &recordingBatchCreator{}
	svc := NewReEnrich(client.Queries, taskMock, batchMock)

	batchID, _, _, err := svc.ReEnrichMany(ctx, 0)
	testutil.AssertNoError(t, err, "re-enrich-many")

	// all tasks in the run share the same batch id, which matches the returned id
	for _, call := range taskMock.calls {
		testutil.AssertEqual(t, call.BatchID, batchID, "task batch ID matches returned batch ID")
		testutil.AssertEqual(t, call.DedupKey, "enrich:doc:"+docUUID, "dedup key format")
	}
}

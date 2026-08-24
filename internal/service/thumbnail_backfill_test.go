package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func newThumbnailBackfill(t *testing.T) (*ThumbnailBackfill, *database.Client, *recordingTaskCreator, *recordingBatchCreator) {
	t.Helper()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })

	taskMock := &recordingTaskCreator{}
	batchMock := &recordingBatchCreator{}
	svc := NewThumbnailBackfill(client.Queries, testutil.NewTestLogger(), taskMock, batchMock)
	return svc, client, taskMock, batchMock
}

// createBackfillTestDoc inserts a document with unique checksums; unlike
// CreateTestDocument it can be called more than once per test.
func createBackfillTestDoc(t *testing.T, client *database.Client, title, seed string) string {
	t.Helper()
	ctx := context.Background()
	docID := uuid.New().String()
	_, err := client.Queries.CreateDocument(ctx, database.CreateDocumentParams{
		DocumentID:     docID,
		Title:          title,
		Md5Checksum:    "md5-" + seed,
		Sha512Checksum: "sha512-" + seed,
		OriginalType:   "application/pdf",
		FileSize:       1024,
		OriginalPath:   "/tmp/orig.pdf",
		StoragePath:    "/tmp/storage.pdf",
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	return docID
}

func createTestConsumeTask(t *testing.T, client *database.Client, batchID, status string, documentID string) {
	t.Helper()
	ctx := context.Background()
	raw, _ := json.Marshal(map[string]any{"document_id": documentID})
	payload := json.RawMessage(raw)
	_, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   uuid.New().String(),
		TaskType: "consume",
		Status:   status,
		BatchID:  sql.NullString{String: batchID, Valid: true},
		Payload:  &payload,
	})
	if err != nil {
		t.Fatalf("create consume task: %v", err)
	}
}

func TestThumbnailBackfill_BackfillAll_Success(t *testing.T) {
	svc, client, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	docA := createBackfillTestDoc(t, client, "a.pdf", "a")
	docB := createBackfillTestDoc(t, client, "b.pdf", "b")

	batchID, enqueued, skipped, err := svc.BackfillAll(ctx)
	testutil.AssertNoError(t, err, "backfill all")
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}
	testutil.AssertEqual(t, enqueued, 2, "enqueued count")
	testutil.AssertEqual(t, skipped, 0, "skipped count")

	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch creation called once")
	testutil.AssertEqual(t, batchMock.calls[0].Source, "thumbbackfill", "batch source")
	testutil.AssertEqual(t, batchMock.calls[0].Status, "queued", "batch status")
	testutil.AssertEqual(t, batchMock.calls[0].BatchID, batchID, "batch ID matches returned ID")

	testutil.AssertEqual(t, len(taskMock.calls), 2, "task creation called for each document")
	wantDedup := map[string]string{"thumbnail:doc:" + docA: docA, "thumbnail:doc:" + docB: docB}
	gotDedup := map[string]bool{}
	for _, call := range taskMock.calls {
		testutil.AssertEqual(t, call.TaskType, "thumbnail", "task type")
		testutil.AssertEqual(t, call.Status, "pending", "task status")
		testutil.AssertEqual(t, call.BatchID, batchID, "task batch ID matches")
		if call.DedupKey == "" {
			t.Fatal("expected a dedup key on every task")
		}
		gotDedup[call.DedupKey] = true

		var payload map[string]any
		json.Unmarshal(call.Payload, &payload)
		testutil.AssertEqual(t, payload["document_id"], wantDedup[call.DedupKey], "document_id in payload matches dedup key")
		testutil.AssertEqual(t, payload["storage_path"], "/tmp/storage.pdf", "storage_path in payload")
	}
	testutil.AssertEqual(t, len(gotDedup), 2, "distinct dedup keys")
}

func TestThumbnailBackfill_BackfillAll_NoDocuments(t *testing.T) {
	svc, _, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	batchID, enqueued, skipped, err := svc.BackfillAll(ctx)
	testutil.AssertNoError(t, err, "backfill all")
	testutil.AssertEqual(t, batchID, "", "no batch without documents")
	testutil.AssertEqual(t, enqueued, 0, "enqueued count")
	testutil.AssertEqual(t, skipped, 0, "skipped count")
	testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch created")
	testutil.AssertEqual(t, len(taskMock.calls), 0, "no tasks created")
}

func TestThumbnailBackfill_BackfillAll_SkipsPendingDuplicate(t *testing.T) {
	svc, client, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	docA := createBackfillTestDoc(t, client, "dup.pdf", "a")
	docB := createBackfillTestDoc(t, client, "ok.pdf", "b")

	taskMock.createFn = func(taskType, batchID string, payload json.RawMessage, taskID, status, dedupKey string) (string, error) {
		if dedupKey == "thumbnail:doc:"+docA {
			return "", &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "idx_task_dedup"}
		}
		taskMock.calls = append(taskMock.calls, mockTaskCall{
			TaskType: taskType, BatchID: batchID, Payload: payload, TaskID: taskID, Status: status, DedupKey: dedupKey,
		})
		return taskID, nil
	}

	batchID, enqueued, skipped, err := svc.BackfillAll(ctx)
	testutil.AssertNoError(t, err, "backfill all")
	testutil.AssertEqual(t, enqueued, 1, "enqueued count")
	testutil.AssertEqual(t, skipped, 1, "skipped count")

	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch created once")
	testutil.AssertEqual(t, batchMock.calls[0].BatchID, batchID, "batch ID matches returned ID")
	testutil.AssertEqual(t, len(taskMock.calls), 1, "only the non-duplicate task created")
	testutil.AssertEqual(t, taskMock.calls[0].DedupKey, "thumbnail:doc:"+docB, "remaining task belongs to the other document")
}

func TestThumbnailBackfill_BackfillDocument_Success(t *testing.T) {
	svc, client, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "single.pdf")

	batchID, err := svc.BackfillDocument(ctx, docUUID)
	testutil.AssertNoError(t, err, "backfill document")
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}

	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch creation called")
	testutil.AssertEqual(t, batchMock.calls[0].Source, "thumbbackfill", "batch source")
	testutil.AssertEqual(t, batchMock.calls[0].Status, "queued", "batch status")

	testutil.AssertEqual(t, len(taskMock.calls), 1, "task creation called")
	testutil.AssertEqual(t, taskMock.calls[0].TaskType, "thumbnail", "task type")
	testutil.AssertEqual(t, taskMock.calls[0].Status, "pending", "task status")
	testutil.AssertEqual(t, taskMock.calls[0].DedupKey, "thumbnail:doc:"+docUUID, "dedup key format")

	var payload map[string]any
	json.Unmarshal(taskMock.calls[0].Payload, &payload)
	testutil.AssertEqual(t, payload["document_id"], docUUID, "document_id in payload")
}

func TestThumbnailBackfill_BackfillDocument_NotFound(t *testing.T) {
	svc, _, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	_, err := svc.BackfillDocument(ctx, "nonexistent-uuid")
	testutil.AssertError(t, err, "backfill document")
	testutil.AssertEqual(t, errs.KindOf(err), errs.KindNotFound, "error kind")
	testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch on not-found")
	testutil.AssertEqual(t, len(taskMock.calls), 0, "no task on not-found")
}

func TestThumbnailBackfill_BackfillDocument_AlreadyHasThumbnail(t *testing.T) {
	svc, client, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "done.pdf")
	if err := client.Queries.SetDocumentHasThumbnail(ctx, docUUID); err != nil {
		t.Fatalf("set has_thumbnail: %v", err)
	}

	_, err := svc.BackfillDocument(ctx, docUUID)
	testutil.AssertError(t, err, "backfill document")
	testutil.AssertEqual(t, errs.KindOf(err), errs.KindConflict, "error kind")
	testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch for a document that already has a thumbnail")
	testutil.AssertEqual(t, len(taskMock.calls), 0, "no task for a document that already has a thumbnail")
}

func TestThumbnailBackfill_BackfillDocument_SurfacesPendingConflict(t *testing.T) {
	svc, client, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "queued.pdf")

	taskMock.createFn = func(_, _ string, _ json.RawMessage, _, _, dedupKey string) (string, error) {
		return "", &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "idx_task_dedup"}
	}

	_, err := svc.BackfillDocument(ctx, docUUID)
	testutil.AssertError(t, err, "backfill document")
	testutil.AssertEqual(t, errs.KindOf(err), errs.KindConflict, "dedup conflict surfaces instead of being swallowed")
	testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch when the task conflicts")
	testutil.AssertEqual(t, len(taskMock.calls), 0, "no task recorded for the failed attempt")
}

func TestThumbnailBackfill_BackfillBatch_Success(t *testing.T) {
	svc, client, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	docA := createBackfillTestDoc(t, client, "a.pdf", "a")
	docB := createBackfillTestDoc(t, client, "b.pdf", "b")
	docC := createBackfillTestDoc(t, client, "c.pdf", "c")
	docD := createBackfillTestDoc(t, client, "d.pdf", "d")
	docE := createBackfillTestDoc(t, client, "e.pdf", "e")

	const srcBatch = "src-batch"
	createTestConsumeTask(t, client, srcBatch, "completed", docA)
	createTestConsumeTask(t, client, srcBatch, "completed", docB)
	createTestConsumeTask(t, client, "other-batch", "completed", docC)
	createTestConsumeTask(t, client, srcBatch, "pending", docD)
	createTestConsumeTask(t, client, srcBatch, "completed", docE)
	if err := client.Queries.SetDocumentHasThumbnail(ctx, docE); err != nil {
		t.Fatalf("set has_thumbnail: %v", err)
	}

	newBatchID, enqueued, skipped, err := svc.BackfillBatch(ctx, srcBatch)
	testutil.AssertNoError(t, err, "backfill batch")
	if newBatchID == "" {
		t.Fatal("expected non-empty batch ID")
	}
	testutil.AssertEqual(t, enqueued, 2, "only completed consume tasks of the batch, without thumbnails")
	testutil.AssertEqual(t, skipped, 0, "skipped count")

	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch creation called once")
	testutil.AssertEqual(t, batchMock.calls[0].Source, "thumbbackfill", "batch source")
	testutil.AssertEqual(t, batchMock.calls[0].BatchID, newBatchID, "batch ID matches returned ID")

	gotDedup := map[string]bool{}
	for _, call := range taskMock.calls {
		gotDedup[call.DedupKey] = true
	}
	wantDedup := map[string]bool{"thumbnail:doc:" + docA: true, "thumbnail:doc:" + docB: true}
	testutil.AssertEqual(t, len(gotDedup), 2, "two tasks enqueued")
	for key := range wantDedup {
		if !gotDedup[key] {
			t.Errorf("missing task for %s", key)
		}
	}
}

func TestThumbnailBackfill_BackfillAll_DeletesTasksWhenBatchCreationFails(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })
	ctx := context.Background()

	createBackfillTestDoc(t, client, "a.pdf", "a")
	createBackfillTestDoc(t, client, "b.pdf", "b")

	store := task.NewStore(client.Queries)
	batchMock := &recordingBatchCreator{}
	batchMock.createFn = func(id, source, status string) error {
		batchMock.calls = append(batchMock.calls, mockTaskCall{BatchID: id, Source: source, Status: status})
		return fmt.Errorf("simulated batch insert failure")
	}
	svc := NewThumbnailBackfill(client.Queries, testutil.NewTestLogger(), store, batchMock)

	_, enqueued, _, err := svc.BackfillAll(ctx)
	testutil.AssertError(t, err, "backfill all")
	testutil.AssertEqual(t, enqueued, 2, "both tasks were inserted before the batch attempt")
	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch creation attempted once")

	count, err := client.Queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
		BatchID: sql.NullString{String: batchMock.calls[0].BatchID, Valid: true},
		Status:  "pending",
	})
	testutil.AssertNoError(t, err, "count tasks")
	testutil.AssertEqual(t, count, int64(0), "deferred cleanup deleted the unbatched tasks")
}

func TestThumbnailBackfill_BackfillDocument_DeletesTaskWhenBatchCreationFails(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "single.pdf")

	store := task.NewStore(client.Queries)
	batchMock := &recordingBatchCreator{}
	batchMock.createFn = func(id, source, status string) error {
		batchMock.calls = append(batchMock.calls, mockTaskCall{BatchID: id, Source: source, Status: status})
		return fmt.Errorf("simulated batch insert failure")
	}
	svc := NewThumbnailBackfill(client.Queries, testutil.NewTestLogger(), store, batchMock)

	_, err := svc.BackfillDocument(ctx, docUUID)
	testutil.AssertError(t, err, "backfill document")
	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch creation attempted after the task insert")

	count, err := client.Queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
		BatchID: sql.NullString{String: batchMock.calls[0].BatchID, Valid: true},
		Status:  "pending",
	})
	testutil.AssertNoError(t, err, "count tasks")
	testutil.AssertEqual(t, count, int64(0), "cleanup deleted the unbatched task")
}

func TestThumbnailBackfill_BackfillAll_ContinuesOnTaskError(t *testing.T) {
	svc, client, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	docA := createBackfillTestDoc(t, client, "fail.pdf", "a")
	docB := createBackfillTestDoc(t, client, "ok.pdf", "b")

	taskMock.createFn = func(taskType, batchID string, payload json.RawMessage, taskID, status, dedupKey string) (string, error) {
		if dedupKey == "thumbnail:doc:"+docA {
			return "", fmt.Errorf("simulated task insert failure")
		}
		taskMock.calls = append(taskMock.calls, mockTaskCall{
			TaskType: taskType, BatchID: batchID, Payload: payload, TaskID: taskID, Status: status, DedupKey: dedupKey,
		})
		return taskID, nil
	}

	batchID, enqueued, skipped, err := svc.BackfillAll(ctx)
	testutil.AssertNoError(t, err, "backfill all")
	testutil.AssertEqual(t, enqueued, 1, "the failing task did not abort the sweep")
	testutil.AssertEqual(t, skipped, 1, "the failing task is counted as skipped")
	testutil.AssertEqual(t, len(batchMock.calls), 1, "batch creation called once")
	testutil.AssertEqual(t, batchMock.calls[0].BatchID, batchID, "batch created for the remaining task")
	testutil.AssertEqual(t, len(taskMock.calls), 1, "only the successful task recorded")
	testutil.AssertEqual(t, taskMock.calls[0].DedupKey, "thumbnail:doc:"+docB, "remaining task belongs to the other document")
}

func TestThumbnailBackfill_BackfillAll_AllSkippedCreatesNoBatch(t *testing.T) {
	svc, client, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	createBackfillTestDoc(t, client, "dup1.pdf", "a")
	createBackfillTestDoc(t, client, "dup2.pdf", "b")

	taskMock.createFn = func(_, _ string, _ json.RawMessage, _, _, _ string) (string, error) {
		return "", &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "idx_task_dedup"}
	}

	batchID, enqueued, skipped, err := svc.BackfillAll(ctx)
	testutil.AssertNoError(t, err, "backfill all")
	testutil.AssertEqual(t, enqueued, 0, "enqueued count")
	testutil.AssertEqual(t, skipped, 2, "skipped count")
	testutil.AssertEqual(t, batchID, "", "no batch when nothing was enqueued")
	testutil.AssertEqual(t, len(batchMock.calls), 0, "no batch creation attempt")
	testutil.AssertEqual(t, len(taskMock.calls), 0, "no tasks recorded")
}

func TestThumbnailBackfill_BackfillAll_PaginatesAllDocuments(t *testing.T) {
	svc, client, taskMock, batchMock := newThumbnailBackfill(t)
	ctx := context.Background()

	const total = backfillPageSize + 1
	for i := range total {
		createBackfillTestDoc(t, client, "page.pdf", fmt.Sprintf("page-%d", i))
	}

	batchID, enqueued, skipped, err := svc.BackfillAll(ctx)
	testutil.AssertNoError(t, err, "backfill all")
	testutil.AssertEqual(t, enqueued, total, "all documents across pages enqueued")
	testutil.AssertEqual(t, skipped, 0, "skipped count")
	testutil.AssertEqual(t, len(batchMock.calls), 1, "one batch for the whole sweep")
	testutil.AssertEqual(t, batchMock.calls[0].BatchID, batchID, "batch ID matches returned ID")
	testutil.AssertEqual(t, len(taskMock.calls), total, "task per document")
}

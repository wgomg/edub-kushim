package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

func newThumbnailBackfill(t *testing.T) (*ThumbnailBackfill, *database.Client) {
	t.Helper()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })

	return NewThumbnailBackfill(client.Queries, newServiceEnqueuer(t, client)), client
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

func createTestConsumeTask(t *testing.T, client *database.Client, batchID string, status types.TaskStatus, documentID string) {
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

func TestThumbnailBackfill_BackfillDocument_Success(t *testing.T) {
	svc, client := newThumbnailBackfill(t)
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "single.pdf")

	batchID, err := svc.BackfillDocument(ctx, testBatchSpec(t, types.Batch.Origin.API), docUUID)
	testutil.AssertNoError(t, err, "backfill document")
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}

	batch, err := client.Queries.GetBatch(ctx, batchID)
	testutil.AssertNoError(t, err, "get batch")
	testutil.AssertEqual(t, string(batch.Origin), "api", "batch origin")

	tasks, err := client.Queries.ListTasksByBatch(ctx, database.ListTasksByBatchParams{
		BatchID: sql.NullString{String: batchID, Valid: true},
		Limit:   100,
	})
	testutil.AssertNoError(t, err, "list tasks")
	testutil.AssertEqual(t, len(tasks), 1, "one task")
	testutil.AssertEqual(t, string(tasks[0].TaskType), "thumbbackfill", "task type")
	testutil.AssertEqual(t, string(tasks[0].Status), "pending", "task status")
	testutil.AssertEqual(t, tasks[0].DedupKey.String, "thumbnail:doc:"+docUUID, "dedup key format")

	var payload map[string]any
	json.Unmarshal(*tasks[0].Payload, &payload)
	testutil.AssertEqual(t, payload["document_id"], docUUID, "document_id in payload")
}

func TestThumbnailBackfill_BackfillDocument_NotFound(t *testing.T) {
	svc, _ := newThumbnailBackfill(t)
	ctx := context.Background()

	_, err := svc.BackfillDocument(ctx, testBatchSpec(t, types.Batch.Origin.API), "nonexistent-uuid")
	testutil.AssertError(t, err, "backfill document")
	testutil.AssertEqual(t, errs.KindOf(err), errs.KindNotFound, "error kind")
}

func TestThumbnailBackfill_BackfillDocument_AlreadyHasThumbnail(t *testing.T) {
	svc, client := newThumbnailBackfill(t)
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "done.pdf")
	if err := client.Queries.SetDocumentHasThumbnail(ctx, docUUID); err != nil {
		t.Fatalf("set has_thumbnail: %v", err)
	}

	_, err := svc.BackfillDocument(ctx, testBatchSpec(t, types.Batch.Origin.API), docUUID)
	testutil.AssertError(t, err, "backfill document")
	testutil.AssertEqual(t, errs.KindOf(err), errs.KindConflict, "error kind")
}

func TestThumbnailBackfill_BackfillDocument_SurfacesPendingConflict(t *testing.T) {
	svc, client := newThumbnailBackfill(t)
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "queued.pdf")
	payload, _ := json.Marshal(map[string]any{"document_id": docUUID})
	_, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "pre-existing-thumb",
		TaskType: types.Task.Type.ThumbBackfill,
		Status:   types.Task.Status.Pending,
		Payload:  (*json.RawMessage)(&payload),
		DedupKey: sql.NullString{String: "thumbnail:doc:" + docUUID, Valid: true},
	})
	testutil.AssertNoError(t, err, "seed pending thumbnail task")

	_, err = svc.BackfillDocument(ctx, testBatchSpec(t, types.Batch.Origin.API), docUUID)
	testutil.AssertError(t, err, "backfill document")
	testutil.AssertEqual(t, errs.KindOf(err), errs.KindConflict, "dedup conflict surfaces instead of being swallowed")
}

func TestThumbnailBackfill_BackfillBatch_Success(t *testing.T) {
	svc, client := newThumbnailBackfill(t)
	ctx := context.Background()

	docA := createBackfillTestDoc(t, client, "a.pdf", "a")
	docB := createBackfillTestDoc(t, client, "b.pdf", "b")
	docC := createBackfillTestDoc(t, client, "c.pdf", "c")
	docD := createBackfillTestDoc(t, client, "d.pdf", "d")
	docE := createBackfillTestDoc(t, client, "e.pdf", "e")

	const srcBatch = "src-batch"
	createTestConsumeTask(t, client, srcBatch, types.Task.Status.Completed, docA)
	createTestConsumeTask(t, client, srcBatch, types.Task.Status.Completed, docB)
	createTestConsumeTask(t, client, "other-batch", types.Task.Status.Completed, docC)
	createTestConsumeTask(t, client, srcBatch, types.Task.Status.Pending, docD)
	createTestConsumeTask(t, client, srcBatch, types.Task.Status.Completed, docE)
	if err := client.Queries.SetDocumentHasThumbnail(ctx, docE); err != nil {
		t.Fatalf("set has_thumbnail: %v", err)
	}

	newBatchID, enqueued, skipped, err := svc.BackfillBatch(ctx, testBatchSpec(t, types.Batch.Origin.CLI), srcBatch)
	testutil.AssertNoError(t, err, "backfill batch")
	if newBatchID == "" {
		t.Fatal("expected non-empty batch ID")
	}
	testutil.AssertEqual(t, enqueued, 2, "only completed consume tasks of the batch, without thumbnails")
	testutil.AssertEqual(t, skipped, 0, "skipped count")

	tasks, err := client.Queries.ListTasksByBatch(ctx, database.ListTasksByBatchParams{
		BatchID: sql.NullString{String: newBatchID, Valid: true},
		Limit:   100,
	})
	testutil.AssertNoError(t, err, "list tasks")
	gotDedup := map[string]bool{}
	for _, created := range tasks {
		gotDedup[created.DedupKey.String] = true
	}
	testutil.AssertEqual(t, len(gotDedup), 2, "two tasks enqueued")
	for _, key := range []string{"thumbnail:doc:" + docA, "thumbnail:doc:" + docB} {
		if !gotDedup[key] {
			t.Errorf("missing task for %s", key)
		}
	}
}

func TestThumbnailBackfill_BackfillBatch_AllSkippedCreatesNoBatch(t *testing.T) {
	svc, client := newThumbnailBackfill(t)
	ctx := context.Background()

	doc := createBackfillTestDoc(t, client, "dup.pdf", "a")
	createTestConsumeTask(t, client, "src-batch", types.Task.Status.Completed, doc)

	payload, _ := json.Marshal(map[string]any{"document_id": doc})
	_, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "pre-existing-thumb",
		TaskType: types.Task.Type.ThumbBackfill,
		Status:   types.Task.Status.Pending,
		Payload:  (*json.RawMessage)(&payload),
		DedupKey: sql.NullString{String: "thumbnail:doc:" + doc, Valid: true},
	})
	testutil.AssertNoError(t, err, "seed pending thumbnail task")

	batchID, enqueued, skipped, err := svc.BackfillBatch(ctx, testBatchSpec(t, types.Batch.Origin.CLI), "src-batch")
	testutil.AssertNoError(t, err, "backfill batch")
	testutil.AssertEqual(t, enqueued, 0, "enqueued count")
	testutil.AssertEqual(t, skipped, 1, "skipped count")
	testutil.AssertEqual(t, batchID, "", "no batch when nothing was enqueued")
}

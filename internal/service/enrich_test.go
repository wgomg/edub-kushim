package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
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

// serviceTestCatalog is the task type table the service tests enqueue
// against: identity plus the dedup derivations the services rely on.
type serviceTestCatalog struct {
	defs map[types.TaskType]task.TypeDefinition
}

func (c serviceTestCatalog) Definition(taskType types.TaskType) (task.TypeDefinition, error) {
	def, ok := c.defs[taskType]
	if !ok {
		return task.TypeDefinition{}, fmt.Errorf("unknown task type: %q", taskType)
	}
	return def, nil
}

func newServiceEnqueuer(_ *testing.T, client *database.Client) *task.Enqueuer {
	documentID := func(payload json.RawMessage) string {
		var p struct {
			DocumentID string `json:"document_id"`
		}
		_ = json.Unmarshal(payload, &p)
		return p.DocumentID
	}
	md5Key := func(payload json.RawMessage) string {
		var p struct {
			MD5 string `json:"md5"`
		}
		_ = json.Unmarshal(payload, &p)
		if p.MD5 == "" {
			return ""
		}
		return "consume:" + p.MD5
	}
	enrichKey := func(payload json.RawMessage) string {
		if id := documentID(payload); id != "" {
			return "enrich:doc:" + id
		}
		return ""
	}
	thumbnailKey := func(payload json.RawMessage) string {
		if id := documentID(payload); id != "" {
			return "thumbnail:doc:" + id
		}
		return ""
	}

	catalog := serviceTestCatalog{defs: map[types.TaskType]task.TypeDefinition{
		types.Task.Type.Consume:       {Type: types.Task.Type.Consume, DedupKey: md5Key},
		types.Task.Type.Enrich:        {Type: types.Task.Type.Enrich, DedupKey: enrichKey},
		types.Task.Type.Thumbnail:     {Type: types.Task.Type.Thumbnail, DedupKey: thumbnailKey},
		types.Task.Type.ThumbBackfill: {Type: types.Task.Type.ThumbBackfill, DedupKey: thumbnailKey},
	}}
	return task.NewEnqueuer(client, catalog)
}

func testBatchSpec(t *testing.T, origin types.BatchOrigin) task.BatchSpec {
	t.Helper()
	spec, err := task.NewBatchSpec(origin, "")
	if err != nil {
		t.Fatalf("build spec: %v", err)
	}
	return spec
}

func TestReEnrich_Success(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "re-test.pdf")

	svc := NewReEnrich(client.Queries, newServiceEnqueuer(t, client))

	batchID, err := svc.ReEnrich(ctx, testBatchSpec(t, types.Batch.Origin.API), docUUID)
	testutil.AssertNoError(t, err, "re-enrich")
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}

	batch, err := client.Queries.GetBatch(ctx, batchID)
	testutil.AssertNoError(t, err, "get batch")
	testutil.AssertEqual(t, string(batch.Origin), "api", "batch origin")
	testutil.AssertEqual(t, string(batch.Status), "queued", "batch status")

	tasks, err := client.Queries.ListTasksByBatch(ctx, database.ListTasksByBatchParams{
		BatchID: sql.NullString{String: batchID, Valid: true},
		Limit:   100,
	})
	testutil.AssertNoError(t, err, "list tasks")
	testutil.AssertEqual(t, len(tasks), 1, "one enrich task")
	testutil.AssertEqual(t, string(tasks[0].TaskType), "enrich", "task type")
	testutil.AssertEqual(t, string(tasks[0].Status), "pending", "task status")
	testutil.AssertEqual(t, tasks[0].DedupKey.String, "enrich:doc:"+docUUID, "dedup key format")

	var payload map[string]any
	json.Unmarshal(*tasks[0].Payload, &payload)
	testutil.AssertEqual(t, payload["document_id"], docUUID, "document_id in payload")
}

func TestReEnrich_DocumentNotFound(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	svc := NewReEnrich(client.Queries, newServiceEnqueuer(t, client))

	_, err := svc.ReEnrich(ctx, testBatchSpec(t, types.Batch.Origin.API), "nonexistent-uuid")
	testutil.AssertError(t, err, "not found")

	overviews, listErr := client.Queries.ListBatchOverviews(ctx, database.ListBatchOverviewsParams{
		RowLimit:          10,
		StaleAfterSeconds: 15,
	})
	testutil.AssertNoError(t, listErr, "list batches")
	testutil.AssertEqual(t, len(overviews), 0, "no batch on not-found")
}

func TestReEnrich_DuplicateIsConflict(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	_, docUUID := database.CreateTestDocument(t, client.Queries, "dup-re.pdf")
	svc := NewReEnrich(client.Queries, newServiceEnqueuer(t, client))
	spec := testBatchSpec(t, types.Batch.Origin.API)

	_, err := svc.ReEnrich(ctx, spec, docUUID)
	testutil.AssertNoError(t, err, "first enqueue")

	_, err = svc.ReEnrich(ctx, spec, docUUID)
	testutil.AssertError(t, err, "second enqueue for the same document conflicts")
}

func TestReEnrichMany_LimitCapped(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	createTestDoc(t, client.Queries, "bulk-1.pdf")
	createTestDoc(t, client.Queries, "bulk-2.pdf")

	svc := NewReEnrich(client.Queries, newServiceEnqueuer(t, client))

	batchID, enqueued, skipped, err := svc.ReEnrichMany(ctx, testBatchSpec(t, types.Batch.Origin.CLI), 1)
	testutil.AssertNoError(t, err, "re-enrich-many")
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}
	testutil.AssertEqual(t, enqueued, 1, "enqueued")
	testutil.AssertEqual(t, skipped, 0, "skipped")

	tasks, err := client.Queries.ListTasksByBatch(ctx, database.ListTasksByBatchParams{
		BatchID: sql.NullString{String: batchID, Valid: true},
		Limit:   100,
	})
	testutil.AssertNoError(t, err, "list tasks")
	testutil.AssertEqual(t, len(tasks), 1, "one task")
	testutil.AssertEqual(t, string(tasks[0].TaskType), "enrich", "task type")
}

func TestReEnrichMany_All(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	createTestDoc(t, client.Queries, "bulk-1.pdf")
	createTestDoc(t, client.Queries, "bulk-2.pdf")
	createTestDoc(t, client.Queries, "bulk-3.pdf")

	svc := NewReEnrich(client.Queries, newServiceEnqueuer(t, client))

	batchID, enqueued, skipped, err := svc.ReEnrichMany(ctx, testBatchSpec(t, types.Batch.Origin.CLI), 0)
	testutil.AssertNoError(t, err, "re-enrich-many all")
	if batchID == "" {
		t.Fatal("expected non-empty batch ID")
	}
	testutil.AssertEqual(t, enqueued, 3, "enqueued all")
	testutil.AssertEqual(t, skipped, 0, "skipped")
}

func TestReEnrichMany_ConflictSkipped(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	docUUID := createTestDoc(t, client.Queries, "conflict.pdf")

	// a pending enrich task for the same document makes the derive key collide
	payload, _ := json.Marshal(map[string]any{"document_id": docUUID})
	_, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "pre-existing-enrich",
		TaskType: types.Task.Type.Enrich,
		Status:   types.Task.Status.Pending,
		Payload:  (*json.RawMessage)(&payload),
		DedupKey: sql.NullString{String: "enrich:doc:" + docUUID, Valid: true},
	})
	testutil.AssertNoError(t, err, "seed pending enrich task")

	svc := NewReEnrich(client.Queries, newServiceEnqueuer(t, client))

	batchID, enqueued, skipped, err := svc.ReEnrichMany(ctx, testBatchSpec(t, types.Batch.Origin.CLI), 0)
	testutil.AssertNoError(t, err, "re-enrich-many conflict")
	testutil.AssertEqual(t, batchID, "", "no batch when all skipped")
	testutil.AssertEqual(t, enqueued, 0, "none enqueued")
	testutil.AssertEqual(t, skipped, 1, "one skipped")
}

func TestReEnrichMany_NoDocuments(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	svc := NewReEnrich(client.Queries, newServiceEnqueuer(t, client))

	batchID, enqueued, skipped, err := svc.ReEnrichMany(ctx, testBatchSpec(t, types.Batch.Origin.CLI), 0)
	testutil.AssertNoError(t, err, "re-enrich-many empty")
	testutil.AssertEqual(t, batchID, "", "no batch when no documents")
	testutil.AssertEqual(t, enqueued, 0, "none enqueued")
	testutil.AssertEqual(t, skipped, 0, "none skipped")
}

func TestReEnrichMany_TasksShareBatch(t *testing.T) {
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	defer client.DB().Close()
	ctx := context.Background()

	docUUID := createTestDoc(t, client.Queries, "verify-bid.pdf")

	svc := NewReEnrich(client.Queries, newServiceEnqueuer(t, client))

	batchID, _, _, err := svc.ReEnrichMany(ctx, testBatchSpec(t, types.Batch.Origin.CLI), 0)
	testutil.AssertNoError(t, err, "re-enrich-many")

	tasks, err := client.Queries.ListTasksByBatch(ctx, database.ListTasksByBatchParams{
		BatchID: sql.NullString{String: batchID, Valid: true},
		Limit:   100,
	})
	testutil.AssertNoError(t, err, "list tasks")
	testutil.AssertEqual(t, len(tasks), 1, "one task in the batch")
	testutil.AssertEqual(t, tasks[0].BatchID.String, batchID, "task batch ID matches returned batch ID")
	testutil.AssertEqual(t, tasks[0].DedupKey.String, "enrich:doc:"+docUUID, "dedup key format")
}

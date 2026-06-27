package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
)

func TestCreateAndGetDocument(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	_, docID := CreateTestDocument(t, q, "test.pdf")
	doc, err := q.GetDocument(ctx, docID)
	assertNoError(t, err, "get")
	assertEqual(t, doc.Title, "test.pdf", "title")
}

func TestDuplicateChecks(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	md5 := "d41d8cd98f00b204e9800998ecf8427e"
	n, _ := q.GetDocumentByMD5Checksum(ctx, md5)
	assertEqual(t, len(n), 0, "none before")
	insertDoc(t, q, "dup.pdf", md5, "")
	n, _ = q.GetDocumentByMD5Checksum(ctx, md5)
	assertEqual(t, len(n), 1, "found after")
}

func TestUpdateDeleteDocument(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	_, docID := CreateTestDocument(t, q, "up.pdf")
	err := q.UpdateDocumentEditable(ctx, UpdateDocumentEditableParams{Title: "renamed.pdf", DocumentTypeID: 1, Language: "spa", DocumentID: docID})
	assertNoError(t, err, "update")
	assertNoError(t, q.DeleteDocument(ctx, docID), "delete")
}

func TestListDocuments(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	insertDoc(t, q, "aa.pdf", "md5-a", "sha512-a")
	insertDoc(t, q, "bbb.pdf", "md5-b", "sha512-b")
	docs, _ := q.ListDocumentsWithSort(ctx, ListDocumentsWithSortParams{Limit: 10, Offset: 0, SortBy: "created_at", SortOrder: "desc"})
	assertEqual(t, len(docs), 2, "count")
}

func TestTagCRUD(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	_, err := q.CreateTag(ctx, "my-tag")
	assertNoError(t, err, "create")
	tag, _ := q.GetTagByName(ctx, "my-tag")
	assertNoError(t, q.UpdateTag(ctx, UpdateTagParams{Name: "renamed", ID: tag.ID}), "rename")
	assertNoError(t, q.DeleteTag(ctx, tag.ID), "delete")
}

func TestTagSearch(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	for _, n := range []string{"zzz-finance", "zzz-financial", "fishing"} {
		q.CreateTag(ctx, n)
	}
	r, _ := q.SearchTagsByName(ctx, SearchTagsByNameParams{Name: "zzz-%", Limit: 10, Offset: 0})
	assertEqual(t, len(r), 2, "matches")
}

func TestDocumentTags(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	dID, _ := CreateTestDocument(t, q, "tags.pdf")
	tag := SeedTagByName(t, q, "")
	assertNoError(t, q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: dID, TagID: tag.ID}), "add")
	tags, _ := q.GetDocumentTags(ctx, dID)
	assertEqual(t, len(tags), 1, "tag")
	assertNoError(t, q.RemoveDocumentTag(ctx, RemoveDocumentTagParams{DocumentID: dID, TagID: tag.ID}), "remove")
}

func TestTaskLifecycle(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	id := insertTask(t, q, "tc-1", "pending")
	task, _ := q.GetTask(ctx, id)
	assertEqual(t, task.Status, "pending", "pending")
	rows, _ := q.ClaimTask(ctx, id)
	assertEqual(t, rows, int64(1), "claimed")
	task, _ = q.GetTask(ctx, id)
	assertEqual(t, task.Status, "processing", "processing")
	result := json.RawMessage(`{"ok":true}`)
	assertNoError(t, q.CompleteTask(ctx, CompleteTaskParams{ID: id, Result: &result}), "complete")
	task, _ = q.GetTask(ctx, id)
	assertEqual(t, task.Status, "completed", "completed")
}

func TestTaskRetry(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	id := insertTask(t, q, "ret-1", "failed")
	assertNoError(t, q.RetryTask(ctx, id), "retry")
	task, _ := q.GetTask(ctx, id)
	assertEqual(t, task.Status, "pending", "after retry")
}

func TestEnrichWaitingFlow(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	id := insertEnrichTask(t, q, "ew-1", "waiting")
	assertNoError(t, q.SetEnrichTaskPending(ctx, SetEnrichTaskPendingParams{
		ID: id, Payload: []byte(`{"document_id":"doc-1"}`),
	}), "set pending")
	task, _ := q.GetTask(ctx, id)
	assertEqual(t, task.Status, "pending", "pending")
	assertNoError(t, q.DiscardEnrichTask(ctx, DiscardEnrichTaskParams{
		ID: id, Error: sql.NullString{String: "parent failed", Valid: true},
	}), "discard")
}

func TestDocumentTypeCRUD(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	types, _ := q.ListAllDocumentTypes(ctx)
	assertEqual(t, len(types) > 0, true, "seeded")
	res, _ := q.CreateDocumentTypeFull(ctx, CreateDocumentTypeFullParams{Name: "custom", Description: "C"})
	id := getID(t, res)
	assertNoError(t, q.UpdateDocumentTypeFull(ctx, UpdateDocumentTypeFullParams{Name: "renamed", Description: "R", ID: id}), "update")
	assertNoError(t, q.DeleteDocumentType(ctx, id), "delete")
}

func TestPeopleCRUD(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	res, _ := q.CreatePeople(ctx, CreatePeopleParams{Name: "Alice"})
	pID := getID(t, res)
	assertNoError(t, q.UpdatePeopleFull(ctx, UpdatePeopleFullParams{Name: "Alice U", ID: pID}), "update")
	assertNoError(t, q.DeletePeople(ctx, pID), "delete")
}

func TestPeopleTypeCRUD(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	res, _ := q.CreatePeopleType(ctx, CreatePeopleTypeParams{Name: "reviewer"})
	id := getID(t, res)
	assertNoError(t, q.UpdatePeopleType(ctx, UpdatePeopleTypeParams{Name: "sr-reviewer", Description: "S", ID: id}), "update")
	assertNoError(t, q.DeletePeopleType(ctx, id), "delete")
}

func TestSavedSearchCRUD(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	_, err := q.CreateSavedSearch(ctx, CreateSavedSearchParams{Name: "My S", FilterJson: `{"q":"t"}`})
	assertNoError(t, err, "create")
	ss, _ := q.ListSavedSearches(ctx)
	assertEqual(t, len(ss), 1, "count")
	assertNoError(t, q.DeleteSavedSearch(ctx, ss[0].ID), "delete")
}

func TestBatchOwnerOps(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	_, err := q.CreateBatch(ctx, CreateBatchParams{ID: "bo-test", Source: "test"})
	assertNoError(t, err, "create batch")
	_, err = q.TryInsertBatchOwner(ctx, TryInsertBatchOwnerParams{BatchID: "bo-test", OwnerID: "o1", Pid: 123})
	assertNoError(t, err, "insert owner")
	q.ReleaseBatchOwner(ctx, ReleaseBatchOwnerParams{BatchID: "bo-test", OwnerID: "o1"})
}

func TestDocumentTagsPeople(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	dID, _ := CreateTestDocument(t, q, "tp.pdf")
	tag := SeedTagByName(t, q, "")
	q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: dID, TagID: tag.ID})
	tags, _ := q.GetDocumentTags(ctx, dID)
	assertEqual(t, len(tags), 1, "tag")

	res, err := q.CreatePeople(ctx, CreatePeopleParams{Name: "Bob"})
	assertNoError(t, err, "create people")
	pID := getID(t, res)
	ptRes, err := q.CreatePeopleType(ctx, CreatePeopleTypeParams{Name: "custom-author-type"})
	assertNoError(t, err, "create people type")
	ptID := getID(t, ptRes)
	q.AddDocumentPeople(ctx, AddDocumentPeopleParams{DocumentID: dID, PeopleID: pID, PeopleTypeID: ptID})
	ppl, _ := q.GetDocumentPeopleWithType(ctx, dID)
	assertEqual(t, len(ppl), 1, "person")
}

func TestListActivityTimeline(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	events, err := q.ListActivityTimeline(ctx)
	assertNoError(t, err, "empty timeline")
	assertEqual(t, len(events), 0, "no events initially")

	_, _ = CreateTestDocument(t, q, "activity-test.pdf")

	_, err = q.CreateBatch(ctx, CreateBatchParams{ID: "act-batch-1", Source: "test-upload"})
	assertNoError(t, err, "create batch")

	taskID1 := "act-task-completed"
	res, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID: taskID1, TaskType: "consume", Status: "pending",
		Payload: json.RawMessage(`{"file_name":"report.pdf","document_id":"doc-uuid-1"}`),
	})
	assertNoError(t, err, "create completed task")
	id1 := getID(t, res)
	assertNoError(t, q.CompleteTask(ctx, CompleteTaskParams{ID: id1, Result: nil}), "complete task")

	taskID2 := "act-task-failed"
	res2, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID: taskID2, TaskType: "consume", Status: "pending",
		Payload: json.RawMessage(`{"file_path":"/tmp/uploads/invoice.pdf","document_id":"doc-uuid-2"}`),
	})
	assertNoError(t, err, "create failed task")
	id2 := getID(t, res2)
	assertNoError(t, q.FailTask(ctx, FailTaskParams{
		ID: id2, Error: sql.NullString{String: "processing error", Valid: true},
	}), "fail task")

	events, err = q.ListActivityTimeline(ctx)
	assertNoError(t, err, "list timeline")
	if len(events) < 4 {
		t.Fatalf("expected at least 4 events, got %d", len(events))
	}

	for i := 1; i < len(events); i++ {
		if events[i-1].EventTime < events[i].EventTime {
			t.Fatalf("events not in descending order: %s < %s", events[i-1].EventTime, events[i].EventTime)
		}
	}

	typeCounts := map[string]int{}
	for _, e := range events {
		typeCounts[e.EventType]++
	}
	for _, et := range []string{"document_uploaded", "batch_created", "task_completed", "task_failed"} {
		if typeCounts[et] == 0 {
			t.Fatalf("expected at least one %q event", et)
		}
	}

	var foundCompleted bool
	for _, e := range events {
		if e.EventType == "task_completed" && e.TaskID == taskID1 {
			foundCompleted = true
			assertEqual(t, e.Title, "report.pdf", "completed task title from file_name")
			assertEqual(t, e.RefID, "doc-uuid-1", "completed task ref_id from document_id")
			break
		}
	}
	if !foundCompleted {
		t.Fatal("did not find task_completed event with task_id", taskID1)
	}

	var foundFailed bool
	for _, e := range events {
		if e.EventType == "task_failed" && e.TaskID == taskID2 {
			foundFailed = true
			assertEqual(t, e.Title, "", "failed task title from empty file_name")
			assertEqual(t, e.PayloadFilePath, "/tmp/uploads/invoice.pdf", "failed task payload_file_path")
			assertEqual(t, e.RefID, "doc-uuid-2", "failed task ref_id")
			break
		}
	}
	if !foundFailed {
		t.Fatal("did not find task_failed event with task_id", taskID2)
	}

	var foundBatch bool
	for _, e := range events {
		if e.EventType == "batch_created" && e.BatchID == "act-batch-1" {
			foundBatch = true
			assertEqual(t, e.Title, "test-upload", "batch title from source")
			assertEqual(t, e.RefID, "act-batch-1", "batch ref_id")
			assertEqual(t, e.BatchID, "act-batch-1", "batch batch_id")
			assertEqual(t, e.TaskID, "", "batch task_id empty")
			break
		}
	}
	if !foundBatch {
		t.Fatal("did not find batch_created event with batch_id act-batch-1")
	}
}

func TestListTasksByType(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	insertTask(t, q, "lt-1", "pending")
	tasks, _ := q.ListTasksByType(ctx, ListTasksByTypeParams{TaskType: "consume", Limit: 10, Offset: 0})
	assertEqual(t, len(tasks), 1, "one task")
}

// --- helpers ---

func insertDoc(t *testing.T, q *Queries, title, md5, sha512 string) (int64, string) {
	t.Helper()
	if md5 == "" {
		md5 = fmt.Sprintf("md5-%d", len(title))
	}
	if sha512 == "" {
		sha512 = fmt.Sprintf("sha512-%d", len(title))
	}
	docID := fmt.Sprintf("doc-%d", len(title))
	types, _ := q.ListAllDocumentTypes(context.Background())
	dtID := int64(1)
	if len(types) > 0 {
		dtID = types[0].ID
	}
	res, err := q.CreateDocument(context.Background(), CreateDocumentParams{
		DocumentID: docID, Title: title,
		Md5Checksum: md5, Sha512Checksum: sha512,
		MimeType: "application/pdf", FileSize: 100,
		OriginalPath: "/tmp/" + title, StoragePath: "/tmp/storage/" + title,
		TextContent: sql.NullString{String: "content", Valid: true},
		PageCount:   1, WordCount: 1, CharCount: 7, Language: "eng",
	})
	assertNoError(t, err, "create doc")
	id := getID(t, res)
	_ = dtID
	return id, docID
}

func insertEnrichTask(t *testing.T, q *Queries, taskID, status string) int64 {
	t.Helper()
	res, err := q.CreateTask(context.Background(), CreateTaskParams{
		TaskID: taskID, TaskType: "enrich", Status: status, Payload: []byte(`{}`),
	})
	assertNoError(t, err, "insert enrich "+taskID)
	return getID(t, res)
}

func insertTask(t *testing.T, q *Queries, taskID, status string) int64 {
	t.Helper()
	res, err := q.CreateTask(context.Background(), CreateTaskParams{
		TaskID: taskID, TaskType: "consume", Status: status, Payload: []byte(`{}`),
	})
	assertNoError(t, err, "insert "+taskID)
	return getID(t, res)
}

func getID(t *testing.T, res sql.Result) int64 {
	t.Helper()
	if res == nil {
		t.Fatal("expected non-nil sql.Result")
	}
	id, err := res.LastInsertId()
	assertNoError(t, err, "last insert id")
	return id
}

func assertEqual(t *testing.T, got, want any, msg string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", msg, got, want)
	}
}

func assertNoError(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", msg, err)
	}
}

func assertError(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error, got nil", msg)
	}
}

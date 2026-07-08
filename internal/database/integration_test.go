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
	res, _ := q.CreatePeople(ctx, CreatePeopleParams{Name: "Alice", NormalizedName: "alice"})
	pID := getID(t, res)
	assertNoError(t, q.UpdatePeopleFull(ctx, UpdatePeopleFullParams{Name: "Alice U", NormalizedName: "alice u", ID: pID}), "update")
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
	_, err := q.CreateBatch(ctx, CreateBatchParams{ID: "bo-test", Source: "test", Status: "queued"})
	assertNoError(t, err, "create batch")
	_, err = q.TryInsertBatchOwner(ctx, TryInsertBatchOwnerParams{BatchID: "bo-test", OwnerID: "o1", Pid: 123})
	assertNoError(t, err, "insert owner")
	q.ReleaseBatchOwner(ctx, ReleaseBatchOwnerParams{BatchID: "bo-test", OwnerID: "o1"})
}

func TestDeleteBatchOwnerByBatchID(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	_, err := q.CreateBatch(ctx, CreateBatchParams{ID: "del-test", Source: "test", Status: "queued"})
	assertNoError(t, err, "create batch")
	_, err = q.TryInsertBatchOwner(ctx, TryInsertBatchOwnerParams{BatchID: "del-test", OwnerID: "o1", Pid: 100})
	assertNoError(t, err, "insert owner")

	rows, err := q.DeleteBatchOwnerByBatchID(ctx, "del-test")
	assertNoError(t, err, "delete by batch id")
	assertEqual(t, rows, int64(1), "one row deleted")

	_, err = q.GetBatchOwner(ctx, "del-test")
	assertEqual(t, err, sql.ErrNoRows, "owner gone after delete")

	rows, err = q.DeleteBatchOwnerByBatchID(ctx, "del-test")
	assertNoError(t, err, "delete non-existent")
	assertEqual(t, rows, int64(0), "no rows for missing owner")
}

func TestDocumentTagsPeople(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	dID, _ := CreateTestDocument(t, q, "tp.pdf")
	tag := SeedTagByName(t, q, "")
	q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: dID, TagID: tag.ID})
	tags, _ := q.GetDocumentTags(ctx, dID)
	assertEqual(t, len(tags), 1, "tag")

	res, err := q.CreatePeople(ctx, CreatePeopleParams{Name: "Bob", NormalizedName: "bob"})
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

	_, err = q.CreateBatch(ctx, CreateBatchParams{ID: "act-batch-1", Source: "test-upload", Status: "queued"})
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

func TestGetQuarantinedConsumeTaskPayloads(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	_, err := q.CreateBatch(ctx, CreateBatchParams{ID: "gq-batch", Source: "test", Status: "processing"})
	assertNoError(t, err, "create batch")

	res, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID:   "gq-quarantined",
		TaskType: "consume",
		Status:   "pending",
		Payload:  json.RawMessage(`{"file_path":"/tmp/test.pdf","on_completed":"enrich-1"}`),
		BatchID:  sql.NullString{String: "gq-batch", Valid: true},
	})
	assertNoError(t, err, "create task")
	id := getID(t, res)

	assertNoError(t, q.FailTask(ctx, FailTaskParams{
		ID:    id,
		Error: sql.NullString{String: "Max retries exceeded (3)", Valid: true},
	}), "fail task")

	res2, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID:   "gq-other-failed",
		TaskType: "consume",
		Status:   "pending",
		Payload:  json.RawMessage(`{}`),
		BatchID:  sql.NullString{String: "gq-batch", Valid: true},
	})
	assertNoError(t, err, "create other task")
	id2 := getID(t, res2)

	assertNoError(t, q.FailTask(ctx, FailTaskParams{
		ID:    id2,
		Error: sql.NullString{String: "some other error", Valid: true},
	}), "fail other task")

	rows, err := q.GetQuarantinedConsumeTaskPayloads(ctx, sql.NullString{String: "gq-batch", Valid: true})
	assertNoError(t, err, "get quarantined")
	assertEqual(t, len(rows), 1, "only quarantine-matched task")
	assertEqual(t, rows[0].TaskID, "gq-quarantined", "task id")
}

func TestDiscardEnrichTaskByTaskID(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	_, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID:   "de-by-tid",
		TaskType: "enrich",
		Status:   "waiting",
		Payload:  json.RawMessage(`{}`),
	})
	assertNoError(t, err, "create enrich task")

	n, err := q.DiscardEnrichTaskByTaskID(ctx, DiscardEnrichTaskByTaskIDParams{
		TaskID: "de-by-tid",
		Error:  sql.NullString{String: "parent task quarantined", Valid: true},
	})
	assertNoError(t, err, "discard by task id")
	assertEqual(t, n, int64(1), "one row affected")

	task, err := q.GetTaskByTaskID(ctx, "de-by-tid")
	assertNoError(t, err, "get task")
	assertEqual(t, task.Status, "discarded", "status")

	n, err = q.DiscardEnrichTaskByTaskID(ctx, DiscardEnrichTaskByTaskIDParams{
		TaskID: "de-by-tid",
		Error:  sql.NullString{String: "parent task quarantined", Valid: true},
	})
	assertNoError(t, err, "discard again (idempotent)")
	assertEqual(t, n, int64(0), "no rows on second call")
}

func TestListTasksByType(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	insertTask(t, q, "lt-1", "pending")
	tasks, _ := q.ListTasksByType(ctx, ListTasksByTypeParams{TaskType: "consume", Limit: 10, Offset: 0})
	assertEqual(t, len(tasks), 1, "one task")
}

func TestAnalyticsQueries(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	t.Run("empty database", func(t *testing.T) {
		langs, err := q.LanguageDistribution(ctx)
		assertNoError(t, err, "language distribution")
		assertEqual(t, len(langs), 0, "no languages")

		types, err := q.DocumentTypeDistribution(ctx)
		assertNoError(t, err, "document type distribution")
		assertEqual(t, len(types), 0, "no doc types")

		tags, err := q.TagFrequency(ctx)
		assertNoError(t, err, "tag frequency")
		assertEqual(t, len(tags), 0, "no tags")

		missing, err := q.MissingCounts(ctx)
		assertNoError(t, err, "missing counts")
		assertEqual(t, missing.MissingLanguage, int64(0), "missing language")
		assertEqual(t, missing.MissingType, int64(0), "missing type")
		assertEqual(t, missing.MissingTags, int64(0), "missing tags")
	})

	t.Run("with mixed data", func(t *testing.T) {
		d1, _ := CreateTestDocument(t, q, "eng-doc.pdf")

		_, err := q.db.ExecContext(ctx,
			`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"analytics-d2", "spa-article.pdf", "m2", "s2", "text/plain", 500,
			"/tmp/spa.pdf", "/tmp/spa-storage.pdf", 2, 10, 50, "spa", 3,
		)
		assertNoError(t, err, "insert spa article doc")

		_, err = q.db.ExecContext(ctx,
			`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"analytics-d3", "fra-book.pdf", "m3", "s3", "application/pdf", 800,
			"/tmp/fra.pdf", "/tmp/fra-storage.pdf", 4, 20, 100, "fra", 4,
		)
		assertNoError(t, err, "insert fra book doc")

		_, err = q.db.ExecContext(ctx,
			`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, page_count, word_count, char_count, language)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"analytics-d4", "und-doc.pdf", "m4", "s4", "text/html", 100,
			"/tmp/und.pdf", "/tmp/und-storage.pdf", 1, 3, 15, "und",
		)
		assertNoError(t, err, "insert und doc")

		financeTag, err := q.GetTagByName(ctx, "finance")
		assertNoError(t, err, "get finance tag")
		assertNoError(t, q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: d1, TagID: financeTag.ID}), "tag d1 finance")

		d2Row := q.db.QueryRowContext(ctx, `SELECT id FROM document WHERE document_id = 'analytics-d2'`)
		var d2DBID int64
		assertNoError(t, d2Row.Scan(&d2DBID), "get d2 db id")
		assertNoError(t, q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: d2DBID, TagID: financeTag.ID}), "tag d2 finance")

		urgentRes, err := q.CreateTag(ctx, "urgent")
		assertNoError(t, err, "create urgent tag")
		urgentID := getID(t, urgentRes)
		assertNoError(t, q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: d1, TagID: urgentID}), "tag d1 urgent")

		_, err = q.CreateTag(ctx, "unused")
		assertNoError(t, err, "create unused tag")

		langs, err := q.LanguageDistribution(ctx)
		assertNoError(t, err, "language distribution")
		assertEqual(t, len(langs), 3, "three determined languages")

		langSet := map[string]int64{}
		for _, l := range langs {
			langSet[l.Label] = l.Count
		}
		_, engOk := langSet["eng"]
		_, spaOk := langSet["spa"]
		_, fraOk := langSet["fra"]
		_, undOk := langSet["und"]
		assertEqual(t, engOk, true, "eng present")
		assertEqual(t, spaOk, true, "spa present")
		assertEqual(t, fraOk, true, "fra present")
		assertEqual(t, undOk, false, "und excluded")

		docTypes, err := q.DocumentTypeDistribution(ctx)
		assertNoError(t, err, "document type distribution")
		assertEqual(t, len(docTypes), 2, "two determined doc types")

		typeSet := map[string]int64{}
		for _, dt := range docTypes {
			typeSet[dt.Label] = dt.Count
		}
		_, articleOk := typeSet["article"]
		_, bookOk := typeSet["book"]
		_, undeterminedOk := typeSet["undetermined"]
		assertEqual(t, articleOk, true, "article present")
		assertEqual(t, bookOk, true, "book present")
		assertEqual(t, undeterminedOk, false, "undetermined excluded")

		tags, err := q.TagFrequency(ctx)
		assertNoError(t, err, "tag frequency")
		assertEqual(t, len(tags), 2, "two used tags")

		tagSet := map[string]int64{}
		for _, tg := range tags {
			tagSet[tg.Label] = tg.Count
		}
		assertEqual(t, tagSet["finance"], int64(2), "finance count")
		assertEqual(t, tagSet["urgent"], int64(1), "urgent count")
		_, unusedOk := tagSet["unused"]
		assertEqual(t, unusedOk, false, "unused excluded")

		assertEqual(t, tags[0].Label, "finance", "first tag by frequency")
		assertEqual(t, tags[1].Label, "urgent", "second tag by frequency")

		missing, err := q.MissingCounts(ctx)
		assertNoError(t, err, "missing counts")
		assertEqual(t, missing.MissingLanguage, int64(1), "one missing language")
		assertEqual(t, missing.MissingType, int64(2), "two missing types")
		assertEqual(t, missing.MissingTags, int64(2), "two missing tags")
	})
}

func TestStructuredSearchMissingFilters(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	tag := SeedTagByName(t, q, "")

	// Document with language='eng', type=article (ID=3), tagged
	d1, err := q.db.ExecContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"msf-1", "regular.pdf", "mdf-1", "sf-1", "application/pdf", 1000,
		"/tmp/regular.pdf", "/tmp/storage1.pdf", 1, 10, 50, "eng", 3,
	)
	assertNoError(t, err, "create regular doc")
	regularID := getID(t, d1)
	q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: regularID, TagID: tag.ID})

	// Document with language='und', type=article (ID=3), untagged
	d2, err := q.db.ExecContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"msf-2", "und-doc.pdf", "mdf-2", "sf-2", "application/pdf", 2000,
		"/tmp/und.pdf", "/tmp/storage2.pdf", 2, 20, 100, "und", 3,
	)
	assertNoError(t, err, "create und doc")
	undID := getID(t, d2)
	_ = undID

	// Document with language='eng', type=undetermined (ID=1), untagged
	d3, err := q.db.ExecContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"msf-3", "typed-doc.pdf", "mdf-3", "sf-3", "application/pdf", 3000,
		"/tmp/typed.pdf", "/tmp/storage3.pdf", 3, 30, 150, "eng", 1,
	)
	assertNoError(t, err, "create typed doc")
	typedID := getID(t, d3)
	_ = typedID

	// Document with language='', type=article, untagged (empty string language)
	d4, err := q.db.ExecContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"msf-4", "empty-lang.pdf", "mdf-4", "sf-4", "application/pdf", 4000,
		"/tmp/empty.pdf", "/tmp/storage4.pdf", 4, 40, 200, "", 3,
	)
	assertNoError(t, err, "create empty-lang doc")
	emptyLangID := getID(t, d4)
	_ = emptyLangID

	// Document with language='eng', type=article, untagged
	d5, err := q.db.ExecContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"msf-5", "untagged.pdf", "mdf-5", "sf-5", "application/pdf", 5000,
		"/tmp/untagged.pdf", "/tmp/storage5.pdf", 5, 50, 250, "eng", 3,
	)
	assertNoError(t, err, "create untagged doc")
	untaggedID := getID(t, d5)
	_ = untaggedID

	t.Run("MissingLanguage filters lang=und and lang=empty", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:           100,
			MissingLanguage: true,
		})
		assertNoError(t, err, "search missing language")
		assertEqual(t, len(results), 2, "two docs with missing language")
		ids := map[string]bool{}
		for _, r := range results {
			ids[r.DocumentID] = true
		}
		assertEqual(t, ids["msf-2"], true, "und doc found")
		assertEqual(t, ids["msf-4"], true, "empty-lang doc found")
	})

	t.Run("MissingType filters document_type_id=1", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:      100,
			MissingType: true,
		})
		assertNoError(t, err, "search missing type")
		assertEqual(t, len(results), 1, "one doc with missing type")
		assertEqual(t, results[0].DocumentID, "msf-3", "typed doc found")
	})

	t.Run("Untagged filters docs without tags", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:    100,
			Untagged: true,
		})
		assertNoError(t, err, "search untagged")
		assertEqual(t, len(results), 4, "four untagged docs")
		ids := map[string]bool{}
		for _, r := range results {
			ids[r.DocumentID] = true
		}
		assertEqual(t, ids["msf-1"], false, "tagged doc excluded")
		assertEqual(t, ids["msf-2"], true, "und doc included")
		assertEqual(t, ids["msf-3"], true, "typed doc included")
		assertEqual(t, ids["msf-4"], true, "empty-lang doc included")
		assertEqual(t, ids["msf-5"], true, "untagged doc included")
	})

	t.Run("MissingLanguage+Untagged combined", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:           100,
			MissingLanguage: true,
			Untagged:        true,
		})
		assertNoError(t, err, "search combined")
		assertEqual(t, len(results), 2, "two docs match both")
		ids := map[string]bool{}
		for _, r := range results {
			ids[r.DocumentID] = true
		}
		assertEqual(t, ids["msf-2"], true, "und+untagged doc")
		assertEqual(t, ids["msf-4"], true, "empty-lang+untagged doc")
	})

	t.Run("Count matches SearchDocumentsStructured", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:           100,
			MissingLanguage: true,
			Untagged:        true,
		})
		assertNoError(t, err, "search")
		count, err := q.CountDocumentsStructured(ctx, SearchFilter{
			Limit:           100,
			MissingLanguage: true,
			Untagged:        true,
		})
		assertNoError(t, err, "count")
		assertEqual(t, count, int64(len(results)), "count matches search")
	})

	t.Run("Missing filters with other constraints", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:           100,
			MissingLanguage: true,
			MimeType:        "text/plain",
		})
		assertNoError(t, err, "search missing lang + mime")
		assertEqual(t, len(results), 0, "no text/plain docs with missing lang")
	})
}

func TestWithDocumentCountQueries(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	d1Res, err := q.CreateDocument(ctx, CreateDocumentParams{
		DocumentID: "wdc-1", Title: "count-test-1.pdf",
		Md5Checksum: "md5-wdc1", Sha512Checksum: "sha512-wdc1",
		MimeType: "application/pdf", FileSize: 100,
		OriginalPath: "/tmp/count1.pdf", StoragePath: "/tmp/sc1.pdf",
		PageCount: 1, WordCount: 1, CharCount: 5, Language: "eng",
	})
	assertNoError(t, err, "create doc 1")
	d1 := getID(t, d1Res)

	d2Res, err := q.CreateDocument(ctx, CreateDocumentParams{
		DocumentID: "wdc-2", Title: "count-test-2.pdf",
		Md5Checksum: "md5-wdc2", Sha512Checksum: "sha512-wdc2",
		MimeType: "application/pdf", FileSize: 200,
		OriginalPath: "/tmp/count2.pdf", StoragePath: "/tmp/sc2.pdf",
		PageCount: 2, WordCount: 2, CharCount: 10, Language: "eng",
	})
	assertNoError(t, err, "create doc 2")
	d2 := getID(t, d2Res)

	tag := SeedTagByName(t, q, "")
	q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: d1, TagID: tag.ID})
	q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: d2, TagID: tag.ID})

	t.Run("ListTagsWithDocumentCount", func(t *testing.T) {
		tags, err := q.ListTagsWithDocumentCount(ctx, ListTagsWithDocumentCountParams{Limit: 100, Offset: 0})
		assertNoError(t, err, "list tags with count")
		found := false
		for _, tg := range tags {
			if tg.ID == tag.ID {
				assertEqual(t, tg.DocumentCount > int64(0), true, "tag has document count")
				found = true
			}
		}
		assertEqual(t, found, true, "tag found")
	})

	t.Run("SearchTagsByNameWithDocumentCount", func(t *testing.T) {
		tags, err := q.SearchTagsByNameWithDocumentCount(ctx, SearchTagsByNameWithDocumentCountParams{
			Name:   "%finance%", Limit: 10, Offset: 0,
		})
		assertNoError(t, err, "search tags with count")
		for _, tg := range tags {
			assertEqual(t, tg.DocumentCount >= int64(0), true, "count is non-negative")
		}
	})

	t.Run("ListPeopleWithDocumentCount", func(t *testing.T) {
		ppl, err := q.ListPeopleWithDocumentCount(ctx, ListPeopleWithDocumentCountParams{Limit: 100, Offset: 0})
		assertNoError(t, err, "list people with count")
		_ = ppl
	})

	t.Run("SearchPeopleByNameWithDocumentCount", func(t *testing.T) {
		ppl, err := q.SearchPeopleByNameWithDocumentCount(ctx, SearchPeopleByNameWithDocumentCountParams{
			Name: "%alice%", Limit: 10,
		})
		assertNoError(t, err, "search people with count")
		_ = ppl
	})

	t.Run("ListDocumentTypesWithDocumentCount", func(t *testing.T) {
		dts, err := q.ListDocumentTypesWithDocumentCount(ctx, ListDocumentTypesWithDocumentCountParams{Limit: 100, Offset: 0})
		assertNoError(t, err, "list doc types with count")
		assertEqual(t, len(dts) > 0, true, "has seeded types")
	})

	t.Run("SearchDocumentTypeByNameWithDocumentCount", func(t *testing.T) {
		dts, err := q.SearchDocumentTypeByNameWithDocumentCount(ctx, SearchDocumentTypeByNameWithDocumentCountParams{
			Name: "%article%", Limit: 10,
		})
		assertNoError(t, err, "search doc types with count")
		assertEqual(t, len(dts) > 0, true, "found article type")
	})

	t.Run("ListAllDocumentTypesWithDocumentCount", func(t *testing.T) {
		dts, err := q.ListAllDocumentTypesWithDocumentCount(ctx)
		assertNoError(t, err, "list all doc types with count")
		assertEqual(t, len(dts) > 0, true, "has seeded types")
		for _, dt := range dts {
			assertEqual(t, dt.DocumentCount >= int64(0), true, "count non-negative")
		}
	})
}

func TestTaskHealthQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()

	t.Run("empty database", func(t *testing.T) {
		rate, err := q.TaskSuccessRate(ctx)
		assertNoError(t, err, "task success rate")
		assertEqual(t, rate.Completed, int64(0), "completed")
		assertEqual(t, rate.Failed, int64(0), "failed")

		dur, err := q.AvgTaskDurationMs(ctx)
		assertNoError(t, err, "avg duration")
		assertEqual(t, dur.AvgDurationMs, int64(0), "avg duration ms")

		ids, err := q.ActiveBatchIDs(ctx)
		assertNoError(t, err, "active batch ids")
		assertEqual(t, len(ids), 0, "no active batches")
	})

	t.Run("with mixed tasks", func(t *testing.T) {
		r1, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-completed", TaskType: "consume", Status: "pending",
		})
		assertNoError(t, err, "create completed task")
		id1 := getID(t, r1)
		assertNoError(t, q.CompleteTask(ctx, CompleteTaskParams{ID: id1, Result: nil}), "complete")

		r2, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-failed", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "th-batch-1", Valid: true},
		})
		assertNoError(t, err, "create failed task")
		id2 := getID(t, r2)
		assertNoError(t, q.FailTask(ctx, FailTaskParams{ID: id2, Error: sql.NullString{String: "x", Valid: true}}), "fail")

		_, err = q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-pending", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "th-batch-1", Valid: true},
		})
		assertNoError(t, err, "create pending task")

		r4, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-processing", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "th-batch-2", Valid: true},
		})
		assertNoError(t, err, "create processing task")
		id4 := getID(t, r4)
		rows, err := q.ClaimTask(ctx, id4)
		assertNoError(t, err, "claim processing task")
		assertEqual(t, rows, int64(1), "claimed")

		rate, err := q.TaskSuccessRate(ctx)
		assertNoError(t, err, "task success rate")
		assertEqual(t, rate.Completed, int64(1), "completed")
		assertEqual(t, rate.Failed, int64(1), "failed")

		dur, err := q.AvgTaskDurationMs(ctx)
		assertNoError(t, err, "avg duration")
		assertEqual(t, dur.AvgDurationMs, int64(0), "avg duration ms")

		ids, err := q.ActiveBatchIDs(ctx)
		assertNoError(t, err, "active batch ids")
		assertEqual(t, len(ids), 2, "two active batches")
		found := map[string]bool{}
		for _, id := range ids {
			found[id] = true
		}
		if !found["th-batch-1"] {
			t.Fatal("expected th-batch-1 in active batches")
		}
		if !found["th-batch-2"] {
			t.Fatal("expected th-batch-2 in active batches")
		}
	})
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

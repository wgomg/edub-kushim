package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
)

func TestCreateAndGetDocument(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	_, docID := CreateTestDocument(t, q, "test.pdf")
	doc, err := q.GetDocument(ctx, docID)
	assertNoError(t, err, "get")
	assertEqual(t, doc.Title, "test.pdf", "title")
}

func TestDuplicateChecks(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	md5 := "d41d8cd98f00b204e9800998ecf8427e"
	n, _ := q.GetDocumentByMD5Checksum(ctx, md5)
	assertEqual(t, len(n), 0, "none before")
	insertDoc(t, q, "dup.pdf", md5, "")
	n, _ = q.GetDocumentByMD5Checksum(ctx, md5)
	assertEqual(t, len(n), 1, "found after")
}

func TestUpdateDeleteDocument(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	_, docID := CreateTestDocument(t, q, "up.pdf")
	err := q.UpdateDocumentEditable(ctx, UpdateDocumentEditableParams{Title: "renamed.pdf", DocumentTypeID: 1, Language: "spa", DocumentID: docID})
	assertNoError(t, err, "update")
	assertNoError(t, q.SoftDeleteDocument(ctx, SoftDeleteDocumentParams{
		DocumentID: docID, OriginalPath: "/tmp/orig.pdf", StoragePath: "/tmp/storage.pdf",
	}), "soft delete")
	_, err = q.GetDocument(ctx, docID)
	assertEqual(t, err, sql.ErrNoRows, "doc gone after soft delete")
}

func TestListDocuments(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
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
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
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
	_, err := q.CompleteTask(ctx, CompleteTaskParams{ID: id, Result: &result})
	assertNoError(t, err, "complete")
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
	ewPayload := json.RawMessage(`{"document_id":"doc-1"}`)
	assertNoError(t, q.SetEnrichTaskPending(ctx, SetEnrichTaskPendingParams{
		ID: id, Payload: &ewPayload,
	}), "set pending")
	task, _ := q.GetTask(ctx, id)
	assertEqual(t, task.Status, "pending", "pending")
	_, err := q.DiscardEnrichTask(ctx, DiscardEnrichTaskParams{
		ID: id, Error: sql.NullString{String: "parent failed", Valid: true},
	})
	assertNoError(t, err, "discard")
}

func TestDocumentTypeCRUD(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	types, _ := q.ListAllDocumentTypes(ctx)
	assertEqual(t, len(types) > 0, true, "seeded")
	id, _ := q.CreateDocumentTypeFull(ctx, CreateDocumentTypeFullParams{Name: "custom", Description: "C"})
	assertNoError(t, q.UpdateDocumentTypeFull(ctx, UpdateDocumentTypeFullParams{Name: "renamed", Description: "R", ID: id}), "update")
	assertNoError(t, q.DeleteDocumentType(ctx, id), "delete")
}

func TestPeopleCRUD(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	pID, _ := q.CreatePeople(ctx, CreatePeopleParams{Name: "Alice", NormalizedName: "alice"})
	assertNoError(t, q.UpdatePeopleFull(ctx, UpdatePeopleFullParams{Name: "Alice U", NormalizedName: "alice u", ID: pID}), "update")
	assertNoError(t, q.DeletePeople(ctx, pID), "delete")
}

func TestPeopleTypeCRUD(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	id, _ := q.CreatePeopleType(ctx, CreatePeopleTypeParams{Name: "reviewer"})
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
	err := q.CreateBatch(ctx, CreateBatchParams{ID: "bo-test", Source: "test", Status: "queued"})
	assertNoError(t, err, "create batch")
	_, err = q.TryInsertBatchOwner(ctx, TryInsertBatchOwnerParams{BatchID: "bo-test", OwnerID: "o1", Pid: 123})
	assertNoError(t, err, "insert owner")
	q.ReleaseBatchOwner(ctx, ReleaseBatchOwnerParams{BatchID: "bo-test", OwnerID: "o1"})
}

func TestDeleteBatchOwnerByBatchID(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	err := q.CreateBatch(ctx, CreateBatchParams{ID: "del-test", Source: "test", Status: "queued"})
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
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	dID, _ := CreateTestDocument(t, q, "tp.pdf")
	tag := SeedTagByName(t, q, "")
	q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: dID, TagID: tag.ID})
	tags, _ := q.GetDocumentTags(ctx, dID)
	assertEqual(t, len(tags), 1, "tag")

	pID, err := q.CreatePeople(ctx, CreatePeopleParams{Name: "Bob", NormalizedName: "bob"})
	assertNoError(t, err, "create people")
	ptID, err := q.CreatePeopleType(ctx, CreatePeopleTypeParams{Name: "custom-author-type"})
	assertNoError(t, err, "create people type")
	q.AddDocumentPeople(ctx, AddDocumentPeopleParams{DocumentID: dID, PeopleID: pID, PeopleTypeID: ptID})
	ppl, _ := q.GetDocumentPeopleWithType(ctx, dID)
	assertEqual(t, len(ppl), 1, "person")
}

func TestGetQuarantinedConsumeTaskPayloads(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	err := q.CreateBatch(ctx, CreateBatchParams{ID: "gq-batch", Source: "test", Status: "processing"})
	assertNoError(t, err, "create batch")

	gqPayload := json.RawMessage(`{"file_path":"/tmp/test.pdf","on_completed":"enrich-1"}`)
	id, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID:   "gq-task",
		TaskType: "consume",
		Status:   "pending",
		Payload:  &gqPayload,
		BatchID:  sql.NullString{String: "gq-batch", Valid: true},
	})
	assertNoError(t, err, "create task")

	assertNoError(t, q.FailTask(ctx, FailTaskParams{
		ID:    id,
		Error: sql.NullString{String: "Max retries exceeded (3)", Valid: true},
	}), "fail task")

	gqPayload2 := json.RawMessage(`{}`)
	id2, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID:   "gq-other-failed",
		TaskType: "consume",
		Status:   "pending",
		Payload:  &gqPayload2,
		BatchID:  sql.NullString{String: "gq-batch", Valid: true},
	})
	assertNoError(t, err, "create other task")

	assertNoError(t, q.FailTask(ctx, FailTaskParams{
		ID:    id2,
		Error: sql.NullString{String: "some other error", Valid: true},
	}), "fail other task")

	rows, err := q.GetQuarantinedConsumeTaskPayloads(ctx, sql.NullString{String: "gq-batch", Valid: true})
	assertNoError(t, err, "get quarantined")
	assertEqual(t, len(rows), 1, "only quarantine-matched task")
	assertEqual(t, rows[0].TaskID, "gq-task", "task id")
}

func TestDiscardEnrichTaskByTaskID(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	dePayload := json.RawMessage(`{}`)
	_, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID:   "de-by-tid",
		TaskType: "enrich",
		Status:   "waiting",
		Payload:  &dePayload,
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

func TestSetEnrichTaskWaiting(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	// happy path: discarded enrich is restored to waiting, error cleared
	id := insertEnrichTask(t, q, "sew-1", "waiting")
	_, err := q.DiscardEnrichTask(ctx, DiscardEnrichTaskParams{
		ID: id, Error: sql.NullString{String: "parent failed", Valid: true},
	})
	assertNoError(t, err, "discard")
	task, _ := q.GetTask(ctx, id)
	assertEqual(t, task.Status, "discarded", "discarded")
	assertEqual(t, task.Error.String, "parent failed", "error recorded")

	n, err := q.SetEnrichTaskWaiting(ctx, "sew-1")
	assertNoError(t, err, "restore")
	assertEqual(t, n, int64(1), "one row restored")
	task, _ = q.GetTask(ctx, id)
	assertEqual(t, task.Status, "waiting", "waiting after restore")
	if task.Error.Valid {
		t.Fatal("error should be cleared after restore")
	}
	if task.CompletedAt.Valid {
		t.Fatal("completed_at should be cleared after restore")
	}

	// error path: only 'discarded' enriches match — a waiting one is a no-op
	n, err = q.SetEnrichTaskWaiting(ctx, "sew-1")
	assertNoError(t, err, "restore again")
	assertEqual(t, n, int64(0), "waiting enrich not restored again")

	// re-failure after restore: the 'waiting' guard is satisfied again
	_, err = q.DiscardEnrichTask(ctx, DiscardEnrichTaskParams{
		ID: id, Error: sql.NullString{String: "parent failed again", Valid: true},
	})
	assertNoError(t, err, "re-discard after restore")
	task, _ = q.GetTask(ctx, id)
	assertEqual(t, task.Status, "discarded", "discarded again")
}

func TestRestoreDiscardedEnrichTasks(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	insertEnrichTask(t, q, "rde-e1", "discarded")
	consumePayload := json.RawMessage(`{"on_completed":"rde-e1"}`)
	_, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID: "rde-c1", TaskType: "consume", Status: "pending", Payload: &consumePayload,
	})
	assertNoError(t, err, "create pending consume")

	n, err := q.RestoreDiscardedEnrichTasks(ctx)
	assertNoError(t, err, "global restore")
	assertEqual(t, n, int64(1), "one enrich restored")
	task, _ := q.GetTaskByTaskID(ctx, "rde-e1")
	assertEqual(t, task.Status, "waiting", "enrich restored to waiting")

	t.Run("skips enrich whose consume is not pending", func(t *testing.T) {
		q, _ := NewTestQueries(t)
		ctx := context.Background()

		insertEnrichTask(t, q, "rde-e2", "discarded")
		consumePayload := json.RawMessage(`{"on_completed":"rde-e2"}`)
		_, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "rde-c2", TaskType: "consume", Status: "failed", Payload: &consumePayload,
		})
		assertNoError(t, err, "create failed consume")

		n, err := q.RestoreDiscardedEnrichTasks(ctx)
		assertNoError(t, err, "global restore")
		assertEqual(t, n, int64(0), "no enrich restored")
		task, _ := q.GetTaskByTaskID(ctx, "rde-e2")
		assertEqual(t, task.Status, "discarded", "enrich stays discarded")
	})

	t.Run("batch variant is scoped to the batch", func(t *testing.T) {
		q, _ := NewTestQueries(t)
		ctx := context.Background()

		insertEnrichTask(t, q, "rde-e3", "discarded")
		inBatchPayload := json.RawMessage(`{"on_completed":"rde-e3"}`)
		_, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "rde-c3", TaskType: "consume", Status: "pending",
			Payload: &inBatchPayload, BatchID: sql.NullString{String: "b1", Valid: true},
		})
		assertNoError(t, err, "create pending consume in b1")

		insertEnrichTask(t, q, "rde-e4", "discarded")
		outBatchPayload := json.RawMessage(`{"on_completed":"rde-e4"}`)
		_, err = q.CreateTask(ctx, CreateTaskParams{
			TaskID: "rde-c4", TaskType: "consume", Status: "pending",
			Payload: &outBatchPayload, BatchID: sql.NullString{String: "b2", Valid: true},
		})
		assertNoError(t, err, "create pending consume in b2")

		n, err := q.RestoreDiscardedEnrichTasksByBatch(ctx, sql.NullString{String: "b1", Valid: true})
		assertNoError(t, err, "batch restore")
		assertEqual(t, n, int64(1), "only b1 enrich restored")
		task, _ := q.GetTaskByTaskID(ctx, "rde-e3")
		assertEqual(t, task.Status, "waiting", "b1 enrich restored")
		task, _ = q.GetTaskByTaskID(ctx, "rde-e4")
		assertEqual(t, task.Status, "discarded", "b2 enrich untouched")
	})
}

func TestDiscardWaitingEnrichesOfFailedConsumes(t *testing.T) {
	q, db := NewTestQueries(t)
	ctx := context.Background()

	// happy path: failed consume discards its waiting enrich, copying the parent error
	insertEnrichTask(t, q, "swe-e1", "waiting")
	failedPayload := json.RawMessage(`{"on_completed":"swe-e1"}`)
	_, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID: "swe-c1", TaskType: "consume", Status: "failed",
		Payload: &failedPayload, BatchID: sql.NullString{String: "b1", Valid: true},
	})
	assertNoError(t, err, "create failed consume")
	_, err = db.ExecContext(ctx, "UPDATE task SET error = 'Max retries exceeded (3)' WHERE task_id = 'swe-c1'")
	assertNoError(t, err, "set parent error")

	// no false positive: pending consume keeps its waiting enrich
	insertEnrichTask(t, q, "swe-e2", "waiting")
	pendingPayload := json.RawMessage(`{"on_completed":"swe-e2"}`)
	_, err = q.CreateTask(ctx, CreateTaskParams{
		TaskID: "swe-c2", TaskType: "consume", Status: "pending",
		Payload: &pendingPayload, BatchID: sql.NullString{String: "b1", Valid: true},
	})
	assertNoError(t, err, "create pending consume")

	n, err := q.DiscardWaitingEnrichesOfFailedConsumes(ctx, sql.NullString{String: "b1", Valid: true})
	assertNoError(t, err, "batch sweep")
	assertEqual(t, n, int64(1), "one enrich discarded")
	task, _ := q.GetTaskByTaskID(ctx, "swe-e1")
	assertEqual(t, task.Status, "discarded", "enrich of failed consume discarded")
	assertEqual(t, task.Error.String, "Max retries exceeded (3)", "parent error copied")
	task, _ = q.GetTaskByTaskID(ctx, "swe-e2")
	assertEqual(t, task.Status, "waiting", "enrich of pending consume untouched")

	t.Run("global variant discards across batches", func(t *testing.T) {
		q, _ := NewTestQueries(t)
		ctx := context.Background()

		insertEnrichTask(t, q, "swe-e3", "waiting")
		globalPayload := json.RawMessage(`{"on_completed":"swe-e3"}`)
		_, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "swe-c3", TaskType: "consume", Status: "failed",
			Payload: &globalPayload, BatchID: sql.NullString{String: "b9", Valid: true},
		})
		assertNoError(t, err, "create failed consume")

		n, err := q.DiscardWaitingEnrichesOfFailedConsumesGlobal(ctx)
		assertNoError(t, err, "global sweep")
		assertEqual(t, n, int64(1), "one enrich discarded")
		task, _ := q.GetTaskByTaskID(ctx, "swe-e3")
		assertEqual(t, task.Status, "discarded", "enrich discarded by global sweep")
	})
}

func TestListTasksByType(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	insertTask(t, q, "lt-1", "pending")
	tasks, _ := q.ListTasksByType(ctx, ListTasksByTypeParams{TaskType: "consume", Limit: 10, Offset: 0})
	assertEqual(t, len(tasks), 1, "one task")
}

func TestAnalyticsQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
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

		var spaID int64
		err := q.db.QueryRowContext(ctx,
			`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
			"analytics-d2", "spa-article.pdf", "m2", "s2", "text/plain", 500,
			"/tmp/spa.pdf", "/tmp/spa-storage.pdf", 2, 10, 50, "spa", 3,
		).Scan(&spaID)
		assertNoError(t, err, "insert spa article doc")

		var fraID int64
		err = q.db.QueryRowContext(ctx,
			`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
			"analytics-d3", "fra-book.pdf", "m3", "s3", "application/pdf", 800,
			"/tmp/fra.pdf", "/tmp/fra-storage.pdf", 4, 20, 100, "fra", 4,
		).Scan(&fraID)
		assertNoError(t, err, "insert fra book doc")

		var undID int64
		err = q.db.QueryRowContext(ctx,
			`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path, page_count, word_count, char_count, language)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id`,
			"analytics-d4", "und-doc.pdf", "m4", "s4", "text/html", 100,
			"/tmp/und.pdf", "/tmp/und-storage.pdf", 1, 3, 15, "und",
		).Scan(&undID)
		assertNoError(t, err, "insert und doc")

		financeTag, err := q.GetTagByName(ctx, "finance")
		assertNoError(t, err, "get finance tag")
		assertNoError(t, q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: d1, TagID: financeTag.ID}), "tag d1 finance")

		d2Row := q.db.QueryRowContext(ctx, `SELECT id FROM document WHERE document_id = 'analytics-d2'`)
		var d2DBID int64
		assertNoError(t, d2Row.Scan(&d2DBID), "get d2 db id")
		assertNoError(t, q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: d2DBID, TagID: financeTag.ID}), "tag d2 finance")

		urgentID, err := q.CreateTag(ctx, "urgent")
		assertNoError(t, err, "create urgent tag")
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

func TestLanguageDistributionLimit(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	for i := range 12 {
		lang := fmt.Sprintf("l%02d", i)
		_, err := q.db.ExecContext(ctx,
			`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path, page_count, word_count, char_count, language)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			fmt.Sprintf("langlim-%d", i), fmt.Sprintf("doc%d.pdf", i), fmt.Sprintf("md5-%d", i), fmt.Sprintf("sha-%d", i),
			"application/pdf", 100, fmt.Sprintf("/tmp/o%d.pdf", i), fmt.Sprintf("/tmp/s%d.pdf", i),
			1, 5, 25, lang,
		)
		assertNoError(t, err, "insert doc with language")
	}

	langs, err := q.LanguageDistribution(ctx)
	assertNoError(t, err, "language distribution")
	assertEqual(t, len(langs), 10, "limited to 10 languages")
}

func TestStructuredSearchMissingFilters(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	tag := SeedTagByName(t, q, "")

	// Document with language='eng', type=article (ID=3), tagged
	var regularID int64
	err := q.db.QueryRowContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		"msf-1", "regular.pdf", "mdf-1", "sf-1", "application/pdf", 1000,
		"/tmp/regular.pdf", "/tmp/storage1.pdf", 1, 10, 50, "eng", 3,
	).Scan(&regularID)
	assertNoError(t, err, "create regular doc")
	q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: regularID, TagID: tag.ID})

	// Document with language='und', type=article (ID=3), untagged
	var undID int64
	err = q.db.QueryRowContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		"msf-2", "und-doc.pdf", "mdf-2", "sf-2", "application/pdf", 2000,
		"/tmp/und.pdf", "/tmp/storage2.pdf", 2, 20, 100, "und", 3,
	).Scan(&undID)
	assertNoError(t, err, "create und doc")
	_ = undID

	// Document with language='eng', type=undetermined (ID=1), untagged
	var typedID int64
	err = q.db.QueryRowContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		"msf-3", "typed-doc.pdf", "mdf-3", "sf-3", "application/pdf", 3000,
		"/tmp/typed.pdf", "/tmp/storage3.pdf", 3, 30, 150, "eng", 1,
	).Scan(&typedID)
	assertNoError(t, err, "create typed doc")
	_ = typedID

	// Document with language='', type=article, untagged (empty string language)
	var emptyLangID int64
	err = q.db.QueryRowContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		"msf-4", "empty-lang.pdf", "mdf-4", "sf-4", "application/pdf", 4000,
		"/tmp/empty.pdf", "/tmp/storage4.pdf", 4, 40, 200, "", 3,
	).Scan(&emptyLangID)
	assertNoError(t, err, "create empty-lang doc")
	_ = emptyLangID

	// Document with language='eng', type=article, untagged
	var untaggedID int64
	err = q.db.QueryRowContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		"msf-5", "untagged.pdf", "mdf-5", "sf-5", "application/pdf", 5000,
		"/tmp/untagged.pdf", "/tmp/storage5.pdf", 5, 50, 250, "eng", 3,
	).Scan(&untaggedID)
	assertNoError(t, err, "create untagged doc")
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
			Limit:       100,
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

}

func TestStructuredSearchPeopleFilter(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	mariaID, err := q.CreatePeople(ctx, CreatePeopleParams{Name: "Maria", NormalizedName: "maria"})
	assertNoError(t, err, "create Maria")
	juanID, err := q.CreatePeople(ctx, CreatePeopleParams{Name: "Juan", NormalizedName: "juan"})
	assertNoError(t, err, "create Juan")

	authorTypeID, err := q.CreatePeopleType(ctx, CreatePeopleTypeParams{Name: "test-author"})
	assertNoError(t, err, "create author type")
	recipientTypeID, err := q.CreatePeopleType(ctx, CreatePeopleTypeParams{Name: "test-recipient"})
	assertNoError(t, err, "create recipient type")

	createDoc := func(id, title string) int64 {
		t.Helper()
		docID, err := q.CreateDocument(ctx, CreateDocumentParams{
			DocumentID: id, Title: title,
			Md5Checksum: "md5-" + id, Sha512Checksum: "sha512-" + id,
			OriginalType: "application/pdf", FileSize: 1000,
			OriginalPath: "/tmp/" + id + ".pdf", StoragePath: "/tmp/" + id + "-s.pdf",
			TextContent: sql.NullString{String: title, Valid: true},
			PageCount: 1, WordCount: 1, CharCount: 5, Language: "eng",
		})
		assertNoError(t, err, "create "+id)
		return docID
	}

	const (
		docAUUID = "pf-a"
		docBUUID = "pf-b"
		docCUUID = "pf-c"
		docDUUID = "pf-d"
	)
	docAID := createDoc(docAUUID, "maria-author.pdf")
	docBID := createDoc(docBUUID, "maria-recipient.pdf")
	docCID := createDoc(docCUUID, "juan-author.pdf")
	createDoc(docDUUID, "no-people.pdf")

	assertNoError(t, q.AddDocumentPeople(ctx, AddDocumentPeopleParams{
		DocumentID: docAID, PeopleID: mariaID, PeopleTypeID: authorTypeID,
	}), "add Maria as author")
	assertNoError(t, q.AddDocumentPeople(ctx, AddDocumentPeopleParams{
		DocumentID: docBID, PeopleID: mariaID, PeopleTypeID: recipientTypeID,
	}), "add Maria as recipient")
	assertNoError(t, q.AddDocumentPeople(ctx, AddDocumentPeopleParams{
		DocumentID: docCID, PeopleID: juanID, PeopleTypeID: authorTypeID,
	}), "add Juan as author")

	docIDs := func(results []FTSDocumentRow) map[string]bool {
		m := map[string]bool{}
		for _, r := range results {
			m[r.DocumentID] = true
		}
		return m
	}
	peopleMaria := func(types ...string) []struct{ Name, Type string } {
		out := make([]struct{ Name, Type string }, len(types))
		for i, ty := range types {
			out[i] = struct{ Name, Type string }{Name: "Maria", Type: ty}
		}
		return out
	}

	t.Run("Typed author filter scopes to the author relationship only", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:  100,
			People: peopleMaria("test-author"),
		})
		assertNoError(t, err, "typed search")
		assertEqual(t, len(results), 1, "exactly one doc")
		ids := docIDs(results)
		assertEqual(t, ids[docAUUID], true, "Maria/author doc matched")
		assertEqual(t, ids[docBUUID], false, "Maria/recipient doc excluded")
		assertEqual(t, ids[docCUUID], false, "other person excluded")
		assertEqual(t, ids[docDUUID], false, "people-less doc excluded")
	})

	t.Run("PersonAnyType matches Maria across every relationship type", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:  100,
			People: peopleMaria(PersonAnyType),
		})
		assertNoError(t, err, "any-type search")
		ids := docIDs(results)
		assertEqual(t, len(results), 2, "two docs across both relationships")
		assertEqual(t, ids[docAUUID], true, "Maria/author doc matched")
		assertEqual(t, ids[docBUUID], true, "Maria/recipient doc matched")
		assertEqual(t, ids[docCUUID], false, "different person excluded")
		assertEqual(t, ids[docDUUID], false, "people-less doc excluded")
	})

	t.Run("PersonAnyType returns no results for an unknown person", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:  100,
			People: []struct{ Name, Type string }{{Name: "Nobody", Type: PersonAnyType}},
		})
		assertNoError(t, err, "any-type miss")
		assertEqual(t, len(results), 0, "no docs matched")
	})

	t.Run("Count matches Search for PersonAnyType", func(t *testing.T) {
		searchR, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:  100,
			People: peopleMaria(PersonAnyType),
		})
		assertNoError(t, err, "search")
		count, err := q.CountDocumentsStructured(ctx, SearchFilter{
			Limit:  100,
			People: peopleMaria(PersonAnyType),
		})
		assertNoError(t, err, "count")
		assertEqual(t, count, int64(len(searchR)), "count equals search length")
	})
}

func TestWithDocumentCountQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	d1, err := q.CreateDocument(ctx, CreateDocumentParams{
		DocumentID: "wdc-1", Title: "count-test-1.pdf",
		Md5Checksum: "md5-wdc1", Sha512Checksum: "sha512-wdc1",
		OriginalType: "application/pdf", FileSize: 100,
		OriginalPath: "/tmp/count1.pdf", StoragePath: "/tmp/sc1.pdf",
		PageCount: 1, WordCount: 1, CharCount: 5, Language: "eng",
	})
	assertNoError(t, err, "create doc 1")

	d2, err := q.CreateDocument(ctx, CreateDocumentParams{
		DocumentID: "wdc-2", Title: "count-test-2.pdf",
		Md5Checksum: "md5-wdc2", Sha512Checksum: "sha512-wdc2",
		OriginalType: "application/pdf", FileSize: 200,
		OriginalPath: "/tmp/count2.pdf", StoragePath: "/tmp/sc2.pdf",
		PageCount: 2, WordCount: 2, CharCount: 10, Language: "eng",
	})
	assertNoError(t, err, "create doc 2")

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
			Name: "%finance%", Limit: 10, Offset: 0,
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
			Name: "%alice%", Limit: 10, Offset: 0,
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

	t.Run("CountPeople", func(t *testing.T) {
		count, err := q.CountPeople(ctx)
		assertNoError(t, err, "count people")
		assertEqual(t, count >= int64(0), true, "count is non-negative")
	})

	t.Run("CountPeopleByName", func(t *testing.T) {
		count, err := q.CountPeopleByName(ctx, "%alice%")
		assertNoError(t, err, "count people by name")
		assertEqual(t, count >= int64(0), true, "count is non-negative")
	})
}

func TestTaskHealthQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
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
		id1, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-completed", TaskType: "consume", Status: "pending",
		})
		assertNoError(t, err, "create completed task")
		_, err = q.ClaimTask(ctx, id1)
		assertNoError(t, err, "claim")
		_, err = q.CompleteTask(ctx, CompleteTaskParams{ID: id1, Result: nil})
		assertNoError(t, err, "complete")

		id2, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-failed", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "th-batch-1", Valid: true},
		})
		assertNoError(t, err, "create failed task")
		assertNoError(t, q.FailTask(ctx, FailTaskParams{ID: id2, Error: sql.NullString{String: "x", Valid: true}}), "fail")

		_, err = q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-pending", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "th-batch-1", Valid: true},
		})
		assertNoError(t, err, "create pending task")

		id4, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-processing", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: "th-batch-2", Valid: true},
		})
		assertNoError(t, err, "create processing task")
		rows, err := q.ClaimTask(ctx, id4)
		assertNoError(t, err, "claim processing task")
		assertEqual(t, rows, int64(1), "claimed")

		rate, err := q.TaskSuccessRate(ctx)
		assertNoError(t, err, "task success rate")
		assertEqual(t, rate.Completed, int64(1), "completed")
		assertEqual(t, rate.Failed, int64(1), "failed")

		dur, err := q.AvgTaskDurationMs(ctx)
		assertNoError(t, err, "avg duration")
		// Loose bound: the completed task takes ~0ms locally, but CI
		// runners measure 2-3ms, so a tight upper bound would flake.
		if dur.AvgDurationMs > 5000 {
			t.Fatalf("avg duration ms: got %d, want <= 5000", dur.AvgDurationMs)
		}

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

func TestBackupLockLifecycle(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()

	t.Run("initially unlocked", func(t *testing.T) {
		locked, err := q.IsBackupLocked(ctx)
		assertNoError(t, err, "is backup locked")
		assertEqual(t, locked, int32(0), "not locked initially")
	})

	t.Run("acquire and verify locked", func(t *testing.T) {
		rows, err := q.AcquireBackupLock(ctx)
		assertNoError(t, err, "acquire")
		assertEqual(t, rows, int64(1), "one row affected")

		locked, err := q.IsBackupLocked(ctx)
		assertNoError(t, err, "check locked")
		assertEqual(t, locked, int32(1), "locked after acquire")
	})

	t.Run("release and verify unlocked", func(t *testing.T) {
		rows, err := q.ReleaseBackupLock(ctx)
		assertNoError(t, err, "release")
		assertEqual(t, rows, int64(1), "one row released")

		locked, err := q.IsBackupLocked(ctx)
		assertNoError(t, err, "check unlocked")
		assertEqual(t, locked, int32(0), "unlocked after release")
	})

	// Cleanup in case test fails mid-way
	q.ReleaseBackupLock(ctx)
}

func TestAcquireBackupLockConflict(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()
	defer q.ReleaseBackupLock(ctx)

	rows, err := q.AcquireBackupLock(ctx)
	assertNoError(t, err, "first acquire")
	assertEqual(t, rows, int64(1), "first acquire succeeds")

	rows, err = q.AcquireBackupLock(ctx)
	assertNoError(t, err, "second acquire")
	assertEqual(t, rows, int64(0), "second acquire returns 0 when already held")
}

func TestCountProcessingTasks(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		count, err := q.CountProcessingTasks(ctx)
		assertNoError(t, err, "count")
		assertEqual(t, count, int64(0), "zero processing tasks")
	})

	t.Run("counts only consume and enrich", func(t *testing.T) {
		id1, _ := q.CreateTask(ctx, CreateTaskParams{TaskID: "cpt-1", TaskType: "consume", Status: "pending"})
		q.ClaimTask(ctx, id1)

		id2, _ := q.CreateTask(ctx, CreateTaskParams{TaskID: "cpt-2", TaskType: "enrich", Status: "pending"})
		q.ClaimTask(ctx, id2)

		// config task in processing should NOT be counted
		id3, _ := q.CreateTask(ctx, CreateTaskParams{TaskID: "cpt-3", TaskType: "config", Status: "pending"})
		q.ClaimTask(ctx, id3)

		count, err := q.CountProcessingTasks(ctx)
		assertNoError(t, err, "count with tasks")
		assertEqual(t, count, int64(2), "only consume + enrich")
	})

	// Cleanup
	q.ReleaseBackupLock(ctx)
}

func TestGatedQueriesBlockDuringBackup(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	defer q.ReleaseBackupLock(ctx)

	// Create a pending consume task
	payload := json.RawMessage(`{"file":"test.pdf"}`)
	id, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID: "gate-1", TaskType: "consume", Status: "pending", Payload: &payload,
	})
	assertNoError(t, err, "create task")
	_ = id

	t.Run("ungated returns task when unlocked", func(t *testing.T) {
		taskID, err := q.GetNextPendingTaskOfType(ctx, "consume")
		assertNoError(t, err, "ungated claim")
		assertEqual(t, taskID > 0, true, "got a task id")
	})

	t.Run("gated returns task when unlocked", func(t *testing.T) {
		taskID, err := q.GetNextPendingTaskOfTypeWithGate(ctx, "consume")
		assertNoError(t, err, "gated claim unlocked")
		assertEqual(t, taskID > 0, true, "got a task id")
	})

	// Lock backup
	_, err = q.AcquireBackupLock(ctx)
	assertNoError(t, err, "acquire lock")

	t.Run("gated returns ErrNoRows when locked", func(t *testing.T) {
		_, err := q.GetNextPendingTaskOfTypeWithGate(ctx, "consume")
		assertEqual(t, err, sql.ErrNoRows, "gated claim blocked during backup")
	})

	t.Run("ungated still returns task when locked", func(t *testing.T) {
		taskID, err := q.GetNextPendingTaskOfType(ctx, "consume")
		assertNoError(t, err, "ungated claim still works")
		assertEqual(t, taskID > 0, true, "got a task id")
	})

	t.Run("gated with owner also blocked when locked", func(t *testing.T) {
		batchID := "gate-batch"
		q.CreateBatch(ctx, CreateBatchParams{ID: batchID, Source: "test", Status: "queued"})
		q.TryInsertBatchOwner(ctx, TryInsertBatchOwnerParams{BatchID: batchID, OwnerID: "gate-owner", Pid: 999})

		q.CreateTask(ctx, CreateTaskParams{
			TaskID: "gate-owner-task", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: batchID, Valid: true},
		})

		_, err := q.GetNextPendingTaskOfTypeForOwnerWithGate(ctx, GetNextPendingTaskOfTypeForOwnerWithGateParams{
			TaskType: "consume", OwnerID: "gate-owner",
		})
		assertEqual(t, err, sql.ErrNoRows, "gated owner claim blocked during backup")
	})
}

func TestCountPausedBatches(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	count, err := q.CountPausedBatches(ctx)
	assertNoError(t, err, "count paused (empty)")
	assertEqual(t, count, int64(0), "no paused batches initially")

	// paused batch
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "paused-1", Source: "test", Status: "paused"}), "create paused")
	// non-paused batches
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "queued-1", Source: "test", Status: "queued"}), "create queued")
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "completed-1", Source: "test", Status: "completed"}), "create completed")

	count, err = q.CountPausedBatches(ctx)
	assertNoError(t, err, "count paused")
	assertEqual(t, count, int64(1), "one paused batch")

	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "paused-2", Source: "test", Status: "paused"}), "create paused 2")
	count, err = q.CountPausedBatches(ctx)
	assertNoError(t, err, "count paused after second")
	assertEqual(t, count, int64(2), "two paused batches")
}

func TestListPausedBatches(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	ids, err := q.ListPausedBatches(ctx)
	assertNoError(t, err, "list paused (empty)")
	assertEqual(t, len(ids), 0, "no paused batches initially")

	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "paused-a", Source: "test", Status: "paused"}), "create paused a")
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "queued-x", Source: "test", Status: "queued"}), "create queued")
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "paused-b", Source: "test", Status: "paused"}), "create paused b")

	ids, err = q.ListPausedBatches(ctx)
	assertNoError(t, err, "list paused")
	assertEqual(t, len(ids), 2, "two paused batches")
	assertEqual(t, ids[0], "paused-a", "first paused (ordered by created_at)")
	assertEqual(t, ids[1], "paused-b", "second paused")
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
	id, err := q.CreateDocument(context.Background(), CreateDocumentParams{
		DocumentID: docID, Title: title,
		Md5Checksum: md5, Sha512Checksum: sha512,
		OriginalType: "application/pdf", FileSize: 100,
		OriginalPath: "/tmp/" + title, StoragePath: "/tmp/storage/" + title,
		TextContent: sql.NullString{String: "content", Valid: true},
		PageCount:   1, WordCount: 1, CharCount: 7, Language: "eng",
	})
	assertNoError(t, err, "create doc")
	_ = dtID
	return id, docID
}

func insertEnrichTask(t *testing.T, q *Queries, taskID, status string) int64 {
	t.Helper()
	p := json.RawMessage(`{}`)
	id, err := q.CreateTask(context.Background(), CreateTaskParams{
		TaskID: taskID, TaskType: "enrich", Status: status, Payload: &p,
	})
	assertNoError(t, err, "insert enrich "+taskID)
	return id
}

func insertTask(t *testing.T, q *Queries, taskID, status string) int64 {
	t.Helper()
	p := json.RawMessage(`{}`)
	id, err := q.CreateTask(context.Background(), CreateTaskParams{
		TaskID: taskID, TaskType: "consume", Status: status, Payload: &p,
	})
	assertNoError(t, err, "insert "+taskID)
	return id
}

func getResID(t *testing.T, res sql.Result) int64 {
	t.Helper()
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

func resetDB(t *testing.T, q *Queries) {
	t.Helper()
	ctx := context.Background()
	tables := []string{
		"orphaned_file", "batch_owner", "batch",
		"document_tag", "document_people", "document",
		"task", "saved_search", `"user"`,
		"tag", "people", "people_type", "document_type",
	}
	for _, tbl := range tables {
		if _, err := q.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", tbl)); err != nil {
			t.Fatalf("delete from %s: %v", tbl, err)
		}
	}
	for _, tbl := range []string{"document_type", "people_type", "tag", `"user"`, "task", "document", "saved_search", "orphaned_file"} {
		q.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN id RESTART WITH 1", tbl))
	}
	seeds := []string{"document-types", "people-types", "tags"}
	for _, seed := range seeds {
		data, err := SchemaFS.ReadFile(fmt.Sprintf("sql/schema/seed-%s.sql", seed))
		if err != nil {
			t.Fatalf("read seed %s: %v", seed, err)
		}
		if _, err := q.db.ExecContext(ctx, string(data)); err != nil {
			t.Fatalf("seed %s: %v", seed, err)
		}
	}
}

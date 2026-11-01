package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/types"
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

func TestListStoreTagKeys(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	canonicalID := MustCreateTag(t, q, "store key canonical")
	MustCreateTag(t, q, "store key tag")
	_, err := q.InsertTagAlias(ctx, InsertTagAliasParams{
		AliasName:      "store key alias",
		CanonicalTagID: canonicalID,
		RuleSetVersion: "v1",
	})
	assertNoError(t, err, "insert alias")

	keys, err := q.ListStoreTagKeys(ctx)
	assertNoError(t, err, "list store keys")

	present := make(map[string]bool, len(keys))
	for _, k := range keys {
		present[k] = true
	}
	// the decomposition gate skips compounds whose key matches a tag name
	// or an alias name, so both sides of the union must reach the caller
	for _, want := range []string{"store key tag", "store key canonical", "store key alias"} {
		if !present[want] {
			t.Errorf("store keys missing %q: %v", want, keys)
		}
	}
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
	seedLease(t, q, "tc-batch", "tc-owner")
	id := insertBatchTask(t, q, "tc-1", "tc-batch", types.Task.Status.Pending)
	task, _ := q.GetTask(ctx, id)
	assertEqual(t, string(task.Status), "pending", "pending")
	rows, _ := q.ClaimTask(ctx, id)
	assertEqual(t, rows, int64(1), "claimed")
	task, _ = q.GetTask(ctx, id)
	assertEqual(t, string(task.Status), "processing", "processing")
	result := json.RawMessage(`{"ok":true}`)
	_, err := q.CompleteBlockingTask(ctx, CompleteBlockingTaskParams{
		ID: id, Result: &result, OwnerID: "tc-owner",
	})
	assertNoError(t, err, "complete")
	task, _ = q.GetTask(ctx, id)
	assertEqual(t, string(task.Status), "completed", "completed")
}

func TestWaitingSuccessorFlow(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	id := insertLinkedTask(t, q, "ws-1", "ws-parent", "", types.Task.Type.Enrich, types.Task.Status.Waiting)
	wsPayload := json.RawMessage(`{"document_id":"doc-1","storage_path":"/store/a.pdf"}`)
	rows, err := q.SetWaitingSuccessorPending(ctx, SetWaitingSuccessorPendingParams{
		ID: id, Payload: &wsPayload, DedupKey: "enrich:doc:doc-1",
	})
	assertNoError(t, err, "set pending")
	assertEqual(t, rows, int64(1), "one row flipped")

	task, _ := q.GetTask(ctx, id)
	assertEqual(t, string(task.Status), "pending", "pending")
	assertEqual(t, task.DedupKey.String, "enrich:doc:doc-1", "key written with the flip")

	discarded, err := q.DiscardWaitingSuccessor(ctx, DiscardWaitingSuccessorParams{ID: id, Error: "parent failed"})
	assertNoError(t, err, "discard")
	assertEqual(t, discarded, int64(0), "the waiting guard leaves a pending row alone")
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
	err := q.CreateBatch(ctx, CreateBatchParams{ID: "bo-test", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Queued})
	assertNoError(t, err, "create batch")
	_, err = q.TryInsertBatchOwner(ctx, TryInsertBatchOwnerParams{BatchID: "bo-test", OwnerID: "o1", Pid: 123})
	assertNoError(t, err, "insert owner")
	q.ReleaseBatchOwner(ctx, ReleaseBatchOwnerParams{BatchID: "bo-test", OwnerID: "o1"})
}

func TestDeleteBatchOwnerByBatchID(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	err := q.CreateBatch(ctx, CreateBatchParams{ID: "del-test", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Queued})
	assertNoError(t, err, "create batch")
	_, err = q.TryInsertBatchOwner(ctx, TryInsertBatchOwnerParams{BatchID: "del-test", OwnerID: "o1", Pid: 100})
	assertNoError(t, err, "insert owner")

	rows, err := q.DeleteBatchOwnerByBatchID(ctx, "del-test")
	assertNoError(t, err, "delete by batch id")
	assertEqual(t, rows, int64(1), "one row deleted")

	_, err = q.GetBatchOwner(ctx, GetBatchOwnerParams{BatchID: "del-test", StaleAfterSeconds: 15})
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

func TestGetPermanentlyFailedConsumeTaskPayloads(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	err := q.CreateBatch(ctx, CreateBatchParams{ID: "gq-batch", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Processing})
	assertNoError(t, err, "create batch")
	seedLease(t, q, "gq-batch", "gq-owner")

	gqPayload := json.RawMessage(`{"file_path":"/tmp/test.pdf","on_completed":"enrich-1"}`)
	id, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID:   "gq-task",
		TaskType: types.Task.Type.Consume,
		Status:   types.Task.Status.Pending,
		Payload:  &gqPayload,
		BatchID:  sql.NullString{String: "gq-batch", Valid: true},
	})
	assertNoError(t, err, "create task")

	_, err = q.ClaimTask(ctx, id)
	assertNoError(t, err, "claim failed task")
	_, err = q.FailBlockingTask(ctx, FailBlockingTaskParams{
		ID: id, Error: sql.NullString{String: "Max retries exceeded (3)", Valid: true}, OwnerID: "gq-owner",
	})
	assertNoError(t, err, "fail task")
	if _, err := db.ExecContext(ctx, "UPDATE task SET attempts = 3 WHERE id = $1", id); err != nil {
		t.Fatalf("set attempts: %v", err)
	}

	gqPayload2 := json.RawMessage(`{}`)
	id2, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID:   "gq-other-failed",
		TaskType: types.Task.Type.Consume,
		Status:   types.Task.Status.Pending,
		Payload:  &gqPayload2,
		BatchID:  sql.NullString{String: "gq-batch", Valid: true},
	})
	assertNoError(t, err, "create other task")

	_, err = q.ClaimTask(ctx, id2)
	assertNoError(t, err, "claim other task")
	_, err = q.FailBlockingTask(ctx, FailBlockingTaskParams{
		ID: id2, Error: sql.NullString{String: "some other error", Valid: true}, OwnerID: "gq-owner",
	})
	assertNoError(t, err, "fail other task")

	rows, err := q.GetPermanentlyFailedConsumeTaskPayloads(ctx, GetPermanentlyFailedConsumeTaskPayloadsParams{
		BatchID:  sql.NullString{String: "gq-batch", Valid: true},
		Attempts: 3,
	})
	assertNoError(t, err, "get quarantined")
	assertEqual(t, len(rows), 1, "only the attempts-exhausted task")
	assertEqual(t, rows[0].TaskID, "gq-task", "task id")
}

func TestDiscardWaitingSuccessorBranch(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	insertTask(t, q, "branch-root", types.Task.Status.Pending)
	insertLinkedTask(t, q, "branch-a", "branch-root", "", types.Task.Type.TagReconcile, types.Task.Status.Waiting)
	insertLinkedTask(t, q, "branch-b", "branch-a", "", types.Task.Type.TagIdentity, types.Task.Status.Waiting)
	insertLinkedTask(t, q, "branch-c", "branch-b", "", types.Task.Type.TagAudit, types.Task.Status.Waiting)
	insertLinkedTask(t, q, "branch-pending", "branch-b", "", types.Task.Type.TagMeasure, types.Task.Status.Pending)

	n, err := q.DiscardWaitingSuccessorBranch(ctx, DiscardWaitingSuccessorBranchParams{
		ParentTaskID: "branch-root", Error: "duplicate of live work",
	})
	assertNoError(t, err, "branch discard")
	assertEqual(t, n, int64(3), "every waiting step in the branch discarded")
	for _, id := range []string{"branch-a", "branch-b", "branch-c"} {
		task, _ := q.GetTaskByTaskID(ctx, id)
		assertEqual(t, string(task.Status), "discarded", id+" discarded")
		assertEqual(t, task.Error.String, "duplicate of live work", id+" reason")
	}
	pending, _ := q.GetTaskByTaskID(ctx, "branch-pending")
	assertEqual(t, string(pending.Status), "pending", "a non-waiting row is untouched")
}

func TestRestoreDiscardedSuccessors(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	insertTask(t, q, "restore-c1", types.Task.Status.Pending)
	insertLinkedTask(t, q, "restore-e1", "restore-c1", "", types.Task.Type.Enrich, types.Task.Status.Discarded)
	insertLinkedTask(t, q, "restore-s1", "restore-e1", "", types.Task.Type.TagIdentity, types.Task.Status.Discarded)

	n, err := q.RestoreDiscardedSuccessors(ctx)
	assertNoError(t, err, "global restore")
	assertEqual(t, n, int64(2), "the discarded tail is re-armed through the chain")
	for _, id := range []string{"restore-e1", "restore-s1"} {
		task, _ := q.GetTaskByTaskID(ctx, id)
		assertEqual(t, string(task.Status), "waiting", id+" restored")
		if task.Error.Valid || task.CompletedAt.Valid {
			t.Fatalf("%s kept its discard fields after restore", id)
		}
	}

	t.Run("skips a successor whose parent ended", func(t *testing.T) {
		q, _ := NewTestQueries(t)
		ctx := context.Background()

		insertTask(t, q, "restore-c2", types.Task.Status.Failed)
		insertLinkedTask(t, q, "restore-e2", "restore-c2", "", types.Task.Type.Enrich, types.Task.Status.Discarded)

		n, err := q.RestoreDiscardedSuccessors(ctx)
		assertNoError(t, err, "global restore")
		assertEqual(t, n, int64(0), "no successor restored under a failed parent")
		task, _ := q.GetTaskByTaskID(ctx, "restore-e2")
		assertEqual(t, string(task.Status), "discarded", "successor stays discarded")
	})

	t.Run("batch variant is scoped to the batch", func(t *testing.T) {
		q, _ := NewTestQueries(t)
		ctx := context.Background()

		insertLinkedTask(t, q, "restore-c3", "", "b1", types.Task.Type.Consume, types.Task.Status.Pending)
		insertLinkedTask(t, q, "restore-e3", "restore-c3", "b1", types.Task.Type.Enrich, types.Task.Status.Discarded)
		insertLinkedTask(t, q, "restore-c4", "", "b2", types.Task.Type.Consume, types.Task.Status.Pending)
		insertLinkedTask(t, q, "restore-e4", "restore-c4", "b2", types.Task.Type.Enrich, types.Task.Status.Discarded)

		n, err := q.RestoreDiscardedSuccessorsByBatch(ctx, "b1")
		assertNoError(t, err, "batch restore")
		assertEqual(t, n, int64(1), "only the b1 chain restored")
		task, _ := q.GetTaskByTaskID(ctx, "restore-e3")
		assertEqual(t, string(task.Status), "waiting", "b1 successor restored")
		task, _ = q.GetTaskByTaskID(ctx, "restore-e4")
		assertEqual(t, string(task.Status), "discarded", "b2 successor untouched")
	})
}

func TestDiscardOrphanedWaitingSuccessors(t *testing.T) {
	q, db := NewTestQueries(t)
	ctx := context.Background()

	// a failed parent discards its waiting successor, copying the parent's error
	insertLinkedTask(t, q, "swe-c1", "", "b1", types.Task.Type.Consume, types.Task.Status.Failed)
	_, err := db.ExecContext(ctx, "UPDATE task SET error = 'Max retries exceeded (3)' WHERE task_id = 'swe-c1'")
	assertNoError(t, err, "set parent error")
	insertLinkedTask(t, q, "swe-e1", "swe-c1", "b1", types.Task.Type.Enrich, types.Task.Status.Waiting)

	// a waiting grandchild dies with the branch: the quarantine paths fail a mid-chain task without
	// cascading, so one sweep pass must clear the whole dead chain
	insertLinkedTask(t, q, "swe-g1", "swe-e1", "b1", types.Task.Type.TagIdentity, types.Task.Status.Waiting)

	// a pending parent keeps its waiting successor
	insertLinkedTask(t, q, "swe-c2", "", "b1", types.Task.Type.Consume, types.Task.Status.Pending)
	insertLinkedTask(t, q, "swe-e2", "swe-c2", "b1", types.Task.Type.Enrich, types.Task.Status.Waiting)

	// a waiting parent is a chain step not reached yet: it keeps its successor too
	insertLinkedTask(t, q, "swe-c3", "", "b1", types.Task.Type.Consume, types.Task.Status.Waiting)
	insertLinkedTask(t, q, "swe-e3", "swe-c3", "b1", types.Task.Type.Enrich, types.Task.Status.Waiting)

	n, err := q.DiscardOrphanedWaitingSuccessors(ctx, "b1")
	assertNoError(t, err, "batch sweep")
	assertEqual(t, n, int64(2), "the whole dead branch discarded in one pass")
	for _, id := range []string{"swe-e1", "swe-g1"} {
		task, _ := q.GetTaskByTaskID(ctx, id)
		assertEqual(t, string(task.Status), "discarded", id+" discarded")
		assertEqual(t, task.Error.String, "Max retries exceeded (3)", id+" carries the root cause")
	}
	for _, id := range []string{"swe-e2", "swe-e3"} {
		row, _ := q.GetTaskByTaskID(ctx, id)
		assertEqual(t, string(row.Status), "waiting", id+" kept under a live chain")
	}

	t.Run("global variant discards across batches", func(t *testing.T) {
		q, _ := NewTestQueries(t)
		ctx := context.Background()

		insertLinkedTask(t, q, "swe-c9", "", "b9", types.Task.Type.Consume, types.Task.Status.Failed)
		insertLinkedTask(t, q, "swe-e9", "swe-c9", "b9", types.Task.Type.Enrich, types.Task.Status.Waiting)

		n, err := q.DiscardOrphanedWaitingSuccessorsGlobal(ctx)
		assertNoError(t, err, "global sweep")
		assertEqual(t, n, int64(1), "one successor discarded")
		task, _ := q.GetTaskByTaskID(ctx, "swe-e9")
		assertEqual(t, string(task.Status), "discarded", "successor discarded by the global sweep")
	})
}

func TestListTasksByType(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	insertTask(t, q, "lt-1", types.Task.Status.Pending)
	tasks, _ := q.ListTasksByType(ctx, ListTasksByTypeParams{TaskType: types.Task.Type.Consume, Limit: 10, Offset: 0})
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

func TestStructuredSearchSortByPageCount(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	createDoc := func(uuid string, pages int32) {
		t.Helper()
		_, err := q.CreateDocument(ctx, CreateDocumentParams{
			DocumentID: uuid, Title: uuid + ".pdf",
			Md5Checksum: "md5-" + uuid, Sha512Checksum: "sha512-" + uuid,
			OriginalType: "application/pdf", FileSize: 100,
			OriginalPath: "/tmp/" + uuid + ".pdf", StoragePath: "/tmp/s-" + uuid + ".pdf",
			PageCount: pages, WordCount: 1, CharCount: 5, Language: "eng",
		})
		assertNoError(t, err, "create "+uuid)
	}

	createDoc("pc-1", 3)
	createDoc("pc-2", 1)
	createDoc("pc-3", 7)

	t.Run("asc returns smallest page count first", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit: 10, SortBy: "page_count", SortOrder: "asc",
		})
		assertNoError(t, err, "search asc")
		assertEqual(t, len(results), 3, "three results")
		assertEqual(t, results[0].PageCount, int64(1), "first is smallest")
		assertEqual(t, results[2].PageCount, int64(7), "last is largest")
	})

	t.Run("desc returns largest page count first", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit: 10, SortBy: "page_count", SortOrder: "desc",
		})
		assertNoError(t, err, "search desc")
		assertEqual(t, results[0].PageCount, int64(7), "first is largest")
		assertEqual(t, results[2].PageCount, int64(1), "last is smallest")
	})

	t.Run("unknown sort key falls back to default ordering", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit: 10, SortBy: "not_a_column", SortOrder: "desc",
		})
		assertNoError(t, err, "search unknown key")
		assertEqual(t, len(results), 3, "three results despite unknown key")
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
			PageCount:   1, WordCount: 1, CharCount: 5, Language: "eng",
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

	t.Run("person name and type filters ignore case", func(t *testing.T) {
		typed := []struct{ Name, Type string }{{Name: "mArIa", Type: "TEST-Author"}}
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{Limit: 100, People: typed})
		assertNoError(t, err, "mixed-case typed search")
		ids := docIDs(results)
		assertEqual(t, len(results), 1, "typed filter still scopes to one doc")
		assertEqual(t, ids[docAUUID], true, "author doc matched with mixed-case name and type")

		results, err = q.SearchDocumentsStructured(ctx, SearchFilter{
			Limit:  100,
			People: []struct{ Name, Type string }{{Name: "MARIA", Type: PersonAnyType}},
		})
		assertNoError(t, err, "mixed-case any-type search")
		assertEqual(t, len(results), 2, "any-type filter matches both docs")
	})
}

func TestStructuredSearchValueFiltersCaseInsensitive(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	articleType, err := q.GetDocumentTypeByName(ctx, "article")
	assertNoError(t, err, "get seeded article type")
	seedReportType, err := q.GetDocumentTypeByName(ctx, "report")
	assertNoError(t, err, "get seeded report type")
	caseReportType, err := q.CreateDocumentTypeFull(ctx, CreateDocumentTypeFullParams{
		Name: "Report", Description: "case variant of the seeded report type",
	})
	assertNoError(t, err, "create Report type")

	createDoc := func(id string, language string) {
		t.Helper()
		_, err := q.CreateDocument(ctx, CreateDocumentParams{
			DocumentID: id, Title: id + ".pdf",
			Md5Checksum: "md5-" + id, Sha512Checksum: "sha512-" + id,
			OriginalType: "application/pdf", FileSize: 1000,
			OriginalPath: "/tmp/" + id + ".pdf", StoragePath: "/tmp/" + id + "-s.pdf",
			TextContent: sql.NullString{String: id, Valid: true},
			PageCount:   1, WordCount: 1, CharCount: 5, Language: language,
		})
		assertNoError(t, err, "create "+id)
	}

	createDoc("ci-article", "eng")
	createDoc("ci-spanish-report", "spa")
	createDoc("ci-case-report", "eng")

	setType := func(typeID int64, docIDs ...string) {
		t.Helper()
		_, err := q.SetDocumentsDocumentType(ctx, SetDocumentsDocumentTypeParams{
			DocumentTypeID: typeID, Column2: docIDs,
		})
		assertNoError(t, err, "set document type")
	}
	setType(articleType.ID, "ci-article")
	setType(seedReportType.ID, "ci-spanish-report")
	setType(caseReportType, "ci-case-report")

	docIDsOf := func(results []FTSDocumentRow) map[string]bool {
		m := map[string]bool{}
		for _, r := range results {
			m[r.DocumentID] = true
		}
		return m
	}

	t.Run("document type filter ignores case", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{Limit: 100, DocumentType: "ARTICLE"})
		assertNoError(t, err, "mixed-case article filter")
		ids := docIDsOf(results)
		assertEqual(t, ids["ci-article"], true, "article doc matched")
		assertEqual(t, ids["ci-spanish-report"], false, "report doc excluded")
	})

	t.Run("document type filter tolerates case-variant duplicate names", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{Limit: 100, DocumentType: "REPORT"})
		assertNoError(t, err, "filter matching both report and Report must not error")
		ids := docIDsOf(results)
		assertEqual(t, ids["ci-spanish-report"], true, "doc on seeded report matched")
		assertEqual(t, ids["ci-case-report"], true, "doc on Report matched")
		count, err := q.CountDocumentsStructured(ctx, SearchFilter{Limit: 100, DocumentType: "REPORT"})
		assertNoError(t, err, "count with duplicate report types")
		assertEqual(t, count, int64(len(results)), "count equals search length")
	})

	t.Run("language filter ignores case", func(t *testing.T) {
		results, err := q.SearchDocumentsStructured(ctx, SearchFilter{Limit: 100, Language: "ENG"})
		assertNoError(t, err, "mixed-case language filter")
		ids := docIDsOf(results)
		assertEqual(t, ids["ci-article"], true, "eng doc matched")
		assertEqual(t, ids["ci-case-report"], true, "second eng doc matched")
		assertEqual(t, ids["ci-spanish-report"], false, "spa doc excluded")
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

func TestSearchPrefixQueriesCaseInsensitive(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	_, err := q.CreatePeople(ctx, CreatePeopleParams{Name: "García", NormalizedName: "garcia"})
	assertNoError(t, err, "create García")
	MustCreateTag(t, q, "FinanceTag")

	t.Run("people prefix search and count ignore case", func(t *testing.T) {
		people, err := q.SearchPeopleByNameWithDocumentCount(ctx, SearchPeopleByNameWithDocumentCountParams{
			Name: "GARC%", Limit: 10, Offset: 0,
		})
		assertNoError(t, err, "search people by mixed-case prefix")
		assertEqual(t, len(people), 1, "one person matched")
		assertEqual(t, people[0].Name, "García", "matched person")

		count, err := q.CountPeopleByName(ctx, "GARC%")
		assertNoError(t, err, "count people by mixed-case prefix")
		assertEqual(t, count, int64(1), "count agrees with search")
	})

	t.Run("tag prefix search and count ignore case", func(t *testing.T) {
		tags, err := q.SearchTagsByNameWithDocumentCount(ctx, SearchTagsByNameWithDocumentCountParams{
			Name: "FINANCET%", Limit: 10, Offset: 0,
		})
		assertNoError(t, err, "search tags by mixed-case prefix")
		found := false
		for _, tg := range tags {
			if tg.Name == "financetag" {
				found = true
			}
		}
		assertEqual(t, found, true, "created tag matched")

		count, err := q.CountTagsByName(ctx, "FINANCET%")
		assertNoError(t, err, "count tags by mixed-case prefix")
		assertEqual(t, count, int64(1), "count agrees with search")
	})

	t.Run("document type prefix search ignores case", func(t *testing.T) {
		dts, err := q.SearchDocumentTypeByNameWithDocumentCount(ctx, SearchDocumentTypeByNameWithDocumentCountParams{
			Name: "%ARTICLE%", Limit: 10,
		})
		assertNoError(t, err, "search document types by mixed-case pattern")
		assertEqual(t, len(dts) > 0, true, "seeded article type matched")
	})

	t.Run("people type prefix search ignores case", func(t *testing.T) {
		pts, err := q.SearchPeopleTypeByName(ctx, SearchPeopleTypeByNameParams{Name: "AUTHOR%", Limit: 10})
		assertNoError(t, err, "search people types by mixed-case prefix")
		assertEqual(t, len(pts), 1, "one people type matched")
		assertEqual(t, pts[0].Name, "author", "seeded author type matched")
	})

	t.Run("deleted tag list and count ignore case", func(t *testing.T) {
		assertNoError(t, q.UpsertDeletedTag(ctx, UpsertDeletedTagParams{Name: "OldBlockedTag", Kind: "blocked"}), "upsert deleted tag")

		rows, err := q.ListDeletedTags(ctx, ListDeletedTagsParams{NamePrefix: "OLDBLOCKEDTAG", PageLimit: 10, PageOffset: 0})
		assertNoError(t, err, "list deleted tags by mixed-case prefix")
		assertEqual(t, len(rows), 1, "one deleted tag matched")
		assertEqual(t, rows[0].Name, "OldBlockedTag", "matched deleted tag")

		count, err := q.CountDeletedTags(ctx, "OLDBLOCKEDTAG")
		assertNoError(t, err, "count deleted tags by mixed-case prefix")
		assertEqual(t, count, int64(1), "count agrees with list")
	})

	t.Run("no match returns empty results and zero count", func(t *testing.T) {
		people, err := q.SearchPeopleByNameWithDocumentCount(ctx, SearchPeopleByNameWithDocumentCountParams{
			Name: "zzzq%", Limit: 10, Offset: 0,
		})
		assertNoError(t, err, "search people with unmatched prefix")
		assertEqual(t, len(people), 0, "no people matched")

		count, err := q.CountPeopleByName(ctx, "zzzq%")
		assertNoError(t, err, "count people with unmatched prefix")
		assertEqual(t, count, int64(0), "count is zero")
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

		activeBatches, err := q.CountActiveBatches(ctx)
		assertNoError(t, err, "count active batches")
		assertEqual(t, activeBatches, int64(0), "no active batches")
	})

	t.Run("with mixed tasks", func(t *testing.T) {
		seedLease(t, q, "th-batch-0", "th-owner-0")
		id1, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-completed", TaskType: types.Task.Type.Consume, Status: types.Task.Status.Pending,
			BatchID: sql.NullString{String: "th-batch-0", Valid: true},
		})
		assertNoError(t, err, "create completed task")
		_, err = q.ClaimTask(ctx, id1)
		assertNoError(t, err, "claim")
		_, err = q.CompleteBlockingTask(ctx, CompleteBlockingTaskParams{
			ID: id1, Result: nil, OwnerID: "th-owner-0",
		})
		assertNoError(t, err, "complete")

		seedLease(t, q, "th-batch-1", "th-owner-1")
		id2, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-failed", TaskType: types.Task.Type.Consume, Status: types.Task.Status.Pending,
			BatchID: sql.NullString{String: "th-batch-1", Valid: true},
		})
		assertNoError(t, err, "create failed task")
		_, err = q.ClaimTask(ctx, id2)
		assertNoError(t, err, "claim failed task")
		_, err = q.FailBlockingTask(ctx, FailBlockingTaskParams{
			ID: id2, Error: sql.NullString{String: "x", Valid: true}, OwnerID: "th-owner-1",
		})
		assertNoError(t, err, "fail")

		_, err = q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-pending", TaskType: types.Task.Type.Consume, Status: types.Task.Status.Pending,
			BatchID: sql.NullString{String: "th-batch-1", Valid: true},
		})
		assertNoError(t, err, "create pending task")

		seedLease(t, q, "th-batch-2", "th-owner-2")
		id4, err := q.CreateTask(ctx, CreateTaskParams{
			TaskID: "th-processing", TaskType: types.Task.Type.Consume, Status: types.Task.Status.Pending,
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

		activeBatches, err := q.CountActiveBatches(ctx)
		assertNoError(t, err, "count active batches")
		assertEqual(t, activeBatches, int64(3), "three active batches")
	})
}

func TestLockLifecycle(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()
	maintenance := []string{string(types.LockMaintenance)}
	token := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	defer q.ReleaseLocksForToken(ctx, ReleaseLocksForTokenParams{LockNames: types.LockNameValues(), OwnerToken: token})

	t.Run("initially unlocked", func(t *testing.T) {
		held, err := q.IsLockHeld(ctx, string(types.LockMaintenance))
		assertNoError(t, err, "is lock held")
		assertEqual(t, held, int32(0), "not held initially")
	})

	t.Run("acquire and verify held", func(t *testing.T) {
		rows, err := q.AcquireLocks(ctx, AcquireLocksParams{OwnerToken: token, LockNames: maintenance})
		assertNoError(t, err, "acquire")
		assertEqual(t, rows, int64(1), "one row affected")

		held, err := q.IsLockHeld(ctx, string(types.LockMaintenance))
		assertNoError(t, err, "check held")
		assertEqual(t, held, int32(1), "held after acquire")
	})

	t.Run("release and verify unlocked", func(t *testing.T) {
		released, err := q.ReleaseLocksForToken(ctx, ReleaseLocksForTokenParams{
			LockNames:  maintenance,
			OwnerToken: token,
		})
		assertNoError(t, err, "release")
		assertEqual(t, len(released), 1, "one row released")

		held, err := q.IsLockHeld(ctx, string(types.LockMaintenance))
		assertNoError(t, err, "check unlocked")
		assertEqual(t, held, int32(0), "unlocked after release")
	})
}

func TestAcquireLocksConflict(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()
	maintenance := []string{string(types.LockMaintenance)}
	first := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	defer q.ReleaseLocksForToken(ctx, ReleaseLocksForTokenParams{LockNames: types.LockNameValues(), OwnerToken: first})

	rows, err := q.AcquireLocks(ctx, AcquireLocksParams{OwnerToken: first, LockNames: maintenance})
	assertNoError(t, err, "first acquire")
	assertEqual(t, rows, int64(1), "first acquire succeeds")

	rows, err = q.AcquireLocks(ctx, AcquireLocksParams{
		OwnerToken: uuid.NullUUID{UUID: uuid.New(), Valid: true},
		LockNames:  maintenance,
	})
	assertNoError(t, err, "second acquire")
	assertEqual(t, rows, int64(0), "second acquire returns 0 when already held")
}

func TestCountProcessingTasks(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		count, err := q.CountProcessingTasks(ctx, types.TaskTypesWaitingOn(types.LockMaintenance))
		assertNoError(t, err, "count")
		assertEqual(t, count, int64(0), "zero processing tasks")
	})

	t.Run("counts only consume and enrich", func(t *testing.T) {
		id1, _ := q.CreateTask(ctx, CreateTaskParams{TaskID: "cpt-1", TaskType: types.Task.Type.Consume, Status: types.Task.Status.Pending})
		q.ClaimTask(ctx, id1)

		id2, _ := q.CreateTask(ctx, CreateTaskParams{TaskID: "cpt-2", TaskType: types.Task.Type.Enrich, Status: types.Task.Status.Pending})
		q.ClaimTask(ctx, id2)

		// a maintenance-holding task in processing should NOT be counted
		id3, _ := q.CreateTask(ctx, CreateTaskParams{TaskID: "cpt-3", TaskType: types.Task.Type.TagMeasure, Status: types.Task.Status.Pending})
		q.ClaimTask(ctx, id3)

		count, err := q.CountProcessingTasks(ctx, types.TaskTypesWaitingOn(types.LockMaintenance))
		assertNoError(t, err, "count with tasks")
		assertEqual(t, count, int64(2), "only consume + enrich")
	})

	t.Run("excludes blocking types so a drain never self-waits", func(t *testing.T) {
		// the registry-driven drain list must ignore backup/mirror so a
		// blocking task in processing is invisible to WaitForTaskDrain.
		resetDB(t, q)
		idBk, _ := q.CreateTask(ctx, CreateTaskParams{TaskID: "cpt-bk", TaskType: types.Task.Type.Backup, Status: types.Task.Status.Pending})
		q.ClaimTask(ctx, idBk)

		idMr, _ := q.CreateTask(ctx, CreateTaskParams{TaskID: "cpt-mr", TaskType: types.Task.Type.Mirror, Status: types.Task.Status.Pending})
		q.ClaimTask(ctx, idMr)

		idCn, _ := q.CreateTask(ctx, CreateTaskParams{TaskID: "cpt-cn", TaskType: types.Task.Type.Consume, Status: types.Task.Status.Pending})
		q.ClaimTask(ctx, idCn)

		count, err := q.CountProcessingTasks(ctx, types.TaskTypesWaitingOn(types.LockMaintenance))
		assertNoError(t, err, "count")
		assertEqual(t, count, int64(1), "only the consume task; blocking excluded")
	})

	// Cleanup
	q.ReleaseLocksForToken(ctx, ReleaseLocksForTokenParams{LockNames: types.LockNameValues(), OwnerToken: uuid.NullUUID{}})
}

func TestWaitedQueriesBlockDuringMaintenance(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	maintenance := []string{string(types.LockMaintenance)}
	defer q.ReleaseLocksForToken(ctx, ReleaseLocksForTokenParams{LockNames: types.LockNameValues(), OwnerToken: uuid.NullUUID{}})

	// Create a leased batch with a pending consume task
	const batchID, owner = "gate-batch", "gate-owner"
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{
		ID: batchID, Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Queued,
	}), "create batch")
	seedLease(t, q, batchID, owner)
	payload := json.RawMessage(`{"file":"test.pdf"}`)
	_, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID: "gate-1", TaskType: types.Task.Type.Consume, Status: types.Task.Status.Pending, Payload: &payload,
		BatchID: sql.NullString{String: batchID, Valid: true},
	})
	assertNoError(t, err, "create task")

	t.Run("no waits returns task when unlocked", func(t *testing.T) {
		taskID, err := q.GetNextPendingTaskOfTypeForOwner(ctx, GetNextPendingTaskOfTypeForOwnerParams{
			TaskType: types.Task.Type.Consume, OwnerID: owner,
		})
		assertNoError(t, err, "ungated claim")
		assertEqual(t, taskID > 0, true, "got a task id")
	})

	t.Run("waiting on maintenance returns task when unlocked", func(t *testing.T) {
		taskID, err := q.GetNextPendingTaskOfTypeForOwner(ctx, GetNextPendingTaskOfTypeForOwnerParams{
			TaskType: types.Task.Type.Consume, OwnerID: owner, WaitLocks: maintenance,
		})
		assertNoError(t, err, "gated claim unlocked")
		assertEqual(t, taskID > 0, true, "got a task id")
	})

	// Hold the maintenance lock with a manual holder
	rows, err := q.AcquireLocks(ctx, AcquireLocksParams{LockNames: maintenance})
	assertNoError(t, err, "acquire lock")
	assertEqual(t, rows, int64(1), "acquire lock")

	t.Run("waiting on maintenance returns ErrNoRows when held", func(t *testing.T) {
		_, err := q.GetNextPendingTaskOfTypeForOwner(ctx, GetNextPendingTaskOfTypeForOwnerParams{
			TaskType: types.Task.Type.Consume, OwnerID: owner, WaitLocks: maintenance,
		})
		assertEqual(t, err, sql.ErrNoRows, "gated claim blocked during maintenance")
	})

	t.Run("no waits still returns task when held", func(t *testing.T) {
		taskID, err := q.GetNextPendingTaskOfTypeForOwner(ctx, GetNextPendingTaskOfTypeForOwnerParams{
			TaskType: types.Task.Type.Consume, OwnerID: owner,
		})
		assertNoError(t, err, "ungated claim still works")
		assertEqual(t, taskID > 0, true, "got a task id")
	})

	t.Run("another owner's lease does not see the task", func(t *testing.T) {
		_, err := q.GetNextPendingTaskOfTypeForOwner(ctx, GetNextPendingTaskOfTypeForOwnerParams{
			TaskType: types.Task.Type.Consume, OwnerID: "other-owner", WaitLocks: maintenance,
		})
		assertEqual(t, err, sql.ErrNoRows, "an execution only claims tasks of the batches it owns")
	})
}

// TestThumbnailClaimWaitsForStorage pins the second lock of the thumbnail declaration: its claims
// defer on both maintenance and storage, so a storage writer pauses new thumbnail work while
// maintenance-holders alone do not.
func TestThumbnailClaimWaitsForStorage(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	waits := []string{string(types.LockMaintenance), string(types.LockStorage)}
	storageToken := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	t.Cleanup(func() {
		q.ReleaseLocksForToken(ctx, ReleaseLocksForTokenParams{
			LockNames:  types.LockNameValues(),
			OwnerToken: storageToken,
		})
	})

	payload := json.RawMessage(`{"file":"t.pdf"}`)
	const batchID, owner = "thumb-gate-batch", "thumb-gate-owner"
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{
		ID: batchID, Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Queued,
	}), "create batch")
	seedLease(t, q, batchID, owner)
	_, err := q.CreateTask(ctx, CreateTaskParams{
		TaskID: "thumb-gate", TaskType: types.Task.Type.Thumbnail, Status: types.Task.Status.Pending, Payload: &payload,
		BatchID: sql.NullString{String: batchID, Valid: true},
	})
	assertNoError(t, err, "create thumbnail task")

	id, err := q.GetNextPendingTaskOfTypeForOwner(ctx, GetNextPendingTaskOfTypeForOwnerParams{
		TaskType: types.Task.Type.Thumbnail, OwnerID: owner, WaitLocks: waits,
	})
	assertNoError(t, err, "claim while storage is free")
	assertEqual(t, id > 0, true, "got a task id")

	rows, err := q.AcquireLocks(ctx, AcquireLocksParams{
		OwnerToken: storageToken, LockNames: []string{string(types.LockStorage)},
	})
	assertNoError(t, err, "hold storage")
	assertEqual(t, rows, int64(1), "hold storage")

	_, err = q.GetNextPendingTaskOfTypeForOwner(ctx, GetNextPendingTaskOfTypeForOwnerParams{
		TaskType: types.Task.Type.Thumbnail, OwnerID: owner, WaitLocks: waits,
	})
	assertEqual(t, err, sql.ErrNoRows, "thumbnail claim deferred while storage is held")
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
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "paused-1", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Paused}), "create paused")
	// non-paused batches
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "queued-1", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Queued}), "create queued")
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "completed-1", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Completed}), "create completed")

	count, err = q.CountPausedBatches(ctx)
	assertNoError(t, err, "count paused")
	assertEqual(t, count, int64(1), "one paused batch")

	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "paused-2", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Paused}), "create paused 2")
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

	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "paused-a", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Paused}), "create paused a")
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "queued-x", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Queued}), "create queued")
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{ID: "paused-b", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Paused}), "create paused b")

	ids, err = q.ListPausedBatches(ctx)
	assertNoError(t, err, "list paused")
	assertEqual(t, len(ids), 2, "two paused batches")
	assertEqual(t, ids[0], "paused-a", "first paused (ordered by created_at)")
	assertEqual(t, ids[1], "paused-b", "second paused")
}

func TestReassignDocumentTagsSkipsAndClears(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()

	doc1ID := CreateUniqueDocument(t, q, "ra-1.pdf")
	doc2ID := CreateUniqueDocument(t, q, "ra-2.pdf")
	doc3ID := CreateUniqueDocument(t, q, "ra-3.pdf")

	sourceID, err := q.CreateTag(ctx, "reassign-source")
	assertNoError(t, err, "create source tag")
	targetID, err := q.CreateTag(ctx, "reassign-target")
	assertNoError(t, err, "create target tag")

	assertNoError(t, q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: doc1ID, TagID: sourceID}), "doc1 → source")
	assertNoError(t, q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: doc2ID, TagID: sourceID}), "doc2 → source")
	assertNoError(t, q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: doc2ID, TagID: targetID}), "doc2 → target")
	assertNoError(t, q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: doc3ID, TagID: targetID}), "doc3 → target")

	reassigned, err := q.ReassignDocumentTags(ctx, ReassignDocumentTagsParams{SourceID: sourceID, TargetID: targetID})
	assertNoError(t, err, "reassign")
	assertEqual(t, reassigned, int64(1), "only doc1 (source-only) is re-pointed; doc2 already has target so its source row is skipped")

	leftovers, err := q.DeleteDocumentTagsForTag(ctx, sourceID)
	assertNoError(t, err, "delete leftovers")
	assertEqual(t, leftovers, int64(1), "doc2's skipped source row is the only leftover")

	assertNoError(t, q.DeleteTag(ctx, sourceID), "delete source tag")

	_, err = q.GetTag(ctx, sourceID)
	assertEqual(t, err, sql.ErrNoRows, "source tag is gone")

	target, err := q.GetTag(ctx, targetID)
	assertNoError(t, err, "target still exists")
	assertEqual(t, target.Name, "reassign-target", "target name untouched")

	assertEqual(t, len(mustGetDocumentTags(t, q, doc1ID)), 1, "doc1 carries one tag")
	assertEqual(t, mustGetDocumentTags(t, q, doc1ID)[0].ID, targetID, "doc1's tag is the target")

	assertEqual(t, len(mustGetDocumentTags(t, q, doc2ID)), 1, "doc2 carries one tag (leftover cleared)")
	assertEqual(t, mustGetDocumentTags(t, q, doc2ID)[0].ID, targetID, "doc2's tag is the target")

	assertEqual(t, len(mustGetDocumentTags(t, q, doc3ID)), 1, "doc3 carries one tag")
	assertEqual(t, mustGetDocumentTags(t, q, doc3ID)[0].ID, targetID, "doc3's tag is the target (unchanged)")
}

func TestTagPairEvidenceStatsAggregates(t *testing.T) {
	q, _ := NewTestQueries(t)
	ctx := context.Background()
	docID, _ := CreateTestDocument(t, q, "tpe-1.pdf")

	insert := func(query, target string, verdict types.TagVerdict, model, policy string) {
		var vm sql.NullString
		if model != "" {
			vm = sql.NullString{String: model, Valid: true}
		}
		_, err := q.InsertTagVerdictEvent(ctx, InsertTagVerdictEventParams{
			DocumentID:    docID,
			Query:         query,
			Target:        target,
			Sim:           0.85,
			Verdict:       verdict,
			VerdictModel:  vm,
			PolicyVersion: policy,
		})
		assertNoError(t, err, "insert event "+query+"→"+target)
	}

	insert("software development", "software engineering", types.TagVerdicts.Variant, "model-a", "v1")
	insert("software development", "software engineering", types.TagVerdicts.Same, "model-a", "v2")
	insert("software engineering", "software development", types.TagVerdicts.Related, "model-a", "v1")
	insert("java", "javascript", types.TagVerdicts.Related, "model-a", "v1")

	rows, err := q.TagPairEvidenceStats(ctx, TagPairEvidenceStatsParams{
		Queries: []string{"software development", "software engineering", "java"},
		Targets: []string{"software engineering", "software development", "javascript"},
	})
	assertNoError(t, err, "stats")

	var ab, ba, java TagPairEvidenceStatsRow
	for _, r := range rows {
		switch {
		case r.Query == "software development" && r.Target == "software engineering":
			ab = r
		case r.Query == "software engineering" && r.Target == "software development":
			ba = r
		case r.Query == "java" && r.Target == "javascript":
			java = r
		}
	}

	assertEqual(t, ab.EventCount, int64(2), "(a,b) event count")
	assertEqual(t, ab.VariantCount, int64(1), "(a,b) variant count")
	assertEqual(t, ab.SameCount, int64(1), "(a,b) same count")
	assertEqual(t, ab.RelatedCount, int64(0), "(a,b) related count")
	assertEqual(t, ab.DocumentCount, int64(1), "(a,b) distinct docs in this orientation")

	assertEqual(t, ba.EventCount, int64(1), "(b,a) event count")
	assertEqual(t, ba.RelatedCount, int64(1), "(b,a) related count")

	assertEqual(t, java.EventCount, int64(1), "java pair event count")
	assertEqual(t, java.RelatedCount, int64(1), "java pair related count")
}

// --- helpers ---

func mustGetDocumentTags(t *testing.T, q *Queries, documentID int64) []Tag {
	t.Helper()
	tags, err := q.GetDocumentTags(context.Background(), documentID)
	assertNoError(t, err, "get document tags")
	return tags
}

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

func insertTask(t *testing.T, q *Queries, taskID string, status types.TaskStatus) int64 {
	t.Helper()
	p := json.RawMessage(`{}`)
	id, err := q.CreateTask(context.Background(), CreateTaskParams{
		TaskID: taskID, TaskType: types.Task.Type.Consume, Status: status, Payload: &p,
	})
	assertNoError(t, err, "insert "+taskID)
	return id
}

// insertBatchTask creates a consume task attached to batchID; the transition guard reads the batch's lease,
// so callers seed one with seedLease.
func insertBatchTask(t *testing.T, q *Queries, taskID, batchID string, status types.TaskStatus) int64 {
	t.Helper()
	p := json.RawMessage(`{}`)
	id, err := q.CreateTask(context.Background(), CreateTaskParams{
		TaskID: taskID, TaskType: types.Task.Type.Consume, Status: status, Payload: &p,
		BatchID: sql.NullString{String: batchID, Valid: true},
	})
	assertNoError(t, err, "insert "+taskID)
	return id
}

// insertLinkedTask creates a task row linked to parentTaskID, optionally inside batchID: the shape every
// linkage query reads.
func insertLinkedTask(t *testing.T, q *Queries, taskID, parentTaskID, batchID string, taskType types.TaskType, status types.TaskStatus) int64 {
	t.Helper()
	p := json.RawMessage(`{}`)
	id, err := q.CreateTask(context.Background(), CreateTaskParams{
		TaskID:       taskID,
		TaskType:     taskType,
		Status:       status,
		Payload:      &p,
		BatchID:      sql.NullString{String: batchID, Valid: batchID != ""},
		ParentTaskID: sql.NullString{String: parentTaskID, Valid: parentTaskID != ""},
	})
	assertNoError(t, err, "insert "+taskID)
	return id
}

// seedLease creates a queued batch and its execution lease, the guard every task transition checks.
func seedLease(t *testing.T, q *Queries, batchID, owner string) {
	t.Helper()
	ctx := context.Background()
	assertNoError(t, q.CreateBatch(ctx, CreateBatchParams{
		ID: batchID, Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Queued,
	}), "create lease batch "+batchID)
	rows, err := q.TryInsertBatchOwner(ctx, TryInsertBatchOwnerParams{BatchID: batchID, OwnerID: owner, Pid: 1})
	assertNoError(t, err, "insert lease "+batchID)
	assertEqual(t, rows, int64(1), "lease inserted")
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
		"tag_verdict_event",
	}
	for _, tbl := range tables {
		if _, err := q.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", tbl)); err != nil {
			t.Fatalf("delete from %s: %v", tbl, err)
		}
	}
	for _, tbl := range []string{"document_type", "people_type", "tag", `"user"`, "task", "document", "saved_search", "orphaned_file"} {
		q.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN id RESTART WITH 1", tbl))
	}
	seeds := []string{"document-types", "people-types", "tags", "tag-aliases"}
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

func TestBackfillProcessedSizes_SentinelContract(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.pdf")
	if err := os.WriteFile(realPath, make([]byte, 12345), 0o644); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(dir, "missing.pdf") // never created

	insert := func(id string, storagePath string) {
		_, err := q.db.ExecContext(ctx,
			`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, processed_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id)
			 VALUES ($1, $2, $3, $4, 'application/pdf', 100, 0, '/tmp/o.pdf', $5, 1, 1, 5, 'eng', 1)`,
			id, id+".pdf", "md5-"+id, "sha512-"+id, storagePath)
		if err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("bf-real", realPath)
	insert("bf-missing", missingPath)

	if err := BackfillProcessedSizes(ctx, db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var real, missing int64
	if err := q.db.QueryRowContext(ctx, `SELECT processed_size FROM document WHERE document_id = 'bf-real'`).Scan(&real); err != nil {
		t.Fatal(err)
	}
	if err := q.db.QueryRowContext(ctx, `SELECT processed_size FROM document WHERE document_id = 'bf-missing'`).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if real != 12345 {
		t.Fatalf("real file processed_size = %d, want 12345", real)
	}
	if missing != -1 {
		t.Fatalf("missing file processed_size = %d, want -1 sentinel", missing)
	}

	// Second run must not re-stat the sentinelised row.
	if err := BackfillProcessedSizes(ctx, db); err != nil {
		t.Fatalf("backfill second run: %v", err)
	}
	if err := q.db.QueryRowContext(ctx, `SELECT processed_size FROM document WHERE document_id = 'bf-missing'`).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != -1 {
		t.Fatalf("sentinelised row changed on re-run: %d", missing)
	}

	// Aggregate must exclude the -1 sentinel (the GREATEST guard).
	agg, err := q.DocumentAggregates(ctx)
	if err != nil {
		t.Fatalf("DocumentAggregates: %v", err)
	}
	if agg.ProcessedBytes != 12345 {
		t.Fatalf("DocumentAggregates.ProcessedBytes = %d, want 12345 (-1 excluded)", agg.ProcessedBytes)
	}
}

// insertContentDoc creates a document with explicit TextContent and an
// optional pre-set TextHash. Used by the search/text_hash tests below
// where the existing insertDoc helper (fixed docID, hardcoded "content"
// text) is too rigid.
func insertContentDoc(t *testing.T, q *Queries, docID, content string, textHash sql.NullString) int64 {
	t.Helper()
	id, err := q.CreateDocument(context.Background(), CreateDocumentParams{
		DocumentID:     docID,
		Title:          docID + ".pdf",
		Md5Checksum:    "md5-" + docID,
		Sha512Checksum: "sha512-" + docID,
		OriginalType:   "application/pdf",
		FileSize:       int64(len(content)),
		OriginalPath:   "/tmp/" + docID,
		StoragePath:    "/tmp/storage/" + docID,
		TextContent:    sql.NullString{String: content, Valid: true},
		TextHash:       textHash,
		PageCount:      1,
		WordCount:      1,
		CharCount:      int32(len(content)),
		Language:       "eng",
	})
	if err != nil {
		t.Fatalf("create %s: %v", docID, err)
	}
	return id
}

// TestStructuredSearchMultiTagAnd pins the conjunction contract: a document
// must carry every requested tag to match. The baseline measured
// 116+890=1006 under the old OR semantics; with AND the same input must
// return exactly the intersection.
func TestStructuredSearchMultiTagAnd(t *testing.T) {
	q, _ := NewTestQueries(t)
	resetDB(t, q)
	ctx := context.Background()

	taxID, err := q.CreateTag(ctx, "taxes")
	if err != nil {
		t.Fatalf("create tag taxes: %v", err)
	}
	govID, err := q.CreateTag(ctx, "government")
	if err != nil {
		t.Fatalf("create tag government: %v", err)
	}
	secID, err := q.CreateTag(ctx, "government secrecy")
	if err != nil {
		t.Fatalf("create tag government secrecy: %v", err)
	}

	// docA: government + government secrecy + taxes (matches all three)
	// docB: government only (matches one of three)
	// docC: government secrecy only (matches one of three)
	// docD: government + taxes (matches two)
	docA := insertContentDoc(t, q, "tag-and-a", "alpha", sql.NullString{})
	docB := insertContentDoc(t, q, "tag-and-b", "beta", sql.NullString{})
	docC := insertContentDoc(t, q, "tag-and-c", "gamma", sql.NullString{})
	docD := insertContentDoc(t, q, "tag-and-d", "delta", sql.NullString{})

	mustAdd := func(docID int64, tagID int64) {
		t.Helper()
		if err := q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: docID, TagID: tagID}); err != nil {
			t.Fatalf("add tag: %v", err)
		}
	}
	mustAdd(docA, govID)
	mustAdd(docA, secID)
	mustAdd(docA, taxID)
	mustAdd(docB, govID)
	mustAdd(docC, secID)
	mustAdd(docD, govID)
	mustAdd(docD, taxID)

	ids := func(results []FTSDocumentRow) map[string]bool {
		m := map[string]bool{}
		for _, r := range results {
			m[r.DocumentID] = true
		}
		return m
	}

	t.Run("two tags returns intersection only", func(t *testing.T) {
		got, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Tags:  []string{"government", "government secrecy"},
			Limit: 100,
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		count, err := q.CountDocumentsStructured(ctx, SearchFilter{
			Tags:  []string{"government", "government secrecy"},
			Limit: 100,
		})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		gotIDs := ids(got)
		if len(got) != 1 || !gotIDs["tag-and-a"] {
			t.Fatalf("intersection search = %v, want exactly [tag-and-a]", gotIDs)
		}
		if count != 1 {
			t.Fatalf("intersection count = %d, want 1", count)
		}
	})

	t.Run("three tags requires all three", func(t *testing.T) {
		got, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Tags:  []string{"taxes", "government", "government secrecy"},
			Limit: 100,
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		gotIDs := ids(got)
		if len(got) != 1 || !gotIDs["tag-and-a"] {
			t.Fatalf("three-tag search = %v, want exactly [tag-and-a]", gotIDs)
		}
	})

	t.Run("duplicate tag names do not inflate HAVING count", func(t *testing.T) {
		got, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Tags:  []string{"government", "government", "government secrecy"},
			Limit: 100,
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		gotIDs := ids(got)
		if len(got) != 1 || !gotIDs["tag-and-a"] {
			t.Fatalf("dup-tag search = %v, want exactly [tag-and-a] (dedupe)", gotIDs)
		}
	})

	t.Run("unknown tag returns nothing", func(t *testing.T) {
		got, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Tags:  []string{"nonexistent"},
			Limit: 100,
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("unknown tag search = %d results, want 0", len(got))
		}
		count, _ := q.CountDocumentsStructured(ctx, SearchFilter{Tags: []string{"nonexistent"}, Limit: 100})
		if count != 0 {
			t.Fatalf("unknown tag count = %d, want 0", count)
		}
	})

	t.Run("single tag still returns union (degenerate AND)", func(t *testing.T) {
		got, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Tags:  []string{"government"},
			Limit: 100,
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		gotIDs := ids(got)
		if len(got) != 3 || !gotIDs["tag-and-a"] || !gotIDs["tag-and-b"] || !gotIDs["tag-and-d"] {
			t.Fatalf("single-tag search = %v, want [tag-and-a tag-and-b tag-and-d]", gotIDs)
		}
	})
}

// TestStructuredSearchRankDescOrderTieBreak pins the FTS ordering contract:
// results are ranked best-first by ts_rank, and equal-rank ties are
// broken deterministically by document id so LIMIT/OFFSET pagination
// doesn't skip or duplicate rows across pages.
func TestStructuredSearchRankDescOrderTieBreak(t *testing.T) {
	q, _ := NewTestQueries(t)
	resetDB(t, q)
	ctx := context.Background()

	// Three documents with the same repeated query term so ts_rank lands
	// on ties; the tie-break by d.id must give ascending id order.
	insertContentDoc(t, q, "rank-c", "budget budget budget report", sql.NullString{})
	insertContentDoc(t, q, "rank-a", "budget report", sql.NullString{})
	insertContentDoc(t, q, "rank-b", "budget budget report", sql.NullString{})

	got, err := q.SearchDocumentsStructured(ctx, SearchFilter{
		Query: "budget",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("need at least 2 results to assert order, got %d", len(got))
	}

	// First result must have rank >= every later result.
	for i := 1; i < len(got); i++ {
		if got[0].Rank < got[i].Rank {
			t.Fatalf("rank not best-first: result[0].Rank=%f < result[%d].Rank=%f", got[0].Rank, i, got[i].Rank)
		}
	}

	// Among rows with the same rank (ties), d.id must be ascending.
	for i := 1; i < len(got); i++ {
		if got[i-1].Rank == got[i].Rank && got[i-1].ID >= got[i].ID {
			t.Fatalf("rank ties not broken by d.id: got %d (id=%d) before %d (id=%d) at equal rank %f",
				i-1, got[i-1].ID, i, got[i].ID, got[i].Rank)
		}
	}
}

// TestStructuredSearchPastTheEndOffset verifies the past-the-end behaviour
// at the database layer. The guard that returns empty results without
// issuing the search query lives in the search engine; at the DB layer
// past-the-end offsets simply yield zero rows with the correct count.
func TestStructuredSearchPastTheEndOffset(t *testing.T) {
	q, _ := NewTestQueries(t)
	resetDB(t, q)
	ctx := context.Background()

	insertContentDoc(t, q, "past-end-1", "alpha alpha alpha", sql.NullString{})
	insertContentDoc(t, q, "past-end-2", "alpha alpha", sql.NullString{})

	t.Run("offset=0 hot path returns full page", func(t *testing.T) {
		got, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Query:  "alpha",
			Limit:  10,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("offset=0 results = %d, want 2", len(got))
		}
	})

	t.Run("offset past total returns empty results, count unaffected", func(t *testing.T) {
		got, err := q.SearchDocumentsStructured(ctx, SearchFilter{
			Query:  "alpha",
			Limit:  10,
			Offset: 50,
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("past-the-end results = %d, want 0", len(got))
		}
		count, err := q.CountDocumentsStructured(ctx, SearchFilter{Query: "alpha", Limit: 10, Offset: 50})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 2 {
			t.Fatalf("count = %d, want 2 (offset does not affect count)", count)
		}
	})
}

// TestBackfillTextHash covers the backfill contract:
//   - rows with text_content get text_hash = sha256(text_content);
//   - rows with NULL text_content keep text_hash NULL;
//   - a row whose text_hash was set concurrently (e.g. by the consumer)
//     is not clobbered by the backfill (the TOCTOU-safe WHERE clause);
//   - re-running is idempotent.
func TestBackfillTextHash(t *testing.T) {
	q, db := NewTestQueries(t)
	resetDB(t, q)
	ctx := context.Background()

	// bf-hash1: text_content present, text_hash NULL → backfilled.
	// bf-hash2: text_content present + pre-set text_hash from a concurrent
	// writer → must NOT be overwritten by the backfill.
	insertContentDoc(t, q, "bf-hash1", "hello world", sql.NullString{})
	insertContentDoc(t, q, "bf-hash2", "stale text", sql.NullString{
		String: "writer-set-hash",
		Valid:  true,
	})
	// bf-null: text_content column is actually NULL (not just empty).
	if _, err := q.db.ExecContext(ctx,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path, page_count, word_count, char_count, language)
		 VALUES ('bf-null', 'bf-null.pdf', 'md5-bf-null', 'sha512-bf-null', 'application/pdf', 0, '/tmp/bf-null', '/tmp/storage/bf-null', 0, 0, 0, 'eng')`,
	); err != nil {
		t.Fatalf("insert bf-null: %v", err)
	}

	if err := BackfillTextHash(ctx, db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var (
		got1, got2 sql.NullString
	)
	if err := q.db.QueryRowContext(ctx, `SELECT text_hash FROM document WHERE document_id='bf-hash1'`).Scan(&got1); err != nil {
		t.Fatalf("read bf-hash1: %v", err)
	}
	if err := q.db.QueryRowContext(ctx, `SELECT text_hash FROM document WHERE document_id='bf-hash2'`).Scan(&got2); err != nil {
		t.Fatalf("read bf-hash2: %v", err)
	}
	want1 := storage.SHA256Hex("hello world")
	if !got1.Valid || got1.String != want1 {
		t.Fatalf("bf-hash1.text_hash = %+v, want %q", got1, want1)
	}
	if !got2.Valid || got2.String != "writer-set-hash" {
		t.Fatalf("bf-hash2.text_hash = %+v, want \"writer-set-hash\" (backfill must not clobber concurrent writers)", got2)
	}

	var gotNull sql.NullString
	if err := q.db.QueryRowContext(ctx, `SELECT text_hash FROM document WHERE document_id='bf-null'`).Scan(&gotNull); err != nil {
		t.Fatalf("read bf-null: %v", err)
	}
	if gotNull.Valid {
		t.Fatalf("bf-null.text_hash = %+v, want NULL (no content ⇒ no hash)", gotNull)
	}

	if err := BackfillTextHash(ctx, db); err != nil {
		t.Fatalf("backfill second run: %v", err)
	}
	var got1Again sql.NullString
	if err := q.db.QueryRowContext(ctx, `SELECT text_hash FROM document WHERE document_id='bf-hash1'`).Scan(&got1Again); err != nil {
		t.Fatalf("read bf-hash1 (re-run): %v", err)
	}
	if got1Again != got1 {
		t.Fatalf("bf-hash1.text_hash changed on re-run: was %q, now %q", got1.String, got1Again.String)
	}
}

// TestTextHashPersistedAtWrite pins the consume-side contract: a
// CreateDocument call with an explicit TextHash must persist that value
// to the row. The end-to-end round-trip through Consumer.Process is
// covered in internal/documents/consumption/integration_test.go.
func TestTextHashPersistedAtWrite(t *testing.T) {
	q, _ := NewTestQueries(t)
	resetDB(t, q)
	ctx := context.Background()

	want := storage.SHA256Hex("round-trip content for the text_hash column")
	insertContentDoc(t, q, "hash-rt", "round-trip content for the text_hash column", sql.NullString{
		String: want,
		Valid:  true,
	})

	// GetDocumentRow doesn't currently select text_hash, so confirm via
	// direct SQL — this is the field the backfill / cache contract reads.
	var got sql.NullString
	if err := q.db.QueryRowContext(ctx, `SELECT text_hash FROM document WHERE document_id='hash-rt'`).Scan(&got); err != nil {
		t.Fatalf("read text_hash: %v", err)
	}
	if !got.Valid || got.String != want {
		t.Fatalf("persisted text_hash = %+v, want %q", got, want)
	}
}

func TestTagVerdictEvent_RoundTrip(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	docID, _ := CreateTestDocument(t, q, "verdict-rt.pdf")

	insert := func(query, target string, sim float64, verdict types.TagVerdict, model string, policy string) {
		var vm sql.NullString
		if model != "" {
			vm = sql.NullString{String: model, Valid: true}
		}
		_, err := q.InsertTagVerdictEvent(ctx, InsertTagVerdictEventParams{
			DocumentID:    docID,
			Query:         query,
			Target:        target,
			Sim:           sim,
			Verdict:       verdict,
			VerdictModel:  vm,
			PolicyVersion: policy,
		})
		assertNoError(t, err, "insert event")
	}

	// Two rows for the same pair at v1: the latest by verdict_at wins.
	insert("democracy", "direct democracy", 0.81, types.TagVerdicts.Variant, "model-a", "v1")
	insert("democracy", "direct democracy", 0.82, types.TagVerdicts.Same, "", "v1")
	// A row at v2 must be excluded by the version filter.
	insert("democracy", "direct democracy", 0.83, types.TagVerdicts.Related, "model-b", "v2")
	// An unrelated pair that should not match the cache lookup.
	insert("anarchism", "anarcho communism", 0.80, types.TagVerdicts.Related, "model-a", "v1")

	rows, err := q.LatestTagVerdictsForPairs(ctx, LatestTagVerdictsForPairsParams{
		PolicyVersion: "v1",
		Queries:       []string{"democracy", "anarchism"},
		Targets:       []string{"direct democracy", "anarcho communism"},
	})
	assertNoError(t, err, "cache lookup")
	assertEqual(t, len(rows), 2, "two distinct pairs")

	byQuery := map[string]LatestTagVerdictsForPairsRow{}
	for _, r := range rows {
		byQuery[r.Query] = r
	}

	democracy := byQuery["democracy"]
	assertEqual(t, string(democracy.Verdict), "same", "latest verdict at v1 wins")
	assertEqual(t, democracy.VerdictModel.Valid, false, "NULL model round-trips as invalid")
	assertEqual(t, democracy.PolicyVersion, "v1", "v2 row excluded by version filter")

	anarchism := byQuery["anarchism"]
	assertEqual(t, string(anarchism.Verdict), "related", "second pair matched")
	assertEqual(t, anarchism.VerdictModel.Valid, true, "non-NULL model round-trips as valid")
	assertEqual(t, anarchism.VerdictModel.String, "model-a", "model preserved")
}

func TestDumpSkipsMatcherTextEmbedding(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()

	if err := q.InsertMatcherTextEmbedding(ctx, InsertMatcherTextEmbeddingParams{
		Fingerprint: "fp-dump-test",
		InputHash:   []byte{1, 2, 3},
		Embedding:   []byte{0x00, 0x00, 0x80, 0x3F},
	}); err != nil {
		t.Fatalf("seed cache row: %v", err)
	}

	var buf bytes.Buffer
	if err := DumpSchemaAndData(ctx, db, SchemaFS, &buf); err != nil {
		t.Fatalf("DumpSchemaAndData: %v", err)
	}
	out := buf.String()

	if strings.Contains(out, `INSERT INTO "matcher_text_embedding"`) {
		t.Fatal("cache rows leaked into the dump: bytea data cannot round-trip the literal value " +
			"formatter, so an unrestorable backup results if a bytea table is not excluded from data dumps")
	}
	if !strings.Contains(out, `INSERT INTO "document_type"`) {
		t.Fatal("dump carries no document_type rows — the exclusion assertion above is vacuous without data in the dump")
	}
	if !strings.Contains(out, "CREATE TABLE matcher_text_embedding") {
		t.Fatal("schema preamble lost matcher_text_embedding — a restore must recreate the table the daemon populates")
	}
}

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/types"
)

// reconcile tag names — chosen to avoid collisions with the seed-tag list.
const (
	rQ  = "quantum entanglement"
	rT  = "anti quantum entanglement"
	rR  = "string theory"
	rS  = "tensor field"
	rR2 = "wavelet"
)

func reconcileInsertDoc(t *testing.T, q *Queries, docID string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := q.db.QueryRowContext(ctx,
		"INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path) VALUES ($1, 't', $2, $2, 'application/pdf', 10, '/x', '/y') RETURNING id",
		docID, docID).Scan(&id)
	assertNoError(t, err, "insert document")
	return id
}

func reconcileInsertTag(t *testing.T, q *Queries, name string) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := q.db.ExecContext(ctx, "INSERT INTO tag (name) VALUES ($1) ON CONFLICT DO NOTHING", name); err != nil {
		t.Fatalf("insert tag %q: %v", name, err)
	}
	var id int64
	if err := q.db.QueryRowContext(ctx, "SELECT id FROM tag WHERE name = $1", name).Scan(&id); err != nil {
		t.Fatalf("lookup tag %q: %v", name, err)
	}
	return id
}

func TestReconcileBatchAndTaskQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	if n, err := q.CountActiveTagReconcileBatches(ctx); err != nil || n != 0 {
		t.Fatalf("initial active batches = %d, %v", n, err)
	}
	if _, err := q.GetLastTagReconcileBatchCreatedAt(ctx); err != sql.ErrNoRows {
		t.Fatalf("initial GetLastTagReconcileBatchCreatedAt: want ErrNoRows, got %v", err)
	}
	if _, err := q.GetLastCompletedTagReconcileTask(ctx); err != sql.ErrNoRows {
		t.Fatalf("initial GetLastCompletedTagReconcileTask: want ErrNoRows, got %v", err)
	}

	if _, err := q.db.ExecContext(ctx, "INSERT INTO batch (id, origin, status) VALUES ('rc-b1', 'schedule', 'queued')"); err != nil {
		t.Fatalf("insert batch: %v", err)
	}
	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO task (task_id, task_type, status, batch_id, dedup_key, completed_at) VALUES ('rc-t1', 'tagreconcile', 'completed', 'rc-b1', 'tagreconcile:2026-09-12', now())"); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if n, err := q.CountActiveTagReconcileBatches(ctx); err != nil || n != 1 {
		t.Fatalf("active batches = %d, %v", n, err)
	}
	if ts, err := q.GetLastTagReconcileBatchCreatedAt(ctx); err != nil || !ts.Valid {
		t.Fatalf("GetLastTagReconcileBatchCreatedAt = %v, %v", ts, err)
	}
	if ts, err := q.GetLastCompletedTagReconcileTask(ctx); err != nil || !ts.Valid {
		t.Fatalf("GetLastCompletedTagReconcileTask = %v, %v", ts, err)
	}

	// GetLastCompletedTagReconcileTaskWithResult returns the same row with result.
	row, err := q.GetLastCompletedTagReconcileTaskWithResult(ctx)
	if err != nil {
		t.Fatalf("GetLastCompletedTagReconcileTaskWithResult: %v", err)
	}
	if row.TaskID != "rc-t1" {
		t.Fatalf("GetLastCompletedTagReconcileTaskWithResult TaskID = %q, want rc-t1", row.TaskID)
	}
	if row.Result != nil {
		t.Fatalf("GetLastCompletedTagReconcileTaskWithResult Result = %v, want nil", row.Result)
	}

	// Insert a second completed task with a non-nil result.
	resultJSON := json.RawMessage(`{"report_only":true,"worklist":{"stale":1}}`)
	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO task (task_id, task_type, status, batch_id, dedup_key, result, completed_at) VALUES ('rc-t2', 'tagreconcile', 'completed', 'rc-b1', 'tagreconcile:2026-09-13', $1, now())",
		resultJSON); err != nil {
		t.Fatalf("insert task with result: %v", err)
	}

	// Latest completed row should now be rc-t2.
	row2, err := q.GetLastCompletedTagReconcileTaskWithResult(ctx)
	if err != nil {
		t.Fatalf("GetLastCompletedTagReconcileTaskWithResult after insert: %v", err)
	}
	if row2.TaskID != "rc-t2" {
		t.Fatalf("GetLastCompletedTagReconcileTaskWithResult TaskID = %q, want rc-t2", row2.TaskID)
	}
	if row2.Result == nil {
		t.Fatal("GetLastCompletedTagReconcileTaskWithResult Result = nil, want non-nil")
	}
	var report struct {
		ReportOnly bool `json:"report_only"`
	}
	if err := json.Unmarshal(*row2.Result, &report); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !report.ReportOnly {
		t.Fatal("result.report_only = false, want true")
	}
}

func TestReconcileWorklistScans(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	docID := reconcileInsertDoc(t, q, "rc-doc")
	reconcileInsertTag(t, q, rQ)
	reconcileInsertTag(t, q, rT)

	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO tag_verdict_event (document_id, query, target, sim, verdict, verdict_model, policy_version, verdict_at) VALUES ($1, $2, $3, 0.9, 'same', 'm', 'v0', now() - interval '1 day')",
		docID, rQ, rT); err != nil {
		t.Fatalf("insert v0 event: %v", err)
	}
	// v1 row is cache-applied (verdict_model NULL) so the pair has no fresh
	// row at v1 and surfaces in scan 1.
	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO tag_verdict_event (document_id, query, target, sim, verdict, verdict_model, policy_version, verdict_at) VALUES ($1, $2, $3, 0.9, 'same', NULL, 'v1', now())",
		docID, rQ, rT); err != nil {
		t.Fatalf("insert v1 cache-applied: %v", err)
	}

	scan1, err := q.TagVerdictPairsWithoutFreshVerdict(ctx, "v1")
	assertNoError(t, err, "scan1")
	if len(scan1) != 1 {
		t.Fatalf("scan1 = %+v", scan1)
	}
	if scan1[0].Query != rQ || scan1[0].Target != rT || scan1[0].PolicyVersion != "v1" {
		t.Fatalf("scan1 row = %+v", scan1[0])
	}

	n, err := q.UpsertFreshTagVerdict(ctx, UpsertFreshTagVerdictParams{
		DocumentID: docID, Query: rQ, Target: rT, Sim: 0.91,
		Verdict:       types.TagVerdicts.Variant,
		VerdictModel:  sql.NullString{String: "policy:v1", Valid: true},
		PolicyVersion: "v1",
	})
	assertNoError(t, err, "upsert insert")
	if n != 1 {
		t.Fatalf("upsert rows = %d", n)
	}
	scan1After, err := q.TagVerdictPairsWithoutFreshVerdict(ctx, "v1")
	assertNoError(t, err, "scan1 after upsert")
	if len(scan1After) != 0 {
		t.Fatalf("scan1 after upsert = %+v", scan1After)
	}

	n, err = q.UpsertFreshTagVerdict(ctx, UpsertFreshTagVerdictParams{
		DocumentID: docID, Query: rQ, Target: rT, Sim: 0.92,
		Verdict:       types.TagVerdicts.Related,
		VerdictModel:  sql.NullString{String: "policy:v1", Valid: true},
		PolicyVersion: "v1",
	})
	assertNoError(t, err, "upsert conflict")
	if n != 1 {
		t.Fatalf("upsert conflict rows = %d", n)
	}

	// The conflict update reset verdict_at = now(), so the upserted row is
	// not yet old — exercise the age boundary with a separate fresh row
	// at v1 with an old verdict_at.
	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO tag_verdict_event (document_id, query, target, sim, verdict, verdict_model, policy_version, verdict_at) VALUES ($1, 'stale-q', 'stale-t', 0.9, 'variant', 'm', 'v1', now() - interval '2 days')",
		docID); err != nil {
		t.Fatalf("insert stale fresh row: %v", err)
	}
	// Cutoff 1 hour ago: the stale row (2 days old) is strictly before the
	// cutoff → matches. The fresh upserted row (verdict_at = now) is after
	// the cutoff → excluded.
	cutoff := time.Now().Add(-1 * time.Hour)
	aged, err := q.TagVerdictPairsOlderThan(ctx, TagVerdictPairsOlderThanParams{PolicyVersion: "v1", VerdictAt: cutoff})
	assertNoError(t, err, "aged past cutoff")
	if len(aged) != 1 || aged[0].Query != "stale-q" {
		t.Fatalf("aged past cutoff = %+v", aged)
	}
	// Cutoff far in the future: every fresh row's verdict_at is strictly
	// before it → all returned.
	futureCutoff := time.Now().Add(100 * 365 * 24 * time.Hour)
	agedAll, err := q.TagVerdictPairsOlderThan(ctx, TagVerdictPairsOlderThanParams{PolicyVersion: "v1", VerdictAt: futureCutoff})
	assertNoError(t, err, "aged future cutoff")
	if len(agedAll) != 2 {
		t.Fatalf("aged future cutoff = %+v", agedAll)
	}
}

func TestReconcileMissingTagsAndDelete(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	docID := reconcileInsertDoc(t, q, "rc-doc")
	reconcileInsertTag(t, q, rT)
	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO tag_verdict_event (document_id, query, target, sim, verdict, verdict_model, policy_version) VALUES ($1, 'ghost', $2, 0.9, 'variant', 'm', 'v1')",
		docID, rT); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	missing, err := q.TagVerdictPairsWithMissingTags(ctx)
	assertNoError(t, err, "missing")
	if len(missing) != 1 || missing[0].Query != "ghost" || !missing[0].QueryMissing {
		t.Fatalf("missing = %+v", missing)
	}

	n, err := q.DeleteFreshTagVerdictsForPairs(ctx, DeleteFreshTagVerdictsForPairsParams{
		Column1: []string{"ghost"}, Column2: []string{rT},
	})
	assertNoError(t, err, "delete fresh")
	if n != 1 {
		t.Fatalf("delete rows = %d", n)
	}
}

func TestReconcileKeptPairsAndStats(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	docID := reconcileInsertDoc(t, q, "rc-doc")
	reconcileInsertTag(t, q, rR)
	reconcileInsertTag(t, q, rS)

	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO tag_verdict_event (document_id, query, target, sim, verdict, verdict_model, policy_version) VALUES ($1, $2, $3, 0.85, 'variant', 'm', 'v1')",
		docID, rR, rS); err != nil {
		t.Fatalf("insert kept: %v", err)
	}

	kept, err := q.TagVerdictPairsByVerdict(ctx, "v1")
	assertNoError(t, err, "kept")
	if len(kept) != 1 || kept[0].Query != rR || kept[0].Verdict != types.TagVerdicts.Variant {
		t.Fatalf("kept = %+v", kept)
	}

	stats, err := q.TagPairEvidenceStats(ctx, TagPairEvidenceStatsParams{
		Queries: []string{rR, rS}, Targets: []string{rS, rR},
	})
	assertNoError(t, err, "stats")
	if len(stats) != 1 || stats[0].DocumentCount != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestReconcileDocumentQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	docID := reconcileInsertDoc(t, q, "rc-doc-1")
	reconcileInsertDoc(t, q, "rc-doc-2")
	tID := reconcileInsertTag(t, q, rT)
	reconcileInsertTag(t, q, "ghost")

	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO tag_verdict_event (document_id, query, target, sim, verdict, verdict_model, policy_version) VALUES ($1, 'ghost', $2, 0.9, 'variant', 'm', 'v1')",
		docID, rT); err != nil {
		t.Fatalf("insert ghost event: %v", err)
	}

	if _, err := q.db.ExecContext(ctx, "INSERT INTO document_tag (document_id, tag_id) VALUES ($1, $2)", docID, tID); err != nil {
		t.Fatalf("insert doc_tag: %v", err)
	}
	res, err := q.GetDocumentIDsByDocumentIDs(ctx, []string{"rc-doc-1", "rc-doc-2", "missing"})
	assertNoError(t, err, "resolve")
	if len(res) != 2 {
		t.Fatalf("resolve = %+v", res)
	}

	qID := reconcileInsertTag(t, q, rQ)
	if _, err := q.db.ExecContext(ctx, "INSERT INTO document_tag (document_id, tag_id) VALUES ($1, $2)", docID, qID); err != nil {
		t.Fatalf("insert existing q-tag: %v", err)
	}
	rp, err := q.RepointDocumentTagsForDocs(ctx, RepointDocumentTagsForDocsParams{
		TargetID: qID, SourceID: tID, DocumentIds: []int64{docID},
	})
	assertNoError(t, err, "repoint")
	if rp != 0 {
		t.Fatalf("repoint on a doc that already has target must noop (NOT EXISTS guard), got %d", rp)
	}

	ids, err := q.DocumentIDsForPairs(ctx, DocumentIDsForPairsParams{
		Column1: []string{"missing-pair"}, Column2: []string{"also-missing"},
	})
	assertNoError(t, err, "doc ids for pairs")
	if len(ids) != 0 {
		t.Fatalf("doc ids for missing pair = %v", ids)
	}

	withRows, err := q.TagVerdictQueriesWithRows(ctx, []string{"ghost", "phantom"})
	assertNoError(t, err, "queries with rows")
	if len(withRows) != 1 || withRows[0] != "ghost" {
		t.Fatalf("withRows = %v", withRows)
	}

	latest, err := q.LatestTagVerdictRowsForPairs(ctx, LatestTagVerdictRowsForPairsParams{
		Column1: []string{"ghost"}, Column2: []string{rT},
	})
	assertNoError(t, err, "latest")
	if len(latest) != 1 {
		t.Fatalf("latest = %+v", latest)
	}
}

func TestReconcileConsumeSlotExclusion(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	if _, err := q.db.ExecContext(ctx, "INSERT INTO batch (id, origin, status) VALUES ('rc-b', 'schedule', 'queued')"); err != nil {
		t.Fatalf("insert tagreconcile batch: %v", err)
	}
	if _, err := q.db.ExecContext(ctx,
		"INSERT INTO task (task_id, task_type, status, batch_id, dedup_key) VALUES ('rc-b-task', 'tagreconcile', 'pending', 'rc-b', 'tagreconcile')"); err != nil {
		t.Fatalf("insert tagreconcile task: %v", err)
	}
	if n, err := q.CountQueuedConsumeBatches(ctx, types.NonConsumeTaskTypeValues()); err != nil || n != 0 {
		t.Fatalf("tagreconcile batch counted as consume: n=%d %v", n, err)
	}
	if _, err := q.GetNextQueuedBatch(ctx, types.NonConsumeTaskTypeValues()); err != sql.ErrNoRows {
		t.Fatalf("GetNextQueuedBatch must not return tagreconcile batch, got %v", err)
	}
}

func assertOrphanMark(t *testing.T, q *Queries, id int64, want bool) {
	t.Helper()
	var marked sql.NullTime
	if err := q.db.QueryRowContext(context.Background(), "SELECT orphaned_since FROM tag WHERE id = $1", id).Scan(&marked); err != nil {
		t.Fatalf("read orphan mark for tag %d: %v", id, err)
	}
	if marked.Valid != want {
		t.Fatalf("tag %d orphan mark = %v, want marked=%v", id, marked, want)
	}
}

// TestOrphanTagMarkLifecycle covers the marker maintenance the sweep relies on:
// only document-less tags get a mark, assignment clears it by tag id, and the
// sweep predicates keep tags that currently hold documents out of the delete set.
func TestOrphanTagMarkLifecycle(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	cutoff := time.Now().Add(-7 * 24 * time.Hour)

	orphanID := reconcileInsertTag(t, q, "orphan lifecycle tag")
	usedID := reconcileInsertTag(t, q, "used lifecycle tag")
	docID := reconcileInsertDoc(t, q, "orphan-lifecycle-doc")
	if err := q.AddDocumentTag(ctx, AddDocumentTagParams{DocumentID: docID, TagID: usedID, Source: "enrich"}); err != nil {
		t.Fatalf("tag document: %v", err)
	}

	if _, err := q.MarkOrphanTags(ctx); err != nil {
		t.Fatalf("MarkOrphanTags: %v", err)
	}
	assertOrphanMark(t, q, orphanID, true)
	assertOrphanMark(t, q, usedID, false)

	if err := q.ClearOrphanTagMarksByTagIDs(ctx, []int64{orphanID}); err != nil {
		t.Fatalf("ClearOrphanTagMarksByTagIDs: %v", err)
	}
	assertOrphanMark(t, q, orphanID, false)

	// Age both marks: both look sweep-eligible by timestamp, but only the tag
	// without document_tag rows may be listed or deleted.
	if _, err := q.MarkOrphanTags(ctx); err != nil {
		t.Fatalf("MarkOrphanTags: %v", err)
	}
	for _, id := range []int64{orphanID, usedID} {
		if _, err := q.db.ExecContext(ctx, "UPDATE tag SET orphaned_since = now() - interval '30 days' WHERE id = $1", id); err != nil {
			t.Fatalf("age orphan mark %d: %v", id, err)
		}
	}

	names, err := q.ListOrphanTags(ctx, cutoff)
	if err != nil {
		t.Fatalf("ListOrphanTags: %v", err)
	}
	if !slices.Contains(names, "orphan lifecycle tag") {
		t.Fatalf("ListOrphanTags = %v, want the aged orphan", names)
	}
	if slices.Contains(names, "used lifecycle tag") {
		t.Fatalf("ListOrphanTags = %v, must exclude a tag that still has documents", names)
	}

	deleted, err := q.DeleteOrphanTags(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteOrphanTags: %v", err)
	}
	if !slices.Contains(deleted, "orphan lifecycle tag") {
		t.Fatalf("DeleteOrphanTags = %v, want the aged orphan", deleted)
	}
	if slices.Contains(deleted, "used lifecycle tag") {
		t.Fatalf("DeleteOrphanTags = %v, must not delete a tag that still has documents", deleted)
	}

	var surviving int
	if err := q.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tag WHERE id = $1", usedID).Scan(&surviving); err != nil {
		t.Fatalf("count used tag: %v", err)
	}
	if surviving != 1 {
		t.Fatal("tag holding documents must survive the sweep")
	}
}

// TestOrphanTagMarkTransactionRollback pins the report-only contract: a sweep
// that refreshes marks inside a transaction must see them within that
// transaction and leave no trace once it rolls back.
func TestOrphanTagMarkTransactionRollback(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	freshID := reconcileInsertTag(t, q, "rollback mark tag")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	txQ := New(tx)
	if _, err := txQ.MarkOrphanTags(ctx); err != nil {
		t.Fatalf("MarkOrphanTags in tx: %v", err)
	}
	names, err := txQ.ListOrphanTags(ctx, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("ListOrphanTags in tx: %v", err)
	}
	if !slices.Contains(names, "rollback mark tag") {
		t.Fatalf("ListOrphanTags in tx = %v, want the freshly marked tag", names)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	assertOrphanMark(t, q, freshID, false)
}

func TestMigration00026TagPromotionCorrection(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	docID1 := reconcileInsertDoc(t, q, "m26-doc1")
	docID2 := reconcileInsertDoc(t, q, "m26-doc2")

	// Fixture A: backfill signature (promoted_at = created_at), 1 doc → demoted by Up.
	backfillSig := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tagA := reconcileInsertTag(t, q, "m26-backfill-single")
	q.db.ExecContext(ctx, "UPDATE tag SET created_at = $1, promoted_at = $1 WHERE id = $2", backfillSig, tagA)
	q.db.ExecContext(ctx, "INSERT INTO document_tag (document_id, tag_id, source) VALUES ($1, $2, 'enrich')", docID1, tagA)

	// Fixture B: backfill signature, 2 docs → survives Up.
	tagB := reconcileInsertTag(t, q, "m26-backfill-double")
	q.db.ExecContext(ctx, "UPDATE tag SET created_at = $1, promoted_at = $1 WHERE id = $2", backfillSig, tagB)
	q.db.ExecContext(ctx, "INSERT INTO document_tag (document_id, tag_id, source) VALUES ($1, $2, 'enrich')", docID1, tagB)
	q.db.ExecContext(ctx, "INSERT INTO document_tag (document_id, tag_id, source) VALUES ($1, $2, 'enrich')", docID2, tagB)

	// Fixture C: runtime promotion (promoted_at ≠ created_at), 1 doc → survives Up.
	tagC := reconcileInsertTag(t, q, "m26-runtime-single")
	q.db.ExecContext(ctx, "UPDATE tag SET created_at = $1, promoted_at = $2 WHERE id = $3",
		backfillSig, time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC), tagC)
	q.db.ExecContext(ctx, "INSERT INTO document_tag (document_id, tag_id, source) VALUES ($1, $2, 'enrich')", docID1, tagC)

	// Fixture D: NULL created_at, promoted_at set, 1 doc → survives (documented edge).
	tagD := reconcileInsertTag(t, q, "m26-null-created")
	q.db.ExecContext(ctx, "UPDATE tag SET created_at = NULL, promoted_at = now() - interval '1 day' WHERE id = $1", tagD)
	q.db.ExecContext(ctx, "INSERT INTO document_tag (document_id, tag_id, source) VALUES ($1, $2, 'enrich')", docID1, tagD)

	isProvisional := func(tagID int64) bool {
		t.Helper()
		var v bool
		if err := q.db.QueryRowContext(ctx, "SELECT (promoted_at IS NULL) FROM tag WHERE id = $1", tagID).Scan(&v); err != nil {
			t.Fatalf("check provisional for tag %d: %v", tagID, err)
		}
		return v
	}

	// --- Up: demote backfill singletons (signature + <2 docs) ---
	n, err := q.db.ExecContext(ctx, `
		UPDATE tag t SET promoted_at = NULL
		WHERE t.promoted_at IS NOT NULL
		  AND t.promoted_at = t.created_at
		  AND (SELECT COUNT(*) FROM document_tag dt WHERE dt.tag_id = t.id) < 2`)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if up, _ := n.RowsAffected(); up < 1 {
		t.Fatalf("Up affected %d rows, want >= 1 (at least fixture A)", up)
	}
	if !isProvisional(tagA) {
		t.Error("A (backfill + 1 doc): want provisional after Up")
	}
	if isProvisional(tagB) {
		t.Error("B (backfill + 2 docs): want promoted after Up")
	}
	if isProvisional(tagC) {
		t.Error("C (runtime promotion): want promoted after Up")
	}
	if isProvisional(tagD) {
		t.Error("D (NULL created_at): want promoted after Up")
	}

	// --- Down: re-promote provisionals that still have assignments ---
	n, err = q.db.ExecContext(ctx, `
		UPDATE tag t SET promoted_at = coalesce(t.created_at, now())
		WHERE t.promoted_at IS NULL
		  AND EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id)`)
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	if down, _ := n.RowsAffected(); down < 1 {
		t.Fatalf("Down affected %d rows, want >= 1 (at least fixture A)", down)
	}
	if isProvisional(tagA) {
		t.Error("A (backfill + 1 doc): want promoted after Down")
	}
	if isProvisional(tagD) {
		t.Error("D (NULL created_at): want promoted after Down")
	}

	// After Down, A's backfill signature should be restored (promoted_at = created_at).
	var sigOK bool
	if err := q.db.QueryRowContext(ctx,
		"SELECT (promoted_at IS NOT DISTINCT FROM created_at) FROM tag WHERE id = $1", tagA).Scan(&sigOK); err != nil {
		t.Fatalf("check sig for A: %v", err)
	}
	if !sigOK {
		t.Error("A after Down: want promoted_at = created_at (backfill signature restored)")
	}
}

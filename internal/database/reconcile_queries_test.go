package database

import (
	"context"
	"database/sql"
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

	if _, err := q.db.ExecContext(ctx, "INSERT INTO batch (id, source, status) VALUES ('rc-b1', 'tagreconcile', 'queued')"); err != nil {
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
		Verdict: types.TagVerdicts.Variant,
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
		Verdict: types.TagVerdicts.Related,
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

	if _, err := q.db.ExecContext(ctx, "INSERT INTO batch (id, source, status) VALUES ('rc-b', 'tagreconcile', 'queued')"); err != nil {
		t.Fatalf("insert tagreconcile batch: %v", err)
	}
	if n, err := q.CountQueuedConsumeBatches(ctx); err != nil || n != 0 {
		t.Fatalf("tagreconcile batch counted as consume: n=%d %v", n, err)
	}
	if _, err := q.GetNextQueuedBatch(ctx); err != sql.ErrNoRows {
		t.Fatalf("GetNextQueuedBatch must not return tagreconcile batch, got %v", err)
	}
}

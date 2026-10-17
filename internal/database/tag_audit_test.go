package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"testing"
)

// seedCoverageEvent inserts one coverage event with the defaults the audit writes, overridden per case.
func seedCoverageEvent(t *testing.T, q *Queries, docID int64, mutate func(p *InsertTagCoverageEventParams)) {
	t.Helper()
	p := InsertTagCoverageEventParams{
		DocumentID:      docID,
		TagName:         "zqauditdim",
		Class:           "gray",
		Source:          "trigger",
		Verdict:         json.RawMessage("{}"),
		VerdictModel:    sql.NullString{String: "judge-model", Valid: true},
		TextHash:        "hash-1",
		CriteriaVersion: "v1",
		Action:          "flag",
		RuleSetVersion:  "v1",
	}
	if mutate != nil {
		mutate(&p)
	}
	if _, err := q.InsertTagCoverageEvent(context.Background(), p); err != nil {
		t.Fatalf("insert coverage event: %v", err)
	}
}

// TestTagCoverageEvent_FreshDedupe pins the two writing paths' guarantees: a judged verdict is written once
// per (document, dimension, content, criteria) so a re-audit reuses it, while unjudged derived rows carry no
// model and are guarded by the audit's own read instead of the fresh index.
func TestTagCoverageEvent_FreshDedupe(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	doc := CreateUniqueDocument(t, q, "coverage dedupe")

	seedCoverageEvent(t, q, doc, nil)
	rows, err := q.InsertTagCoverageEvent(ctx, InsertTagCoverageEventParams{
		DocumentID: doc, TagName: "zqauditdim", Class: "gray", Source: "trigger",
		Verdict: json.RawMessage("{}"), VerdictModel: sql.NullString{String: "judge-model", Valid: true},
		TextHash: "hash-1", CriteriaVersion: "v1", Action: "flag", RuleSetVersion: "v1",
	})
	assertNoError(t, err, "re-insert the judged verdict")
	if rows != 0 {
		t.Errorf("the same judged verdict must not be written twice, inserted %d rows", rows)
	}

	seedCoverageEvent(t, q, doc, func(p *InsertTagCoverageEventParams) {
		p.Class = "pending_coverage"
		p.Source = "skip"
		p.VerdictModel = sql.NullString{}
		p.TextHash = ""
		p.Action = "none"
	})
	rows, err = q.InsertTagCoverageEvent(ctx, InsertTagCoverageEventParams{
		DocumentID: doc, TagName: "zqauditdim", Class: "pending_coverage", Source: "skip",
		Verdict: json.RawMessage("{}"), TextHash: "", CriteriaVersion: "v1", Action: "none", RuleSetVersion: "v1",
	})
	assertNoError(t, err, "re-insert a derived row")
	if rows == 0 {
		t.Error("derived rows carry no model, so the fresh index does not dedupe them")
	}
}

// TestListOpenTagCoverageFlags pins the review surface's reading: the latest event of a pair decides, so a
// flag disappears once a later event closes it and reappears when the audit re-flags under a new content.
func TestListOpenTagCoverageFlags(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	doc := CreateUniqueDocument(t, q, "coverage flags")
	seedCoverageEvent(t, q, doc, nil)

	flags, err := q.ListOpenTagCoverageFlags(ctx, ListOpenTagCoverageFlagsParams{PageLimit: 10, PageOffset: 0})
	assertNoError(t, err, "list open flags")
	if len(flags) != 1 {
		t.Fatalf("open flags = %d, want 1", len(flags))
	}
	if flags[0].TagName != "zqauditdim" || flags[0].DocumentTitle == "" {
		t.Errorf("flag = %+v, want the dimension with its document title", flags[0])
	}
	total, err := q.CountOpenTagCoverageFlags(ctx)
	assertNoError(t, err, "count open flags")
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}

	// a later review action for the same pair closes the flag; carrying no judge model, it is not
	// deduplicated against the judged verdict the fresh index holds
	seedCoverageEvent(t, q, doc, func(p *InsertTagCoverageEventParams) {
		p.Class = "explicit"
		p.Action = "fill"
		p.Provisional = true
		p.VerdictModel = sql.NullString{}
	})
	flags, err = q.ListOpenTagCoverageFlags(ctx, ListOpenTagCoverageFlagsParams{PageLimit: 10, PageOffset: 0})
	assertNoError(t, err, "list flags after the fill")
	if len(flags) != 0 {
		t.Errorf("open flags after the fill = %+v, want none: the latest event closed it", flags)
	}
}

// TestLatestTagWorklistRunIgnoresCoverageOnlyRun pins the substrate split the reconciler depends on: the
// completeness audit writes coverage rows into the shared substrate, and such a run must never become the
// run the review worklists are derived from.
func TestLatestTagWorklistRunIgnoresCoverageOnlyRun(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	if _, err := q.LatestTagWorklistRun(ctx); err != sql.ErrNoRows {
		t.Fatalf("LatestTagWorklistRun on an empty substrate: want ErrNoRows, got %v", err)
	}

	measure := "3e0f0c1e-0000-4000-8000-000000000001"
	coverage := "3e0f0c1e-0000-4000-8000-000000000002"
	insertMeasurementRun(t, q, measure, "mass")
	insertMeasurementRun(t, q, coverage, "coverage")

	run, err := q.LatestTagWorklistRun(ctx)
	assertNoError(t, err, "latest worklist run")
	if run.RunID.String() != measure {
		t.Errorf("latest worklist run = %q, want the measurement run %q, not the coverage-only audit run", run.RunID, measure)
	}
}

func insertMeasurementRun(t *testing.T, q *Queries, runID, operator string) {
	t.Helper()
	if _, err := q.db.ExecContext(context.Background(),
		`INSERT INTO tag_measurement (run_id, snapshot_at, rule_set_version, operator, operands, numbers, state)
		 VALUES ($1, now(), 'v1', $2, ARRAY[]::text[], '{}'::jsonb, 'sufficient')`, runID, operator); err != nil {
		t.Fatalf("insert %s run: %v", operator, err)
	}
}

// TestTagAuditSweepRegister pins the epoch register: the row records the sweep a document was last audited
// in, so an audited document leaves the eligible pool for the rest of its sweep and returns exactly one
// sweep later, a never-audited document (a mid-sweep arrival) is eligible in the running sweep, and a
// re-audit moves the stamp with it. The three pool reads answer under the same predicate.
func TestTagAuditSweepRegister(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	doc := CreateUniqueDocument(t, q, "sweep register")

	raw, err := q.GetTagAuditSweep(ctx)
	assertNoError(t, err, "read sweep")
	sweep, err := strconv.ParseInt(raw, 10, 64)
	assertNoError(t, err, "parse sweep")
	if sweep != 0 {
		t.Fatalf("initial sweep = %d, want 0", sweep)
	}

	// a document with no register row has never been audited: it is a mid-sweep arrival, eligible once a
	// sweep is running (the initial sweep 0 holds nobody because no epoch has opened yet)
	if n, err := q.CountEligibleLiveDocuments(ctx, 0); err != nil || n != 0 {
		t.Errorf("eligible under sweep 0 = %d, %v, want 0: no epoch has opened yet", n, err)
	}
	if n, err := q.CountEligibleLiveDocuments(ctx, 1); err != nil || n != 1 {
		t.Errorf("eligible under sweep 1 = %d, %v, want 1: the never-audited document is a late arrival", n, err)
	}
	sample, err := q.SampleEligibleLiveDocumentIDs(ctx, SampleEligibleLiveDocumentIDsParams{SweepCount: 1, PageLimit: 10})
	assertNoError(t, err, "sample the unregistered document")
	if len(sample) != 1 || sample[0] != doc {
		t.Errorf("sample under sweep 1 = %v, want the never-audited document", sample)
	}
	below, err := q.ListEligibleLiveDocumentsBelowTagFloor(ctx, ListEligibleLiveDocumentsBelowTagFloorParams{SweepCount: 1, TagFloor: 10, PageLimit: 10})
	assertNoError(t, err, "list the unregistered below-floor document")
	if len(below) != 1 || below[0] != doc {
		t.Errorf("below-floor under sweep 1 = %v, want the never-audited document", below)
	}

	// the audit stamps the document with the sweep it ran in: it leaves the pool for the rest of that sweep
	// and returns exactly one sweep later
	if err := q.UpsertTagAuditRegister(ctx, UpsertTagAuditRegisterParams{DocumentID: doc, SweepCount: 1}); err != nil {
		t.Fatalf("stamp document: %v", err)
	}
	if n, err := q.CountEligibleLiveDocuments(ctx, 1); err != nil || n != 0 {
		t.Errorf("eligible under sweep 1 = %d, %v, want 0: the document was audited this sweep", n, err)
	}
	if n, err := q.CountEligibleLiveDocuments(ctx, 2); err != nil || n != 1 {
		t.Errorf("eligible under sweep 2 = %d, %v, want 1: the stamp is one sweep behind", n, err)
	}
	sample, err = q.SampleEligibleLiveDocumentIDs(ctx, SampleEligibleLiveDocumentIDsParams{SweepCount: 2, PageLimit: 10})
	assertNoError(t, err, "sample the returned document")
	if len(sample) != 1 || sample[0] != doc {
		t.Errorf("sample under sweep 2 = %v, want the audited document back", sample)
	}

	// a re-audit in a later sweep overwrites the stamp: eligibility follows the newest audit, never the
	// first one (the ledger model's double-audit skip is gone)
	if err := q.UpsertTagAuditRegister(ctx, UpsertTagAuditRegisterParams{DocumentID: doc, SweepCount: 3}); err != nil {
		t.Fatalf("re-stamp document: %v", err)
	}
	if n, err := q.CountEligibleLiveDocuments(ctx, 3); err != nil || n != 0 {
		t.Errorf("eligible under sweep 3 = %d, %v, want 0: the re-audit moved the stamp", n, err)
	}
	if n, err := q.CountEligibleLiveDocuments(ctx, 4); err != nil || n != 1 {
		t.Errorf("eligible under sweep 4 = %d, %v, want 1: the document returns after its newest audit", n, err)
	}
}

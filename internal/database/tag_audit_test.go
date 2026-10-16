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

// TestTagAuditSweepRegister pins the once-per-sweep register the sample is drawn from: a document that was
// audited in a sweep leaves the eligible pool until the sweep counter advances past its count.
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

	if err := q.UpsertTagAuditRegister(ctx, doc); err != nil {
		t.Fatalf("register document: %v", err)
	}
	if n, err := q.CountEligibleLiveDocuments(ctx, 0); err != nil || n != 0 {
		t.Errorf("eligible under sweep 0 = %d, %v, want 0: the document is a sweep ahead", n, err)
	}
	if err := q.UpsertTagAuditRegister(ctx, doc); err != nil {
		t.Fatalf("register document twice: %v", err)
	}

	// the second audit is what makes the counter observable: with one audit the document is admitted again
	// as soon as the sweep advances, with two it costs a sweep each
	if err := q.SetTagAuditSweep(ctx, "1"); err != nil {
		t.Fatalf("bump sweep: %v", err)
	}
	eligible, err := q.SampleEligibleLiveDocumentIDs(ctx, SampleEligibleLiveDocumentIDsParams{SweepCount: 1, PageLimit: 10})
	assertNoError(t, err, "sample eligible")
	if len(eligible) != 0 {
		t.Errorf("eligible under sweep 1 = %v, want none: the document was audited twice", eligible)
	}
	if n, err := q.CountEligibleLiveDocuments(ctx, 1); err != nil || n != 0 {
		t.Errorf("eligible count under sweep 1 = %d, %v, want 0", n, err)
	}

	if err := q.SetTagAuditSweep(ctx, "2"); err != nil {
		t.Fatalf("bump sweep: %v", err)
	}
	if n, err := q.CountEligibleLiveDocuments(ctx, 2); err != nil || n != 0 {
		t.Errorf("eligible count under sweep 2 = %d, %v, want 0: the document outran this sweep too", n, err)
	}
	if err := q.SetTagAuditSweep(ctx, "3"); err != nil {
		t.Fatalf("bump sweep: %v", err)
	}
	raw, err = q.GetTagAuditSweep(ctx)
	assertNoError(t, err, "read the rolled-over sweep")
	sweep, err = strconv.ParseInt(raw, 10, 64)
	assertNoError(t, err, "parse the rolled-over sweep")
	eligible, err = q.SampleEligibleLiveDocumentIDs(ctx, SampleEligibleLiveDocumentIDsParams{SweepCount: sweep, PageLimit: 10})
	assertNoError(t, err, "sample after rollover")
	if len(eligible) != 1 || eligible[0] != doc {
		t.Errorf("eligible under sweep %d = %v, want the audited document back", sweep, eligible)
	}
}

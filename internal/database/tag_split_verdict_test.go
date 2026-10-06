package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// TestSplitVerdictQueries pins migration 00032's schema and its queries together: the fresh-row partial
// unique index is exactly the ON CONFLICT target the inserts name, the NULL-model cache rows sit outside
// that index by construction, and the latest-row reads stay scoped to one criteria version. A mismatch
// between the two halves fails at runtime on the first conflicting insert rather than at compile time,
// which is why neither half is tested without the other
func TestSplitVerdictQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()
	docID := CreateUniqueDocument(t, q, "split-verdicts")
	now := time.Now()

	part := InsertTagPartDimensionEventParams{
		DocumentID:    docID,
		Value:         "russia",
		Criteria:      []byte(`{"dimensional":true}`),
		Dimensional:   true,
		VerdictModel:  sql.NullString{String: "judge-m", Valid: true},
		PolicyVersion: "part-v1",
	}
	if n, err := q.InsertTagPartDimensionEvent(ctx, part); err != nil || n != 1 {
		t.Fatalf("first part verdict: rows=%d err=%v, want one row", n, err)
	}
	if n, err := q.InsertTagPartDimensionEvent(ctx, part); err != nil || n != 0 {
		t.Fatalf("second part verdict: rows=%d err=%v, want the partial unique index to refuse it", n, err)
	}

	cache := part
	cache.Value = "poland"
	cache.Dimensional = false
	cache.VerdictModel = sql.NullString{}
	if n, err := q.InsertTagPartDimensionEvent(ctx, cache); err != nil || n != 1 {
		t.Fatalf("NULL-model part verdict: rows=%d err=%v, want one row", n, err)
	}
	if n, err := q.InsertTagPartDimensionEvent(ctx, cache); err != nil || n != 1 {
		t.Fatalf("second NULL-model part verdict: rows=%d err=%v, cache rows must stay outside the index", n, err)
	}

	otherVersion := part
	otherVersion.Value = "italy"
	otherVersion.PolicyVersion = "part-v0"
	if n, err := q.InsertTagPartDimensionEvent(ctx, otherVersion); err != nil || n != 1 {
		t.Fatalf("part verdict under another policy version: rows=%d err=%v, want one row", n, err)
	}

	seedPart := func(value string, dimensional bool, verdictAt time.Time) {
		t.Helper()
		if _, err := q.db.ExecContext(ctx, `
			INSERT INTO tag_part_dimension_event
			    (document_id, value, criteria, dimensional, policy_version, verdict_at)
			VALUES ($1, $2, '{"dimensional":true}', $3, 'part-v1', $4)`,
			docID, value, dimensional, verdictAt); err != nil {
			t.Fatalf("seed part row for %q: %v", value, err)
		}
	}
	seedPart("spain", false, now.Add(-time.Hour))
	seedPart("spain", true, now)

	readParts := func(policyVersion string, values []string) []LatestTagPartVerdictsForValuesRow {
		t.Helper()
		rows, err := q.LatestTagPartVerdictsForValues(ctx, LatestTagPartVerdictsForValuesParams{
			PolicyVersion: policyVersion,
			PartValues:    values,
		})
		if err != nil {
			t.Fatalf("LatestTagPartVerdictsForValues(%s): %v", policyVersion, err)
		}
		return rows
	}

	rows := readParts("part-v1", []string{"russia", "poland", "italy"})
	if len(rows) != 2 {
		t.Fatalf("part verdicts under part-v1 = %v, want one row per value with italy excluded", rows)
	}
	if rows[0].Value != "poland" || rows[1].Value != "russia" {
		t.Errorf("part verdict values = %q, %q, want them ordered by value", rows[0].Value, rows[1].Value)
	}
	if !rows[1].VerdictModel.Valid || rows[1].VerdictModel.String != "judge-m" {
		t.Errorf("russia verdict model = %v, want the fresh row's model", rows[1].VerdictModel)
	}
	if spain := readParts("part-v1", []string{"spain"}); len(spain) != 1 || !spain[0].Dimensional {
		t.Errorf("latest part verdict for spain = %v, want the newer dimensional row", spain)
	}
	if rows := readParts("part-v1", []string{"italy"}); len(rows) != 0 {
		t.Errorf("part verdicts read under the wrong version = %v, want none", rows)
	}
	if rows := readParts("part-v0", []string{"italy"}); len(rows) != 1 || rows[0].PolicyVersion != "part-v0" {
		t.Errorf("part verdicts under part-v0 = %v, want the row written there", rows)
	}

	compound := InsertTagCompoundStatusEventParams{
		DocumentID:           docID,
		Candidate:            "russian history",
		Status:               "composition",
		Criteria:             []byte(`{"status":"composition"}`),
		VerdictModel:         sql.NullString{String: "judge-m", Valid: true},
		SplitCriteriaVersion: "split-v2",
	}
	if n, err := q.InsertTagCompoundStatusEvent(ctx, compound); err != nil || n != 1 {
		t.Fatalf("first compound verdict: rows=%d err=%v, want one row", n, err)
	}
	if n, err := q.InsertTagCompoundStatusEvent(ctx, compound); err != nil || n != 0 {
		t.Fatalf("second compound verdict: rows=%d err=%v, want the partial unique index to refuse it", n, err)
	}
	bogus := compound
	bogus.Status = "bogus"
	if _, err := q.InsertTagCompoundStatusEvent(ctx, bogus); err == nil {
		t.Error("compound verdict outside the trichotomy must be refused by the status check")
	}
	otherCompound := compound
	otherCompound.Candidate = "labor movement"
	otherCompound.SplitCriteriaVersion = "split-v1"
	if n, err := q.InsertTagCompoundStatusEvent(ctx, otherCompound); err != nil || n != 1 {
		t.Fatalf("compound verdict under another criteria version: rows=%d err=%v, want one row", n, err)
	}

	seedCompound := func(candidate, status, criteriaVersion string, verdictAt time.Time) {
		t.Helper()
		if _, err := q.db.ExecContext(ctx, `
			INSERT INTO tag_compound_status_event
			    (document_id, candidate, status, criteria, split_criteria_version, verdict_at)
			VALUES ($1, $2, $3, '{"status":"composition"}', $4, $5)`,
			docID, candidate, status, criteriaVersion, verdictAt); err != nil {
			t.Fatalf("seed compound row for %q: %v", candidate, err)
		}
	}
	seedCompound("labor movement", "named_unit", "split-v2", now.Add(-time.Hour))
	seedCompound("labor movement", "composition", "split-v2", now)

	compounds, err := q.LatestTagCompoundStatusForCandidates(ctx, LatestTagCompoundStatusForCandidatesParams{
		SplitCriteriaVersion: "split-v2",
		Candidates:           []string{"russian history", "labor movement", "cold war"},
	})
	if err != nil {
		t.Fatalf("LatestTagCompoundStatusForCandidates: %v", err)
	}
	if len(compounds) != 2 {
		t.Fatalf("compound verdicts under split-v2 = %v, want the two candidates written there", compounds)
	}
	byCandidate := make(map[string]string, len(compounds))
	for _, row := range compounds {
		byCandidate[row.Candidate] = row.Status
	}
	if byCandidate["russian history"] != "composition" {
		t.Errorf("russian history status = %q, want composition", byCandidate["russian history"])
	}
	if byCandidate["labor movement"] != "composition" {
		t.Errorf("latest labor movement status = %q, want the newer row", byCandidate["labor movement"])
	}

	reconcileYes := InsertTagCompositionEventParams{
		DocumentID:      docID,
		Candidate:       "recon yes",
		Parts:           []byte(`[]`),
		Disposition:     "keep",
		Reason:          "named_unit",
		CriteriaVersion: "split-v2",
		Reconstructible: sql.NullBool{Bool: true, Valid: true},
	}
	if err := q.InsertTagCompositionEvent(ctx, reconcileYes); err != nil {
		t.Fatalf("composition row with a reconstructibility reading: %v", err)
	}
	reconcileNo := reconcileYes
	reconcileNo.Candidate = "recon no"
	reconcileNo.Reconstructible = sql.NullBool{}
	if err := q.InsertTagCompositionEvent(ctx, reconcileNo); err != nil {
		t.Fatalf("composition row without a reconstructibility reading: %v", err)
	}

	compositions, err := q.LatestCompositionEventsForNames(ctx, []string{"recon yes", "recon no"})
	if err != nil {
		t.Fatalf("LatestCompositionEventsForNames: %v", err)
	}
	if len(compositions) != 2 {
		t.Fatalf("composition events = %v, want both candidates", compositions)
	}
	for _, row := range compositions {
		want := row.Candidate == "recon yes"
		if row.Reconstructible.Valid != want || row.Reconstructible.Bool != want {
			t.Errorf("composition row %q reconstructible = %v, want it preserved rather than collapsed", row.Candidate, row.Reconstructible)
		}
	}

	seedComposition := func(candidate, reason, criteriaVersion string, createdAt time.Time) {
		t.Helper()
		if _, err := q.db.ExecContext(ctx, `
			INSERT INTO tag_composition_event
			    (document_id, candidate, parts, disposition, reason, criteria_version, created_at)
			VALUES ($1, $2, '[]'::jsonb, 'keep', $3, $4, $5)`,
			docID, candidate, reason, criteriaVersion, createdAt); err != nil {
			t.Fatalf("seed composition row for %q: %v", candidate, err)
		}
	}
	seedComposition("russian history", "split_defer", "split-v2", now.Add(-time.Hour))
	seedComposition("russian history", "named_unit", "split-v2", now)
	seedComposition("russian history", "split_defer", "split-v1", now.Add(time.Hour))
	seedComposition("cold war", "named_unit", "split-v1", now)

	reasons, err := q.LatestCompositionReasonsForNames(ctx, LatestCompositionReasonsForNamesParams{
		CriteriaVersion: "split-v2",
		Candidates:      []string{"russian history", "cold war"},
	})
	if err != nil {
		t.Fatalf("LatestCompositionReasonsForNames: %v", err)
	}
	if len(reasons) != 1 {
		t.Fatalf("composition reasons under split-v2 = %v, want only the candidate written there", reasons)
	}
	if reasons[0].Candidate != "russian history" || reasons[0].Reason != "named_unit" {
		t.Errorf("latest composition reason = %q/%q, want the newer row under split-v2", reasons[0].Candidate, reasons[0].Reason)
	}
}

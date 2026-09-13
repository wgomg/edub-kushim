package tagreconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func TestSumReassigned(t *testing.T) {
	tests := []struct {
		name    string
		entries []MergeEntry
		want    int64
	}{
		{name: "nil", entries: nil, want: 0},
		{name: "empty", entries: []MergeEntry{}, want: 0},
		{
			name:    "mixed",
			entries: []MergeEntry{{Reassigned: 3}, {Reassigned: 0}, {Reassigned: 7}},
			want:    10,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sumReassigned(tt.entries); got != tt.want {
				t.Errorf("sumReassigned() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestReasonSuffix(t *testing.T) {
	if got := reasonSuffix(""); got != "" {
		t.Errorf("reasonSuffix(\"\") = %q, want \"\"", got)
	}
	if got := reasonSuffix("auto_replace"); got != " reason=auto_replace" {
		t.Errorf("reasonSuffix(\"auto_replace\") = %q", got)
	}
}

func TestMergeEntryJSON(t *testing.T) {
	t.Run("fields present when non-zero", func(t *testing.T) {
		e := MergeEntry{Query: "a", Target: "b", Reassigned: 5, Reason: "adjudicated", CreatesTag: true}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if m["reason"] != "adjudicated" {
			t.Errorf("reason = %v, want adjudicated", m["reason"])
		}
		if m["creates_tag"] != true {
			t.Errorf("creates_tag = %v, want true", m["creates_tag"])
		}
	})

	t.Run("reason omitted when zero, creates_tag always emitted", func(t *testing.T) {
		e := MergeEntry{Query: "a", Target: "b", Reassigned: 5}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if _, ok := m["reason"]; ok {
			t.Error("reason should be omitted when empty")
		}
		v, ok := m["creates_tag"]
		if !ok {
			t.Fatal("creates_tag must always be emitted — omitting false breaks the web classifier")
		}
		if v != false {
			t.Errorf("creates_tag = %v, want false", v)
		}
	})

	t.Run("backward compatible", func(t *testing.T) {
		// Old persisted reports lack reason/creates_tag; unmarshaling must not break.
		raw := []byte(`{"query":"x","target":"y","reassigned":3}`)
		var e MergeEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		if e.Reason != "" || e.CreatesTag {
			t.Errorf("defaults wrong: reason=%q creates_tag=%v", e.Reason, e.CreatesTag)
		}
	})
}

func TestResolvePlannedKeepFlipsHappyPath(t *testing.T) {
	r := &Reconciler{logger: testutil.NewTestLogger()}
	report := &Report{Skips: map[string]int{}}

	entries := []*entry{
		{query: "a", target: "b", docIDs: []int64{1, 2}, reason: "auto_replace", queryMissing: true},
		{query: "c", target: "d", docIDs: []int64{3}, reason: "adjudicated", queryMissing: false},
	}
	r.resolvePlannedKeepFlips(context.Background(), entries, report)

	if len(report.PlannedKeepFlips) != 2 {
		t.Fatalf("PlannedKeepFlips = %d, want 2", len(report.PlannedKeepFlips))
	}
	if report.Flips.ToKeep != 2 {
		t.Errorf("Flips.ToKeep = %d, want 2", report.Flips.ToKeep)
	}

	e0 := report.PlannedKeepFlips[0]
	if e0.Reassigned != 2 || e0.Reason != "auto_replace" || !e0.CreatesTag {
		t.Errorf("entry[0] = %+v", e0)
	}

	e1 := report.PlannedKeepFlips[1]
	if e1.Reassigned != 1 || e1.Reason != "adjudicated" || e1.CreatesTag {
		t.Errorf("entry[1] = %+v", e1)
	}
}

func TestResolvePlannedKeepFlipsStatsError(t *testing.T) {
	// Use a sql.DB with an invalid DSN so TagPairEvidenceStats fails.
	db, err := sql.Open("pgx", "postgres://invalid:invalid@localhost:0/invalid")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	client := database.NewClient(db)

	r := &Reconciler{client: client, logger: testutil.NewTestLogger()}
	report := &Report{Skips: map[string]int{}}

	// Entries with no docIDs — forces the stats lookup path.
	entries := []*entry{
		{query: "x", target: "y", reason: "negation_guard", queryMissing: true},
		{query: "p", target: "q", reason: "shared_token_guard", queryMissing: false},
	}
	r.resolvePlannedKeepFlips(context.Background(), entries, report)

	// Both entries must be kept (not dropped) with reassigned=0.
	if len(report.PlannedKeepFlips) != 2 {
		t.Fatalf("PlannedKeepFlips = %d, want 2 (stats error must not drop entries)", len(report.PlannedKeepFlips))
	}
	if report.Flips.ToKeep != 2 {
		t.Errorf("Flips.ToKeep = %d, want 2", report.Flips.ToKeep)
	}

	e0 := report.PlannedKeepFlips[0]
	if e0.Reassigned != 0 || e0.Reason != "negation_guard" || !e0.CreatesTag {
		t.Errorf("entry[0] = %+v", e0)
	}

	e1 := report.PlannedKeepFlips[1]
	if e1.Reassigned != 0 || e1.Reason != "shared_token_guard" || e1.CreatesTag {
		t.Errorf("entry[1] = %+v", e1)
	}
}

func TestKeepFlipOutcomeOf(t *testing.T) {
	tests := []struct {
		name         string
		queryMissing bool
		documents    int64
		want         keepFlipOutcome
	}{
		{name: "creates with documents", queryMissing: true, documents: 3, want: keepFlipCreatedWithDocs},
		{name: "creates without documents", queryMissing: true, documents: 0, want: keepFlipCreatedNoDocs},
		{name: "moves with documents", queryMissing: false, documents: 3, want: keepFlipMovedWithDocs},
		{name: "moves without documents", queryMissing: false, documents: 0, want: keepFlipMovedNoDocs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &entry{queryMissing: tt.queryMissing}
			if got := keepFlipOutcomeOf(e, tt.documents); got != tt.want {
				t.Errorf("keepFlipOutcomeOf() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLogKeepFlipResultBranches(t *testing.T) {
	r := &Reconciler{logger: testutil.NewTestLogger()}

	// Smoke-test the four branches; logger is discard, so we only verify no panic.
	e := &entry{query: "a", target: "b", queryMissing: true}
	r.logKeepFlipResult(e, 5)
	r.logKeepFlipResult(e, 0)

	e.queryMissing = false
	r.logKeepFlipResult(e, 5)
	r.logKeepFlipResult(e, 0)
}

func TestLogPlannedKeepFlipBranches(t *testing.T) {
	r := &Reconciler{logger: testutil.NewTestLogger()}

	// Smoke-test the four branches; logger is discard, so we only verify no panic.
	e := &entry{query: "a", target: "b", queryMissing: true, reason: "auto_replace"}
	r.logPlannedKeepFlip(e, 5)
	r.logPlannedKeepFlip(e, 0)

	e.queryMissing = false
	e.reason = "adjudicated"
	r.logPlannedKeepFlip(e, 5)
	r.logPlannedKeepFlip(e, 0)
}

func TestLogSummaryReportOnly(t *testing.T) {
	r := &Reconciler{logger: testutil.NewTestLogger()}
	report := Report{
		ReportOnly: true,
		Worklist:   WorklistCounts{Stale: 3, Unjudged: 2, Drifted: 1, Aged: 4},
		Judged:     5,
		Flips:      FlipCounts{ToMerge: 1, ToKeep: 2},
		PlannedMerges: []MergeEntry{
			{Reassigned: 10},
		},
		PlannedKeepFlips: []MergeEntry{
			{Reassigned: 3, CreatesTag: true},
			{Reassigned: 0, CreatesTag: true},
		},
		Skips:  map[string]int{"below_floor": 4},
		Capped: 1,
	}
	// Should not panic.
	r.logSummary(report)
}

func TestLogSummaryApplied(t *testing.T) {
	r := &Reconciler{logger: testutil.NewTestLogger()}
	report := Report{
		Worklist: WorklistCounts{Stale: 2},
		Judged:   2,
		Flips:    FlipCounts{ToKeep: 1},
		Merges:   []MergeEntry{{Reassigned: 7}},
		RetagEntries: []MergeEntry{
			{Reassigned: 3, Reason: "adjudicated", CreatesTag: true},
		},
		Skips: map[string]int{},
	}
	// Should not panic.
	r.logSummary(report)
}

func TestEntryReasonField(t *testing.T) {
	// Verify the reason field is carried on entries through the entry struct.
	e := &entry{
		query:        "x",
		target:       "y",
		reason:       "auto_replace",
		queryMissing: true,
	}
	if e.reason != "auto_replace" {
		t.Errorf("reason = %q, want auto_replace", e.reason)
	}
	if !e.queryMissing {
		t.Error("queryMissing should be true")
	}
}

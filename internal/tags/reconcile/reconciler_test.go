package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
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

func TestBuildJointWorklist(t *testing.T) {
	r := &Reconciler{logger: testutil.NewTestLogger()}
	allTags := []database.Tag{
		{ID: 1, Name: "supply and demand"},
		{ID: 2, Name: "physics"},
		{ID: 3, Name: "capitalism vs socialism"},
		{ID: 4, Name: "and"},
		{ID: 5, Name: "rock or roll"},
	}
	tagCounts := map[string]int64{
		"supply and demand":       10,
		"capitalism vs socialism": 5,
		"rock or roll":            3,
	}

	entries := r.buildJointWorklist(allTags, tagCounts)

	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}

	// sorted by name
	if entries[0].name != "capitalism vs socialism" {
		t.Errorf("[0].name = %q, want capitalism vs socialism", entries[0].name)
	}
	if entries[1].name != "rock or roll" {
		t.Errorf("[1].name = %q, want rock or roll", entries[1].name)
	}
	if entries[2].name != "supply and demand" {
		t.Errorf("[2].name = %q, want supply and demand", entries[2].name)
	}

	if entries[2].docCount != 10 {
		t.Errorf("[2].docCount = %d, want 10", entries[2].docCount)
	}
	if entries[0].docCount != 5 {
		t.Errorf("[0].docCount = %d, want 5", entries[0].docCount)
	}
	if entries[1].docCount != 3 {
		t.Errorf("[1].docCount = %d, want 3", entries[1].docCount)
	}

	if entries[2].segments[0] != "supply" || entries[2].segments[1] != "demand" {
		t.Errorf("[2].segments = %v, want [supply demand]", entries[2].segments)
	}
}

func TestBuildJointWorklist_NoJointTags(t *testing.T) {
	r := &Reconciler{logger: testutil.NewTestLogger()}
	allTags := []database.Tag{
		{ID: 1, Name: "physics"},
		{ID: 2, Name: "machine learning"},
	}
	entries := r.buildJointWorklist(allTags, nil)
	if len(entries) != 0 {
		t.Errorf("len(entries) = %d, want 0", len(entries))
	}
}

func TestSampleNamesSuffix(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{"few", []string{"alpha", "beta"}, ": alpha, beta"},
		{"exactly five", []string{"a", "b", "c", "d", "e"}, ": a, b, c, d, e"},
		{"truncated at six", []string{"a", "b", "c", "d", "e", "f"}, ": a, b, c, d, e, …"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sampleNamesSuffix(tt.names)
			if got != tt.want {
				t.Errorf("sampleNamesSuffix() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLogDeletedTagKeepFlip(t *testing.T) {
	r := &Reconciler{logger: testutil.NewTestLogger()}
	e := &entry{query: "removed", target: "kept"}

	// blocked: no panic, no extra fields needed.
	r.logDeletedTagKeepFlip(e, database.ListAllDeletedTagsRow{
		Kind: "blocked",
	})

	// merged with target: no panic.
	r.logDeletedTagKeepFlip(e, database.ListAllDeletedTagsRow{
		Kind:       "merged",
		TargetName: sql.NullString{String: "target", Valid: true},
	})

	// merged with null target: no panic.
	r.logDeletedTagKeepFlip(e, database.ListAllDeletedTagsRow{
		Kind:       "merged",
		TargetName: sql.NullString{},
	})
}

// TestApplyKeepFlip_ZeroDocsCreatesNoTag guards the §3.2 ordering fix: when
// there are no documents linking the query→target pair, applyKeepFlip must
// return early without creating the query tag. This prevents the reconciler
// from polluting the tag store with unattributed names.
func TestApplyKeepFlip_ZeroDocsCreatesNoTag(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })

	tagSvc := service.NewTag(client, testutil.NewTestLogger(), testutil.NewMockEmbedder())
	services := &service.CrudServices{Tag: tagSvc}

	r := &Reconciler{
		client:   client,
		services: services,
		logger:   testutil.NewTestLogger(),
	}

	// Seed a target tag so tagByName succeeds.
	database.MustCreateTag(t, client.Queries, "keepflip zero target")

	e := &entry{
		query:  "keepflip zero query",
		target: "keepflip zero target",
		docIDs: nil, // empty: DocumentIDsForPairs would return nothing
	}
	blocklist := make(map[string]database.ListAllDeletedTagsRow)

	repointed, skipped, err := r.applyKeepFlip(ctx, e, blocklist)
	if err != nil {
		t.Fatalf("applyKeepFlip: %v", err)
	}
	if skipped {
		t.Fatal("applyKeepFlip must not be skipped for a non-blocked query")
	}
	if repointed != 0 {
		t.Fatalf("repointed = %d, want 0", repointed)
	}

	// The query tag must NOT have been created.
	_, err = client.Queries.GetTagByName(ctx, "keepflip zero query")
	if err == nil {
		t.Fatal("zero-docs keep-flip must not create a tag in the database")
	}
}

func TestVerdictModelLabel(t *testing.T) {
	tests := []struct {
		name  string
		model sql.NullString
		want  string
	}{
		{"valid model", sql.NullString{String: "gpt-4o", Valid: true}, "gpt-4o"},
		{"empty string", sql.NullString{String: "", Valid: true}, "cached"},
		{"null", sql.NullString{}, "cached"},
		{"null with string", sql.NullString{String: "something", Valid: false}, "cached"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verdictModelLabel(tt.model); got != tt.want {
				t.Errorf("verdictModelLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProvisionalCountsJSON(t *testing.T) {
	pc := ProvisionalCounts{
		Worklist:   10,
		ZeroAssign: 2,
		Promoted:   3,
		Rejudged:   4,
		Deferred:   1,
		NoJudge:    5,
		Skipped:    2,
		Failed:     1,
	}
	raw, err := json.Marshal(pc)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]int
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	testutil.AssertEqual(t, m["worklist"], 10, "worklist")
	testutil.AssertEqual(t, m["zero_assignment"], 2, "zero_assignment")
	testutil.AssertEqual(t, m["promoted"], 3, "promoted")
	testutil.AssertEqual(t, m["rejudged"], 4, "rejudged")
	testutil.AssertEqual(t, m["deferred"], 1, "deferred")
	testutil.AssertEqual(t, m["no_judge"], 5, "no_judge")
	testutil.AssertEqual(t, m["skipped"], 2, "skipped")
	testutil.AssertEqual(t, m["failed"], 1, "failed")
}

func TestProvisionalCountsZero(t *testing.T) {
	pc := ProvisionalCounts{}
	raw, err := json.Marshal(pc)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]int
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	// Zero values should still be present in JSON (struct, not omitempty)
	for _, key := range []string{"worklist", "zero_assignment", "promoted", "rejudged", "deferred", "no_judge", "skipped", "failed"} {
		if _, ok := m[key]; !ok {
			t.Errorf("key %q missing from zero-value JSON", key)
		}
	}
}

func TestReportProvisionalPresent(t *testing.T) {
	report := Report{
		Worklist: WorklistCounts{Stale: 1},
		Skips:    map[string]int{},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	// ProvisionslCounts is a struct; Go's omitempty does not omit zero-valued structs,
	// so the key is always present. Verify the shape is correct.
	prov, ok := m["provisional"].(map[string]any)
	if !ok {
		t.Fatalf("provisional key missing or wrong type: %v", m["provisional"])
	}
	testutil.AssertEqual(t, int(prov["worklist"].(float64)), 0, "zero worklist")
}

package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

func TestSumReassigned(t *testing.T) {
	tests := []struct {
		name    string
		entries []PairEntry
		want    int64
	}{
		{name: "nil", entries: nil, want: 0},
		{name: "empty", entries: []PairEntry{}, want: 0},
		{
			name:    "mixed",
			entries: []PairEntry{{Reassigned: 3}, {Reassigned: 0}, {Reassigned: 7}},
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

func TestPairEntryJSON(t *testing.T) {
	t.Run("fields present when non-zero", func(t *testing.T) {
		e := PairEntry{Query: "a", Target: "b", Reassigned: 5, Reason: "adjudicated", CreatesTag: true}
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
		e := PairEntry{Query: "a", Target: "b", Reassigned: 5}
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
		var e PairEntry
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
		Flips:      FlipCounts{ToAlias: 1, ToKeep: 2},
		PlannedAliases: []PairEntry{
			{Reassigned: 10},
		},
		PlannedKeepFlips: []PairEntry{
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
		Aliases:  []PairEntry{{Reassigned: 7}},
		RetagEntries: []PairEntry{
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

func TestBuildCompoundWorklist(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	r := &Reconciler{client: client, logger: testutil.NewTestLogger()}
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

	entries := r.buildCompoundWorklist(ctx, allTags, tagCounts, &Report{Skips: map[string]int{}})

	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}

	// ordered by live mass descending
	if entries[0].name != "supply and demand" {
		t.Errorf("[0].name = %q, want supply and demand", entries[0].name)
	}
	if entries[1].name != "capitalism vs socialism" {
		t.Errorf("[1].name = %q, want capitalism vs socialism", entries[1].name)
	}
	if entries[2].name != "rock or roll" {
		t.Errorf("[2].name = %q, want rock or roll", entries[2].name)
	}

	if entries[0].docCount != 10 {
		t.Errorf("[0].docCount = %d, want 10", entries[0].docCount)
	}
	if entries[1].docCount != 5 {
		t.Errorf("[1].docCount = %d, want 5", entries[1].docCount)
	}
	if entries[2].docCount != 3 {
		t.Errorf("[2].docCount = %d, want 3", entries[2].docCount)
	}
}

func TestBuildCompoundWorklist_IncludesEveryCompound(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	r := &Reconciler{client: client, logger: testutil.NewTestLogger()}
	allTags := []database.Tag{
		{ID: 1, Name: "physics"},
		{ID: 2, Name: "machine learning"},
	}
	entries := r.buildCompoundWorklist(ctx, allTags, nil, &Report{Skips: map[string]int{}})
	if len(entries) != 1 || entries[0].name != "machine learning" {
		t.Errorf("entries = %v, want the non-conjunction compound machine learning", entries)
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

	// blocked: no panic.
	r.logDeletedTagKeepFlip(e)
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

	tagSvc := curation.NewTag(client, testutil.NewTestLogger(), testutil.NewMockEmbedder())

	r := &Reconciler{
		client: client,
		tag:    tagSvc,
		logger: testutil.NewTestLogger(),
	}

	// Seed a target tag so tagByName succeeds.
	database.MustCreateTag(t, client.Queries, "keepflip zero target")

	e := &entry{
		query:  "keepflip zero query",
		target: "keepflip zero target",
		docIDs: nil, // empty: DocumentIDsForPairs would return nothing
	}
	blocklist := make(map[string]struct{})

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

// TestReportJSONKeys pins the report wire keys the tags page parses: the alias vocabulary and no
// leftover of the retired merge one.
func TestReportJSONKeys(t *testing.T) {
	raw, err := json.Marshal(Report{
		Aliases:        []PairEntry{{Query: "a", Target: "b"}},
		PlannedAliases: []PairEntry{{Query: "c", Target: "d"}},
		Flips:          FlipCounts{ToAlias: 1, ToKeep: 2},
	})
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	for _, key := range []string{"aliases", "planned_aliases", "flips", "retags"} {
		if _, ok := wire[key]; !ok {
			t.Errorf("report JSON missing %q: %s", key, raw)
		}
	}
	flips, ok := wire["flips"].(map[string]any)
	if !ok {
		t.Fatalf("flips is not an object: %s", raw)
	}
	if _, ok := flips["to_alias"]; !ok {
		t.Errorf("flips JSON missing to_alias: %s", raw)
	}
	for _, retired := range []string{"merges", "planned_merges"} {
		if _, ok := wire[retired]; ok {
			t.Errorf("report JSON carries the retired key %q: %s", retired, raw)
		}
	}
	if _, ok := flips["to_merge"]; ok {
		t.Errorf("flips JSON carries the retired key to_merge: %s", raw)
	}
}

// TestApplyAliasFlips_AppliesAndTalliesSkips pins the batched flip seam: an applied action reports the
// plan's real re-pointed count and records its verdict, while a skipped action only tallies.
func TestApplyAliasFlips_AppliesAndTalliesSkips(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })

	r := &Reconciler{
		client: client,
		tag:    curation.NewTag(client, testutil.NewTestLogger(), testutil.NewMockEmbedder()),
		logger: testutil.NewTestLogger(),
	}

	database.MustCreateTag(t, client.Queries, "aliasflip canonical")
	loserID := database.MustCreateTag(t, client.Queries, "aliasflip loser")
	doc := database.CreateUniqueDocument(t, client.Queries, "aliasflip-doc")
	database.MustAddDocumentTag(t, client.Queries, doc, loserID)
	testutil.AssertNoError(t, client.Queries.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
		Name: "aliasflip blocked",
		Kind: curation.DeletedTagKindBlocked,
	}), "seed the blocklist")

	report := &Report{Skips: map[string]int{}}
	r.applyAliasFlips(ctx, []*entry{
		{
			query:        "aliasflip loser",
			target:       "aliasflip canonical",
			documentID:   doc,
			freshVerdict: types.TagVerdicts.Variant,
			freshModel:   "test",
		},
		{
			query:        "aliasflip blocked",
			target:       "aliasflip canonical",
			documentID:   doc,
			freshVerdict: types.TagVerdicts.Variant,
			freshModel:   "test",
		},
	}, report)

	testutil.AssertEqual(t, report.Flips.ToAlias, 1, "only the applicable action counts as an alias")
	testutil.AssertEqual(t, len(report.Aliases), 1, "one alias entry")
	testutil.AssertEqual(t, report.Aliases[0].Reassigned, int64(1), "the entry carries the plan's re-pointed count")
	testutil.AssertEqual(t, report.Skips["alias_skip"], 1, "the blocked action is tallied as a skip")

	if _, err := client.Queries.GetTagAliasByName(ctx, "aliasflip loser"); err != nil {
		t.Fatalf("the applicable pair must alias: %v", err)
	}
	if _, err := client.Queries.GetTagAliasByName(ctx, "aliasflip blocked"); err == nil {
		t.Error("a blocklisted name must never become an alias")
	}

	if rows := latestVerdictsForPair(ctx, t, client, "aliasflip loser", "aliasflip canonical"); len(rows) != 1 {
		t.Errorf("the applied action must record its verdict, got %d rows", len(rows))
	}
	if rows := latestVerdictsForPair(ctx, t, client, "aliasflip blocked", "aliasflip canonical"); len(rows) != 0 {
		t.Errorf("a skipped action must leave no verdict behind, got %d rows", len(rows))
	}
}

// TestApplyKeepFlip_WithdrawsAlias pins the withdrawal: a pair previously collapsed onto the target
// gets its alias removed and its name recreated with the documents back on it, while an alias onto
// another canonical is left alone.
func TestApplyKeepFlip_WithdrawsAlias(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })

	r := &Reconciler{
		client: client,
		tag:    curation.NewTag(client, testutil.NewTestLogger(), testutil.NewMockEmbedder()),
		logger: testutil.NewTestLogger(),
	}

	targetID := database.MustCreateTag(t, client.Queries, "withdraw target")
	if _, err := client.Queries.InsertTagAlias(ctx, database.InsertTagAliasParams{
		AliasName:      "withdraw query",
		CanonicalTagID: targetID,
		RuleSetVersion: tags.IdentityRuleSetVersion,
	}); err != nil {
		t.Fatalf("seed the collapse: %v", err)
	}
	doc := database.CreateUniqueDocument(t, client.Queries, "withdraw-doc")
	database.MustAddDocumentTag(t, client.Queries, doc, targetID)

	repointed, skipped, err := r.applyKeepFlip(ctx, &entry{
		query:        "withdraw query",
		target:       "withdraw target",
		docIDs:       []int64{doc},
		documentID:   doc,
		freshVerdict: types.TagVerdicts.Variant,
		freshModel:   "test",
	}, map[string]struct{}{})
	testutil.AssertNoError(t, err, "apply keep flip")
	if skipped {
		t.Fatal("a withdrawn alias must not report the flip as skipped")
	}
	testutil.AssertEqual(t, repointed, 1, "the document returns to the recreated name")

	if _, err := client.Queries.GetTagAliasByName(ctx, "withdraw query"); err == nil {
		t.Error("the alias row must be withdrawn before the name is recreated")
	}
	queryTag, err := client.Queries.GetTagByName(ctx, "withdraw query")
	testutil.AssertNoError(t, err, "the query name must be recreated")
	queryDocs, err := client.Queries.GetTagDocumentIDs(ctx, queryTag.ID)
	testutil.AssertNoError(t, err, "recreated tag assignments")
	testutil.AssertEqual(t, len(queryDocs), 1, "the document carries the recreated name")

	t.Run("an alias onto another canonical is left alone", func(t *testing.T) {
		otherID := database.MustCreateTag(t, client.Queries, "settle canonical")
		if _, err := client.Queries.InsertTagAlias(ctx, database.InsertTagAliasParams{
			AliasName:      "settle query",
			CanonicalTagID: otherID,
			RuleSetVersion: tags.IdentityRuleSetVersion,
		}); err != nil {
			t.Fatalf("seed an alias onto another canonical: %v", err)
		}
		doc2 := database.CreateUniqueDocument(t, client.Queries, "settle-doc")
		database.MustAddDocumentTag(t, client.Queries, doc2, targetID)

		repointed, skipped, err := r.applyKeepFlip(ctx, &entry{
			query:        "settle query",
			target:       "withdraw target",
			docIDs:       []int64{doc2},
			documentID:   doc2,
			freshVerdict: types.TagVerdicts.Variant,
			freshModel:   "test",
		}, map[string]struct{}{})
		testutil.AssertNoError(t, err, "the flip settles through the existing alias")
		if skipped {
			t.Fatal("settling through an alias must not report the flip as skipped")
		}
		testutil.AssertEqual(t, repointed, 0, "no documents move")
		if _, err := client.Queries.GetTagAliasByName(ctx, "settle query"); err != nil {
			t.Errorf("an alias onto another canonical must be left alone: %v", err)
		}
		if _, err := client.Queries.GetTagByName(ctx, "settle query"); err == nil {
			t.Error("the name must not be re-created while it resolves through its alias")
		}
	})
}

func latestVerdictsForPair(ctx context.Context, t *testing.T, client *database.Client, query, target string) []database.LatestTagVerdictsForPairsRow {
	t.Helper()
	rows, err := client.Queries.LatestTagVerdictsForPairs(ctx, database.LatestTagVerdictsForPairsParams{
		PolicyVersion: policy.Version,
		Queries:       []string{query},
		Targets:       []string{target},
	})
	testutil.AssertNoError(t, err, "latest verdicts for pair")
	return rows
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

func TestProvisionalOutcome(t *testing.T) {
	r := &Reconciler{logger: testutil.NewTestLogger()}

	cases := []struct {
		name         string
		decision     tags.AdmissionDecision
		docs         int64
		wantPromote  bool
		wantDeferred bool
	}{
		{
			name:         "admitted at the floor promotes",
			decision:     tags.AdmissionDecision{Outcome: tags.AdmissionAdmit, Reason: tags.ReasonAdmitted},
			docs:         10,
			wantPromote:  true,
			wantDeferred: false,
		},
		{
			name:         "admitted below the floor stays a silent pending candidate",
			decision:     tags.AdmissionDecision{Outcome: tags.AdmissionAdmit, Reason: tags.ReasonAdmitted},
			docs:         9,
			wantPromote:  false,
			wantDeferred: false,
		},
		{
			name:         "deferred at any mass stays for review",
			decision:     tags.AdmissionDecision{Outcome: tags.AdmissionDefer, Reason: tags.ReasonSingleSignal},
			docs:         5,
			wantPromote:  false,
			wantDeferred: true,
		},
		{
			name:         "rejected at any mass stays for review",
			decision:     tags.AdmissionDecision{Outcome: tags.AdmissionReject, Reason: tags.ReasonCorroborated},
			docs:         5,
			wantPromote:  false,
			wantDeferred: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			promote, deferred := r.provisionalOutcome("provisional tag", tc.decision, tc.docs)
			testutil.AssertEqual(t, promote, tc.wantPromote, "promote")
			testutil.AssertEqual(t, deferred, tc.wantDeferred, "deferred")
		})
	}
}

// TestSweepProvisionalTags_CompositionHold pins the sweep's half of D24: a held compound past the
// grace cutoff is counted as skipped before the verdict lookup, so it neither promotes nor enters
// the re-judge budget — which this run could not serve anyway without a configured quality judge.
func TestSweepProvisionalTags_CompositionHold(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })
	logger := testutil.NewTestLogger()

	tagID := database.MustCreateTag(t, client.Queries, "held sweep compound")
	if _, err := client.DB().ExecContext(ctx,
		`UPDATE tag SET promoted_at = NULL, created_at = now() - interval '90 days' WHERE id = $1`, tagID); err != nil {
		t.Fatalf("backdate provisional tag: %v", err)
	}
	docID := database.CreateUniqueDocument(t, client.Queries, "held sweep compound")
	database.MustAddDocumentTag(t, client.Queries, docID, tagID)
	if _, err := client.DB().ExecContext(ctx, `
		INSERT INTO tag_composition_event
		    (document_id, candidate, parts, disposition, reason, criteria_version, created_at)
		VALUES ($1, $2, '[]'::jsonb, 'keep', $3, $4, now())`,
		docID, "held sweep compound", tags.CompositionReasonSplitDefer, analyzer.TagSplitVersion); err != nil {
		t.Fatalf("seed composition event: %v", err)
	}

	cfg := &config.Config{
		Enricher: config.EnricherConfig{
			TagReconcile: config.TagReconcileConfig{ProvisionalGraceDays: 30},
		},
	}
	r := &Reconciler{
		client: client,
		tag:    curation.NewTag(client, logger, testutil.NewMockEmbedder()),
		logger: logger,
	}
	report := &Report{}
	r.sweepProvisionalTags(ctx, cfg, report)

	testutil.AssertEqual(t, report.Provisional.Worklist, 1, "worklist")
	testutil.AssertEqual(t, report.Provisional.Skipped, 1, "held skip")
	testutil.AssertEqual(t, report.Provisional.Promoted, 0, "no promotion")

	var isPromoted bool
	testutil.AssertNoError(t,
		client.DB().QueryRowContext(ctx,
			"SELECT promoted_at IS NOT NULL FROM tag WHERE id = $1", tagID).Scan(&isPromoted),
		"read promoted state")
	testutil.AssertEqual(t, isPromoted, false, "the held compound stays provisional")
}

func TestBuildCompoundWorklist_DecidedDropQueuesDemotion(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })
	r := &Reconciler{client: client, logger: testutil.NewTestLogger()}

	database.MustCreateTag(t, client.Queries, "drop standing")
	provisionalID := database.MustCreateTag(t, client.Queries, "drop pending")
	if _, err := client.DB().ExecContext(ctx,
		"UPDATE tag SET promoted_at = now() WHERE name = 'drop standing'"); err != nil {
		t.Fatalf("set standing: %v", err)
	}
	if _, err := client.DB().ExecContext(ctx,
		"UPDATE tag SET promoted_at = NULL WHERE id = $1", provisionalID); err != nil {
		t.Fatalf("set provisional: %v", err)
	}
	// one decided no-dimension drop over both compounds, under the current criteria version
	if _, err := client.DB().ExecContext(ctx, `
		INSERT INTO tag_composition_event
		    (document_id, candidate, parts, disposition, reason, criteria_version, created_at)
		VALUES (NULL, $1, '[]'::jsonb, 'drop', $2, $3, now()),
		       (NULL, $4, '[]'::jsonb, 'drop', $2, $3, now())`,
		tags.StoredName("drop standing"), tags.CompositionReasonNoDimension, analyzer.TagSplitVersion,
		tags.StoredName("drop pending")); err != nil {
		t.Fatalf("seed decided drops: %v", err)
	}
	allTags, err := client.Queries.ListAllTags(ctx)
	testutil.AssertNoError(t, err, "list tags")

	report := &Report{Skips: map[string]int{}}
	entries := r.buildCompoundWorklist(ctx, allTags, nil, report)

	if len(entries) != 1 {
		t.Fatalf("entries = %d, want only the decided drop that still stands", len(entries))
	}
	if entries[0].name != "drop standing" {
		t.Errorf("entries[0].name = %q, want the promoted drop standing", entries[0].name)
	}
	if !entries[0].demote {
		t.Error("a decided drop of a standing value queues a revocation")
	}
	testutil.AssertEqual(t, entries[0].reason, tags.CompositionReasonNoDimension, "the journal reason")
	testutil.AssertEqual(t, report.Skips["decided"], 1, "the provisional drop has no standing to revoke")
}

func TestSweepPromotedTags_MassDecayDemotesAndSparesSeeded(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })
	logger := testutil.NewTestLogger()

	// a promoted value with no live documents has decayed below the recurrence floor
	decayed := database.MustCreateTag(t, client.Queries, "decayed standpoints")
	// the seed inventory is operator-owned and never reaches the demotion arm
	seeded := database.MustCreateTag(t, client.Queries, "seeded standpoints")
	if _, err := client.DB().ExecContext(ctx,
		"UPDATE tag SET promoted_at = now() WHERE id = $1", decayed); err != nil {
		t.Fatalf("set standing: %v", err)
	}
	if _, err := client.DB().ExecContext(ctx,
		"UPDATE tag SET promoted_at = now(), seeded = true WHERE id = $1", seeded); err != nil {
		t.Fatalf("mark seeded: %v", err)
	}

	cfg := &config.Config{
		Enricher: config.EnricherConfig{
			TagReconcile: config.TagReconcileConfig{MaxRejudge: 10},
		},
	}
	r := &Reconciler{
		client: client,
		tag:    curation.NewTag(client, logger, testutil.NewMockEmbedder()),
		logger: logger,
	}
	report := &Report{}
	r.sweepPromotedTags(ctx, cfg, report)

	testutil.AssertEqual(t, report.Demotion.Worklist, 1, "the seeded value never reaches the worklist")
	testutil.AssertEqual(t, report.Demotion.MassDecay, 1, "mass decay trigger")
	testutil.AssertEqual(t, report.Demotion.Demoted, 1, "the influence is revoked")

	var promoted, demoted bool
	var reason string
	testutil.AssertNoError(t,
		client.DB().QueryRowContext(ctx,
			`SELECT promoted_at IS NOT NULL, demoted_at IS NOT NULL, coalesce(demoted_reason, '')
			 FROM tag WHERE id = $1`, decayed).Scan(&promoted, &demoted, &reason),
		"read the decayed row")
	if promoted {
		t.Error("the decayed value loses its standing")
	}
	if !demoted {
		t.Error("the revocation is recorded on the row")
	}
	testutil.AssertEqual(t, reason, tags.DemotionMassDecay, "the row names the trigger")

	var seededPromoted, seededDemoted bool
	testutil.AssertNoError(t,
		client.DB().QueryRowContext(ctx,
			"SELECT promoted_at IS NOT NULL, demoted_at IS NOT NULL FROM tag WHERE id = $1",
			seeded).Scan(&seededPromoted, &seededDemoted),
		"read the seeded row")
	if !seededPromoted || seededDemoted {
		t.Error("the operator-owned value keeps its standing untouched")
	}
}

func TestSweepPromotedTags_RunBoundIsSharedWithRejudge(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })
	logger := testutil.NewTestLogger()

	// two decayed values and one whose mass still stands, under a one-unit bound
	database.MustCreateTag(t, client.Queries, "decay standpoints one")
	database.MustCreateTag(t, client.Queries, "decay standpoints two")
	standing := database.MustCreateTag(t, client.Queries, "standing standpoints")
	if _, err := client.DB().ExecContext(ctx, `UPDATE tag SET promoted_at = now()
		WHERE name IN ('decay standpoints one', 'decay standpoints two', 'standing standpoints')`); err != nil {
		t.Fatalf("set standing: %v", err)
	}
	for range tags.RecurrenceFloor {
		docID := database.CreateUniqueDocument(t, client.Queries, "standing standpoints")
		database.MustAddDocumentTag(t, client.Queries, docID, standing)
	}

	cfg := &config.Config{
		Enricher: config.EnricherConfig{
			TagReconcile: config.TagReconcileConfig{MaxRejudge: 1},
		},
	}
	r := &Reconciler{
		client: client,
		tag:    curation.NewTag(client, logger, testutil.NewMockEmbedder()),
		logger: logger,
	}
	report := &Report{}
	r.sweepPromotedTags(ctx, cfg, report)

	testutil.AssertEqual(t, report.Demotion.MassDecay, 1, "the bound admits one revocation")
	testutil.AssertEqual(t, report.Demotion.Demoted, 1, "one revocation applied")
	testutil.AssertEqual(t, report.Capped, 2, "one revocation and one re-judge both wait for the next run")
	testutil.AssertEqual(t, report.Demotion.Rejudged, 0, "no re-judge capacity remains this run")

	var decayedCount int
	testutil.AssertNoError(t,
		client.DB().QueryRowContext(ctx,
			`SELECT count(*) FROM tag
			 WHERE name IN ('decay standpoints one', 'decay standpoints two') AND demoted_at IS NOT NULL`).Scan(&decayedCount),
		"count demoted decayed rows")
	testutil.AssertEqual(t, decayedCount, 1, "exactly one decayed value demoted")
	var stillPromoted bool
	testutil.AssertNoError(t,
		client.DB().QueryRowContext(ctx,
			"SELECT promoted_at IS NOT NULL FROM tag WHERE id = $1", standing).Scan(&stillPromoted),
		"read the standing row")
	testutil.AssertEqual(t, stillPromoted, true, "the value above the floor keeps its influence")
}

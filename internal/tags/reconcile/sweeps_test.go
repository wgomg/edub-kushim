package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// twinSweepConfig returns the caps sweepTwins reads; an unset MaxRejudge truncates the whole worklist to
// zero, so every twin would count as capped.
func twinSweepConfig(maxRejudge, maxRetag int) *config.Config {
	return &config.Config{Enricher: config.EnricherConfig{
		TagReconcile: config.TagReconcileConfig{MaxRejudge: maxRejudge, MaxRetag: maxRetag},
	}}
}

// seedTwinRun writes one measurement run whose overlap record derives each given pair as a firm twin, the
// cut the twin arm reads.
func seedTwinRun(ctx context.Context, t *testing.T, q *database.Queries, pairs ...[2]string) {
	t.Helper()
	records := make([]tags.Record, 0, len(pairs))
	for _, p := range pairs {
		records = append(records, tags.Record{
			Operator: tags.OperatorOverlap,
			Operands: []string{p[0], p[1]},
			Numbers:  map[string]any{tags.NumberJaccard: 0.95, tags.NumberMassA: int64(4), tags.NumberMassB: int64(5)},
			State:    tags.StateSufficient,
		})
	}
	if err := tags.NewStore(q).InsertRecords(ctx, uuid.New(), time.Now(), tags.RuleSetVersion, records); err != nil {
		t.Fatalf("seed twin run: %v", err)
	}
}

// resetTwinStore clears the append-only tables the twin arm reads, so each test owns its own runs and
// identity evidence.
func resetTwinStore(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"tag_measurement", "tag_identity_event"} {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}
}

func newTwinSweeper(t *testing.T, client *database.Client) *Reconciler {
	t.Helper()
	logger := testutil.NewTestLogger()
	return &Reconciler{
		client: client,
		tag:    curation.NewTag(client, logger, testutil.NewMockEmbedder()),
		logger: logger,
	}
}

func countPendingTwins(t *testing.T, client *database.Client) int {
	t.Helper()
	var n int
	if err := client.DB().QueryRowContext(context.Background(),
		`SELECT count(*) FROM tag_identity_event WHERE kind = $1 AND verdict IS NULL`,
		tags.IdentityKindSameIntent).Scan(&n); err != nil {
		t.Fatalf("count pending twin evidence: %v", err)
	}
	return n
}

// TestSweepTwins_MechanicalVariantAliases pins the deterministic leg: a pinned variant pair the measurement
// proposed aliases onto the PreferCanonical winner with no judge call, and the loser's name survives as an
// alias rather than being deleted.
func TestSweepTwins_MechanicalVariantAliases(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	resetTwinStore(t, client.DB())
	t.Cleanup(func() { client.DB().Close() })

	a, b := "twin decolonisation", "twin decolonization"
	database.MustCreateTag(t, client.Queries, a)
	bID := database.MustCreateTag(t, client.Queries, b)
	doc := database.CreateUniqueDocument(t, client.Queries, "twin-mechanical-doc")
	database.MustAddDocumentTag(t, client.Queries, doc, bID)
	seedTwinRun(ctx, t, client.Queries, [2]string{a, b})

	r := newTwinSweeper(t, client)
	report := &Report{Skips: map[string]int{}}
	r.sweepTwins(ctx, twinSweepConfig(10, 1000), report, nil)

	winner := tags.PreferCanonical(a, b)
	loser := a
	if winner == a {
		loser = b
	}
	testutil.AssertEqual(t, report.Twins.Worklist, 1, "worklist")
	testutil.AssertEqual(t, report.Twins.Mechanical, 1, "one mechanical alias")
	if report.Measurement == nil || report.Measurement.Absent {
		t.Fatalf("Measurement = %+v, want the consumed run marked present", report.Measurement)
	}
	if len(report.Aliases) != 1 {
		t.Fatalf("Aliases = %v, want exactly one", report.Aliases)
	}
	testutil.AssertEqual(t, report.Aliases[0].Query, loser, "the loser becomes the alias")
	testutil.AssertEqual(t, report.Aliases[0].Target, winner, "the winner is the canonical")
	testutil.AssertEqual(t, report.Aliases[0].Reassigned, int64(1), "the loser's document re-points")

	if _, err := client.Queries.GetTagAliasByName(ctx, loser); err != nil {
		t.Fatalf("the loser must survive as an alias row: %v", err)
	}
	if _, err := client.Queries.GetTagByName(ctx, loser); err == nil {
		t.Error("the loser's tag row must be removed once it resolves as an alias")
	}
}

// TestSweepTwins_Filters pins the pairs the arm must never touch: an identity-held name stays for review, a
// settled pair is never re-asked, a conjunction belongs to the compound scan, a pair already in this run's
// band worklist is not re-questioned, a duplicate worklist entry is deduped, and a dead name is skipped —
// only the one eligible pair leaves pending evidence behind.
func TestSweepTwins_Filters(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	resetTwinStore(t, client.DB())
	t.Cleanup(func() { client.DB().Close() })

	database.MustCreateTag(t, client.Queries, "zq hold anchor")
	database.MustCreateTag(t, client.Queries, "zq held")
	database.MustCreateTag(t, client.Queries, "zq heldb")
	if _, err := client.Queries.InsertTagIdentityEvent(ctx, database.InsertTagIdentityEventParams{
		Kind:           tags.IdentityKindFoldGate,
		Candidate:      "zq held",
		Criteria:       []byte(`{"proposed_fold":"zq hold anchor"}`),
		Verdict:        sql.NullString{String: tags.IdentityVerdictRejected, Valid: true},
		RuleSetVersion: tags.IdentityRuleSetVersion,
	}); err != nil {
		t.Fatalf("seed the refused fold: %v", err)
	}

	database.MustCreateTag(t, client.Queries, "zq settled")
	database.MustCreateTag(t, client.Queries, "zq settledb")
	if _, err := client.Queries.InsertTagIdentityEvent(ctx, database.InsertTagIdentityEventParams{
		Kind:           tags.IdentityKindSameIntent,
		Candidate:      "zq settled",
		Partner:        sql.NullString{String: "zq settledb", Valid: true},
		Criteria:       []byte("{}"),
		Verdict:        sql.NullString{String: string(types.TagVerdicts.Variant), Valid: true},
		RuleSetVersion: tags.IdentityRuleSetVersion,
	}); err != nil {
		t.Fatalf("seed the settled pair: %v", err)
	}

	database.MustCreateTag(t, client.Queries, "supply and demand")
	database.MustCreateTag(t, client.Queries, "zq jointb")
	database.MustCreateTag(t, client.Queries, "zq pairkey")
	database.MustCreateTag(t, client.Queries, "zq pairkeyb")
	database.MustCreateTag(t, client.Queries, "zq dup")
	database.MustCreateTag(t, client.Queries, "zq dupb")
	database.MustCreateTag(t, client.Queries, "zq deadb")

	seedTwinRun(ctx, t, client.Queries,
		[2]string{"zq held", "zq heldb"},
		[2]string{"zq settled", "zq settledb"},
		[2]string{"supply and demand", "zq jointb"},
		[2]string{"zq pairkey", "zq pairkeyb"},
		[2]string{"zq dup", "zq dupb"},
		[2]string{"zq dup", "zq dupb"},
		[2]string{"zq dead", "zq deadb"},
	)

	r := newTwinSweeper(t, client)
	report := &Report{Skips: map[string]int{}}
	r.sweepTwins(ctx, twinSweepConfig(20, 1000), report, map[string]struct{}{
		twinPairKey("zq pairkey", "zq pairkeyb"): {},
	})

	testutil.AssertEqual(t, report.Twins.Worklist, 7, "worklist counts every proposed pair")
	testutil.AssertEqual(t, report.Twins.Held, 1, "the refused fold holds its name for review")
	testutil.AssertEqual(t, report.Twins.Settled, 1, "a judged pair is never re-asked")
	testutil.AssertEqual(t, report.Twins.Skipped, 4, "the dead, conjunction, band-worklist and duplicate pairs")
	testutil.AssertEqual(t, report.Twins.Pending, 1, "only the eligible pair leaves pending evidence")
	testutil.AssertEqual(t, report.Twins.Mechanical, 0, "no variant pair here")

	testutil.AssertEqual(t, countPendingTwins(t, client), 1, "one pending same_intent row in the journal")
	var heldEvidence int
	if err := client.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM tag_identity_event WHERE kind = $1 AND candidate = $2`,
		tags.IdentityKindSameIntent, "zq held").Scan(&heldEvidence); err != nil {
		t.Fatalf("count held twin evidence: %v", err)
	}
	testutil.AssertEqual(t, heldEvidence, 0, "a held name must never be queued as twin evidence")
}

// TestSweepTwins_ReportOnlyPlansWithoutWriting pins the report-only contract: the arm names what it would
// alias and what it would queue, and touches neither the alias table nor the identity journal.
func TestSweepTwins_ReportOnlyPlansWithoutWriting(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	resetTwinStore(t, client.DB())
	t.Cleanup(func() { client.DB().Close() })

	a, b := "twin reportonly labour", "twin reportonly labor"
	database.MustCreateTag(t, client.Queries, a)
	database.MustCreateTag(t, client.Queries, b)
	seedTwinRun(ctx, t, client.Queries, [2]string{a, b})

	r := newTwinSweeper(t, client)
	report := &Report{ReportOnly: true, Skips: map[string]int{}}
	r.sweepTwins(ctx, twinSweepConfig(10, 1000), report, nil)

	testutil.AssertEqual(t, report.Twins.Planned, 1, "one planned mechanical alias")
	testutil.AssertEqual(t, report.Twins.Mechanical, 0, "report-only must not alias")
	if len(report.PlannedAliases) != 1 {
		t.Fatalf("PlannedAliases = %v, want exactly one", report.PlannedAliases)
	}
	loser := report.PlannedAliases[0].Query
	if _, err := client.Queries.GetTagAliasByName(ctx, loser); err == nil {
		t.Error("report-only must not write an alias row")
	}
	if _, err := client.Queries.GetTagByName(ctx, loser); err != nil {
		t.Errorf("report-only must leave the tag row in place: %v", err)
	}
	testutil.AssertEqual(t, countPendingTwins(t, client), 0, "report-only must not queue identity evidence")
}

// TestSweepTwins_RejudgeCapBoundsTheWorklist pins the budget: the measured worklist counts every proposed
// pair, but only MaxRejudge pairs reach a disposition and the rest wait for the next run.
func TestSweepTwins_RejudgeCapBoundsTheWorklist(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	resetTwinStore(t, client.DB())
	t.Cleanup(func() { client.DB().Close() })

	pairs := [][2]string{
		{"cap a labour", "cap a labor"},
		{"cap b catalogue", "cap b catalog"},
		{"cap c analyse", "cap c analyze"},
	}
	for _, p := range pairs {
		database.MustCreateTag(t, client.Queries, p[0])
		database.MustCreateTag(t, client.Queries, p[1])
	}
	seedTwinRun(ctx, t, client.Queries, pairs...)

	r := newTwinSweeper(t, client)
	report := &Report{Skips: map[string]int{}}
	r.sweepTwins(ctx, twinSweepConfig(1, 1000), report, nil)

	testutil.AssertEqual(t, report.Twins.Worklist, 3, "the measured worklist is uncapped")
	testutil.AssertEqual(t, report.Twins.Mechanical, 1, "one pair reaches a disposition")
	testutil.AssertEqual(t, report.Capped, 2, "the rest wait for the next run")
}

// TestBoundTwinAliases_RespectsRetagBudget pins the document budget: the mechanical aliases are applied in
// one batch, truncated to the run's MaxRetag upper bound with each loser's live mass as the estimate, and
// the deferred pairs are tallied rather than silently dropped.
func TestBoundTwinAliases_RespectsRetagBudget(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })

	threeDocs := database.MustCreateTag(t, client.Queries, "bound loser three")
	oneDoc := database.MustCreateTag(t, client.Queries, "bound loser one")
	tagID := map[string]int64{"bound loser three": threeDocs, "bound loser one": oneDoc}
	for range 3 {
		doc := database.CreateUniqueDocument(t, client.Queries, "bound-three-doc")
		database.MustAddDocumentTag(t, client.Queries, doc, threeDocs)
	}
	doc := database.CreateUniqueDocument(t, client.Queries, "bound-one-doc")
	database.MustAddDocumentTag(t, client.Queries, doc, oneDoc)

	actions := []tags.AliasAction{
		{AliasName: "bound loser three", Canonical: "bound canonical three", RemoveExistingTag: true},
		{AliasName: "bound loser one", Canonical: "bound canonical one", RemoveExistingTag: true},
	}
	report := &Report{Skips: map[string]int{}}
	r := newTwinSweeper(t, client)

	kept := r.boundTwinAliases(ctx, twinSweepConfig(10, 3), actions, tagID, report)

	if len(kept) != 1 {
		t.Fatalf("kept = %d actions, want 1 within a MaxRetag of 3", len(kept))
	}
	if kept[0].AliasName != "bound loser three" {
		t.Errorf("kept[0] = %q, want the first action, truncated in order", kept[0].AliasName)
	}
	testutil.AssertEqual(t, report.Capped, 1, "the deferred pair is tallied, not dropped")
}

// TestMeasurementRunJSON pins the absent-run shape: the twin arm always fills the measurement block, and an
// absent run must not emit a zero timestamp for the web to render as a date.
func TestMeasurementRunJSON(t *testing.T) {
	raw, err := json.Marshal(MeasurementRun{Absent: true})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["snapshot_at"]; ok {
		t.Errorf("snapshot_at = %v, want omitted for an absent run", m["snapshot_at"])
	}
	if _, ok := m["run_id"]; ok {
		t.Errorf("run_id = %v, want omitted for an absent run", m["run_id"])
	}
	if m["absent"] != true {
		t.Errorf("absent = %v, want true", m["absent"])
	}
}

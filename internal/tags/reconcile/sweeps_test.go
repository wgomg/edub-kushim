package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/kb"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// mustListIdentityEvents returns every identity event for the candidate, oldest first, including the
// rows other verdicts left standing: the sweep's hold assertions read raw history, not the latest view.
func mustListIdentityEvents(t *testing.T, client *database.Client, candidate string) []database.TagIdentityEvent {
	t.Helper()
	idRows, err := client.DB().QueryContext(context.Background(),
		`SELECT candidate, kind, partner FROM tag_identity_event WHERE candidate = $1 ORDER BY id`, candidate)
	if err != nil {
		t.Fatalf("list raw identity events: %v", err)
	}
	defer idRows.Close()
	var out []database.TagIdentityEvent
	for idRows.Next() {
		var row database.TagIdentityEvent
		var cand string
		if err := idRows.Scan(&cand, &row.Kind, &row.Partner); err != nil {
			t.Fatalf("scan identity event: %v", err)
		}
		row.Candidate = cand
		out = append(out, row)
	}
	if err := idRows.Err(); err != nil {
		t.Fatalf("iterate identity events: %v", err)
	}
	return out
}

// mintSweepRows is the offline lexical knowledge base rows a mint-sweep fixture installs: one concept per
// provisional name, the live target concept, and one exact edge from each provisional concept to it. The
// pairs below stay unjudged: the fixture wants a worklist the sweep would take on its own, so no seeded
// verdict may knock any name off it and the held name carries only its refusal.
type mintSweepRows struct {
	concepts [][3]string
	lemmas   [][2]string
	edges    [][5]string
}

// kbRowsForMintSweep is the offline lexical fixture the mint sweep tests need: each provisional on its own
// concept, the live target on a shared one, with one exact edge from each provisional concept to it. Each
// provisional therefore resolves to the live store tag and the hold lookup is the only difference between
// them.
func kbRowsForMintSweep() (concepts [][3]string, lemmas [][2]string, edges [][5]string) {
	return [][3]string{
			{"ewn-keep-unheld-n", "n", "a kept container"},
			{"ewn-keep-held-n", "n", "a held container"},
			{"ewn-keep-n", "n", "the live target"},
		},
		[][2]string{
			{"ewn-keep-unheld-n", "unheld probe"},
			{"ewn-keep-held-n", "held keep probe"},
			{"ewn-keep-n", "keep target"},
		},
		[][5]string{
			{"ewn-keep-unheld-n", "ewn-keep-n", "exact", "co-lemma", ""},
			{"ewn-keep-held-n", "ewn-keep-n", "exact", "co-lemma", ""},
		}
}

// seedMintSweepRows installs the offline lexical fixture for the held and unheld provisional names.
func seedMintSweepRows(t *testing.T, client *database.Client, source string) error {
	t.Helper()
	concepts, lemmas, edges := kbRowsForMintSweep()
	return insertMintSweepRows(context.Background(), t, client, source, mintSweepRows{
		concepts: concepts,
		lemmas:   lemmas,
		edges:    edges,
	})
}

// insertMintSweepRows writes the fixture rows through the knowledge base batch path (concept, lemma,
// edge records shaped exactly as artifact installs), so the sweep queries read them back identically.
func insertMintSweepRows(ctx context.Context, t *testing.T, client *database.Client, source string, rows mintSweepRows) error {
	t.Helper()
	marshal := func(v any) []byte {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal fixture rows: %v", err)
		}
		return raw
	}
	concepts := make([]kbConceptRecord, 0, len(rows.concepts))
	for _, c := range rows.concepts {
		pos, gloss := c[1], c[2]
		concepts = append(concepts, kbConceptRecord{ConceptKey: c[0], Pos: &pos, Gloss: &gloss})
	}
	lemmas := make([]kbLemmaRecord, 0, len(rows.lemmas))
	for _, l := range rows.lemmas {
		lemmas = append(lemmas, kbLemmaRecord{ConceptKey: l[0], LemmaNorm: l[1]})
	}
	edges := make([]kbEdgeRecord, 0, len(rows.edges))
	for _, e := range rows.edges {
		edges = append(edges, kbEdgeRecord{FromConceptKey: e[0], ToConceptKey: e[1], Family: e[2], SourceReltype: e[3]})
	}

	if err := client.Queries.InsertKBSource(ctx, database.InsertKBSourceParams{
		Source:           source,
		SourceVersion:    "test-1",
		License:          "CC0-1.0",
		Notice:           "fixture notice",
		UpstreamUrl:      "https://example.com/" + source,
		UpstreamSha256:   strings.Repeat("0", 64),
		ConverterVersion: "9",
		ConceptCount:     int64(len(rows.concepts)),
		LemmaCount:       int64(len(rows.lemmas)),
		EdgeCount:        int64(len(rows.edges)),
	}); err != nil {
		return err
	}
	if err := client.Queries.InsertKBConcepts(ctx, database.InsertKBConceptsParams{
		Source:  source,
		Records: marshal(concepts),
	}); err != nil {
		return err
	}
	if err := client.Queries.InsertKBLemmas(ctx, database.InsertKBLemmasParams{
		Source:  source,
		Records: marshal(lemmas),
	}); err != nil {
		return err
	}
	return client.Queries.InsertKBEdges(ctx, database.InsertKBEdgesParams{
		Source:  source,
		Records: marshal(edges),
	})
}

// kbConceptRecord is the concept record shape artifact installs marshal for the batch insert.
type kbConceptRecord struct {
	ConceptKey string  `json:"concept_key"`
	Pos        *string `json:"pos"`
	Gloss      *string `json:"gloss"`
}

// kbLemmaRecord is the lemma record shape artifact installs marshal for the batch insert.
type kbLemmaRecord struct {
	ConceptKey string `json:"concept_key"`
	LemmaNorm  string `json:"lemma_norm"`
}

// kbEdgeRecord is the edge record shape artifact installs marshal for the batch insert; the weight stays
// unset, exactly as the unweighted fixture edges arrive.
type kbEdgeRecord struct {
	FromConceptKey string   `json:"from_concept_key"`
	ToConceptKey   string   `json:"to_concept_key"`
	Family         string   `json:"family"`
	SourceReltype  string   `json:"source_reltype"`
	Weight         *float64 `json:"weight"`
}

// seedMintSweepKB installs a knowledge base fixture with two provisional names reaching the live target
// through their own concepts: "held keep probe" is held by a refused fold, "unheld probe" is left free.
// The fixture uses the local two-concept rows, not the kb package writers.
func seedMintSweepKB(t *testing.T, client *database.Client, source string) {
	t.Helper()
	if err := seedMintSweepRows(t, client, source); err != nil {
		t.Fatalf("install kb fixture: %v", err)
	}
}

// kbInstallTestRows installs the mint sweep rows in the offline lexical shape: the co-lemma exact edges
// naming both provisional names' concepts plus their shared store target, so the store resolves them back.
func kbInstallTestRows(ctx context.Context, t *testing.T, client *database.Client, source string) error {
	t.Helper()
	concepts, lemmas, edges := kbRowsForMintSweep()
	if err := insertMintSweepRows(ctx, t, client, source, mintSweepRows{
		concepts: concepts,
		lemmas:   lemmas,
		edges:    edges,
	}); err != nil {
		t.Fatalf("install kb fixture: %v", err)
	}
	return nil
}

// twinConfigFunc fixes the sweep budget for harness-built reconcilers without a live config source.
func twinConfigFunc(cfg *config.Config) func() *config.Config {
	return func() *config.Config { return cfg }
}

// TestMintSweepSkipsHeldProvisional pins the mint sweep's hold guard: the sweep mints the pair the
// unheld provisional name needs through its shared store target, and skips the held one even though the
// knowledge base nominates both. ReportOnly counts without writing.
func TestMintSweepSkipsHeldProvisional(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	tagCfg := twinSweepConfig(10, 1000)

	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	database.ResetTestDatabase(client.DB())
	if err := kbInstallTestRows(ctx, t, client, "kbsweep"); err != nil {
		t.Fatalf("install kb fixture: %v", err)
	}
	store := kb.NewStore(client, t.TempDir(), []string{"kbsweep"})

	r, report, heldName := newMintSweepHarness(t, client, store)

	tagCfg.Enricher.TagReconcile.MaxRetag = 0
	report.ReportOnly = true
	r.sweepKBMints(ctx, tagCfg, report)
	testutil.AssertEqual(t, len(mustListIdentityEvents(t, client, heldName)), 1, "report-only leaves the hold refusal")
	testutil.AssertEqual(t, report.KBMinted, 1, "report-only counts the unheld pair without writing")

	report = &Report{}
	r.sweepKBMints(ctx, tagCfg, report)
	testutil.AssertEqual(t, report.KBMinted, 1, "the unheld neighbour mints exactly one pending pair")
	rows := mustListIdentityEvents(t, client, heldName)
	testutil.AssertEqual(t, len(rows), 1, "the held name keeps only its refusal")
	partners := map[string]bool{}
	for _, row := range mustListIdentityEvents(t, client, "unheld probe") {
		if row.Kind == tags.IdentityKindSameIntent && row.Partner.Valid {
			partners[row.Partner.String] = true
		}
	}
	testutil.AssertEqual(t, len(partners), 1, "the unheld name keeps exactly one partner nomination")
	if got := partners["keep target"]; !got {
		t.Fatalf("unheld probe partners = %v, want keep target joined", partners)
	}
}

// newMintSweepHarness builds the sweeper on the caller's client with the installed knowledge base, then
// seeds one held and one unheld provisional name sharing a target: the sweep must mint for the unheld
// name and skip the held one.
func newMintSweepHarness(t *testing.T, client *database.Client, store *kb.Store) (r *Reconciler, report *Report, heldName string) {
	t.Helper()
	logger := testutil.NewTestLogger()
	r = NewReconciler(twinConfigFunc(twinSweepConfig(10, 1000)), client,
		curation.NewTag(client, logger, testutil.NewMockEmbedder()), nil, logger, store)
	ctx := context.Background()
	heldName = seedMintSweepTags(ctx, t, client)
	report = &Report{}
	return r, report, heldName
}

// seedMintSweepTags plants the store tags "keep target" and "keep side" plus the held and unheld
// provisional names: the held one carries the refused fold that keeps it out of the sweep's nominations.
func seedMintSweepTags(ctx context.Context, t *testing.T, client *database.Client) string {
	t.Helper()
	database.MustCreateTag(t, client.Queries, "keep target")
	database.MustCreateTag(t, client.Queries, "keep side")
	database.MustCreateTag(t, client.Queries, "unheld probe")
	heldName := "held keep probe"
	database.MustCreateTag(t, client.Queries, heldName)
	if _, err := client.Queries.InsertTagIdentityEvent(ctx, database.InsertTagIdentityEventParams{
		Kind:           tags.IdentityKindFoldGate,
		Candidate:      heldName,
		Criteria:       []byte(`{"proposed_fold":"keep target"}`),
		Verdict:        sql.NullString{String: tags.IdentityVerdictRejected, Valid: true},
		RuleSetVersion: tags.IdentityRuleSetVersion,
	}); err != nil {
		t.Fatalf("seed the refused fold: %v", err)
	}
	return heldName
}

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

// TestSweepTwins_TokenizationVariantAliases pins the deterministic leg for the concatenation class: an
// exact tokenization twin pair aliases onto the spaced canonical with no judge call, exactly like the
// orthographic leg above.
func TestSweepTwins_TokenizationVariantAliases(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	resetTwinStore(t, client.DB())
	t.Cleanup(func() { client.DB().Close() })

	a, b := "twin afro american", "twin afroamerican"
	aID := database.MustCreateTag(t, client.Queries, a)
	bID := database.MustCreateTag(t, client.Queries, b)
	doc := database.CreateUniqueDocument(t, client.Queries, "twin-mechanical-concat-doc")
	database.MustAddDocumentTag(t, client.Queries, doc, aID)
	database.MustAddDocumentTag(t, client.Queries, doc, bID)
	seedTwinRun(ctx, t, client.Queries, [2]string{a, b})

	r := newTwinSweeper(t, client)
	report := &Report{Skips: map[string]int{}}
	r.sweepTwins(ctx, twinSweepConfig(10, 1000), report, nil)

	testutil.AssertEqual(t, report.Twins.Worklist, 1, "worklist")
	testutil.AssertEqual(t, report.Twins.Mechanical, 1, "one mechanical alias")
	if report.Measurement == nil || report.Measurement.Absent {
		t.Fatalf("Measurement = %+v, want the consumed run marked present", report.Measurement)
	}
	if len(report.Aliases) != 1 {
		t.Fatalf("Aliases = %v, want exactly one", report.Aliases)
	}
	testutil.AssertEqual(t, report.Aliases[0].Query, b, "the concatenated name becomes the alias")
	testutil.AssertEqual(t, report.Aliases[0].Target, a, "the spaced form is the canonical")

	if _, err := client.Queries.GetTagAliasByName(ctx, b); err != nil {
		t.Fatalf("the loser must survive as an alias row: %v", err)
	}
	if _, err := client.Queries.GetTagByName(ctx, b); err == nil {
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
func TestMeasurementRunJSONPinsAbsentShape(t *testing.T) {
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

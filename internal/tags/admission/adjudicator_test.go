package admission

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/types"
)

func TestPlanIdentity_LogsPendingPairs(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	t.Cleanup(func() { db.Close() })

	var buf bytes.Buffer
	logger := log.NewLoggerWithWriter(&buf)

	adj := &mockAdjudicator{
		adjudicateFn: func(_ context.Context, _ []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
			return nil, context.DeadlineExceeded
		},
	}
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{
			TagMatcher:     config.TagMatcherConfig{Timeout: 0},
			TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0},
		},
	})
	setUnexportedField(t, runner, "tagAdjudicator", adj)

	a := &Adjudicator{
		config: &config.Config{
			Enricher: config.EnricherConfig{
				TagAdjudicator: config.TagAdjudicatorConfig{Enabled: true, Llm: config.LlmConfig{Model: "test-model"}},
			},
		},
		queries: q,
		runner:  runner,
		logger:  logger,
	}

	ranked := []matcher.RankResult{mkRanked("democratic socialism", "social democracy", 0.81)}
	_, plan, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
		Names:   []string{"democratic socialism"},
		Ranked:  ranked,
		DBNames: map[string]struct{}{"social democracy": {}},
	}, "")

	out := buf.String()
	if !strings.Contains(out, "defer \"democratic socialism\" vs \"social democracy\" to review") {
		t.Errorf("expected pending-pair log, got:\n%s", out)
	}
	pending := 0
	for _, row := range plan.Evidence {
		if row.Kind == tags.IdentityKindSameIntent && row.Verdict == "" {
			pending++
		}
	}
	adjAssertEqual(t, pending, 1, "pending evidence rows")
	adjAssertEqual(t, plan.Registrations[0].Provisional, true, "candidate stays provisional")
}

func TestPlanIdentity_FoldStaysPendingWithoutJudge(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("DELETE FROM tag_identity_event"); err != nil {
		t.Fatalf("clear tag_identity_event: %v", err)
	}

	logger := log.NewDiscardLogger()
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{
			TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0},
		},
	})

	a := &Adjudicator{
		config: &config.Config{
			Enricher: config.EnricherConfig{
				TagAdjudicator: config.TagAdjudicatorConfig{Enabled: false},
			},
		},
		queries: q,
		runner:  runner,
		logger:  logger,
	}

	_, plan, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
		Names:   []string{"buildings"},
		DBNames: map[string]struct{}{},
	}, "")

	if len(plan.Registrations) != 1 {
		t.Fatalf("registrations = %d, want 1", len(plan.Registrations))
	}
	reg := plan.Registrations[0]
	adjAssertEqual(t, reg.Name, "buildings", "unreduced name registers")
	adjAssertEqual(t, reg.Fold, "building", "fold stays proposed")
	adjAssertEqual(t, reg.Provisional, true, "unresolved fold registers provisionally")
	adjAssertEqual(t, len(plan.Aliases), 0, "no alias without a verdict")

	kinds := map[string]int{}
	for _, row := range plan.Evidence {
		if row.Verdict != "" {
			t.Errorf("evidence[%s] carries verdict %q; the criteria are unresolved", row.Kind, row.Verdict)
		}
		kinds[row.Kind]++
	}
	for _, kind := range []string{tags.IdentityKindBaseForm, tags.IdentityKindFoldGate, tags.IdentityKindConventionalForm} {
		adjAssertEqual(t, kinds[kind], 1, "pending criterion row "+kind)
	}
}

func TestPlanIdentity_StoreExactAppliesOntoExistingRow(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	t.Cleanup(func() { db.Close() })

	logger := log.NewDiscardLogger()
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0}},
	})

	a := &Adjudicator{
		config: &config.Config{
			Enricher: config.EnricherConfig{TagAdjudicator: config.TagAdjudicatorConfig{Enabled: false}},
		},
		queries: q,
		runner:  runner,
		logger:  logger,
	}

	outs, plan, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
		Names:   []string{"united states"},
		DBNames: map[string]struct{}{"united states": {}},
	}, "")

	if len(plan.Registrations) != 1 {
		t.Fatalf("registrations = %d, want 1", len(plan.Registrations))
	}
	reg := plan.Registrations[0]
	adjAssertEqual(t, reg.Disposition, tags.DispositionApply, "store-exact emission applies")
	// the writer resolves an Apply through its Name; an empty Name would
	// classify the registration as Invalid and unlink the document
	adjAssertEqual(t, reg.Name, "united states", "apply carries the emitted name")
	adjAssertEqual(t, reg.Resolved, "united states", "apply resolves onto its own row")
	adjAssertEqual(t, len(plan.Evidence), 0, "store-exact emission mints no identity evidence")
	if len(outs) != 1 || outs[0] != "united states" {
		t.Errorf("outputs = %v, want [united states]", outs)
	}
	adjAssertEqual(t, OutputByInput(plan)["united states"], "united states", "plan output mapping")
}

func TestPlanIdentity_ConfirmedFoldAppliesWithoutPendingRows(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	t.Cleanup(func() { db.Close() })
	// verdicts are cached by rule set, so a row left by another test would
	// answer this run without the judge and mask the applied-fold path
	if _, err := db.Exec("DELETE FROM tag_identity_event"); err != nil {
		t.Fatalf("clear tag_identity_event: %v", err)
	}

	var buf bytes.Buffer
	logger := log.NewLoggerWithWriter(&buf)
	adj := &mockAdjudicator{
		identityFn: func(_ context.Context, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
			results := make([]analyzer.TagIdentityResult, len(candidates))
			for i := range candidates {
				results[i] = analyzer.TagIdentityResult{
					Index:                   i,
					FoldToBase:              true,
					FoldPreservesDenotation: true,
					ConventionalForm:        true,
				}
			}
			return results, nil
		},
	}
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0}},
	})
	setUnexportedField(t, runner, "tagIdentityJudge", adj)

	a := &Adjudicator{
		config: &config.Config{
			Enricher: config.EnricherConfig{
				TagAdjudicator: config.TagAdjudicatorConfig{Enabled: true, Llm: config.LlmConfig{Model: "test-model"}},
			},
		},
		queries: q,
		runner:  runner,
		logger:  logger,
	}

	_, plan, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
		Names:   []string{"railways"},
		DBNames: map[string]struct{}{},
	}, "")

	if len(plan.Registrations) != 1 {
		t.Fatalf("registrations = %d, want 1", len(plan.Registrations))
	}
	reg := plan.Registrations[0]
	adjAssertEqual(t, reg.Name, "railway", "confirmed fold registers the base name")
	adjAssertEqual(t, reg.Fold, "", "an applied fold is settled, not left proposed")
	adjAssertEqual(t, reg.Provisional, false, "a decided fold-birth is not provisional")

	wantVerdicts := map[string]string{
		tags.IdentityKindBaseForm:         tags.IdentityVerdictFold,
		tags.IdentityKindFoldGate:         tags.IdentityVerdictConfirmed,
		tags.IdentityKindConventionalForm: tags.IdentityVerdictStands,
	}
	for _, row := range plan.Evidence {
		adjAssertEqual(t, row.Candidate, "railways", "evidence rides on the pre-fold name")
		want, ok := wantVerdicts[row.Kind]
		if !ok {
			t.Errorf("unexpected evidence kind %q", row.Kind)
			continue
		}
		adjAssertEqual(t, row.Verdict, want, "verdict for "+row.Kind)
	}
	adjAssertEqual(t, len(plan.Evidence), len(wantVerdicts), "a fold-birth mints no pending rows for the folded name")

	if out := buf.String(); !strings.Contains(out, `fold applied "railways" -> "railway"`) {
		t.Errorf("expected applied-fold log line, got:\n%s", out)
	}
}

func TestPlanIdentity_DerivationalFoldResolvesOntoStoreNoun(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("DELETE FROM tag_identity_event"); err != nil {
		t.Fatalf("clear tag_identity_event: %v", err)
	}

	var buf bytes.Buffer
	logger := log.NewLoggerWithWriter(&buf)
	var seen []analyzer.TagIdentityCandidate
	adj := &mockAdjudicator{
		identityFn: func(_ context.Context, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
			seen = append(seen, candidates...)
			results := make([]analyzer.TagIdentityResult, len(candidates))
			for i := range candidates {
				results[i] = analyzer.TagIdentityResult{
					Index:                   i,
					FoldToBase:              true,
					FoldPreservesDenotation: true,
					ConventionalForm:        true,
				}
			}
			return results, nil
		},
	}
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0}},
	})
	setUnexportedField(t, runner, "tagIdentityJudge", adj)

	a := &Adjudicator{
		config: &config.Config{
			Enricher: config.EnricherConfig{
				TagAdjudicator: config.TagAdjudicatorConfig{Enabled: false, Llm: config.LlmConfig{Model: "test-model"}},
			},
		},
		queries: q,
		runner:  runner,
		logger:  logger,
	}

	_, plan, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
		Names:   []string{"political"},
		DBNames: map[string]struct{}{"politics": {}},
	}, "")

	// the proposal is what the judge decides: it must arrive as the fold
	// for the adjective, not be recomputed later at each consumer
	if len(seen) != 1 {
		t.Fatalf("judge candidates = %d, want 1", len(seen))
	}
	adjAssertEqual(t, seen[0].Term, "political", "the adjective reaches the judge")
	adjAssertEqual(t, seen[0].ProposedFold, "politics", "the store noun is the proposed fold the judge decides")

	if len(plan.Registrations) != 1 {
		t.Fatalf("registrations = %d, want 1", len(plan.Registrations))
	}
	reg := plan.Registrations[0]
	adjAssertEqual(t, reg.Disposition, tags.DispositionApply, "a confirmed store-anchored fold applies, never a twin birth")
	adjAssertEqual(t, reg.Resolved, "politics", "the fold resolves onto the store noun")

	if out := buf.String(); !strings.Contains(out, `fold applied "political" -> "politics"`) {
		t.Errorf("expected applied-fold log line, got:\n%s", out)
	}
}

func mkRanked(kept, candTag string, sim float64) matcher.RankResult {
	return matcher.RankResult{
		KeptName:   kept,
		Candidates: []matcher.Candidate{{Tag: candTag, Similarity: sim}},
	}
}

type mockAdjudicator struct {
	adjudicateFn func(ctx context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error)
	identityFn   func(ctx context.Context, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error)
	splitFn      func(ctx context.Context, tags []string) ([]analyzer.TagSplitResult, error)
	qualityFn    func(ctx context.Context, text string, candidates []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error)
	called       int
	identityCall int
	splitCall    int
	qualityCall  int
}

func (m *mockAdjudicator) Analyze(_ context.Context, _ string, _ []analyzer.TypeRef, _ []analyzer.TypeRef, _ []string) (*analyzer.AnalysisResult, error) {
	return nil, nil
}

func (m *mockAdjudicator) AnalyzeDocType(_ context.Context, _ *analyzer.AnalysisResult, _ string, _ []analyzer.TypeRef, _ analyzer.DocMetadata) (string, error) {
	return "", nil
}

func (m *mockAdjudicator) Adjudicate(ctx context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
	m.called++
	if m.adjudicateFn != nil {
		return m.adjudicateFn(ctx, pairs)
	}
	return nil, nil
}

func (m *mockAdjudicator) AdjudicateTagSplits(ctx context.Context, tags []string) ([]analyzer.TagSplitResult, error) {
	m.splitCall++
	if m.splitFn != nil {
		return m.splitFn(ctx, tags)
	}
	return nil, nil
}

func (m *mockAdjudicator) JudgeTagQuality(ctx context.Context, text string, candidates []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
	m.qualityCall++
	if m.qualityFn != nil {
		return m.qualityFn(ctx, text, candidates)
	}
	return nil, nil
}

func (m *mockAdjudicator) JudgeTagIdentity(ctx context.Context, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
	m.identityCall++
	if m.identityFn != nil {
		return m.identityFn(ctx, candidates)
	}
	return nil, nil
}

func (m *mockAdjudicator) Name() string { return "mock-adjudicator" }

type mockTagMatcher struct {
	rankFn func(ctx context.Context, docId string, queries []string) ([]matcher.RankResult, error)
}

func (m *mockTagMatcher) Match(_ context.Context, _, _ string) ([]string, error) { return nil, nil }

func (m *mockTagMatcher) Rank(ctx context.Context, docId string, queries []string) ([]matcher.RankResult, error) {
	if m.rankFn != nil {
		return m.rankFn(ctx, docId, queries)
	}
	results := make([]matcher.RankResult, len(queries))
	for i, q := range queries {
		results[i] = matcher.RankResult{KeptName: q}
	}
	return results, nil
}

func (m *mockTagMatcher) SimilarPairs(_ context.Context) ([]matcher.SimilarPair, error) {
	return nil, nil
}

func (m *mockTagMatcher) Close()       {}
func (m *mockTagMatcher) Name() string { return "mock-matcher" }

func setUnexportedField(t *testing.T, target any, fieldName string, value any) {
	t.Helper()
	v := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	if !v.IsValid() {
		t.Fatalf("field %q not found", fieldName)
	}
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func newTestAdjudicator(t *testing.T) (*Adjudicator, *database.Queries, *mockAdjudicator) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	t.Cleanup(func() { db.Close() })

	adj := &mockAdjudicator{}
	matcher := &mockTagMatcher{}

	logger := log.NewDiscardLogger()
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{
			TagMatcher:     config.TagMatcherConfig{Timeout: 0},
			TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0},
		},
	})
	setUnexportedField(t, runner, "tagAdjudicator", adj)
	setUnexportedField(t, runner, "tagMatcher", matcher)

	a := &Adjudicator{
		config: &config.Config{
			Enricher: config.EnricherConfig{
				TagMatcher: config.TagMatcherConfig{
					AutoReplaceSimilarity: 0.95,
				},
				TagAdjudicator: config.TagAdjudicatorConfig{
					Enabled: true,
					Timeout: 0,
					Llm:     config.LlmConfig{Model: "test-model"},
				},
			},
		},
		queries: q,
		runner:  runner,
		logger:  logger,
	}
	return a, q, adj
}

func createTestDocForAdjudication(t *testing.T, q *database.Queries) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := q.CreateDocument(ctx, database.CreateDocumentParams{
		DocumentID:     "adj-test-doc",
		Title:          "adj-test.pdf",
		Md5Checksum:    "adj-md5",
		Sha512Checksum: "adj-sha512",
		OriginalType:   "application/pdf",
		FileSize:       100,
		OriginalPath:   "/tmp/adj-orig.pdf",
		StoragePath:    "/tmp/adj-storage.pdf",
		PageCount:      1,
		WordCount:      5,
		CharCount:      30,
		Language:       "eng",
	})
	if err != nil {
		t.Fatalf("create test document: %v", err)
	}
	return id
}

func adjAssertEqual(t *testing.T, got, want any, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", msg, got, want)
	}
}

func TestAdjudicateIdentityPairs(t *testing.T) {
	insertIdentityVerdict := func(t *testing.T, q *database.Queries, candidate, partner, verdict, direction string) {
		t.Helper()
		criteria := []byte("{}")
		if direction != "" {
			criteria = []byte(`{"direction":"` + direction + `"}`)
		}
		if _, err := q.InsertTagIdentityEvent(context.Background(), database.InsertTagIdentityEventParams{
			Kind:           tags.IdentityKindSameIntent,
			Candidate:      candidate,
			Partner:        sql.NullString{String: partner, Valid: true},
			Criteria:       criteria,
			Verdict:        sql.NullString{String: verdict, Valid: true},
			RuleSetVersion: tags.IdentityRuleSetVersion,
		}); err != nil {
			t.Fatalf("seed identity verdict: %v", err)
		}
	}
	countIdentityVerdicts := func(t *testing.T, q *database.Queries, candidate, partner string) int {
		t.Helper()
		rows, err := q.LatestTagIdentityEventsForCandidates(context.Background(), database.LatestTagIdentityEventsForCandidatesParams{
			RuleSetVersion: tags.IdentityRuleSetVersion,
			Candidates:     []string{candidate},
		})
		if err != nil {
			t.Fatalf("count identity verdicts: %v", err)
		}
		n := 0
		for _, row := range rows {
			if row.Kind == tags.IdentityKindSameIntent && row.Partner.Valid && row.Partner.String == partner && row.Verdict.Valid {
				n++
			}
		}
		return n
	}

	t.Run("cached verdict applies without LLM call", func(t *testing.T) {
		a, q, adj := newTestAdjudicator(t)
		insertIdentityVerdict(t, q, "democratic socialism", "social democracy", "same", "target")

		p := &identityPair{query: "democratic socialism", target: "social democracy"}
		a.adjudicateIdentityPairs(context.Background(), 0, "", []*identityPair{p}, "")

		adjAssertEqual(t, adj.called, 0, "adjudicator call count")
		adjAssertEqual(t, p.decided, true, "pair decided")
		adjAssertEqual(t, string(p.verdict), "same", "cached verdict")
		adjAssertEqual(t, p.direction, "target", "cached direction")
	})

	t.Run("fresh same verdict is recorded", func(t *testing.T) {
		a, q, adj := newTestAdjudicator(t)
		adj.adjudicateFn = func(_ context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
			results := make([]analyzer.TagVerdictResult, len(pairs))
			for i := range pairs {
				results[i] = analyzer.TagVerdictResult{Index: i, Verdict: types.TagVerdicts.Same, Direction: analyzer.TagDirectionQuery}
			}
			return results, nil
		}

		p := &identityPair{query: "democratic socialism", target: "social democracy"}
		a.adjudicateIdentityPairs(context.Background(), 0, "", []*identityPair{p}, "")

		adjAssertEqual(t, adj.called, 1, "adjudicator call count")
		adjAssertEqual(t, p.decided, true, "pair decided")
		adjAssertEqual(t, p.direction, analyzer.TagDirectionQuery, "direction recorded")
		adjAssertEqual(t, countIdentityVerdicts(t, q, "democratic socialism", "social democracy"), 1, "cached verdict rows")
	})

	t.Run("fresh variant verdict is recorded", func(t *testing.T) {
		a, q, adj := newTestAdjudicator(t)
		adj.adjudicateFn = func(_ context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
			results := make([]analyzer.TagVerdictResult, len(pairs))
			for i := range pairs {
				results[i] = analyzer.TagVerdictResult{Index: i, Verdict: types.TagVerdicts.Variant}
			}
			return results, nil
		}

		p := &identityPair{query: "democratic socialism", target: "social democracy"}
		a.adjudicateIdentityPairs(context.Background(), 0, "", []*identityPair{p}, "")

		adjAssertEqual(t, p.decided, true, "pair decided")
		adjAssertEqual(t, string(p.verdict), "variant", "variant recorded")
		adjAssertEqual(t, countIdentityVerdicts(t, q, "democratic socialism", "social democracy"), 1, "cached verdict rows")
	})

	t.Run("adjudicator error leaves the pair pending", func(t *testing.T) {
		a, q, adj := newTestAdjudicator(t)
		adj.adjudicateFn = func(_ context.Context, _ []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
			return nil, context.DeadlineExceeded
		}

		p := &identityPair{query: "democratic socialism", target: "social democracy"}
		a.adjudicateIdentityPairs(context.Background(), 0, "", []*identityPair{p}, "")

		adjAssertEqual(t, p.decided, false, "pair stays undecided")
		adjAssertEqual(t, countIdentityVerdicts(t, q, "democratic socialism", "social democracy"), 0, "no cached verdict rows")
	})
}

func TestWriteVerdictEvent_ConflictReReadsStoredVerdict(t *testing.T) {
	a, q, _ := newTestAdjudicator(t)
	docID := createTestDocForAdjudication(t, q)

	_, err := q.InsertTagVerdictEvent(context.Background(), database.InsertTagVerdictEventParams{
		DocumentID:    docID,
		Query:         "democratic socialism",
		Target:        "social democracy",
		Sim:           0.81,
		Verdict:       types.TagVerdicts.Variant,
		VerdictModel:  sql.NullString{String: "other-model", Valid: true},
		PolicyVersion: "v1",
	})
	if err != nil {
		t.Fatalf("seed fresh verdict: %v", err)
	}

	bp := &bandPair{query: "democratic socialism", target: "social democracy", sim: 0.81}
	bp.verdict = types.TagVerdicts.Same
	bp.model = "test-model"

	a.writeVerdictEvent(context.Background(), docID, policy.Decision{
		Query:  "democratic socialism",
		Target: "social democracy",
		Sim:    0.81,
	}, bp, "")

	adjAssertEqual(t, string(bp.verdict), "variant", "stored verdict wins over conflicting fresh insert")
	adjAssertEqual(t, bp.cached, true, "pair marked as cache-applied")
	adjAssertEqual(t, bp.model, "other-model", "stored model recorded")
}

func TestTagQualityEvent_InsertAndCacheRead(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()

	docID, err := q.CreateDocument(ctx, database.CreateDocumentParams{
		DocumentID:     "tq-test-doc",
		Title:          "tq-test.pdf",
		Md5Checksum:    "tq-md5",
		Sha512Checksum: "tq-sha512",
		OriginalType:   "application/pdf",
		FileSize:       100,
		OriginalPath:   "/tmp/tq-orig.pdf",
		StoragePath:    "/tmp/tq-storage.pdf",
		PageCount:      1,
		WordCount:      5,
		CharCount:      30,
		Language:       "eng",
	})
	if err != nil {
		t.Fatalf("create test document: %v", err)
	}

	criteria := json.RawMessage(`{"subject_matter":true,"dimension":true,"not_person_place_work":true,"atomic":true,"defer":false}`)

	n, err := q.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docID,
		TagName:       "quantum physics",
		Criteria:      criteria,
		Pass:          true,
		Outcome:       sql.NullString{String: "admit", Valid: true},
		VerdictModel:  sql.NullString{String: "judge-model", Valid: true},
		PolicyVersion: "v1",
	})
	if err != nil {
		t.Fatalf("insert fresh: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row inserted, got %d", n)
	}

	n, err = q.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docID,
		TagName:       "quantum physics",
		Criteria:      criteria,
		Pass:          false,
		VerdictModel:  sql.NullString{String: "other-model", Valid: true},
		PolicyVersion: "v1",
	})
	if err != nil {
		t.Fatalf("insert duplicate: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows on conflict, got %d", n)
	}

	rows, err := q.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
		PolicyVersion: "v1",
		TagNames:      []string{"quantum physics", "absent name"},
	})
	if err != nil {
		t.Fatalf("cache read: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 cached row, got %d", len(rows))
	}
	if rows[0].TagName != "quantum physics" {
		t.Errorf("expected tag_name=quantum physics, got %q", rows[0].TagName)
	}
	if !rows[0].Pass {
		t.Error("expected pass=true from cached row")
	}
	if rows[0].Outcome.String != "admit" || !rows[0].Outcome.Valid {
		t.Errorf("expected outcome=admit from cached row, got %+v", rows[0].Outcome)
	}
	if rows[0].VerdictModel.String != "judge-model" {
		t.Errorf("expected model=judge-model, got %q", rows[0].VerdictModel.String)
	}

	n, err = q.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docID,
		TagName:       "quantum physics",
		Criteria:      criteria,
		Pass:          true,
		VerdictModel:  sql.NullString{},
		PolicyVersion: "v1",
	})
	if err != nil {
		t.Fatalf("insert cache-applied: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row for cache-applied, got %d", n)
	}

	rows, err = q.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
		PolicyVersion: "v1",
		TagNames:      []string{"quantum physics"},
	})
	if err != nil {
		t.Fatalf("cache read after cache-applied: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].VerdictModel.Valid {
		t.Errorf("expected NULL model for latest row, got %q", rows[0].VerdictModel.String)
	}
	if rows[0].Outcome.Valid {
		t.Errorf("expected NULL outcome for the cache-applied row, got %q", rows[0].Outcome.String)
	}
}

func TestDecisionFromStored(t *testing.T) {
	rubric := func(subject, dimension, notPersonPlace, atomic, deferJudge bool) json.RawMessage {
		return json.RawMessage(`{"subject_matter":` + boolJSON(subject) +
			`,"dimension":` + boolJSON(dimension) +
			`,"not_person_place_work":` + boolJSON(notPersonPlace) +
			`,"atomic":` + boolJSON(atomic) +
			`,"defer":` + boolJSON(deferJudge) + `}`)
	}
	legacy := json.RawMessage(`{"subject_matter":true,"standard_term":true,"not_person_place_work":true,"appropriately_broad":true}`)

	cases := []struct {
		name       string
		criteria   json.RawMessage
		pass       bool
		features   tags.AdmissionFeatures
		want       tags.AdmissionOutcome
		wantReason string
	}{
		{"rubric with no failures admits whatever pass says", rubric(true, true, true, true, false), false, tags.AdmissionFeatures{}, tags.AdmissionAdmit, tags.ReasonAdmitted},
		{"one failing rubric answer defers whatever pass says", rubric(true, false, true, true, false), true, tags.AdmissionFeatures{}, tags.AdmissionDefer, tags.ReasonSingleSignal},
		{"two failing rubric answers reject", rubric(false, false, true, true, false), true, tags.AdmissionFeatures{}, tags.AdmissionReject, tags.ReasonCorroborated},
		{"stored abstention defers", rubric(true, true, true, true, true), false, tags.AdmissionFeatures{}, tags.AdmissionDefer, tags.ReasonJudgeDefer},
		{"caller deterministic signal joins the rubric", rubric(true, true, true, true, false), false, tags.AdmissionFeatures{CategoryOther: true}, tags.AdmissionDefer, tags.ReasonSingleSignal},
		{"caller deterministic signals corroborate the rubric", rubric(true, true, true, true, false), false, tags.AdmissionFeatures{CategoryOther: true, NoAnchor: true}, tags.AdmissionReject, tags.ReasonCorroborated},
		{"legacy rubric maps pass to admit", legacy, true, tags.AdmissionFeatures{}, tags.AdmissionAdmit, reasonLegacyPass},
		{"legacy rubric maps pass to reject", legacy, false, tags.AdmissionFeatures{}, tags.AdmissionReject, reasonLegacyReject},
		{"malformed criteria map pass to reject", json.RawMessage("not json"), false, tags.AdmissionFeatures{}, tags.AdmissionReject, reasonLegacyReject},
		{"empty criteria map pass to admit", json.RawMessage(""), true, tags.AdmissionFeatures{}, tags.AdmissionAdmit, reasonLegacyPass},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecisionFromStored(tc.criteria, tc.pass, tc.features)
			adjAssertEqual(t, got.Outcome, tc.want, "outcome")
			adjAssertEqual(t, got.Reason, tc.wantReason, "reason")
		})
	}
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestApplyTagQuality(t *testing.T) {
	a, q, adj := newTestAdjudicator(t)
	a.config.Enricher.TagQuality.Llm = config.LlmConfig{Provider: "test", Model: "judge-model"}
	setUnexportedField(t, a.runner, "tagQualityJudge", adj)
	adj.qualityFn = func(_ context.Context, _ string, candidates []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
		results := make([]analyzer.TagQualityResult, len(candidates))
		for i, c := range candidates {
			results[i] = analyzer.TagQualityResult{
				Index:              i,
				SubjectMatter:      true,
				Dimension:          true,
				NotPersonPlaceWork: true,
				Atomic:             true,
			}
			if c.Term == "phase conjugation" {
				results[i].SubjectMatter = false
				results[i].Dimension = false
			}
		}
		return results, nil
	}
	ctx := context.Background()
	docID, _ := database.CreateTestDocument(t, q, "quality-doc")
	noStore := map[string]struct{}{}

	qualityRow := func(t *testing.T, name string) database.LatestTagQualityEventsForNamesRow {
		t.Helper()
		rows, err := q.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
			PolicyVersion: analyzer.TagQualityVersion,
			TagNames:      []string{name},
		})
		if err != nil {
			t.Fatalf("read quality row: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("quality rows for %q = %d, want 1", name, len(rows))
		}
		return rows[0]
	}

	t.Run("admitted novel tag is kept with an admit decision and a recorded outcome", func(t *testing.T) {
		analysis := &tools.ContentAnalysisResult{
			Tags:       []string{"wave mechanics"},
			TagDetails: []analyzer.TagDetail{{Term: "wave mechanics", Category: analyzer.TagCategoryTopic}},
		}
		kept, decisions := a.ApplyTagQuality(ctx, analysis, nil, noStore, "text", docID, "")

		adjAssertEqual(t, len(kept), 1, "kept tags")
		adjAssertEqual(t, kept[0], "wave mechanics", "kept name")
		adjAssertEqual(t, decisions["wave mechanics"].Outcome, tags.AdmissionAdmit, "decision outcome")
		adjAssertEqual(t, decisions["wave mechanics"].Reason, tags.ReasonAdmitted, "decision reason")

		row := qualityRow(t, "wave mechanics")
		adjAssertEqual(t, row.Pass, true, "recorded pass")
		adjAssertEqual(t, row.Outcome.String, "admit", "recorded outcome")
		adjAssertEqual(t, row.VerdictModel.String, "judge-model", "recorded model")
	})

	t.Run("corroborated judge failure drops the tag and records the rejection", func(t *testing.T) {
		analysis := &tools.ContentAnalysisResult{
			Tags:       []string{"phase conjugation"},
			TagDetails: []analyzer.TagDetail{{Term: "phase conjugation", Category: analyzer.TagCategoryTopic}},
		}
		kept, decisions := a.ApplyTagQuality(ctx, analysis, nil, noStore, "text", docID, "")

		adjAssertEqual(t, len(kept), 0, "rejected tag is dropped")
		if _, ok := decisions["phase conjugation"]; ok {
			t.Errorf("rejected names must not carry a decision: %+v", decisions["phase conjugation"])
		}

		row := qualityRow(t, "phase conjugation")
		adjAssertEqual(t, row.Pass, false, "recorded pass")
		adjAssertEqual(t, row.Outcome.String, "reject", "recorded outcome")
	})

	t.Run("a lone deterministic signal defers instead of dropping", func(t *testing.T) {
		analysis := &tools.ContentAnalysisResult{
			Tags:       []string{"spin lattice"},
			TagDetails: []analyzer.TagDetail{{Term: "spin lattice", Category: analyzer.TagCategoryOther}},
		}
		kept, decisions := a.ApplyTagQuality(ctx, analysis, nil, noStore, "text", docID, "")

		adjAssertEqual(t, len(kept), 1, "gray-band tag is kept")
		adjAssertEqual(t, decisions["spin lattice"].Outcome, tags.AdmissionDefer, "decision outcome")
		adjAssertEqual(t, decisions["spin lattice"].Reason, tags.ReasonSingleSignal, "decision reason")

		row := qualityRow(t, "spin lattice")
		adjAssertEqual(t, row.Pass, false, "recorded pass")
		adjAssertEqual(t, row.Outcome.String, "defer", "recorded outcome")
	})

	t.Run("cached rejection drops the tag without a judge call", func(t *testing.T) {
		a2, q2, adj2 := newTestAdjudicator(t)
		setUnexportedField(t, a2.runner, "tagQualityJudge", adj2)
		if _, err := q2.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
			DocumentID:    docID,
			TagName:       "cached candidate",
			Criteria:      json.RawMessage(`{"subject_matter":false,"dimension":false,"not_person_place_work":true,"atomic":true,"defer":false}`),
			Pass:          false,
			Outcome:       sql.NullString{String: "reject", Valid: true},
			VerdictModel:  sql.NullString{String: "seed-model", Valid: true},
			PolicyVersion: analyzer.TagQualityVersion,
		}); err != nil {
			t.Fatalf("seed cached rejection: %v", err)
		}

		analysis := &tools.ContentAnalysisResult{
			Tags:       []string{"cached candidate"},
			TagDetails: []analyzer.TagDetail{{Term: "cached candidate", Category: analyzer.TagCategoryTopic}},
		}
		kept, decisions := a2.ApplyTagQuality(ctx, analysis, nil, noStore, "text", docID, "")

		adjAssertEqual(t, len(kept), 0, "cached rejection is applied")
		adjAssertEqual(t, len(decisions), 0, "no decision for a dropped name")
		adjAssertEqual(t, adj2.qualityCall, 0, "the cache answers without a judge call")
	})

	t.Run("judge unavailable keeps the tag with a defer decision and no row", func(t *testing.T) {
		a2, q2, _ := newTestAdjudicator(t)
		analysis := &tools.ContentAnalysisResult{
			Tags:       []string{"tunneling effect"},
			TagDetails: []analyzer.TagDetail{{Term: "tunneling effect", Category: analyzer.TagCategoryTopic}},
		}
		kept, decisions := a2.ApplyTagQuality(ctx, analysis, nil, noStore, "text", docID, "")

		adjAssertEqual(t, len(kept), 1, "tag survives a judge outage")
		adjAssertEqual(t, decisions["tunneling effect"].Outcome, tags.AdmissionDefer, "decision outcome")
		adjAssertEqual(t, decisions["tunneling effect"].Reason, reasonJudgeUnavailable, "decision reason")

		rows, err := q2.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
			PolicyVersion: analyzer.TagQualityVersion,
			TagNames:      []string{"tunneling effect"},
		})
		if err != nil {
			t.Fatalf("read quality rows: %v", err)
		}
		adjAssertEqual(t, len(rows), 0, "no verdict row without a verdict")
	})
}

func splitVerdictsAll(_ context.Context, names []string) ([]analyzer.TagSplitResult, error) {
	results := make([]analyzer.TagSplitResult, len(names))
	for i, name := range names {
		results[i] = analyzer.TagSplitResult{
			Verdict:    analyzer.TagSplitSplit,
			Components: strings.Fields(name),
		}
	}
	return results, nil
}

func newSplitAdjudicator(t *testing.T, adj *mockAdjudicator, buf *bytes.Buffer) *Adjudicator {
	t.Helper()
	logger := log.NewDiscardLogger()
	if buf != nil {
		logger = log.NewLoggerWithWriter(buf)
	}
	cfg := &config.Config{
		Enricher: config.EnricherConfig{
			TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0},
			ContentAnalyzer: config.ContentAnalyzerConfig{
				Llm: config.LlmConfig{Model: "test-model"},
			},
		},
	}
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", cfg)
	if adj != nil {
		setUnexportedField(t, runner, "tagAdjudicator", adj)
	}
	return &Adjudicator{config: cfg, logger: logger, runner: runner}
}

func TestDecomposeTags_WithoutAdjudicator(t *testing.T) {
	a := newSplitAdjudicator(t, nil, nil)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"conjunction joint splits deterministically", []string{"supply and demand"}, []string{"supply", "demand"}},
		{"non-conjunction compound passes whole", []string{"russian history"}, []string{"russian history"}},
		{"lexical unit passes whole", []string{"machine learning"}, []string{"machine learning"}},
		{"single word is never a candidate", []string{"physics"}, []string{"physics"}},
		{"four-word name is not a compound", []string{"one two three four"}, []string{"one two three four"}},
		{"non-latin name passes whole", []string{"история россии"}, []string{"история россии"}},
		{"split dedupes with an existing tag", []string{"supply and demand", "supply"}, []string{"supply", "demand"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := a.DecomposeTags(context.Background(), tt.in, nil, filter, "")
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DecomposeTags(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDecomposeTags_SplitVerdicts(t *testing.T) {
	keepAll := func(_ context.Context, names []string) ([]analyzer.TagSplitResult, error) {
		results := make([]analyzer.TagSplitResult, len(names))
		for i := range names {
			results[i] = analyzer.TagSplitResult{Verdict: analyzer.TagSplitKeep}
		}
		return results, nil
	}

	tests := []struct {
		name    string
		names   []string
		title   string
		splitFn func(context.Context, []string) ([]analyzer.TagSplitResult, error)
		want    []string
	}{
		{
			name:    "split verdict replaces the compound with its parts",
			names:   []string{"machine learning"},
			splitFn: splitVerdictsAll,
			want:    []string{"machine", "learning"},
		},
		{
			name:    "keep verdict leaves the compound whole",
			names:   []string{"machine learning"},
			splitFn: keepAll,
			want:    []string{"machine learning"},
		},
		{
			name:  "fewer than two survivors keeps the compound",
			names: []string{"machine learning"},
			splitFn: func(_ context.Context, _ []string) ([]analyzer.TagSplitResult, error) {
				return []analyzer.TagSplitResult{{
					Verdict:    analyzer.TagSplitSplit,
					Components: []string{"machine"},
				}}, nil
			},
			want: []string{"machine learning"},
		},
		{
			name:  "verdict above the part-count bound is rejected",
			names: []string{"machine learning"},
			splitFn: func(_ context.Context, _ []string) ([]analyzer.TagSplitResult, error) {
				return []analyzer.TagSplitResult{{
					Verdict:    analyzer.TagSplitSplit,
					Components: []string{"a", "b", "c", "d"},
				}}, nil
			},
			want: []string{"machine learning"},
		},
		{
			name:  "components failing the document rules drop below two survivors",
			names: []string{"machine learning"},
			title: "machine learning applications",
			splitFn: func(_ context.Context, _ []string) ([]analyzer.TagSplitResult, error) {
				return []analyzer.TagSplitResult{{
					Verdict:    analyzer.TagSplitSplit,
					Components: []string{"machine learning", "physics"},
				}}, nil
			},
			want: []string{"machine learning"},
		},
		{
			name:  "judge failure keeps the compound",
			names: []string{"machine learning"},
			splitFn: func(context.Context, []string) ([]analyzer.TagSplitResult, error) {
				return nil, context.DeadlineExceeded
			},
			want: []string{"machine learning"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newSplitAdjudicator(t, &mockAdjudicator{splitFn: tt.splitFn}, nil)
			filter := analyzer.NewTagFilter(nil, nil, tt.title, nil)
			got, _ := a.DecomposeTags(context.Background(), tt.names, nil, filter, "")
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DecomposeTags(%v) = %v, want %v", tt.names, got, tt.want)
			}
		})
	}
}

func TestDecomposeTags_MemoizesSplitVerdicts(t *testing.T) {
	adj := &mockAdjudicator{splitFn: splitVerdictsAll}
	a := newSplitAdjudicator(t, adj, nil)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)

	a.DecomposeTags(context.Background(), []string{"machine learning"}, nil, filter, "")
	_, cached := a.DecomposeTags(context.Background(), []string{"machine learning"}, nil, filter, "")

	adjAssertEqual(t, adj.splitCall, 1, "split adjudicator call count")
	adjAssertEqual(t, len(cached), 1, "composition rows on the cache hit")
	adjAssertEqual(t, cached[0].Disposition, tags.CompositionSplit, "cached verdict keeps its split")
	adjAssertEqual(t, cached[0].Reason, tags.CompositionReasonCached, "cached verdict's reason")
	adjAssertEqual(t, cached[0].Model, "", "a cache-applied verdict carries no model")
}

func TestDecomposeTags_SplitLogCarriesVersion(t *testing.T) {
	var buf bytes.Buffer
	a := newSplitAdjudicator(t, &mockAdjudicator{splitFn: splitVerdictsAll}, &buf)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)

	a.DecomposeTags(context.Background(), []string{"russian history"}, nil, filter, "")

	want := "tag split[" + analyzer.TagSplitVersion + "]"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("split log = %q, want it to carry %q", buf.String(), want)
	}
}

func TestDecomposeTags_StoreKeyedCompoundSkipsJudgment(t *testing.T) {
	var buf bytes.Buffer
	adj := &mockAdjudicator{splitFn: splitVerdictsAll}
	a := newSplitAdjudicator(t, adj, &buf)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)
	storeKeys := map[string]struct{}{tags.Key("machine learning"): {}}

	got, rows := a.DecomposeTags(context.Background(), []string{"machine learning"}, storeKeys, filter, "")

	adjAssertEqual(t, adj.splitCall, 0, "a store-keyed compound never reaches the judge")
	if !reflect.DeepEqual(got, []string{"machine learning"}) {
		t.Errorf("DecomposeTags = %v, want the compound whole", got)
	}
	adjAssertEqual(t, len(rows), 0, "the skip writes no composition row")
	if !strings.Contains(buf.String(), `skip "machine learning" (reason=store_existing)`) {
		t.Errorf("skip must log its reason, got:\n%s", buf.String())
	}
}

func TestDecomposeTags_CompositionRows(t *testing.T) {
	keepAll := func(_ context.Context, _ []string) ([]analyzer.TagSplitResult, error) {
		return []analyzer.TagSplitResult{{Verdict: analyzer.TagSplitKeep}}, nil
	}
	onePart := func(_ context.Context, _ []string) ([]analyzer.TagSplitResult, error) {
		return []analyzer.TagSplitResult{{
			Verdict:    analyzer.TagSplitSplit,
			Components: []string{"machine"},
		}}, nil
	}
	failJudge := func(context.Context, []string) ([]analyzer.TagSplitResult, error) {
		return nil, context.DeadlineExceeded
	}

	tests := []struct {
		name  string
		adj   *mockAdjudicator // nil runs without an adjudicator
		names []string
		want  tags.CompositionRow
	}{
		{
			name:  "fresh split verdict carries parts, judge, and model",
			adj:   &mockAdjudicator{splitFn: splitVerdictsAll},
			names: []string{"machine learning"},
			want: tags.CompositionRow{
				Candidate:       "machine learning",
				Parts:           []string{"machine", "learning"},
				Disposition:     tags.CompositionSplit,
				Reason:          tags.CompositionReasonJudge,
				CriteriaVersion: analyzer.TagSplitVersion,
				Model:           "test-model",
			},
		},
		{
			name:  "keep verdict records the judge's keep with no parts",
			adj:   &mockAdjudicator{splitFn: keepAll},
			names: []string{"machine learning"},
			want: tags.CompositionRow{
				Candidate:       "machine learning",
				Disposition:     tags.CompositionKeep,
				Reason:          tags.CompositionReasonJudge,
				CriteriaVersion: analyzer.TagSplitVersion,
				Model:           "test-model",
			},
		},
		{
			name:  "components failing validation keep with their own reason",
			adj:   &mockAdjudicator{splitFn: onePart},
			names: []string{"machine learning"},
			want: tags.CompositionRow{
				Candidate:       "machine learning",
				Disposition:     tags.CompositionKeep,
				Reason:          tags.CompositionReasonInvalidComponents,
				CriteriaVersion: analyzer.TagSplitVersion,
				Model:           "test-model",
			},
		},
		{
			name:  "a failed judge keeps with judge_unavailable and no model",
			adj:   &mockAdjudicator{splitFn: failJudge},
			names: []string{"machine learning"},
			want: tags.CompositionRow{
				Candidate:       "machine learning",
				Disposition:     tags.CompositionKeep,
				Reason:          tags.CompositionReasonJudgeUnavailable,
				CriteriaVersion: analyzer.TagSplitVersion,
			},
		},
		{
			name:  "the conjunction grammar fallback splits with its own reason",
			adj:   nil,
			names: []string{"supply and demand"},
			want: tags.CompositionRow{
				Candidate:       "supply and demand",
				Parts:           []string{"supply", "demand"},
				Disposition:     tags.CompositionSplit,
				Reason:          tags.CompositionReasonConjunction,
				CriteriaVersion: analyzer.TagSplitVersion,
			},
		},
		{
			name:  "without a judge a non-joint compound keeps as unavailable",
			adj:   nil,
			names: []string{"russian history"},
			want: tags.CompositionRow{
				Candidate:       "russian history",
				Disposition:     tags.CompositionKeep,
				Reason:          tags.CompositionReasonJudgeUnavailable,
				CriteriaVersion: analyzer.TagSplitVersion,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newSplitAdjudicator(t, tt.adj, nil)
			filter := analyzer.NewTagFilter(nil, nil, "", nil)

			_, rows := a.DecomposeTags(context.Background(), tt.names, nil, filter, "")

			if len(rows) != 1 {
				t.Fatalf("composition rows = %d, want 1", len(rows))
			}
			if !reflect.DeepEqual(rows[0], tt.want) {
				t.Errorf("row = %+v, want %+v", rows[0], tt.want)
			}
		})
	}
}

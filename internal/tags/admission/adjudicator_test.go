package admission

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	_, plan, _, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
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

	_, plan, _, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
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

func TestPlanIdentity_JudgeFailureWarns(t *testing.T) {
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
	adj := &mockAdjudicator{
		identityFn: func(context.Context, []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
			return nil, context.DeadlineExceeded
		},
	}
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{
			TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0},
		},
	})
	setUnexportedField(t, runner, "tagIdentityJudge", adj)

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

	_, plan, _, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
		Names:   []string{"buildings"},
		DBNames: map[string]struct{}{},
	}, "")

	adjAssertEqual(t, len(plan.Registrations), 1, "registration minted")
	adjAssertEqual(t, plan.Registrations[0].Provisional, true, "a failed judge call leaves the fold provisional")
	out := buf.String()
	if !strings.Contains(out, "<4>WARN") {
		t.Errorf("expected a WARN level line, got:\n%s", out)
	}
	if !strings.Contains(out, `judge failed for ["buildings"]`) {
		t.Errorf("expected the WARN to name the judge's batch, got:\n%s", out)
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

	outs, plan, _, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
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

	_, plan, _, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
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

	_, plan, _, _ := a.PlanIdentity(context.Background(), IdentityPlanInput{
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
	partFn       func(ctx context.Context, values []string) ([]analyzer.TagPartResult, error)
	compoundFn   func(ctx context.Context, candidates []string) ([]analyzer.TagCompoundResult, error)
	qualityFn    func(ctx context.Context, text string, candidates []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error)
	called       int
	identityCall int
	partCall     int
	compoundCall int
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

func (m *mockAdjudicator) JudgeTagParts(ctx context.Context, values []string) ([]analyzer.TagPartResult, error) {
	m.partCall++
	if m.partFn != nil {
		return m.partFn(ctx, values)
	}
	return nil, nil
}

func (m *mockAdjudicator) JudgeTagCompounds(ctx context.Context, candidates []string) ([]analyzer.TagCompoundResult, error) {
	m.compoundCall++
	if m.compoundFn != nil {
		return m.compoundFn(ctx, candidates)
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

func (m *mockTagMatcher) Match(_ context.Context, _, _ string) ([]matcher.Candidate, error) {
	return nil, nil
}

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
	newValue := reflect.ValueOf(value)
	if value == nil {
		// reflect.ValueOf(nil) is invalid, so clearing an interface or pointer field needs its zero value
		newValue = reflect.Zero(v.Type())
	}
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(newValue)
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

	t.Run("partial reply salvages the decided pair", func(t *testing.T) {
		a, q, adj := newTestAdjudicator(t)
		adj.adjudicateFn = func(_ context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
			results := make([]analyzer.TagVerdictResult, len(pairs))
			results[0] = analyzer.TagVerdictResult{Index: 0, Verdict: types.TagVerdicts.Same, Direction: analyzer.TagDirectionQuery}
			return results, &analyzer.IncompleteReplyError{Missing: []int{1}}
		}

		decided := &identityPair{query: "democratic socialism", target: "social democracy"}
		missing := &identityPair{query: "labour movement", target: "labor movement"}
		a.adjudicateIdentityPairs(context.Background(), 0, "", []*identityPair{decided, missing}, "")

		adjAssertEqual(t, adj.called, 1, "an incomplete reply is not retried")
		adjAssertEqual(t, decided.decided, true, "the returned pair is decided")
		adjAssertEqual(t, string(decided.verdict), "same", "the salvaged verdict")
		adjAssertEqual(t, missing.decided, false, "the missing pair stays undecided")
		adjAssertEqual(t, countIdentityVerdicts(t, q, "democratic socialism", "social democracy"), 1, "the salvaged verdict is recorded")
		adjAssertEqual(t, countIdentityVerdicts(t, q, "labour movement", "labor movement"), 0, "no verdict row for the missing pair")
	})

	t.Run("deterministic variant resolves with no judge call", func(t *testing.T) {
		a, q, adj := newTestAdjudicator(t)
		ctx := context.Background()

		p := &identityPair{query: "decolonisation", target: "decolonization"}
		a.adjudicateIdentityPairs(ctx, 0, "", []*identityPair{p}, "")

		adjAssertEqual(t, adj.called, 0, "adjudicator call count")
		adjAssertEqual(t, p.decided, true, "pair decided")
		adjAssertEqual(t, string(p.verdict), "same", "mechanical verdict")
		adjAssertEqual(t, p.direction, "", "empty direction hands the pair to the M4 tie-break")
		adjAssertEqual(t, p.variantClass, "orthographic", "class recorded on the pair")
		adjAssertEqual(t, p.model, "", "no judge model")

		rows, err := q.LatestTagIdentityEventsForCandidates(ctx, database.LatestTagIdentityEventsForCandidatesParams{
			RuleSetVersion: tags.IdentityRuleSetVersion,
			Candidates:     []string{p.query},
		})
		if err != nil {
			t.Fatalf("read identity events: %v", err)
		}
		recorded := 0
		for _, row := range rows {
			if row.Kind != tags.IdentityKindSameIntent || !row.Partner.Valid || row.Partner.String != p.target || !row.Verdict.Valid {
				continue
			}
			recorded++
			var criteria struct {
				Deterministic string `json:"deterministic"`
			}
			if err := json.Unmarshal(row.Criteria, &criteria); err != nil {
				t.Fatalf("unmarshal criteria: %v", err)
			}
			adjAssertEqual(t, criteria.Deterministic, "orthographic", "the recorded event names the class")
			adjAssertEqual(t, row.Model.Valid, false, "the recorded event carries no model")
		}
		adjAssertEqual(t, recorded, 1, "recorded same-intent events")
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

	criteria := json.RawMessage(`{"subject_matter":true,"dimension":true,"not_person_place_work":true,"atomic":true,"not_form_genre":true,"defer":false}`)

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
			`,"not_form_genre":true` +
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
				NotFormGenre:       true,
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
		kept, decisions, _ := a.ApplyTagQuality(ctx, analysis, nil, noStore, "text", docID, "")

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
		kept, decisions, _ := a.ApplyTagQuality(ctx, analysis, nil, noStore, "text", docID, "")

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
		kept, decisions, _ := a.ApplyTagQuality(ctx, analysis, nil, noStore, "text", docID, "")

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
			Criteria:      json.RawMessage(`{"subject_matter":false,"dimension":false,"not_person_place_work":true,"atomic":true,"not_form_genre":true,"defer":false}`),
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
		kept, decisions, _ := a2.ApplyTagQuality(ctx, analysis, nil, noStore, "text", docID, "")

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
		kept, decisions, _ := a2.ApplyTagQuality(ctx, analysis, nil, noStore, "text", docID, "")

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

func TestApplyTagQuality_JudgeFailureWarns(t *testing.T) {
	a, q, adj := newTestAdjudicator(t)
	var buf bytes.Buffer
	a.logger = log.NewLoggerWithWriter(&buf)
	setUnexportedField(t, a.runner, "tagQualityJudge", adj)
	adj.qualityFn = func(context.Context, string, []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
		return nil, context.DeadlineExceeded
	}
	ctx := context.Background()
	docID, _ := database.CreateTestDocument(t, q, "quality-warn-doc")

	analysis := &tools.ContentAnalysisResult{
		Tags:       []string{"resonance tunneling"},
		TagDetails: []analyzer.TagDetail{{Term: "resonance tunneling", Category: analyzer.TagCategoryTopic}},
	}
	kept, decisions, _ := a.ApplyTagQuality(ctx, analysis, nil, map[string]struct{}{}, "text", docID, "")

	adjAssertEqual(t, len(kept), 1, "the tag survives the failed judge call")
	adjAssertEqual(t, decisions["resonance tunneling"].Outcome, tags.AdmissionDefer, "decision outcome")
	adjAssertEqual(t, decisions["resonance tunneling"].Reason, reasonJudgeUnavailable, "decision reason")
	adjAssertEqual(t, adj.qualityCall, 1, "context errors are not retried")
	out := buf.String()
	if !strings.Contains(out, "<4>WARN") {
		t.Errorf("expected a WARN level line, got:\n%s", out)
	}
	if !strings.Contains(out, `judge failed for ["resonance tunneling"]`) {
		t.Errorf("expected the WARN to name the judge's candidates, got:\n%s", out)
	}
}

func TestApplyTagQuality_PartialSalvage(t *testing.T) {
	a, q, adj := newTestAdjudicator(t)
	a.config.Enricher.TagQuality.Llm = config.LlmConfig{Provider: "test", Model: "judge-model"}
	var buf bytes.Buffer
	a.logger = log.NewLoggerWithWriter(&buf)
	setUnexportedField(t, a.runner, "tagQualityJudge", adj)
	adj.qualityFn = func(_ context.Context, _ string, candidates []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
		results := make([]analyzer.TagQualityResult, len(candidates))
		missing := -1
		for i, c := range candidates {
			results[i] = analyzer.TagQualityResult{
				Index:              i,
				SubjectMatter:      true,
				Dimension:          true,
				NotPersonPlaceWork: true,
				Atomic:             true,
				NotFormGenre:       true,
			}
			switch c.Term {
			case "salvage reject":
				results[i].SubjectMatter = false
				results[i].Dimension = false
			case "salvage missing":
				missing = i
			}
		}
		return results, &analyzer.IncompleteReplyError{Missing: []int{missing}}
	}
	ctx := context.Background()
	docID, _ := database.CreateTestDocument(t, q, "salvage-doc")

	analysis := &tools.ContentAnalysisResult{
		Tags: []string{"salvage kept", "salvage reject", "salvage missing"},
		TagDetails: []analyzer.TagDetail{
			{Term: "salvage kept", Category: analyzer.TagCategoryTopic},
			{Term: "salvage reject", Category: analyzer.TagCategoryTopic},
			{Term: "salvage missing", Category: analyzer.TagCategoryTopic},
		},
	}
	kept, decisions, err := a.ApplyTagQuality(ctx, analysis, nil, map[string]struct{}{}, "text", docID, "")

	if err != nil {
		t.Fatalf("ApplyTagQuality() error = %v", err)
	}
	adjAssertEqual(t, adj.qualityCall, 1, "an incomplete reply is not retried")
	if want := []string{"salvage kept", "salvage missing"}; !reflect.DeepEqual(kept, want) {
		t.Errorf("kept = %v, want %v (the rejected entry dropped, the missing one deferred)", kept, want)
	}
	adjAssertEqual(t, decisions["salvage kept"].Outcome, tags.AdmissionAdmit, "salvaged entry is decided")
	if _, ok := decisions["salvage reject"]; ok {
		t.Errorf("a salvaged rejection drops the name without a decision, got %+v", decisions["salvage reject"])
	}
	adjAssertEqual(t, decisions["salvage missing"].Outcome, tags.AdmissionDefer, "missing entry defers")
	adjAssertEqual(t, decisions["salvage missing"].Reason, reasonJudgeUnavailable, "missing entry defer reason")

	rowsFor := func(name string) []database.LatestTagQualityEventsForNamesRow {
		t.Helper()
		rows, err := q.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
			PolicyVersion: analyzer.TagQualityVersion,
			TagNames:      []string{name},
		})
		if err != nil {
			t.Fatalf("read quality rows for %q: %v", name, err)
		}
		return rows
	}
	adjAssertEqual(t, len(rowsFor("salvage kept")), 1, "the salvaged verdict row is written and cached")
	adjAssertEqual(t, len(rowsFor("salvage reject")), 1, "the salvaged rejection is written")
	adjAssertEqual(t, len(rowsFor("salvage missing")), 0, "the deferred entry carries no row")

	out := buf.String()
	if !strings.Contains(out, "<4>WARN") {
		t.Errorf("expected a WARN level line, got:\n%s", out)
	}
	if !strings.Contains(out, "partial judge reply") || !strings.Contains(out, "salvage missing") {
		t.Errorf("expected the WARN to name the partial classes and the deferred entry, got:\n%s", out)
	}
}

func TestApplyTagQuality_BatchesJudgeCalls(t *testing.T) {
	a, q, adj := newTestAdjudicator(t)
	a.config.Enricher.TagQuality.Llm = config.LlmConfig{Provider: "test", Model: "judge-model"}
	setUnexportedField(t, a.runner, "tagQualityJudge", adj)
	var batchSizes []int
	adj.qualityFn = func(_ context.Context, _ string, candidates []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
		batchSizes = append(batchSizes, len(candidates))
		results := make([]analyzer.TagQualityResult, len(candidates))
		for i, c := range candidates {
			if c.Index != i {
				t.Errorf("candidate index = %d at position %d, want a batch-local index", c.Index, i)
			}
			results[i] = analyzer.TagQualityResult{
				Index:              i,
				SubjectMatter:      true,
				Dimension:          true,
				NotPersonPlaceWork: true,
				Atomic:             true,
				NotFormGenre:       true,
			}
		}
		return results, nil
	}
	ctx := context.Background()
	docID, _ := database.CreateTestDocument(t, q, "quality-batch-doc")

	// one candidate past the batch size: a single unbounded judge call
	// would show up as one oversized batch
	const n = qualityBatchSize + 1
	analysis := &tools.ContentAnalysisResult{}
	for i := range n {
		name := fmt.Sprintf("batched candidate %d", i)
		analysis.Tags = append(analysis.Tags, name)
		analysis.TagDetails = append(analysis.TagDetails, analyzer.TagDetail{Term: name, Category: analyzer.TagCategoryTopic})
	}

	kept, decisions, err := a.ApplyTagQuality(ctx, analysis, nil, map[string]struct{}{}, "text", docID, "")
	if err != nil {
		t.Fatalf("ApplyTagQuality() error = %v", err)
	}
	if !reflect.DeepEqual(batchSizes, []int{qualityBatchSize, 1}) {
		t.Errorf("batch sizes = %v, want [%d 1]", batchSizes, qualityBatchSize)
	}
	adjAssertEqual(t, adj.qualityCall, 2, "the candidate list is split across bounded calls")
	adjAssertEqual(t, len(decisions), n, "every candidate carries a decision")
	adjAssertEqual(t, len(kept), n, "no candidate is dropped by the batching")
}

func partsDimensional(_ context.Context, values []string) ([]analyzer.TagPartResult, error) {
	results := make([]analyzer.TagPartResult, len(values))
	for i := range values {
		results[i] = analyzer.TagPartResult{Index: i, Dimensional: true}
	}
	return results, nil
}

func partsContainer(_ context.Context, values []string) ([]analyzer.TagPartResult, error) {
	results := make([]analyzer.TagPartResult, len(values))
	for i := range values {
		results[i] = analyzer.TagPartResult{Index: i}
	}
	return results, nil
}

func partsDeferred(_ context.Context, values []string) ([]analyzer.TagPartResult, error) {
	results := make([]analyzer.TagPartResult, len(values))
	for i := range values {
		results[i] = analyzer.TagPartResult{Index: i, Dimensional: true, Defer: true}
	}
	return results, nil
}

func compoundsStatus(status analyzer.TagCompoundStatus) func(context.Context, []string) ([]analyzer.TagCompoundResult, error) {
	return func(_ context.Context, candidates []string) ([]analyzer.TagCompoundResult, error) {
		results := make([]analyzer.TagCompoundResult, len(candidates))
		for i := range candidates {
			results[i] = analyzer.TagCompoundResult{Index: i, Status: status}
		}
		return results, nil
	}
}

func compoundsDeferred(_ context.Context, candidates []string) ([]analyzer.TagCompoundResult, error) {
	results := make([]analyzer.TagCompoundResult, len(candidates))
	for i := range candidates {
		results[i] = analyzer.TagCompoundResult{Index: i, Status: analyzer.TagCompoundComposition, Defer: true}
	}
	return results, nil
}

func newSplitAdjudicator(t *testing.T, adj *mockAdjudicator, buf *bytes.Buffer) (*Adjudicator, int64) {
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
	if adj == nil {
		return &Adjudicator{config: cfg, logger: logger, runner: runner}, 0
	}
	setUnexportedField(t, runner, "tagAdjudicator", adj)
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	t.Cleanup(func() { db.Close() })
	for _, table := range []string{"tag_part_dimension_event", "tag_compound_status_event", "tag_composition_event"} {
		if _, err := db.ExecContext(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}
	docID := database.CreateUniqueDocument(t, q, "decompose-split")
	return &Adjudicator{config: cfg, queries: q, logger: logger, runner: runner}, docID
}

func TestDecomposeTags_WithoutAdjudicator(t *testing.T) {
	a, _ := newSplitAdjudicator(t, nil, nil)
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
			got, _, _ := a.DecomposeTags(context.Background(), tt.in, nil, nil, filter, 0, "")
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DecomposeTags(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDecomposeTags_TwoPassDecision(t *testing.T) {
	tests := []struct {
		name       string
		names      []string
		title      string
		partFn     func(context.Context, []string) ([]analyzer.TagPartResult, error)
		compoundFn func(context.Context, []string) ([]analyzer.TagCompoundResult, error)
		want       []string
	}{
		{
			name:       "composition with dimensional parts splits",
			names:      []string{"machine learning"},
			partFn:     partsDimensional,
			compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
			want:       []string{"machine", "learning"},
		},
		{
			name:       "named unit with non-dimensional parts keeps",
			names:      []string{"machine learning"},
			partFn:     partsContainer,
			compoundFn: compoundsStatus(analyzer.TagCompoundNamedUnit),
			want:       []string{"machine learning"},
		},
		{
			name:       "composition against a non-dimensional part keeps",
			names:      []string{"machine learning"},
			partFn:     partsContainer,
			compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
			want:       []string{"machine learning"},
		},
		{
			name:       "no dimension with non-dimensional parts drops",
			names:      []string{"machine learning"},
			partFn:     partsContainer,
			compoundFn: compoundsStatus(analyzer.TagCompoundNoDimension),
			want:       []string{},
		},
		{
			name:       "no dimension against dimensional parts keeps with its parts alongside",
			names:      []string{"machine learning"},
			partFn:     partsDimensional,
			compoundFn: compoundsStatus(analyzer.TagCompoundNoDimension),
			want:       []string{"machine learning", "machine", "learning"},
		},
		{
			name:       "deferred compound keeps with its parts alongside",
			names:      []string{"machine learning"},
			partFn:     partsDimensional,
			compoundFn: compoundsDeferred,
			want:       []string{"machine learning", "machine", "learning"},
		},
		{
			name:       "deferred part keeps",
			names:      []string{"machine learning"},
			partFn:     partsDeferred,
			compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
			want:       []string{"machine learning"},
		},
		{
			name:  "judge failure keeps",
			names: []string{"machine learning"},
			partFn: func(context.Context, []string) ([]analyzer.TagPartResult, error) {
				return nil, context.DeadlineExceeded
			},
			compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
			want:       []string{"machine learning"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adj := &mockAdjudicator{partFn: tt.partFn, compoundFn: tt.compoundFn}
			a, docID := newSplitAdjudicator(t, adj, nil)
			filter := analyzer.NewTagFilter(nil, nil, tt.title, nil)
			got, _, _ := a.DecomposeTags(context.Background(), tt.names, nil, nil, filter, docID, "")
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DecomposeTags(%v) = %v, want %v", tt.names, got, tt.want)
			}
		})
	}
}

func TestDecomposeTags_CachedVerdictSkipsJudge(t *testing.T) {
	adj := &mockAdjudicator{
		partFn:     partsDimensional,
		compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
	}
	a, docID := newSplitAdjudicator(t, adj, nil)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)

	a.DecomposeTags(context.Background(), []string{"machine learning"}, nil, nil, filter, docID, "")
	_, cached, _ := a.DecomposeTags(context.Background(), []string{"machine learning"}, nil, nil, filter, docID, "")

	adjAssertEqual(t, adj.partCall, 1, "part judge calls after the persisted cache hit")
	adjAssertEqual(t, adj.compoundCall, 1, "compound judge calls after the persisted cache hit")
	adjAssertEqual(t, len(cached), 1, "composition rows on the cache hit")
	adjAssertEqual(t, cached[0].Disposition, tags.CompositionSplit, "cached verdict keeps its split")
	adjAssertEqual(t, cached[0].Reason, tags.CompositionReasonCached, "cached verdict's reason")
	adjAssertEqual(t, cached[0].Model, "", "a cache-applied verdict carries no model")
}

func TestDecomposeTags_SplitLogCarriesVersion(t *testing.T) {
	var buf bytes.Buffer
	adj := &mockAdjudicator{
		partFn:     partsDimensional,
		compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
	}
	a, docID := newSplitAdjudicator(t, adj, &buf)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)

	a.DecomposeTags(context.Background(), []string{"russian history"}, nil, nil, filter, docID, "")

	want := "tag split[" + analyzer.TagSplitVersion + "]"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("split log = %q, want it to carry %q", buf.String(), want)
	}
}

func TestDecomposeTags_NoVerdictWarns(t *testing.T) {
	var buf bytes.Buffer
	adj := &mockAdjudicator{
		partFn:     partsDimensional,
		compoundFn: compoundsStatus(""),
	}
	a, docID := newSplitAdjudicator(t, adj, &buf)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)

	got, rows, _ := a.DecomposeTags(context.Background(), []string{"machine learning"}, nil, nil, filter, docID, "")

	adjAssertEqual(t, len(got), 3, "the undecided keep stands whole and applies its dimensional parts alongside")
	adjAssertEqual(t, len(rows), 1, "one composition row")
	adjAssertEqual(t, rows[0].Reason, tags.CompositionReasonJudgeUnavailable, "keep reason")
	out := buf.String()
	if !strings.Contains(out, "<4>WARN") {
		t.Errorf("expected a WARN level line, got:\n%s", out)
	}
	if !strings.Contains(out, `compound judge returned no status for "machine learning"`) {
		t.Errorf("expected the WARN to name the judge and candidate, got:\n%s", out)
	}
}

func TestDecomposeTags_StoreKeyedCompoundSkipsJudgment(t *testing.T) {
	var buf bytes.Buffer
	adj := &mockAdjudicator{
		partFn:     partsDimensional,
		compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
	}
	a, docID := newSplitAdjudicator(t, adj, &buf)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)
	storeKeys := map[string]struct{}{tags.Key("machine learning"): {}}

	if err := a.queries.InsertTagCompositionEvent(context.Background(), database.InsertTagCompositionEventParams{
		DocumentID:      docID,
		Candidate:       tags.StoredName("machine learning"),
		Parts:           json.RawMessage("[]"),
		Disposition:     string(tags.CompositionKeep),
		Reason:          tags.CompositionReasonNamedUnit,
		CriteriaVersion: analyzer.TagSplitVersion,
	}); err != nil {
		t.Fatalf("seed composition row: %v", err)
	}

	got, rows, _ := a.DecomposeTags(context.Background(), []string{"machine learning"}, storeKeys, nil, filter, docID, "")

	adjAssertEqual(t, adj.partCall, 0, "a store-keyed compound never reaches the part judge")
	adjAssertEqual(t, adj.compoundCall, 0, "a store-keyed compound never reaches the compound judge")
	if !reflect.DeepEqual(got, []string{"machine learning"}) {
		t.Errorf("DecomposeTags = %v, want the compound whole", got)
	}
	adjAssertEqual(t, len(rows), 0, "the skip writes no composition row")
	if !strings.Contains(buf.String(), `skip "machine learning" (reason=store_existing disposition=keep)`) {
		t.Errorf("skip must log its reason, got:\n%s", buf.String())
	}
}

// TestDecomposeTags_StoreExistingReJudgedUntilDecided pins the evidence-aware skip: a store-present
// compound is an application only once a current-version verdict exists, so one with no composition row
// and one carrying only a degraded judge_unavailable keep both re-enter judgment on the next emission.
func TestDecomposeTags_StoreExistingReJudgedUntilDecided(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, a *Adjudicator, docID int64)
	}{
		{
			name: "no verdict row under the current criteria version",
			seed: func(t *testing.T, a *Adjudicator, docID int64) {},
		},
		{
			name: "a degraded keep is not a decision",
			seed: func(t *testing.T, a *Adjudicator, docID int64) {
				t.Helper()
				if err := a.queries.InsertTagCompositionEvent(context.Background(), database.InsertTagCompositionEventParams{
					DocumentID:      docID,
					Candidate:       tags.StoredName("machine learning"),
					Parts:           json.RawMessage("[]"),
					Disposition:     string(tags.CompositionKeep),
					Reason:          tags.CompositionReasonJudgeUnavailable,
					CriteriaVersion: analyzer.TagSplitVersion,
				}); err != nil {
					t.Fatalf("seed degraded composition row: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			adj := &mockAdjudicator{partFn: partsDimensional, compoundFn: compoundsStatus(analyzer.TagCompoundComposition)}
			a, docID := newSplitAdjudicator(t, adj, &buf)
			filter := analyzer.NewTagFilter(nil, nil, "", nil)
			storeKeys := map[string]struct{}{tags.Key("machine learning"): {}}
			tt.seed(t, a, docID)

			got, rows, _ := a.DecomposeTags(context.Background(), []string{"machine learning"}, storeKeys, nil, filter, docID, "")

			adjAssertEqual(t, adj.partCall, 1, "the undecided compound reaches the part judge once")
			adjAssertEqual(t, adj.compoundCall, 1, "the undecided compound reaches the compound judge once")
			if !reflect.DeepEqual(got, []string{"machine", "learning"}) {
				t.Errorf("DecomposeTags = %v, want the compound split into its judged parts", got)
			}
			adjAssertEqual(t, len(rows), 1, "the verdict the next emission can skip on is written")
			if !strings.Contains(buf.String(), "re-judged (reason=undecided)") {
				t.Errorf("re-judgment must be observable in the log, got:\n%s", buf.String())
			}
		})
	}
}

// TestDecomposeTags_StoreExistingKeepAppliesCachedParts pins the parts-alongside coverage of a compound
// the store already holds: the decided keep is skipped before judgment, yet its judged-dimensional parts
// are applied from the persisted cache on every emission — no model call, no composition row, idempotent.
func TestDecomposeTags_StoreExistingKeepAppliesCachedParts(t *testing.T) {
	var buf bytes.Buffer
	adj := &mockAdjudicator{partFn: partsDimensional, compoundFn: compoundsStatus(analyzer.TagCompoundComposition)}
	a, docID := newSplitAdjudicator(t, adj, &buf)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)
	storeKeys := map[string]struct{}{tags.Key("machine learning"): {}}
	ctx := context.Background()

	if err := a.queries.InsertTagCompositionEvent(ctx, database.InsertTagCompositionEventParams{
		DocumentID:      docID,
		Candidate:       tags.StoredName("machine learning"),
		Parts:           json.RawMessage("[]"),
		Disposition:     string(tags.CompositionKeep),
		Reason:          tags.CompositionReasonNamedUnit,
		CriteriaVersion: analyzer.TagSplitVersion,
	}); err != nil {
		t.Fatalf("seed composition row: %v", err)
	}
	for _, value := range []string{"machine", "learning"} {
		if _, err := a.queries.InsertTagPartDimensionEvent(ctx, database.InsertTagPartDimensionEventParams{
			DocumentID:    sql.NullInt64{Int64: docID, Valid: true},
			Value:         value,
			Criteria:      json.RawMessage("{}"),
			Dimensional:   true,
			PolicyVersion: analyzer.TagPartDimensionVersion,
		}); err != nil {
			t.Fatalf("seed part verdict for %q: %v", value, err)
		}
	}

	want := []string{"machine learning", "machine", "learning"}
	got, rows, _ := a.DecomposeTags(ctx, []string{"machine learning"}, storeKeys, nil, filter, docID, "")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DecomposeTags = %v, want the keep with its cached parts alongside %v", got, want)
	}
	adjAssertEqual(t, adj.partCall, 0, "coverage reads the part cache, never the judge")
	adjAssertEqual(t, adj.compoundCall, 0, "a decided compound is never re-judged")
	adjAssertEqual(t, len(rows), 0, "an application writes no composition row")
	if !strings.Contains(buf.String(), `applies parts ["machine" "learning"] alongside`) {
		t.Errorf("coverage must be observable in the log, got:\n%s", buf.String())
	}

	again, rowsAgain, _ := a.DecomposeTags(ctx, []string{"machine learning"}, storeKeys, nil, filter, docID, "")
	if !reflect.DeepEqual(again, want) {
		t.Errorf("second emission = %v, want the same idempotent coverage %v", again, want)
	}
	adjAssertEqual(t, adj.partCall, 0, "re-emission still makes no model call")
	adjAssertEqual(t, len(rowsAgain), 0, "re-emission still writes no row")
}

// TestDecomposeTags_StoreExistingWithoutAdjudicatorStaysSkipped pins the bounded journal: without a
// judge no verdict is reachable, so a store-present compound is skipped instead of re-judged — re-judging
// would append a judge_unavailable keep on every emission and never converge.
func TestDecomposeTags_StoreExistingWithoutAdjudicatorStaysSkipped(t *testing.T) {
	var buf bytes.Buffer
	adj := &mockAdjudicator{partFn: partsDimensional, compoundFn: compoundsStatus(analyzer.TagCompoundComposition)}
	a, docID := newSplitAdjudicator(t, adj, &buf)
	setUnexportedField(t, a.runner, "tagAdjudicator", nil)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)
	storeKeys := map[string]struct{}{tags.Key("machine learning"): {}}

	got, rows, _ := a.DecomposeTags(context.Background(), []string{"machine learning"}, storeKeys, nil, filter, docID, "")

	if !reflect.DeepEqual(got, []string{"machine learning"}) {
		t.Errorf("DecomposeTags = %v, want the compound whole", got)
	}
	adjAssertEqual(t, len(rows), 0, "the skip writes no composition row, so the journal cannot grow")
	if !strings.Contains(buf.String(), "undecided, no adjudicator") {
		t.Errorf("the no-adjudicator skip must be observable in the log, got:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "re-judged") {
		t.Errorf("without a judge nothing may be re-judged, got:\n%s", buf.String())
	}
}

func TestDecomposeTags_CompositionRows(t *testing.T) {
	tests := []struct {
		name     string
		adj      *mockAdjudicator // nil runs without an adjudicator
		names    []string
		docTypes []string
		promoted []string // names counted as promoted store labels by the reconstructibility signal
		want     tags.CompositionRow
	}{
		{
			name: "fresh composition split carries parts, judge, and model",
			adj: &mockAdjudicator{
				partFn:     partsDimensional,
				compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
			},
			names: []string{"machine learning"},
			want: tags.CompositionRow{
				Candidate:       "machine learning",
				Parts:           []string{"machine", "learning"},
				Disposition:     tags.CompositionSplit,
				Reason:          tags.CompositionReasonJudge,
				CriteriaVersion: analyzer.TagSplitVersion,
				Model:           "test-model",
				Reconstructible: new(false),
			},
		},
		{
			name: "named unit keeps with the decision reason",
			adj: &mockAdjudicator{
				partFn:     partsContainer,
				compoundFn: compoundsStatus(analyzer.TagCompoundNamedUnit),
			},
			names: []string{"machine learning"},
			want: tags.CompositionRow{
				Candidate:       "machine learning",
				Disposition:     tags.CompositionKeep,
				Reason:          tags.CompositionReasonNamedUnit,
				CriteriaVersion: analyzer.TagSplitVersion,
				Model:           "test-model",
				Reconstructible: new(false),
			},
		},
		{
			name: "components failing validation keep with their own reason",
			adj: &mockAdjudicator{
				partFn:     partsDimensional,
				compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
			},
			names:    []string{"machine learning"},
			docTypes: []string{"machine"},
			want: tags.CompositionRow{
				Candidate:       "machine learning",
				Disposition:     tags.CompositionKeep,
				Reason:          tags.CompositionReasonInvalidComponents,
				CriteriaVersion: analyzer.TagSplitVersion,
				Model:           "test-model",
				Reconstructible: new(false),
			},
		},
		{
			name: "a failed judge keeps with judge_unavailable and no model",
			adj: &mockAdjudicator{
				partFn: partsDimensional,
				compoundFn: func(context.Context, []string) ([]analyzer.TagCompoundResult, error) {
					return nil, context.DeadlineExceeded
				},
			},
			names: []string{"machine learning"},
			want: tags.CompositionRow{
				Candidate:       "machine learning",
				Parts:           []string{"machine", "learning"},
				Disposition:     tags.CompositionKeep,
				Reason:          tags.CompositionReasonJudgeUnavailable,
				CriteriaVersion: analyzer.TagSplitVersion,
				Reconstructible: new(false),
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
		{
			name: "a named unit whose parts are established holds reconstructible",
			adj: &mockAdjudicator{
				partFn:     partsDimensional,
				compoundFn: compoundsStatus(analyzer.TagCompoundNamedUnit),
			},
			names:    []string{"machine learning"},
			promoted: []string{"machine", "learning"},
			want: tags.CompositionRow{
				Candidate:       "machine learning",
				Parts:           []string{"machine", "learning"},
				Disposition:     tags.CompositionKeep,
				Reason:          tags.CompositionReasonNamedUnitReconstructible,
				CriteriaVersion: analyzer.TagSplitVersion,
				Model:           "test-model",
				Reconstructible: new(true),
			},
		},
		{
			name: "the same named unit without an established store view is a relational keep",
			adj: &mockAdjudicator{
				partFn:     partsDimensional,
				compoundFn: compoundsStatus(analyzer.TagCompoundNamedUnit),
			},
			names: []string{"machine learning"},
			want: tags.CompositionRow{
				Candidate:       "machine learning",
				Parts:           []string{"machine", "learning"},
				Disposition:     tags.CompositionKeep,
				Reason:          tags.CompositionReasonRelationalKeep,
				CriteriaVersion: analyzer.TagSplitVersion,
				Model:           "test-model",
				Reconstructible: new(false),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, docID := newSplitAdjudicator(t, tt.adj, nil)
			filter := analyzer.NewTagFilter(nil, nil, "", tt.docTypes)

			_, rows, _ := a.DecomposeTags(context.Background(), tt.names, nil, tags.StoreNames(tt.promoted), filter, docID, "")

			if len(rows) != 1 {
				t.Fatalf("composition rows = %d, want 1", len(rows))
			}
			assertCompositionRow(t, rows[0], tt.want)
		})
	}
}

func assertCompositionRow(t *testing.T, got, want tags.CompositionRow) {
	t.Helper()
	if got.Candidate != want.Candidate {
		t.Errorf("Candidate = %q, want %q", got.Candidate, want.Candidate)
	}
	if got.Disposition != want.Disposition {
		t.Errorf("Disposition = %s, want %s", got.Disposition, want.Disposition)
	}
	if got.Reason != want.Reason {
		t.Errorf("Reason = %s, want %s", got.Reason, want.Reason)
	}
	if !reflect.DeepEqual(got.Parts, want.Parts) {
		t.Errorf("Parts = %v, want %v", got.Parts, want.Parts)
	}
	if got.CriteriaVersion != want.CriteriaVersion {
		t.Errorf("CriteriaVersion = %s, want %s", got.CriteriaVersion, want.CriteriaVersion)
	}
	if got.Model != want.Model {
		t.Errorf("Model = %s, want %s", got.Model, want.Model)
	}
	switch {
	case (got.Reconstructible == nil) != (want.Reconstructible == nil):
		t.Errorf("Reconstructible = %v, want %v", got.Reconstructible, want.Reconstructible)
	case got.Reconstructible != nil && *got.Reconstructible != *want.Reconstructible:
		t.Errorf("Reconstructible = %v, want %v", *got.Reconstructible, *want.Reconstructible)
	}
}

func TestDecomposeTags_FoldResolvesPartsBeforeFilter(t *testing.T) {
	ctx := context.Background()
	storeView := tags.StoreNames([]string{"mexico", "revolution"})
	splitCompounds := func() *mockAdjudicator {
		return &mockAdjudicator{
			partFn:     partsDimensional,
			compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
		}
	}

	t.Run("parts land on their store anchors", func(t *testing.T) {
		a, docID := newSplitAdjudicator(t, splitCompounds(), nil)
		filter := analyzer.NewTagFilter(nil, nil, "", nil)

		got, rows, _ := a.DecomposeTags(ctx, []string{"mexican revolution"}, nil, storeView, filter, docID, "")

		want := []string{"mexico", "revolution"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("DecomposeTags = %v, want the fold-resolved parts %v", got, want)
		}
		if len(rows) != 1 {
			t.Fatalf("composition rows = %d, want 1", len(rows))
		}
		if rows[0].Disposition != tags.CompositionSplit {
			t.Errorf("Disposition = %s, want split", rows[0].Disposition)
		}
		if !reflect.DeepEqual(rows[0].Parts, want) {
			t.Errorf("row Parts = %v, want the resolved parts %v", rows[0].Parts, want)
		}
	})

	t.Run("the parts pass judges the resolved value", func(t *testing.T) {
		var judged []string
		adj := &mockAdjudicator{
			partFn: func(_ context.Context, values []string) ([]analyzer.TagPartResult, error) {
				judged = append(judged, values...)
				return partsDimensional(context.Background(), values)
			},
			compoundFn: compoundsStatus(analyzer.TagCompoundComposition),
		}
		a, docID := newSplitAdjudicator(t, adj, nil)
		filter := analyzer.NewTagFilter(nil, nil, "", nil)

		got, _, _ := a.DecomposeTags(ctx, []string{"mexican revolution"}, nil, storeView, filter, docID, "")
		if !reflect.DeepEqual(got, []string{"mexico", "revolution"}) {
			t.Errorf("DecomposeTags = %v, want the fold-resolved parts", got)
		}
		for _, v := range judged {
			if v != "mexico" && v != "revolution" {
				t.Errorf("parts pass judged %q, want the resolved values only", v)
			}
		}
		if len(judged) != 2 {
			t.Errorf("parts pass judged %v, want the two resolved values", judged)
		}
	})

	t.Run("without a store view the parts pass through", func(t *testing.T) {
		a, docID := newSplitAdjudicator(t, splitCompounds(), nil)
		filter := analyzer.NewTagFilter(nil, nil, "", nil)

		got, rows, _ := a.DecomposeTags(ctx, []string{"mexican revolution"}, nil, nil, filter, docID, "")

		want := []string{"mexican", "revolution"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("DecomposeTags = %v, want the unresolved parts %v", got, want)
		}
		if len(rows) != 1 || rows[0].Disposition != tags.CompositionSplit {
			t.Errorf("rows = %+v, want one split row", rows)
		}
	})

	t.Run("the hard rules evaluate the resolved part", func(t *testing.T) {
		// "mexico" is a document-type token: the rule must see the part
		// after the fold, so only one survivor remains and the compound
		// stays whole
		a, docID := newSplitAdjudicator(t, splitCompounds(), nil)
		filter := analyzer.NewTagFilter(nil, nil, "", []string{"mexico"})

		got, rows, _ := a.DecomposeTags(ctx, []string{"mexican revolution"}, nil, storeView, filter, docID, "")

		if !reflect.DeepEqual(got, []string{"mexican revolution"}) {
			t.Errorf("DecomposeTags = %v, want the compound kept whole", got)
		}
		if len(rows) != 1 {
			t.Fatalf("composition rows = %d, want 1", len(rows))
		}
		if rows[0].Disposition != tags.CompositionKeep {
			t.Errorf("Disposition = %s, want keep", rows[0].Disposition)
		}
		if rows[0].Reason != tags.CompositionReasonInvalidComponents {
			t.Errorf("Reason = %s, want invalid_components", rows[0].Reason)
		}
	})
}

func creditExhausted() error {
	return &analyzer.InsufficientCreditsError{Provider: "test", HTTPStatus: 402}
}

func TestRetryableJudgeFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"generic provider failure", errors.New("API error: status 500"), true},
		{"label-wrapped transport failure", fmt.Errorf("tag adjudication: %w", errors.New("send request: connection reset by peer")), true},
		{"empty response", errors.New("empty response from LLM"), true},
		{"parse failure", &analyzer.ReplyParseError{Step: "tag adjudication", Model: "test-model", RawLen: 8, RawPrefix: "not json"}, false},
		{"label-wrapped incomplete reply", fmt.Errorf("tag quality judge: %w", &analyzer.IncompleteReplyError{Missing: []int{1}}), false},
		{"output cap hit", fmt.Errorf("tag quality judge: %w", &analyzer.OutputCapError{Model: "test-model", Cap: 640}), false},
		{"credit exhaustion", creditExhausted(), false},
		{"label-wrapped credit exhaustion", fmt.Errorf("tag adjudication: %w", creditExhausted()), false},
		{"content too large", &analyzer.ContentTooLargeError{EstimatedTokens: 20, MaxInputTokens: 10}, false},
		{"token limit", &analyzer.TokenLimitError{MaxTokens: 100, RequestedTokens: 200}, false},
		{"context canceled", context.Canceled, false},
		{"context deadline exceeded", context.DeadlineExceeded, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryableJudgeFailure(tt.err); got != tt.want {
				t.Errorf("retryableJudgeFailure(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

func TestDecomposeTags_CreditErrorEscapes(t *testing.T) {
	adj := &mockAdjudicator{
		partFn: func(context.Context, []string) ([]analyzer.TagPartResult, error) {
			return nil, creditExhausted()
		},
	}
	a, docID := newSplitAdjudicator(t, adj, nil)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)

	got, rows, err := a.DecomposeTags(context.Background(), []string{"machine learning"}, nil, nil, filter, docID, "")
	if err == nil {
		t.Fatal("credit exhaustion must escape DecomposeTags")
	}
	if _, ok := errors.AsType[*analyzer.InsufficientCreditsError](err); !ok {
		t.Errorf("error type = %T, want *analyzer.InsufficientCreditsError", err)
	}
	if got != nil || rows != nil {
		t.Errorf("DecomposeTags returned (%v, %v) alongside the error", got, rows)
	}
	adjAssertEqual(t, adj.partCall, 1, "credit errors are not retried")
	adjAssertEqual(t, adj.compoundCall, 0, "a credit failure in the parts pass never reaches the compound pass")
}

func TestDecomposeTags_TransientFailureRetriesOnce(t *testing.T) {
	adj := &mockAdjudicator{compoundFn: compoundsStatus(analyzer.TagCompoundComposition)}
	adj.partFn = func(_ context.Context, values []string) ([]analyzer.TagPartResult, error) {
		if adj.partCall == 1 {
			return nil, errors.New("API error: status 500")
		}
		return partsDimensional(context.Background(), values)
	}
	a, docID := newSplitAdjudicator(t, adj, nil)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)

	got, _, err := a.DecomposeTags(context.Background(), []string{"machine learning"}, nil, nil, filter, docID, "")
	if err != nil {
		t.Fatalf("DecomposeTags() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"machine", "learning"}) {
		t.Errorf("DecomposeTags = %v, want the split after the retry", got)
	}
	adjAssertEqual(t, adj.partCall, 2, "a transient failure is retried once")
}

func TestApplyTagQuality_CreditErrorEscapes(t *testing.T) {
	a, q, adj := newTestAdjudicator(t)
	a.config.Enricher.TagQuality.Llm = config.LlmConfig{Provider: "test", Model: "judge-model"}
	setUnexportedField(t, a.runner, "tagQualityJudge", adj)
	adj.qualityFn = func(context.Context, string, []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
		return nil, creditExhausted()
	}
	ctx := context.Background()
	docID, _ := database.CreateTestDocument(t, q, "credit-quality-doc")

	analysis := &tools.ContentAnalysisResult{
		Tags:       []string{"resonance tunneling"},
		TagDetails: []analyzer.TagDetail{{Term: "resonance tunneling", Category: analyzer.TagCategoryTopic}},
	}
	kept, decisions, err := a.ApplyTagQuality(ctx, analysis, nil, map[string]struct{}{}, "text", docID, "")
	if err == nil {
		t.Fatal("credit exhaustion must escape ApplyTagQuality")
	}
	if _, ok := errors.AsType[*analyzer.InsufficientCreditsError](err); !ok {
		t.Errorf("error type = %T, want *analyzer.InsufficientCreditsError", err)
	}
	if kept != nil || decisions != nil {
		t.Errorf("ApplyTagQuality returned (%v, %v) alongside the error", kept, decisions)
	}
	adjAssertEqual(t, adj.qualityCall, 1, "credit errors are not retried")
}

func TestPlanIdentity_CreditErrorEscapes(t *testing.T) {
	a, _, adj := newTestAdjudicator(t)
	setUnexportedField(t, a.runner, "tagIdentityJudge", adj)
	adj.identityFn = func(context.Context, []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
		return nil, creditExhausted()
	}

	outs, plan, decisions, err := a.PlanIdentity(context.Background(), IdentityPlanInput{
		Names:   []string{"buildings"},
		DBNames: map[string]struct{}{},
	}, "")
	if err == nil {
		t.Fatal("credit exhaustion must escape PlanIdentity")
	}
	if _, ok := errors.AsType[*analyzer.InsufficientCreditsError](err); !ok {
		t.Errorf("error type = %T, want *analyzer.InsufficientCreditsError", err)
	}
	if outs != nil || decisions != nil {
		t.Errorf("PlanIdentity returned (%v, %v) alongside the error", outs, decisions)
	}
	if len(plan.Registrations) != 0 {
		t.Errorf("registrations = %d, want none from an aborted plan", len(plan.Registrations))
	}
	adjAssertEqual(t, adj.identityCall, 1, "credit errors are not retried")
}

func TestIdentityAnswers_CreditErrorStopsRemainingBatches(t *testing.T) {
	a, _, adj := newTestAdjudicator(t)
	setUnexportedField(t, a.runner, "tagIdentityJudge", adj)
	adj.identityFn = func(context.Context, []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
		return nil, creditExhausted()
	}

	// one candidate past the batch size so a surviving loop would attempt a
	// second batch after the dead account fails the first
	const n = identityBatchSize + 1
	candidates := make([]*identityCandidate, n)
	flagged := make(map[string]bool, n)
	for i := range candidates {
		name := fmt.Sprintf("building %d", i)
		candidates[i] = &identityCandidate{name: name, fold: name + "s"}
		flagged[name] = true
	}

	answers, err := a.identityAnswers(context.Background(), flagged, candidates, "")
	if err == nil {
		t.Fatal("credit exhaustion must escape identityAnswers")
	}
	if _, ok := errors.AsType[*analyzer.InsufficientCreditsError](err); !ok {
		t.Errorf("error type = %T, want *analyzer.InsufficientCreditsError", err)
	}
	if answers != nil {
		t.Errorf("answers = %v, want none alongside the error", answers)
	}
	adjAssertEqual(t, adj.identityCall, 1, "the dead account stops the batch loop after batch one")
}

func TestIdentityAnswers_PartialSalvage(t *testing.T) {
	a, _, adj := newTestAdjudicator(t)
	a.config.Enricher.TagAdjudicator.Llm = config.LlmConfig{Provider: "test", Model: "test-model"}
	setUnexportedField(t, a.runner, "tagIdentityJudge", adj)
	var buf bytes.Buffer
	a.logger = log.NewLoggerWithWriter(&buf)
	adj.identityFn = func(_ context.Context, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
		results := make([]analyzer.TagIdentityResult, len(candidates))
		for i := range candidates {
			results[i] = analyzer.TagIdentityResult{
				Index:                   i,
				FoldToBase:              true,
				FoldPreservesDenotation: true,
				ConventionalForm:        true,
			}
		}
		return results, &analyzer.IncompleteReplyError{Missing: []int{1}}
	}

	candidates := make([]*identityCandidate, 3)
	flagged := make(map[string]bool, 3)
	for i := range candidates {
		name := fmt.Sprintf("building %d", i)
		candidates[i] = &identityCandidate{name: name, fold: name + "s"}
		flagged[name] = true
	}

	answers, err := a.identityAnswers(context.Background(), flagged, candidates, "")
	if err != nil {
		t.Fatalf("identityAnswers() error = %v", err)
	}
	adjAssertEqual(t, adj.identityCall, 1, "an incomplete reply is not retried")
	if len(answers) != 2 {
		t.Fatalf("answers = %d entries, want the 2 the reply returned", len(answers))
	}
	salvaged, ok := answers["building 0"]
	if !ok {
		t.Fatal("the first candidate's answer was not salvaged")
	}
	adjAssertEqual(t, salvaged.FoldToBase, true, "the salvaged fold answer")
	adjAssertEqual(t, salvaged.model, "test-model", "the judge model recorded on the salvaged answer")
	if _, ok := answers["building 1"]; ok {
		t.Error("the missing candidate must stay unanswered so it remains provisional")
	}
	if _, ok := answers["building 2"]; !ok {
		t.Error("the last candidate's answer was not salvaged")
	}

	out := buf.String()
	if !strings.Contains(out, "<4>WARN") {
		t.Errorf("expected a WARN for the partial reply, got:\n%s", out)
	}
	if !strings.Contains(out, "partial reply") || !strings.Contains(out, "salvaged 2 of 3") {
		t.Errorf("expected the WARN to name the salvage counts, got:\n%s", out)
	}
}

func TestAdjudicateIdentityPairs_CreditErrorEscapes(t *testing.T) {
	a, _, adj := newTestAdjudicator(t)
	adj.adjudicateFn = func(context.Context, []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
		return nil, creditExhausted()
	}

	p := &identityPair{query: "democratic socialism", target: "social democracy"}
	err := a.adjudicateIdentityPairs(context.Background(), 0, "", []*identityPair{p}, "")
	if err == nil {
		t.Fatal("credit exhaustion must escape adjudicateIdentityPairs")
	}
	if _, ok := errors.AsType[*analyzer.InsufficientCreditsError](err); !ok {
		t.Errorf("error type = %T, want *analyzer.InsufficientCreditsError", err)
	}
	adjAssertEqual(t, p.decided, false, "the pair stays undecided")
	adjAssertEqual(t, adj.called, 1, "credit errors are not retried")
}

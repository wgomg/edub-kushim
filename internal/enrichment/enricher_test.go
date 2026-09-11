package enrichment

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/enrichment/tagpolicy"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/contentanalyzer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/types"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func newPolicyEnricher(autoReplace float64) *Enricher {
	return &Enricher{
		config: &config.Config{
			Enricher: config.EnricherConfig{
				TagMatcher: config.TagMatcherConfig{
					AutoReplaceSimilarity: autoReplace,
				},
			},
		},
		logger: utils.NewDiscardLogger(),
	}
}

func TestApplyConsolidationPolicy_ReplacesAboveAutoReplaceCutoff(t *testing.T) {
	e := newPolicyEnricher(0.85)
	queries := []string{"foo", "bar"}
	ranked := []tagmatcher.RankResult{
		{KeptName: "foo", Candidates: []tagmatcher.Candidate{{Tag: "foobar", Similarity: 0.92}}},
		{KeptName: "bar", Candidates: []tagmatcher.Candidate{{Tag: "baz", Similarity: 0.5}}},
	}

	got := e.applyConsolidationPolicy(context.Background(), 0, "", queries, ranked, nil, nil)

	want := []string{"foobar", "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("applyConsolidationPolicy = %v, want %v", got, want)
	}
}

func TestApplyConsolidationPolicy_KeepsKeptNameInAmbiguousBand(t *testing.T) {
	e := newPolicyEnricher(0.90)
	queries := []string{"foo"}
	ranked := []tagmatcher.RankResult{
		{KeptName: "foo", Candidates: []tagmatcher.Candidate{{Tag: "foobar", Similarity: 0.80}}},
	}

	got := e.applyConsolidationPolicy(context.Background(), 0, "", queries, ranked, nil, nil)

	if len(got) != 1 || got[0] != "foo" {
		t.Errorf("applyConsolidationPolicy = %v, want [foo]", got)
	}
}

func TestApplyConsolidationPolicy_KeepsKeptNameOnNoCandidates(t *testing.T) {
	e := newPolicyEnricher(0.0)
	queries := []string{"foo"}
	ranked := []tagmatcher.RankResult{{KeptName: "foo"}}

	got := e.applyConsolidationPolicy(context.Background(), 0, "", queries, ranked, nil, nil)

	if len(got) != 1 || got[0] != "foo" {
		t.Errorf("applyConsolidationPolicy = %v, want [foo]", got)
	}
}

func TestApplyConsolidationPolicy_FallsBackToQueryOnShortResult(t *testing.T) {
	e := newPolicyEnricher(0.0)
	queries := []string{"a", "b", "c"}
	ranked := []tagmatcher.RankResult{{KeptName: "A"}}

	got := e.applyConsolidationPolicy(context.Background(), 0, "", queries, ranked, nil, nil)

	want := []string{"A", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("applyConsolidationPolicy = %v, want %v", got, want)
	}
}

func TestApplyConsolidationPolicy_EmptyRankedReturnsQueries(t *testing.T) {
	e := newPolicyEnricher(0.5)
	queries := []string{"a", "b"}

	got := e.applyConsolidationPolicy(context.Background(), 0, "", queries, nil, nil, nil)

	if !reflect.DeepEqual(got, queries) {
		t.Errorf("applyConsolidationPolicy(nil) = %v, want %v", got, queries)
	}
}

func newLoggingPolicyEnricher(autoReplace float64, debug bool) (*Enricher, *bytes.Buffer) {
	var buf bytes.Buffer
	l := utils.NewLoggerWithWriter(&buf)
	if debug {
		l.SetLevel(utils.LevelDebug)
	}
	return &Enricher{
		config: &config.Config{
			Enricher: config.EnricherConfig{
				TagMatcher: config.TagMatcherConfig{
					AutoReplaceSimilarity: autoReplace,
				},
			},
		},
		logger: l,
	}, &buf
}

func TestApplyConsolidationPolicy_LogsDecisions(t *testing.T) {
	t.Run("replace is logged at info with version and reason", func(t *testing.T) {
		e, buf := newLoggingPolicyEnricher(0.90, false)
		queries := []string{"foo"}
		ranked := []tagmatcher.RankResult{
			mkRanked("foo", "foobar", 0.93),
		}

		got := e.applyConsolidationPolicy(context.Background(), 0, "", queries, ranked, nil, nil)
		if len(got) != 1 || got[0] != "foobar" {
			t.Fatalf("output = %v, want [foobar]", got)
		}

		out := buf.String()
		if !strings.Contains(out, "consolidation[v1]: replace") {
			t.Errorf("expected replace line in log, got:\n%s", out)
		}
		if !strings.Contains(out, `"foo"`) || !strings.Contains(out, `"foobar"`) {
			t.Errorf("expected query and target in log, got:\n%s", out)
		}
		if !strings.Contains(out, "reason=auto_replace") {
			t.Errorf("expected reason=auto_replace in log, got:\n%s", out)
		}
	})

	t.Run("band keep is logged at info with reason", func(t *testing.T) {
		e, buf := newLoggingPolicyEnricher(0.90, false)
		queries := []string{"foo"}
		ranked := []tagmatcher.RankResult{
			mkRanked("foo", "foobar", 0.80),
		}

		got := e.applyConsolidationPolicy(context.Background(), 0, "", queries, ranked, nil, nil)
		if len(got) != 1 || got[0] != "foo" {
			t.Fatalf("output = %v, want [foo]", got)
		}

		out := buf.String()
		if !strings.Contains(out, "consolidation[v1]: keep") {
			t.Errorf("expected keep line in log, got:\n%s", out)
		}
		if !strings.Contains(out, "reason=ambiguous_band") {
			t.Errorf("expected reason=ambiguous_band in log, got:\n%s", out)
		}
	})

	t.Run("passthrough with no candidates is logged only at debug", func(t *testing.T) {
		e, buf := newLoggingPolicyEnricher(0.90, true)
		queries := []string{"foo"}
		ranked := []tagmatcher.RankResult{{KeptName: "foo"}}

		got := e.applyConsolidationPolicy(context.Background(), 0, "", queries, ranked, nil, nil)
		if len(got) != 1 || got[0] != "foo" {
			t.Fatalf("output = %v, want [foo]", got)
		}

		out := buf.String()
		if !strings.Contains(out, "consolidation[v1]: passthrough") {
			t.Errorf("expected passthrough line in log, got:\n%s", out)
		}
		if !strings.Contains(out, "reason=no_candidates") {
			t.Errorf("expected reason=no_candidates in log, got:\n%s", out)
		}
	})
}

func mkRanked(kept, candTag string, sim float64) tagmatcher.RankResult {
	return tagmatcher.RankResult{
		KeptName:   kept,
		Candidates: []tagmatcher.Candidate{{Tag: candTag, Similarity: sim}},
	}
}

type mockAdjudicator struct {
	adjudicateFn func(ctx context.Context, pairs []contentanalyzer.TagPairEvidence) ([]contentanalyzer.TagVerdictResult, error)
	called       int
}

func (m *mockAdjudicator) Analyze(_ context.Context, _ string, _ []database.DocumentType, _ []database.PeopleType, _ []string) (*contentanalyzer.AnalysisResult, error) {
	return nil, nil
}

func (m *mockAdjudicator) AnalyzeDocType(_ context.Context, _ *contentanalyzer.AnalysisResult, _ string, _ []database.DocumentType, _ contentanalyzer.DocMetadata) (string, error) {
	return "", nil
}

func (m *mockAdjudicator) Adjudicate(ctx context.Context, pairs []contentanalyzer.TagPairEvidence) ([]contentanalyzer.TagVerdictResult, error) {
	m.called++
	if m.adjudicateFn != nil {
		return m.adjudicateFn(ctx, pairs)
	}
	return nil, nil
}

func (m *mockAdjudicator) Name() string { return "mock-adjudicator" }

type mockTagMatcher struct {
	rankFn func(ctx context.Context, docId string, queries []string) ([]tagmatcher.RankResult, error)
}

func (m *mockTagMatcher) Match(_ context.Context, _, _ string) ([]string, error) { return nil, nil }

func (m *mockTagMatcher) Rank(ctx context.Context, docId string, queries []string) ([]tagmatcher.RankResult, error) {
	if m.rankFn != nil {
		return m.rankFn(ctx, docId, queries)
	}
	results := make([]tagmatcher.RankResult, len(queries))
	for i, q := range queries {
		results[i] = tagmatcher.RankResult{KeptName: q}
	}
	return results, nil
}

func (m *mockTagMatcher) Close()       {}
func (m *mockTagMatcher) Name() string { return "mock-matcher" }

// setUnexportedField writes a value to an unexported struct field via
// reflection + unsafe. Test-only: tools.Runner fields are unexported and
// the enrichment package cannot set them through the public API.
func setUnexportedField(t *testing.T, target any, fieldName string, value any) {
	t.Helper()
	v := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	if !v.IsValid() {
		t.Fatalf("field %q not found", fieldName)
	}
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func newAdjudicationEnricher(t *testing.T) (*Enricher, *database.Queries, *mockAdjudicator) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	t.Cleanup(func() { db.Close() })

	adj := &mockAdjudicator{}
	matcher := &mockTagMatcher{}

	logger := utils.NewDiscardLogger()
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

	e := &Enricher{
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
	return e, q, adj
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

func countEvents(t *testing.T, q *database.Queries, docID int64, query, target string) int {
	t.Helper()
	_ = docID
	rows, err := q.LatestTagVerdictsForPairs(context.Background(), database.LatestTagVerdictsForPairsParams{
		PolicyVersion: "v1",
		Queries:       []string{query},
		Targets:       []string{target},
	})
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	return len(rows)
}

func adjAssertEqual(t *testing.T, got, want any, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", msg, got, want)
	}
}

func TestApplyConsolidationPolicy_Adjudication(t *testing.T) {
	t.Run("cached verdict applies without LLM call", func(t *testing.T) {
		e, q, adj := newAdjudicationEnricher(t)
		docID := createTestDocForAdjudication(t, q)

		_, err := q.InsertTagVerdictEvent(context.Background(), database.InsertTagVerdictEventParams{
			DocumentID:    docID,
			Query:         "democratic socialism",
			Target:        "social democracy",
			Sim:           0.81,
			Verdict:       types.TagVerdicts.Same,
			VerdictModel:  sql.NullString{},
			PolicyVersion: "v1",
		})
		if err != nil {
			t.Fatalf("seed cache: %v", err)
		}

		ranked := []tagmatcher.RankResult{mkRanked("democratic socialism", "social democracy", 0.81)}
		got := e.applyConsolidationPolicy(context.Background(), docID, "adj-test-doc", []string{"democratic socialism"}, ranked, nil, nil)
		adjAssertEqual(t, len(got), 1, "output length")
		adjAssertEqual(t, got[0], "social democracy", "cached same replaces")
		adjAssertEqual(t, adj.called, 0, "adjudicator call count")
	})

	t.Run("fresh same verdict replaces", func(t *testing.T) {
		e, q, adj := newAdjudicationEnricher(t)
		docID := createTestDocForAdjudication(t, q)

		adj.adjudicateFn = func(_ context.Context, pairs []contentanalyzer.TagPairEvidence) ([]contentanalyzer.TagVerdictResult, error) {
			results := make([]contentanalyzer.TagVerdictResult, len(pairs))
			for i := range pairs {
				results[i] = contentanalyzer.TagVerdictResult{Index: i, Verdict: types.TagVerdicts.Same}
			}
			return results, nil
		}

		ranked := []tagmatcher.RankResult{mkRanked("democratic socialism", "social democracy", 0.81)}
		got := e.applyConsolidationPolicy(context.Background(), docID, "adj-test-doc", []string{"democratic socialism"}, ranked, nil, nil)
		adjAssertEqual(t, len(got), 1, "output length")
		adjAssertEqual(t, got[0], "social democracy", "fresh same replaces")
		adjAssertEqual(t, adj.called, 1, "adjudicator call count")
		adjAssertEqual(t, countEvents(t, q, docID, "democratic socialism", "social democracy"), 1, "event rows")
	})

	t.Run("fresh variant verdict keeps", func(t *testing.T) {
		e, q, adj := newAdjudicationEnricher(t)
		docID := createTestDocForAdjudication(t, q)

		adj.adjudicateFn = func(_ context.Context, pairs []contentanalyzer.TagPairEvidence) ([]contentanalyzer.TagVerdictResult, error) {
			results := make([]contentanalyzer.TagVerdictResult, len(pairs))
			for i := range pairs {
				results[i] = contentanalyzer.TagVerdictResult{Index: i, Verdict: types.TagVerdicts.Variant}
			}
			return results, nil
		}

		ranked := []tagmatcher.RankResult{mkRanked("democratic socialism", "social democracy", 0.81)}
		got := e.applyConsolidationPolicy(context.Background(), docID, "adj-test-doc", []string{"democratic socialism"}, ranked, nil, nil)
		adjAssertEqual(t, len(got), 1, "output length")
		adjAssertEqual(t, got[0], "democratic socialism", "variant keeps query")
		adjAssertEqual(t, countEvents(t, q, docID, "democratic socialism", "social democracy"), 1, "event rows for variant")
	})

	t.Run("adjudicator error keeps all and logs", func(t *testing.T) {
		if os.Getenv("TEST_DATABASE_URL") == "" {
			t.Skip("TEST_DATABASE_URL not set")
		}
		q, db := database.NewTestQueries(t)
		t.Cleanup(func() { db.Close() })

		var buf bytes.Buffer
		logger := utils.NewLoggerWithWriter(&buf)

		adj := &mockAdjudicator{
			adjudicateFn: func(_ context.Context, _ []contentanalyzer.TagPairEvidence) ([]contentanalyzer.TagVerdictResult, error) {
				return nil, context.DeadlineExceeded
			},
		}
		matcher := &mockTagMatcher{}

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

		e := &Enricher{
			config: &config.Config{
				Enricher: config.EnricherConfig{
					TagMatcher:     config.TagMatcherConfig{AutoReplaceSimilarity: 0.95},
					TagAdjudicator: config.TagAdjudicatorConfig{Enabled: true, Llm: config.LlmConfig{Model: "test-model"}},
				},
			},
			queries: q,
			runner:  runner,
			logger:  logger,
		}
		docID := createTestDocForAdjudication(t, q)

		ranked := []tagmatcher.RankResult{mkRanked("democratic socialism", "social democracy", 0.81)}
		got := e.applyConsolidationPolicy(context.Background(), docID, "adj-test-doc", []string{"democratic socialism"}, ranked, nil, nil)
		adjAssertEqual(t, len(got), 1, "output length")
		adjAssertEqual(t, got[0], "democratic socialism", "error keeps query")
		out := buf.String()
		if !strings.Contains(out, "tag adjudication failed, keeping 1 pairs") {
			t.Errorf("expected adjudication failure log, got:\n%s", out)
		}
		adjAssertEqual(t, countEvents(t, q, docID, "democratic socialism", "social democracy"), 0, "no event rows on failure")
	})
}

func TestWriteVerdictEvent_ConflictReReadsStoredVerdict(t *testing.T) {
	e, q, _ := newAdjudicationEnricher(t)
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

	e.writeVerdictEvent(context.Background(), docID, tagpolicy.Decision{
		Query:  "democratic socialism",
		Target: "social democracy",
		Sim:    0.81,
	}, bp, nil)

	adjAssertEqual(t, string(bp.verdict), "variant", "stored verdict wins over conflicting fresh insert")
	adjAssertEqual(t, bp.cached, true, "pair marked as cache-applied")
	adjAssertEqual(t, bp.model, "other-model", "stored model recorded")
}

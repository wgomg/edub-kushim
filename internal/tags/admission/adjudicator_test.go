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
	called       int
	identityCall int
	splitCall    int
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

func (m *mockAdjudicator) JudgeTagQuality(_ context.Context, _ string, _ []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
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

func TestTagQualityScore(t *testing.T) {
	tTrue := true
	tFalse := false

	tests := []struct {
		name         string
		standardTerm *bool
		category     string
		bestSim      float64
		haveSim      bool
		noAnchorSim  float64
		want         int
	}{
		{"nil standard, topic, no sim", nil, "topic", 0, false, 0.6, 0},
		{"true standard, topic, no sim", &tTrue, "topic", 0, false, 0.6, 1},
		{"false standard, topic, no sim", &tFalse, "topic", 0, false, 0.6, -1},
		{"nil standard, other, no sim", nil, "other", 0, false, 0.6, -1},
		{"true standard, other, no sim", &tTrue, "other", 0, false, 0.6, 0},
		{"false standard, other, no sim", &tFalse, "other", 0, false, 0.6, -2},
		{"true standard, topic, sim above floor", &tTrue, "topic", 0.8, true, 0.6, 1},
		{"true standard, topic, sim below floor", &tTrue, "topic", 0.5, true, 0.6, 0},
		{"true standard, topic, sim==0 haveSim=false", &tTrue, "topic", 0, false, 0.6, 1},
		{"false standard, other, sim below floor", &tFalse, "other", 0.4, true, 0.6, -3},
		{"nil standard, other, sim below floor", nil, "other", 0.4, true, 0.6, -2},
		{"true standard, topic, haveSim=false sim ignored", &tTrue, "topic", 0.5, false, 0.6, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tagQualityScore(tt.standardTerm, tt.category, tt.bestSim, tt.haveSim, tt.noAnchorSim)
			if got != tt.want {
				t.Errorf("tagQualityScore() = %d, want %d", got, tt.want)
			}
		})
	}
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

	criteria := json.RawMessage(`{"subject_matter":true,"standard_term":true,"not_person_place_work":true,"appropriately_broad":true}`)

	n, err := q.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docID,
		TagName:       "quantum physics",
		Criteria:      criteria,
		Pass:          true,
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
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{
			TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0},
		},
	})
	if adj != nil {
		setUnexportedField(t, runner, "tagAdjudicator", adj)
	}
	return &Adjudicator{logger: logger, runner: runner}
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
			got := a.DecomposeTags(context.Background(), tt.in, filter, "")
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
			got := a.DecomposeTags(context.Background(), tt.names, filter, "")
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

	a.DecomposeTags(context.Background(), []string{"machine learning"}, filter, "")
	a.DecomposeTags(context.Background(), []string{"machine learning"}, filter, "")

	adjAssertEqual(t, adj.splitCall, 1, "split adjudicator call count")
}

func TestDecomposeTags_SplitLogCarriesVersion(t *testing.T) {
	var buf bytes.Buffer
	a := newSplitAdjudicator(t, &mockAdjudicator{splitFn: splitVerdictsAll}, &buf)
	filter := analyzer.NewTagFilter(nil, nil, "", nil)

	a.DecomposeTags(context.Background(), []string{"russian history"}, filter, "")

	want := "tag split[" + analyzer.TagSplitVersion + "]"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("split log = %q, want it to carry %q", buf.String(), want)
	}
}

package service

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/types"
)

type mockMatcher struct {
	mu    sync.Mutex
	pairs []tagmatcher.SimilarPair
}

func (m *mockMatcher) Match(ctx context.Context, docId, input string) ([]string, error) {
	return []string{input}, nil
}

func (m *mockMatcher) Rank(ctx context.Context, docId string, queries []string) ([]tagmatcher.RankResult, error) {
	results := make([]tagmatcher.RankResult, len(queries))
	for i, q := range queries {
		results[i] = tagmatcher.RankResult{KeptName: q}
	}
	return results, nil
}

func (m *mockMatcher) SimilarPairs(ctx context.Context) ([]tagmatcher.SimilarPair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]tagmatcher.SimilarPair(nil), m.pairs...), nil
}

func (m *mockMatcher) Close() {}

func (m *mockMatcher) Name() string { return "mock-matcher" }

// trackingEmbedder wraps a base embedder and records the names passed to
// RemoveFromStore, so tests can assert the merge service removed the source.
type trackingEmbedder struct {
	base       tagmatcher.Embedder
	mu         sync.Mutex
	removedAll []string
}

func (t *trackingEmbedder) Encode(ctx context.Context, docId *string, texts []string) ([][]float32, error) {
	return t.base.Encode(ctx, docId, texts)
}

func (t *trackingEmbedder) AddToStore(ctx context.Context, names []string) error {
	return t.base.AddToStore(ctx, names)
}

func (t *trackingEmbedder) RemoveFromStore(ctx context.Context, names []string) error {
	t.mu.Lock()
	t.removedAll = append(t.removedAll, names...)
	t.mu.Unlock()
	return t.base.RemoveFromStore(ctx, names)
}

func (t *trackingEmbedder) Close() {}

func (t *trackingEmbedder) Name() string { return "tracking-embedder" }

func (t *trackingEmbedder) removed() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.removedAll...)
}

func newTestTagMerge(t *testing.T) (*TagMerge, *trackingEmbedder, *database.Client) {
	t.Helper()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	base := testutil.NewMockEmbedder()
	tracker := &trackingEmbedder{base: base}
	svc := NewTagMerge(client, &mockMatcher{}, tracker, testutil.NewTestLogger())
	return svc, tracker, client
}

func TestTagMerge_Merge_HappyPath(t *testing.T) {
	svc, embedder, client := newTestTagMerge(t)
	ctx := context.Background()

	if err := embedder.AddToStore(ctx, []string{"reassign source", "reassign target"}); err != nil {
		t.Fatalf("seed embedder store: %v", err)
	}

	sourceID := database.MustCreateTag(t, client.Queries, "reassign source")
	targetID := database.MustCreateTag(t, client.Queries, "reassign target")

	doc1ID := database.CreateUniqueDocument(t, client.Queries, "rm-1.pdf")
	doc2ID := database.CreateUniqueDocument(t, client.Queries, "rm-2.pdf")
	doc3ID := database.CreateUniqueDocument(t, client.Queries, "rm-3.pdf")

	database.MustAddDocumentTag(t, client.Queries, doc1ID, sourceID)
	database.MustAddDocumentTag(t, client.Queries, doc2ID, sourceID)
	database.MustAddDocumentTag(t, client.Queries, doc2ID, targetID)
	database.MustAddDocumentTag(t, client.Queries, doc3ID, targetID)

	result, err := svc.Merge(ctx, sourceID, targetID)
	testutil.AssertNoError(t, err, "merge")
	testutil.AssertEqual(t, result.SourceID, sourceID, "source id")
	testutil.AssertEqual(t, result.TargetID, targetID, "target id")
	testutil.AssertEqual(t, result.TargetName, "reassign target", "target name")
	testutil.AssertEqual(t, result.Reassigned, int64(1), "one document re-pointed (doc1); doc2 was skipped because it already had the target")

	removed := embedder.removed()
	testutil.AssertEqual(t, len(removed), 1, "RemoveFromStore called exactly once")
	testutil.AssertEqual(t, removed[0], "reassign source", "source name passed to RemoveFromStore")

	_, err = client.Queries.GetTag(ctx, sourceID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("source tag should be gone, got: %v", err)
	}
	target, err := client.Queries.GetTag(ctx, targetID)
	testutil.AssertNoError(t, err, "get target")
	testutil.AssertEqual(t, target.Name, "reassign target", "target name unchanged")

	tags1 := getDocTagNames(t, client.Queries, doc1ID)
	testutil.AssertEqual(t, len(tags1), 1, "doc1 carries one tag")
	testutil.AssertEqual(t, tags1[0], "reassign target", "doc1's tag is the target")

	tags2 := getDocTagNames(t, client.Queries, doc2ID)
	testutil.AssertEqual(t, len(tags2), 1, "doc2 carries one tag (leftover cleared)")
	testutil.AssertEqual(t, tags2[0], "reassign target", "doc2's tag is the target")
}

func TestTagMerge_Merge_Errors(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	t.Run("same id is invalid", func(t *testing.T) {
		id := database.MustCreateTag(t, client.Queries, "self-merge")
		_, err := svc.Merge(ctx, id, id)
		testutil.AssertError(t, err, "merge")
		testutil.AssertEqual(t, errs.KindOf(err), errs.KindInvalid, "invalid kind → 400")
	})

	t.Run("missing source is not found", func(t *testing.T) {
		targetID := database.MustCreateTag(t, client.Queries, "present-target")
		_, err := svc.Merge(ctx, 999_999_999, targetID)
		testutil.AssertError(t, err, "merge")
		testutil.AssertEqual(t, errs.KindOf(err), errs.KindNotFound, "not-found kind → 404")
	})

	t.Run("missing target is not found", func(t *testing.T) {
		sourceID := database.MustCreateTag(t, client.Queries, "present-source")
		_, err := svc.Merge(ctx, sourceID, 999_999_999)
		testutil.AssertError(t, err, "merge")
		testutil.AssertEqual(t, errs.KindOf(err), errs.KindNotFound, "not-found kind → 404")
	})
}

func TestTagMerge_Suggestions_EmptyEventTableIsSimilarityOnly(t *testing.T) {
	svc, embedder, client := newTestTagMerge(t)
	ctx := context.Background()

	_ = database.MustCreateTag(t, client.Queries, "software development")
	_ = database.MustCreateTag(t, client.Queries, "software engineering")

	// Only surface the pair between our two tags; ignore every other tag the
	// service scans, so the test counts exactly one suggestion.
	matcher := &mockMatcher{
		pairs: []tagmatcher.SimilarPair{
			{A: "software development", B: "software engineering", Similarity: 0.92},
		},
	}
	svc.matcher = matcher
	_ = embedder

	results, err := svc.Suggestions(ctx, SuggestionsParams{Limit: 20})
	testutil.AssertNoError(t, err, "suggestions")
	testutil.AssertEqual(t, len(results), 1, "one pair surfaces")
	testutil.AssertEqual(t, results[0].Confidence, mergeSuggestionConfidenceSimilarityOnly, "empty event table → similarity-only")
	testutil.AssertEqual(t, results[0].EventCount, int64(0), "no event rows")
	testutil.AssertEqual(t, results[0].SameCount+results[0].VariantCount+results[0].RelatedCount, int64(0), "no verdict counts")
	testutil.AssertEqual(t, results[0].Similarity, 0.92, "similarity from pair cache")
}

func TestTagMerge_Suggestions_PopulationConfidenceAndOrdering(t *testing.T) {
	svc, embedder, client := newTestTagMerge(t)
	ctx := context.Background()

	// "alpha" has more documents than "beta" — alpha must appear first in the
	// canonical ordering.
	alphaID := database.MustCreateTag(t, client.Queries, "alpha")
	betaID := database.MustCreateTag(t, client.Queries, "beta")

	doc1 := database.CreateUniqueDocument(t, client.Queries, "sg-1.pdf")
	doc2 := database.CreateUniqueDocument(t, client.Queries, "sg-2.pdf")
	doc3 := database.CreateUniqueDocument(t, client.Queries, "sg-3.pdf")
	database.MustAddDocumentTag(t, client.Queries, doc1, alphaID)
	database.MustAddDocumentTag(t, client.Queries, doc2, alphaID)
	database.MustAddDocumentTag(t, client.Queries, doc3, alphaID)
	database.MustAddDocumentTag(t, client.Queries, doc1, betaID)

	// Seed an adjudication history for the pair. Use two different policy
	// versions to bypass the unique-fresh-pair index and exercise the
	// "across all policy versions" aggregation the suggestions scan depends on.
	docID := database.CreateUniqueDocument(t, client.Queries, "sg-event-anchor.pdf")
	insert := func(query, target string, verdict types.TagVerdict, policy string) {
		_, err := client.Queries.InsertTagVerdictEvent(ctx, database.InsertTagVerdictEventParams{
			DocumentID:    docID,
			Query:         query,
			Target:        target,
			Sim:           0.88,
			Verdict:       verdict,
			PolicyVersion: policy,
		})
		if err != nil {
			t.Fatalf("insert event %s→%s: %v", query, target, err)
		}
	}
	insert("alpha", "beta", types.TagVerdicts.Variant, "v1")
	insert("alpha", "beta", types.TagVerdicts.Related, "v2")
	insert("beta", "alpha", types.TagVerdicts.Same, "v1")

	matcher := &mockMatcher{
		pairs: []tagmatcher.SimilarPair{
			{A: "alpha", B: "beta", Similarity: 0.90},
		},
	}
	svc.matcher = matcher
	_ = embedder

	results, err := svc.Suggestions(ctx, SuggestionsParams{Limit: 20})
	testutil.AssertNoError(t, err, "suggestions")
	testutil.AssertEqual(t, len(results), 1, "one pair surfaces")
	s := results[0]

	testutil.AssertEqual(t, s.Confidence, mergeSuggestionConfidencePopulation, "seeded events → population")
	testutil.AssertEqual(t, s.EventCount, int64(3), "three event rows across orientations")
	testutil.AssertEqual(t, s.VariantCount, int64(1), "variant count")
	testutil.AssertEqual(t, s.RelatedCount, int64(1), "related count")
	testutil.AssertEqual(t, s.SameCount, int64(1), "same count from the reverse orientation")
	testutil.AssertEqual(t, s.Similarity, 0.90, "similarity from pair cache")

	testutil.AssertEqual(t, len(s.Tags), 2, "two tag entries")
	testutil.AssertEqual(t, s.Tags[0].ID, alphaID, "alpha (higher document count) is first")
	testutil.AssertEqual(t, s.Tags[0].DocumentCount, int64(3), "alpha document count")
	testutil.AssertEqual(t, s.Tags[1].ID, betaID, "beta is second")
	testutil.AssertEqual(t, s.Tags[1].DocumentCount, int64(1), "beta document count")
}

func TestTagMerge_DismissPair_HappyPath(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	idA := database.MustCreateTag(t, client.Queries, "dismiss alpha")
	idB := database.MustCreateTag(t, client.Queries, "dismiss beta")
	_ = idA
	_ = idB

	err := svc.DismissPair(ctx, "Dismiss Alpha", "Dismiss Beta")
	testutil.AssertNoError(t, err, "dismiss")

	dismissals, err := client.Queries.ListTagMergeDismissals(ctx)
	testutil.AssertNoError(t, err, "list dismissals")
	testutil.AssertEqual(t, len(dismissals), 1, "one dismissal row")
	testutil.AssertEqual(t, dismissals[0].TagA, "dismiss alpha", "canonical tag_a (lowercase)")
	testutil.AssertEqual(t, dismissals[0].TagB, "dismiss beta", "canonical tag_b (lowercase)")
}

func TestTagMerge_DismissPair_Idempotent(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	database.MustCreateTag(t, client.Queries, "idempotent a")
	database.MustCreateTag(t, client.Queries, "idempotent b")

	err := svc.DismissPair(ctx, "Idempotent A", "Idempotent B")
	testutil.AssertNoError(t, err, "first dismiss")
	err = svc.DismissPair(ctx, "Idempotent A", "Idempotent B")
	testutil.AssertNoError(t, err, "second dismiss (idempotent)")

	dismissals, err := client.Queries.ListTagMergeDismissals(ctx)
	testutil.AssertNoError(t, err, "list dismissals")
	testutil.AssertEqual(t, len(dismissals), 1, "still one row after second dismiss")
}

func TestTagMerge_DismissPair_TagNotFound(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	database.MustCreateTag(t, client.Queries, "dismiss present")

	err := svc.DismissPair(ctx, "dismiss present", "dismiss missing")
	testutil.AssertError(t, err, "dismiss missing tag")
	testutil.AssertEqual(t, errs.KindOf(err), errs.KindNotFound, "not-found kind → 404")
}

func TestTagMerge_DismissPair_EmptyName(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	_ = client

	err := svc.DismissPair(ctx, "", "")
	testutil.AssertError(t, err, "dismiss empty")
	testutil.AssertEqual(t, errs.KindOf(err), errs.KindInvalid, "invalid kind → 400")
}

func TestTagMerge_DismissPair_SameTag(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	database.MustCreateTag(t, client.Queries, "same-tag")

	err := svc.DismissPair(ctx, "same-tag", "same-tag")
	testutil.AssertError(t, err, "dismiss same tag")
	testutil.AssertEqual(t, errs.KindOf(err), errs.KindInvalid, "invalid kind → 400")
}

func TestTagMerge_RestorePair_HappyPath(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	database.MustCreateTag(t, client.Queries, "restore alpha")
	database.MustCreateTag(t, client.Queries, "restore beta")

	err := svc.DismissPair(ctx, "Restore Alpha", "Restore Beta")
	testutil.AssertNoError(t, err, "dismiss first")

	err = svc.RestorePair(ctx, "Restore Alpha", "Restore Beta")
	testutil.AssertNoError(t, err, "restore")

	dismissals, err := client.Queries.ListTagMergeDismissals(ctx)
	testutil.AssertNoError(t, err, "list dismissals")
	testutil.AssertEqual(t, len(dismissals), 0, "no dismissals after restore")
}

func TestTagMerge_RestorePair_Idempotent(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	database.MustCreateTag(t, client.Queries, "rest-idem a")
	database.MustCreateTag(t, client.Queries, "rest-idem b")

	err := svc.RestorePair(ctx, "Rest-Idem A", "Rest-Idem B")
	testutil.AssertNoError(t, err, "restore without prior dismiss is idempotent")
}

func TestTagMerge_Suggestions_ForTag(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	_ = database.MustCreateTag(t, client.Queries, "paired alpha")
	_ = database.MustCreateTag(t, client.Queries, "paired beta")
	_ = database.MustCreateTag(t, client.Queries, "paired gamma")

	matcher := &mockMatcher{
		pairs: []tagmatcher.SimilarPair{
			{A: "paired alpha", B: "paired beta", Similarity: 0.90},
			{A: "paired beta", B: "paired gamma", Similarity: 0.85},
		},
	}
	svc.matcher = matcher

	results, err := svc.Suggestions(ctx, SuggestionsParams{
		Limit:  50,
		ForTag: "paired alpha",
	})
	testutil.AssertNoError(t, err, "suggestions")
	testutil.AssertEqual(t, len(results), 1, "only pair involving 'paired alpha'")
	testutil.AssertEqual(t, results[0].Tags[0].Name, "paired alpha", "first tag")
	testutil.AssertEqual(t, results[0].Tags[1].Name, "paired beta", "second tag")
}

func TestTagMerge_Suggestions_ForTag_NoMatch(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	_ = database.MustCreateTag(t, client.Queries, "nomatch alpha")
	_ = database.MustCreateTag(t, client.Queries, "nomatch beta")

	matcher := &mockMatcher{
		pairs: []tagmatcher.SimilarPair{
			{A: "nomatch alpha", B: "nomatch beta", Similarity: 0.90},
		},
	}
	svc.matcher = matcher

	results, err := svc.Suggestions(ctx, SuggestionsParams{
		Limit:  50,
		ForTag: "nonexistent tag",
	})
	testutil.AssertNoError(t, err, "suggestions")
	testutil.AssertEqual(t, len(results), 0, "no matches for nonexistent tag")
}

func TestTagMerge_Suggestions_ExcludesDismissed(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	_ = database.MustCreateTag(t, client.Queries, "dis-a")
	_ = database.MustCreateTag(t, client.Queries, "dis-b")
	_ = database.MustCreateTag(t, client.Queries, "keep-x")

	matcher := &mockMatcher{
		pairs: []tagmatcher.SimilarPair{
			{A: "dis a", B: "dis b", Similarity: 0.90},
			{A: "dis a", B: "keep x", Similarity: 0.80},
		},
	}
	svc.matcher = matcher

	err := svc.DismissPair(ctx, "Dis-A", "Dis-B")
	testutil.AssertNoError(t, err, "dismiss pair")

	results, err := svc.Suggestions(ctx, SuggestionsParams{Limit: 50})
	testutil.AssertNoError(t, err, "suggestions (default: exclude dismissed)")
	testutil.AssertEqual(t, len(results), 1, "only non-dismissed pair")
	testutil.AssertEqual(t, results[0].Dismissed, false, "result not dismissed")
}

func TestTagMerge_Suggestions_IncludeDismissed(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	_ = database.MustCreateTag(t, client.Queries, "inc-a")
	_ = database.MustCreateTag(t, client.Queries, "inc-b")

	matcher := &mockMatcher{
		pairs: []tagmatcher.SimilarPair{
			{A: "inc a", B: "inc b", Similarity: 0.90},
		},
	}
	svc.matcher = matcher

	err := svc.DismissPair(ctx, "Inc-A", "Inc-B")
	testutil.AssertNoError(t, err, "dismiss pair")

	results, err := svc.Suggestions(ctx, SuggestionsParams{Limit: 50, IncludeDismissed: true})
	testutil.AssertNoError(t, err, "suggestions (include dismissed)")
	testutil.AssertEqual(t, len(results), 1, "dismissed pair included")
	testutil.AssertEqual(t, results[0].Dismissed, true, "result is dismissed")
}

func TestTagMerge_Suggestions_PerClassLimit(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	for _, name := range []string{"pcls-a", "pcls-b", "pcls-c", "pcls-d", "pcls-e", "pcls-f"} {
		database.MustCreateTag(t, client.Queries, name)
	}
	matcher := &mockMatcher{
		pairs: []tagmatcher.SimilarPair{
			{A: "pcls a", B: "pcls b", Similarity: 0.90},
			{A: "pcls c", B: "pcls d", Similarity: 0.85},
			{A: "pcls e", B: "pcls f", Similarity: 0.80},
		},
	}
	svc.matcher = matcher

	err := svc.DismissPair(ctx, "Pcls-C", "Pcls-D")
	testutil.AssertNoError(t, err, "dismiss pair")

	results, err := svc.Suggestions(ctx, SuggestionsParams{Limit: 2, IncludeDismissed: true})
	testutil.AssertNoError(t, err, "suggestions")
	pending, dismissed := 0, 0
	for _, r := range results {
		if r.Dismissed {
			dismissed++
		} else {
			pending++
		}
	}
	testutil.AssertEqual(t, pending <= 2, true, "pending capped at limit")
	testutil.AssertEqual(t, dismissed <= 2, true, "dismissed capped at limit")
}

func TestTagMerge_Suggestions_DismissedFlagged(t *testing.T) {
	svc, _, client := newTestTagMerge(t)
	ctx := context.Background()

	_ = database.MustCreateTag(t, client.Queries, "flag-a")
	_ = database.MustCreateTag(t, client.Queries, "flag-b")

	matcher := &mockMatcher{
		pairs: []tagmatcher.SimilarPair{
			{A: "flag a", B: "flag b", Similarity: 0.90},
		},
	}
	svc.matcher = matcher

	err := svc.DismissPair(ctx, "Flag-A", "Flag-B")
	testutil.AssertNoError(t, err, "dismiss")

	results, err := svc.Suggestions(ctx, SuggestionsParams{Limit: 50, IncludeDismissed: true})
	testutil.AssertNoError(t, err, "suggestions")
	testutil.AssertEqual(t, len(results), 1, "one result")
	testutil.AssertEqual(t, results[0].Dismissed, true, "Dismissed flag is set")
}

func getDocTagNames(t *testing.T, q *database.Queries, documentID int64) []string {
	t.Helper()
	tags, err := q.GetDocumentTags(context.Background(), documentID)
	if err != nil {
		t.Fatalf("get document tags: %v", err)
	}
	out := make([]string, len(tags))
	for i, tg := range tags {
		out[i] = tg.Name
	}
	return out
}

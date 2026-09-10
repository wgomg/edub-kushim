package enrichment

import (
	"reflect"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func newPolicyEnricher(threshold float64) *Enricher {
	return &Enricher{
		config: &config.Config{
			Enricher: config.EnricherConfig{
				TagMatcher: config.TagMatcherConfig{
					ConsolidationSimilarity: threshold,
				},
			},
		},
		logger: utils.NewDiscardLogger(),
	}
}

func TestApplyConsolidationPolicy_ReplacesOnThreshold(t *testing.T) {
	e := newPolicyEnricher(0.85)
	queries := []string{"foo", "bar"}
	ranked := []tagmatcher.RankResult{
		{KeptName: "foo", Candidates: []tagmatcher.Candidate{{Tag: "foobar", Similarity: 0.92}}},
		{KeptName: "bar", Candidates: []tagmatcher.Candidate{{Tag: "baz", Similarity: 0.5}}},
	}

	got := e.applyConsolidationPolicy(queries, ranked, nil)

	want := []string{"foobar", "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("applyConsolidationPolicy = %v, want %v", got, want)
	}
}

func TestApplyConsolidationPolicy_KeepsKeptNameBelowThreshold(t *testing.T) {
	e := newPolicyEnricher(0.90)
	queries := []string{"foo"}
	ranked := []tagmatcher.RankResult{
		{KeptName: "foo", Candidates: []tagmatcher.Candidate{{Tag: "foobar", Similarity: 0.80}}},
	}

	got := e.applyConsolidationPolicy(queries, ranked, nil)

	if len(got) != 1 || got[0] != "foo" {
		t.Errorf("applyConsolidationPolicy = %v, want [foo]", got)
	}
}

func TestApplyConsolidationPolicy_KeepsKeptNameOnNoCandidates(t *testing.T) {
	e := newPolicyEnricher(0.0)
	queries := []string{"foo"}
	ranked := []tagmatcher.RankResult{{KeptName: "foo"}}

	got := e.applyConsolidationPolicy(queries, ranked, nil)

	if len(got) != 1 || got[0] != "foo" {
		t.Errorf("applyConsolidationPolicy = %v, want [foo]", got)
	}
}

func TestApplyConsolidationPolicy_FallsBackToQueryOnShortResult(t *testing.T) {
	e := newPolicyEnricher(0.0)
	queries := []string{"a", "b", "c"}
	ranked := []tagmatcher.RankResult{{KeptName: "A"}}

	got := e.applyConsolidationPolicy(queries, ranked, nil)

	want := []string{"A", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("applyConsolidationPolicy = %v, want %v", got, want)
	}
}

func TestApplyConsolidationPolicy_EmptyRankedReturnsQueries(t *testing.T) {
	e := newPolicyEnricher(0.5)
	queries := []string{"a", "b"}

	got := e.applyConsolidationPolicy(queries, nil, nil)

	if !reflect.DeepEqual(got, queries) {
		t.Errorf("applyConsolidationPolicy(nil) = %v, want %v", got, queries)
	}
}

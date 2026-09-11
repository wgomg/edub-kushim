package enrichment

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
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

	got := e.applyConsolidationPolicy(queries, ranked, nil, nil)

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

	got := e.applyConsolidationPolicy(queries, ranked, nil, nil)

	if len(got) != 1 || got[0] != "foo" {
		t.Errorf("applyConsolidationPolicy = %v, want [foo]", got)
	}
}

func TestApplyConsolidationPolicy_KeepsKeptNameOnNoCandidates(t *testing.T) {
	e := newPolicyEnricher(0.0)
	queries := []string{"foo"}
	ranked := []tagmatcher.RankResult{{KeptName: "foo"}}

	got := e.applyConsolidationPolicy(queries, ranked, nil, nil)

	if len(got) != 1 || got[0] != "foo" {
		t.Errorf("applyConsolidationPolicy = %v, want [foo]", got)
	}
}

func TestApplyConsolidationPolicy_FallsBackToQueryOnShortResult(t *testing.T) {
	e := newPolicyEnricher(0.0)
	queries := []string{"a", "b", "c"}
	ranked := []tagmatcher.RankResult{{KeptName: "A"}}

	got := e.applyConsolidationPolicy(queries, ranked, nil, nil)

	want := []string{"A", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("applyConsolidationPolicy = %v, want %v", got, want)
	}
}

func TestApplyConsolidationPolicy_EmptyRankedReturnsQueries(t *testing.T) {
	e := newPolicyEnricher(0.5)
	queries := []string{"a", "b"}

	got := e.applyConsolidationPolicy(queries, nil, nil, nil)

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

		got := e.applyConsolidationPolicy(queries, ranked, nil, nil)
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

		got := e.applyConsolidationPolicy(queries, ranked, nil, nil)
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

		got := e.applyConsolidationPolicy(queries, ranked, nil, nil)
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

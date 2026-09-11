package tagpolicy

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
)

func storeSet(names ...string) map[string]struct{} {
	s := make(map[string]struct{}, len(names))
	for _, n := range names {
		s[n] = struct{}{}
	}
	return s
}

func mkResult(kept string, cands ...tagmatcher.Candidate) tagmatcher.RankResult {
	return tagmatcher.RankResult{KeptName: kept, Candidates: cands}
}

func cand(tag string, sim float64) tagmatcher.Candidate {
	return tagmatcher.Candidate{Tag: tag, Similarity: sim}
}

func TestEvaluate(t *testing.T) {
	// auto-replace cutoff shared across the table; band-boundary cases pin sim
	// just below and at/above it.
	const cutoff = 0.90

	tests := []struct {
		name      string
		queries   []string
		ranked    []tagmatcher.RankResult
		storeTags map[string]struct{}
		want      []Decision
	}{
		{
			name:    "short slice falls back to query",
			queries: []string{"a", "b", "c"},
			ranked:  []tagmatcher.RankResult{mkResult("A")},
			want: []Decision{
				{Query: "a", Output: "A", Action: ActionPassthrough, Reason: ReasonNoCandidates},
				{Query: "b", Output: "b", Action: ActionPassthrough, Reason: ReasonShortSlice},
				{Query: "c", Output: "c", Action: ActionPassthrough, Reason: ReasonShortSlice},
			},
		},
		{
			name:    "nil ranked falls back to every query",
			queries: []string{"a", "b"},
			want: []Decision{
				{Query: "a", Output: "a", Action: ActionPassthrough, Reason: ReasonShortSlice},
				{Query: "b", Output: "b", Action: ActionPassthrough, Reason: ReasonShortSlice},
			},
		},
		{
			name:    "empty candidates passes KeptName through",
			queries: []string{"foo"},
			ranked:  []tagmatcher.RankResult{mkResult("foo")},
			want: []Decision{
				{Query: "foo", Output: "foo", Action: ActionPassthrough, Reason: ReasonNoCandidates},
			},
		},

		{
			name:    "identity replaces with sim and runner recorded",
			queries: []string{"foo"},
			ranked: []tagmatcher.RankResult{
				mkResult("foo", cand("foo", 1.0), cand("bar", 0.5)),
			},
			want: []Decision{
				{Query: "foo", Output: "foo", Target: "foo", Sim: 1.0, RunnerUpSim: 0.5, Action: ActionReplace, Reason: ReasonIdentity},
			},
		},

		{
			name:      "negation spaced first token blocks flip",
			queries:   []string{"anti fascism"},
			ranked:    []tagmatcher.RankResult{mkResult("anti fascism", cand("fascism", 0.95))},
			storeTags: storeSet("fascism"),
			want: []Decision{
				{Query: "anti fascism", Output: "anti fascism", Target: "fascism", Sim: 0.95, Action: ActionKeep, Reason: ReasonNegation},
			},
		},
		{
			name:      "negation closed compound blocks flip when remainder is a store tag",
			queries:   []string{"antifascism"},
			ranked:    []tagmatcher.RankResult{mkResult("antifascism", cand("fascism", 0.92))},
			storeTags: storeSet("fascism"),
			want: []Decision{
				{Query: "antifascism", Output: "antifascism", Target: "fascism", Sim: 0.92, Action: ActionKeep, Reason: ReasonNegation},
			},
		},
		{
			name:      "negation remainder absent from store does not block",
			queries:   []string{"antifascist"},
			ranked:    []tagmatcher.RankResult{mkResult("antifascist", cand("fascism", 0.92))},
			storeTags: storeSet("fascism"), // not "fascist"
			want: []Decision{
				{Query: "antifascist", Output: "fascism", Target: "fascism", Sim: 0.92, Action: ActionReplace, Reason: ReasonAutoReplace},
			},
		},
		{
			name:      "negation both sides marked does not block",
			queries:   []string{"anti capitalism"},
			ranked:    []tagmatcher.RankResult{mkResult("anti capitalism", cand("anti fascism", 0.95))},
			storeTags: storeSet("capitalism", "fascism"),
			want: []Decision{
				{Query: "anti capitalism", Output: "anti capitalism", Target: "anti fascism", Sim: 0.95, Action: ActionKeep, Reason: ReasonSharedToken},
			},
		},
		{
			name:      "non prefix standalone blocks",
			queries:   []string{"non violence"},
			ranked:    []tagmatcher.RankResult{mkResult("non violence", cand("violence", 0.95))},
			storeTags: storeSet("violence"),
			want: []Decision{
				{Query: "non violence", Output: "non violence", Target: "violence", Sim: 0.95, Action: ActionKeep, Reason: ReasonNegation},
			},
		},

		{
			name:    "partial token overlap blocks",
			queries: []string{"access control"},
			ranked:  []tagmatcher.RankResult{mkResult("access control", cand("remote control", 0.92))},
			want: []Decision{
				{Query: "access control", Output: "access control", Target: "remote control", Sim: 0.92, Action: ActionKeep, Reason: ReasonSharedToken},
			},
		},
		{
			name:    "equal token sets with different word order block",
			queries: []string{"labor movement"},
			ranked:  []tagmatcher.RankResult{mkResult("labor movement", cand("movement labor", 0.95))},
			want: []Decision{
				{Query: "labor movement", Output: "labor movement", Target: "movement labor", Sim: 0.95, Action: ActionKeep, Reason: ReasonSharedToken},
			},
		},

		{
			name:    "query strict subset of target keeps so parent is born",
			queries: []string{"democracy"},
			ranked:  []tagmatcher.RankResult{mkResult("democracy", cand("direct democracy", 0.89))},
			want: []Decision{
				{Query: "democracy", Output: "democracy", Target: "direct democracy", Sim: 0.89, Action: ActionKeep, Reason: ReasonDirection},
			},
		},
		{
			name:    "target strict subset of query falls to band keep",
			queries: []string{"social revolution"},
			ranked:  []tagmatcher.RankResult{mkResult("social revolution", cand("revolution", 0.85))},
			want: []Decision{
				{Query: "social revolution", Output: "social revolution", Target: "revolution", Sim: 0.85, Action: ActionKeep, Reason: ReasonBand},
			},
		},
		{
			name:    "target strict subset above cutoff replaces",
			queries: []string{"social revolution"},
			ranked:  []tagmatcher.RankResult{mkResult("social revolution", cand("revolution", 0.92))},
			want: []Decision{
				{Query: "social revolution", Output: "revolution", Target: "revolution", Sim: 0.92, Action: ActionReplace, Reason: ReasonAutoReplace},
			},
		},

		{
			name:    "guard-free below cutoff keeps in band",
			queries: []string{"foo"},
			ranked:  []tagmatcher.RankResult{mkResult("foo", cand("foobar", cutoff-0.01))},
			want: []Decision{
				{Query: "foo", Output: "foo", Target: "foobar", Sim: cutoff - 0.01, Action: ActionKeep, Reason: ReasonBand},
			},
		},
		{
			name:    "guard-free at cutoff replaces",
			queries: []string{"foo"},
			ranked:  []tagmatcher.RankResult{mkResult("foo", cand("foobar", cutoff))},
			want: []Decision{
				{Query: "foo", Output: "foobar", Target: "foobar", Sim: cutoff, Action: ActionReplace, Reason: ReasonAutoReplace},
			},
		},
		{
			name:    "guard-free above cutoff replaces",
			queries: []string{"foo"},
			ranked:  []tagmatcher.RankResult{mkResult("foo", cand("foobar", cutoff+0.02))},
			want: []Decision{
				{Query: "foo", Output: "foobar", Target: "foobar", Sim: cutoff + 0.02, Action: ActionReplace, Reason: ReasonAutoReplace},
			},
		},
		{
			name:    "disjoint guard-free below cutoff keeps in band",
			queries: []string{"foo"},
			ranked:  []tagmatcher.RankResult{mkResult("foo", cand("bar", 0.5))},
			want: []Decision{
				{Query: "foo", Output: "foo", Target: "bar", Sim: 0.5, Action: ActionKeep, Reason: ReasonBand},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.queries, tt.ranked, tt.storeTags, cutoff)
			assertDecisions(t, got, tt.want)
		})
	}
}

func assertDecisions(t *testing.T, got, want []Decision) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("decision count = %d, want %d (got=%+v want=%+v)", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i].Query != want[i].Query {
			t.Errorf("decision[%d].Query = %q, want %q", i, got[i].Query, want[i].Query)
		}
		if got[i].Output != want[i].Output {
			t.Errorf("decision[%d].Output = %q, want %q", i, got[i].Output, want[i].Output)
		}
		if got[i].Target != want[i].Target {
			t.Errorf("decision[%d].Target = %q, want %q", i, got[i].Target, want[i].Target)
		}
		if got[i].Sim != want[i].Sim {
			t.Errorf("decision[%d].Sim = %v, want %v", i, got[i].Sim, want[i].Sim)
		}
		if got[i].RunnerUpSim != want[i].RunnerUpSim {
			t.Errorf("decision[%d].RunnerUpSim = %v, want %v", i, got[i].RunnerUpSim, want[i].RunnerUpSim)
		}
		if got[i].Action != want[i].Action {
			t.Errorf("decision[%d].Action = %v, want %v", i, got[i].Action, want[i].Action)
		}
		if got[i].Reason != want[i].Reason {
			t.Errorf("decision[%d].Reason = %q, want %q", i, got[i].Reason, want[i].Reason)
		}
	}
}

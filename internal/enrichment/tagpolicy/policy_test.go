package tagpolicy

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
)

func mkResult(kept string, cands ...tagmatcher.Candidate) tagmatcher.RankResult {
	return tagmatcher.RankResult{KeptName: kept, Candidates: cands}
}

func cand(tag string, sim float64) tagmatcher.Candidate {
	return tagmatcher.Candidate{Tag: tag, Similarity: sim}
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name    string
		queries []string
		ranked  []tagmatcher.RankResult
		want    []Decision
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
			name:    "candidate neighbour is a band subject",
			queries: []string{"foo"},
			ranked: []tagmatcher.RankResult{
				mkResult("foo", cand("foobar", 0.92), cand("bar", 0.5)),
			},
			want: []Decision{
				{Query: "foo", Output: "foo", Target: "foobar", Sim: 0.92, RunnerUpSim: 0.5, Action: ActionKeep, Reason: ReasonBand},
			},
		},
		{
			name:    "high similarity stays a band subject",
			queries: []string{"social revolution"},
			ranked:  []tagmatcher.RankResult{mkResult("social revolution", cand("revolution", 0.99))},
			want: []Decision{
				{Query: "social revolution", Output: "social revolution", Target: "revolution", Sim: 0.99, Action: ActionKeep, Reason: ReasonBand},
			},
		},
		{
			name:    "partial token overlap stays a band subject",
			queries: []string{"access control"},
			ranked:  []tagmatcher.RankResult{mkResult("access control", cand("remote control", 0.92))},
			want: []Decision{
				{Query: "access control", Output: "access control", Target: "remote control", Sim: 0.92, Action: ActionKeep, Reason: ReasonBand},
			},
		},
		{
			name:    "negation pair stays a band subject",
			queries: []string{"anti fascism"},
			ranked:  []tagmatcher.RankResult{mkResult("anti fascism", cand("fascism", 0.95))},
			want: []Decision{
				{Query: "anti fascism", Output: "anti fascism", Target: "fascism", Sim: 0.95, Action: ActionKeep, Reason: ReasonBand},
			},
		},
		{
			name:    "subset direction stays a band subject",
			queries: []string{"democracy"},
			ranked:  []tagmatcher.RankResult{mkResult("democracy", cand("direct democracy", 0.89))},
			want: []Decision{
				{Query: "democracy", Output: "democracy", Target: "direct democracy", Sim: 0.89, Action: ActionKeep, Reason: ReasonBand},
			},
		},
		{
			name:    "disjoint candidate stays a band subject",
			queries: []string{"foo"},
			ranked:  []tagmatcher.RankResult{mkResult("foo", cand("bar", 0.5))},
			want: []Decision{
				{Query: "foo", Output: "foo", Target: "bar", Sim: 0.5, Action: ActionKeep, Reason: ReasonBand},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.queries, tt.ranked)
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

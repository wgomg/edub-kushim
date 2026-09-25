// Package policy is the pinned layer of the tag admission decision:
// fixed, versioned rules over the matcher's ranked results, with no
// observations and nothing fitted. Per query it decides whether a
// candidate tag's name is replaced by an exact identity match, kept as the
// matcher consolidated it, or passed through unchanged, and records why.
// The staged design (pinned rules as the operational layer, fitted
// refinement only where evidence accumulates) is documented in
// docs/tags/consolidation.md.
package policy

import (
	"github.com/wgomg/edub-kushim/internal/matcher"
)

// Version is bumped whenever the policy's gates, guards, thresholds, or band
// semantics change.
const Version = "v1"

// Action is what the policy says to do with a candidate tag's name.
type Action int

// The actions a decision can take: pass the query through unchanged (no
// usable evidence), replace it with an exact identity match, or keep the
// matcher's consolidated name.
const (
	ActionPassthrough Action = iota
	ActionReplace
	ActionKeep
)

// Reason names why a decision took its action, the audit trail of the
// pinned rules.
type Reason string

// The reasons a decision can carry: an exact identity on the top
// candidate, the ambiguous band between candidates, no candidates at all,
// or the ranked slice being shorter than the query list.
const (
	ReasonIdentity     Reason = "identity"
	ReasonBand         Reason = "ambiguous_band"
	ReasonNoCandidates Reason = "no_candidates"
	ReasonShortSlice   Reason = "short_slice"
)

// Decision is the pinned-rule outcome for one query: the query, the name
// the pipeline should use (Output), the candidate it judged and the
// similarities that informed it, and the action and reason.
type Decision struct {
	Query       string
	Output      string
	Target      string
	Sim         float64
	RunnerUpSim float64
	Action      Action
	Reason      Reason
}

// Evaluate decides each query against the ranked result at the matching
// position; queries beyond the ranked slice pass through unchanged.
func Evaluate(queries []string, ranked []matcher.RankResult) []Decision {
	decisions := make([]Decision, len(queries))
	for i, query := range queries {
		decisions[i] = evaluate(query, ranked, i)
	}
	return decisions
}

func evaluate(query string, ranked []matcher.RankResult, i int) Decision {
	d := Decision{Query: query, Output: query, Action: ActionPassthrough, Reason: ReasonShortSlice}
	if i >= len(ranked) {
		return d
	}

	d.Output = ranked[i].KeptName
	if len(ranked[i].Candidates) == 0 {
		d.Reason = ReasonNoCandidates
		return d
	}

	cand := ranked[i].Candidates[0]
	d.Target = cand.Tag
	d.Sim = cand.Similarity
	if len(ranked[i].Candidates) > 1 {
		d.RunnerUpSim = ranked[i].Candidates[1].Similarity
	}

	if cand.Tag == query {
		d.Output = cand.Tag
		d.Action = ActionReplace
		d.Reason = ReasonIdentity
		return d
	}

	d.Output = ranked[i].KeptName
	d.Action = ActionKeep
	d.Reason = ReasonBand
	return d
}

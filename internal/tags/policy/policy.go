// Package policy is the pinned layer of the tag admission decision: fixed, versioned rules over the
// matcher's ranked candidates, with no observations and nothing fitted. Per query it classifies the top
// candidate as an exact identity match or as an ambiguous-band neighbour and records why; it never takes
// a name from a similarity result — the identity plan owns the outputs it sets. The staged design
// (pinned rules as the operational layer, fitted refinement only where evidence accumulates) is
// specified in docs/specs/tags/definition.md §6.5.
package policy

import (
	"github.com/wgomg/edub-kushim/internal/matcher"
)

// Version is bumped whenever the policy's gates, guards, thresholds, or band
// semantics change.
const Version = "v1"

// Action is what the policy says about a candidate tag's name.
type Action int

// The actions a decision can take: pass the query through unchanged (no usable evidence), mark an exact
// identity match, or keep the query beside an ambiguous-band neighbour.
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

// Decision is the pinned-rule outcome for one query: the query, the output name seeded with the query
// and overwritten by the identity plan, the candidate it judged and the similarity that informed it,
// and the action and reason.
type Decision struct {
	Query  string
	Output string
	Target string
	Sim    float64
	Action Action
	Reason Reason
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

	if len(ranked[i].Candidates) == 0 {
		d.Reason = ReasonNoCandidates
		return d
	}

	cand := ranked[i].Candidates[0]
	d.Target = cand.Tag
	d.Sim = cand.Similarity

	if cand.Tag == query {
		d.Action = ActionReplace
		d.Reason = ReasonIdentity
		return d
	}

	d.Action = ActionKeep
	d.Reason = ReasonBand
	return d
}

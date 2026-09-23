package policy

import (
	"github.com/wgomg/edub-kushim/internal/matcher"
)

// Version is bumped whenever the policy's gates, guards, thresholds, or band
// semantics change.
const Version = "v1"

type Action int

const (
	ActionPassthrough Action = iota
	ActionReplace
	ActionKeep
)

type Reason string

const (
	ReasonIdentity     Reason = "identity"
	ReasonBand         Reason = "ambiguous_band"
	ReasonNoCandidates Reason = "no_candidates"
	ReasonShortSlice   Reason = "short_slice"
)

type Decision struct {
	Query       string
	Output      string
	Target      string
	Sim         float64
	RunnerUpSim float64
	Action      Action
	Reason      Reason
}

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

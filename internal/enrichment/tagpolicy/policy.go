package tagpolicy

import (
	"strings"

	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
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
	ReasonAutoReplace  Reason = "auto_replace"
	ReasonNegation     Reason = "negation_guard"
	ReasonSharedToken  Reason = "shared_token_guard"
	ReasonDirection    Reason = "direction_guard"
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

var negationPrefixes = []string{"anti", "contra", "non"}

func Evaluate(queries []string, ranked []tagmatcher.RankResult, storeTags map[string]struct{}, autoReplaceSim float64) []Decision {
	decisions := make([]Decision, len(queries))
	for i, query := range queries {
		decisions[i] = evaluate(query, ranked, i, storeTags, autoReplaceSim)
	}
	return decisions
}

func evaluate(query string, ranked []tagmatcher.RankResult, i int, storeTags map[string]struct{}, autoReplaceSim float64) Decision {
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

	qset := tokenSet(query)
	tset := tokenSet(cand.Tag)

	switch {
	case hasNegationMark(query, storeTags) != hasNegationMark(cand.Tag, storeTags):
		d.Reason = ReasonNegation
	// only partial overlap counts: when the query's tokens are all contained
	// in the candidate's, the direction guard below handles it; the reverse
	// containment is deliberately left to the bands
	case overlaps(qset, tset) && !strictSubset(qset, tset) && !strictSubset(tset, qset):
		d.Reason = ReasonSharedToken
	case strictSubset(qset, tset):
		d.Reason = ReasonDirection
	case cand.Similarity >= autoReplaceSim:
		d.Output = cand.Tag
		d.Action = ActionReplace
		d.Reason = ReasonAutoReplace
	default:
		d.Reason = ReasonBand
	}
	return d
}

func tokenSet(name string) map[string]struct{} {
	tokens := strings.Fields(utils.NormalizeTag(name))
	set := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		set[t] = struct{}{}
	}
	return set
}

func hasNegationMark(name string, storeTags map[string]struct{}) bool {
	for tok := range strings.FieldsSeq(utils.NormalizeTag(name)) {
		for _, prefix := range negationPrefixes {
			if tok == prefix {
				return true
			}
			remainder := strings.TrimPrefix(tok, prefix)
			if remainder == tok || remainder == "" {
				continue
			}
			if _, ok := storeTags[remainder]; ok {
				return true
			}
		}
	}
	return false
}

func overlaps(a, b map[string]struct{}) bool {
	for tok := range a {
		if _, ok := b[tok]; ok {
			return true
		}
	}
	return false
}

func strictSubset(a, b map[string]struct{}) bool {
	if len(a) == 0 || len(a) >= len(b) {
		return false
	}
	for tok := range a {
		if _, ok := b[tok]; !ok {
			return false
		}
	}
	return true
}

package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/types"
)

// TagPairEvidence is one pair the adjudicator decides: the two terms. The terms are the pair's only
// evidence — similarity selected the pair, it is never an input to the verdict.
type TagPairEvidence struct {
	Query  string
	Target string
}

// TagVerdictResult is the adjudicator's answer for one pair: the verdict
// and, when it is same, the direction.
type TagVerdictResult struct {
	Index     int
	Verdict   types.TagVerdict
	Direction string
}

// The directions a same verdict can carry: which term's standard sense
// is exactly the dimension; a tie leaves the choice to the deterministic
// tie-break.
const (
	TagDirectionQuery  = "query"
	TagDirectionTarget = "target"
	TagDirectionTie    = "tie"
)

// AdjudicationSystemMessage is the system prompt of the pair
// adjudication call.
const AdjudicationSystemMessage = "You are a tag-taxonomy classifier. Decide whether pairs of tag terms denote one value."

// BuildAdjudicationPrompt renders the same-intent pairs for the judging body: the faithful-replacement
// framing and the two terms, with no similarity context.
func BuildAdjudicationPrompt(pairs []TagPairEvidence) string {
	var sb strings.Builder
	sb.WriteString("For each pair below, decide whether the two terms denote one value: faithful replacement holds when either term can stand for the other without loss or change of meaning.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"pairs\":[{\"index\":0,\"verdict\":\"same\",\"direction\":\"query\"},...]}, one entry per pair, no explanations.\n")
	sb.WriteString("Verdict must be exactly one of:\n")
	sb.WriteString("- \"same\": the terms denote one value (morphographic variants, spelling variants, or intent-equivalent derivations)\n")
	sb.WriteString("- \"variant\": closely related but distinct concepts (one is a narrower or broader form of the other)\n")
	sb.WriteString("- \"related\": related but different concepts\n")
	sb.WriteString("Use \"same\" exactly when faithful replacement holds: spelling and morphographic variants of one term (labour/labor, modelling/modeling) and intent-equivalent derivations are \"same\", not \"variant\". Reserve \"variant\" and \"related\" for genuinely distinct concepts — when the two meanings truly differ, answer \"variant\" (narrower or broader) or \"related\".\n")
	sb.WriteString("When the verdict is \"same\", also answer which term is the precise one: \"direction\":\"query\" when the query term's standard sense is exactly the dimension, \"direction\":\"target\" when the target term's is, \"direction\":\"tie\" when neither is more precise. A polysemous term loses to the precise one.\n\n")
	for i, p := range pairs {
		fmt.Fprintf(&sb, "Pair %d:\n", i)
		fmt.Fprintf(&sb, "  query: %q\n", p.Query)
		fmt.Fprintf(&sb, "  target: %q\n", p.Target)
		sb.WriteString("\n")
	}
	return sb.String()
}

// ParseAdjudicationResponse parses n pair verdicts. A pair with no parseable verdict — a missing entry,
// an unrecognized verdict string, or an out-of-range index — counts as missing, so an unanswered pair
// is never invented. An entry with a valid verdict but an unrecognized direction stays present with an
// empty direction rather than counting as missing. A bare top-level array is accepted as the envelope's
// value, and an entry without an index is placed by its position in the array. A truncated reply returns
// *IncompleteReplyError alongside the populated results.
func ParseAdjudicationResponse(content string, n int) ([]TagVerdictResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "pairs")
	var parsed struct {
		Pairs []struct {
			Index     *int   `json:"index"`
			Verdict   string `json:"verdict"`
			Direction string `json:"direction"`
		} `json:"pairs"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse adjudication response: %w", err)
	}
	results := make([]TagVerdictResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for pos, p := range parsed.Pairs {
		idx := pos
		if p.Index != nil {
			idx = *p.Index
		}
		if idx < 0 || idx >= n {
			continue
		}
		v, err := types.ParseTagVerdict(p.Verdict)
		if err != nil {
			continue
		}
		results[idx].Verdict = v
		present[idx] = true
		switch p.Direction {
		case TagDirectionQuery, TagDirectionTarget, TagDirectionTie:
			results[idx].Direction = p.Direction
		}
	}
	var missing []int
	for i, ok := range present {
		if !ok {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		return results, &IncompleteReplyError{Missing: missing}
	}
	return results, nil
}

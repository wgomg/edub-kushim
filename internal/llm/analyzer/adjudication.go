package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/types"
)

// TagPairEvidence is one pair the adjudicator decides: the two terms,
// their similarity, the query's runner-up similarity, and the target's
// nearest other store tag.
type TagPairEvidence struct {
	Query             string
	Target            string
	Sim               float64
	RunnerUpSim       float64
	TargetRunnerUpTag string
	TargetRunnerUpSim float64
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

// BuildAdjudicationPrompt renders the same-intent pairs for the judging
// body: faithful-replacement framing, per-pair similarities, and
// runner-up context.
func BuildAdjudicationPrompt(pairs []TagPairEvidence) string {
	var sb strings.Builder
	sb.WriteString("For each pair below, decide whether the two terms denote one value: faithful replacement holds when either term can stand for the other without loss or change of meaning.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"pairs\":[{\"index\":0,\"verdict\":\"same\",\"direction\":\"query\"},...]}, one entry per pair, no explanations.\n")
	sb.WriteString("Verdict must be exactly one of:\n")
	sb.WriteString("- \"same\": the terms denote one value (morphographic variants, spelling variants, or intent-equivalent derivations)\n")
	sb.WriteString("- \"variant\": closely related but distinct concepts (one is a narrower or broader form of the other)\n")
	sb.WriteString("- \"related\": related but different concepts\n")
	sb.WriteString("Use \"same\" only when faithful replacement holds; when in doubt, answer \"variant\" or \"related\".\n")
	sb.WriteString("When the verdict is \"same\", also answer which term is the precise one: \"direction\":\"query\" when the query term's standard sense is exactly the dimension, \"direction\":\"target\" when the target term's is, \"direction\":\"tie\" when neither is more precise. A polysemous term loses to the precise one.\n\n")
	for i, p := range pairs {
		fmt.Fprintf(&sb, "Pair %d:\n", i)
		fmt.Fprintf(&sb, "  query: %q\n", p.Query)
		fmt.Fprintf(&sb, "  target: %q\n", p.Target)
		fmt.Fprintf(&sb, "  query-target embedding similarity: %.3f\n", p.Sim)
		if p.RunnerUpSim > 0 {
			fmt.Fprintf(&sb, "  query's runner-up similarity: %.3f\n", p.RunnerUpSim)
		}
		if p.TargetRunnerUpTag != "" {
			fmt.Fprintf(&sb, "  target's nearest other store tag: %q (similarity %.3f)\n", p.TargetRunnerUpTag, p.TargetRunnerUpSim)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ParseAdjudicationResponse parses n pair verdicts. Unknown or
// out-of-range indices and unrecognized verdicts leave that pair
// unanswered rather than inventing one.
func ParseAdjudicationResponse(content string, n int) ([]TagVerdictResult, error) {
	content = cleanCodeBlock(strings.TrimSpace(content))
	var parsed struct {
		Pairs []struct {
			Index     int    `json:"index"`
			Verdict   string `json:"verdict"`
			Direction string `json:"direction"`
		} `json:"pairs"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse adjudication response: %w", err)
	}
	results := make([]TagVerdictResult, n)
	for i := range results {
		results[i].Index = i
	}
	for _, p := range parsed.Pairs {
		if p.Index < 0 || p.Index >= n {
			continue
		}
		v, err := types.ParseTagVerdict(p.Verdict)
		if err != nil {
			continue
		}
		results[p.Index].Verdict = v
		switch p.Direction {
		case TagDirectionQuery, TagDirectionTarget, TagDirectionTie:
			results[p.Index].Direction = p.Direction
		}
	}
	return results, nil
}

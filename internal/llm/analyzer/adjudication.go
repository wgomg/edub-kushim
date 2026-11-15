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

// TagVerdictResult is the adjudicator's answer for one pair: the typed relation between the terms and,
// when it is same_concept, the canonical direction (types.TagDirection* values) naming the label whose
// standard sense is exactly the dimension.
type TagVerdictResult struct {
	Index     int
	Relation  types.TagRelation
	Canonical string
}

// AdjudicationSystemMessage is the system prompt of the pair
// adjudication call.
const AdjudicationSystemMessage = "You are a tag-pair judge. Decide how pairs of tag terms relate under the definition in the user message."

// BuildAdjudicationPrompt renders the same-intent pairs for the judging body: the definition's same-extension
// rule and the two terms, with no similarity context. The answer is the typed relation between the terms;
// only same_concept carries a canonical direction, and only that direction lets the pair fold.
func BuildAdjudicationPrompt(pairs []TagPairEvidence) string {
	var sb strings.Builder
	sb.WriteString(TagDefinition + "\n")
	sb.WriteString("For each pair below, decide how the two terms relate: under that definition, labels with the same retrieval meaning are one value with one canonical label, and faithful replacement holds when either term can stand for the other without loss or change of meaning.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"pairs\":[{\"index\":0,\"relation\":\"same_concept\",\"canonical\":\"query\"},...]}, one entry per pair, no explanations.\n")
	sb.WriteString("Relation must be exactly one of:\n")
	sb.WriteString("- \"same_concept\": faithful replacement holds — spelling and morphographic variants of one term (labour/labor, modelling/modeling) and intent-equivalent derivations\n")
	sb.WriteString("- \"near_synonym\": closely related but not substitutable — one is narrower or broader than the other, or the terms are near-synonyms with different usage\n")
	sb.WriteString("- \"distinct\": different concepts — including opposites and same-category siblings\n")
	sb.WriteString("- \"uncertain\": you cannot decide confidently from the two terms alone\n")
	sb.WriteString("Use \"same_concept\" exactly when faithful replacement holds; when the two meanings truly differ, answer \"near_synonym\" or \"distinct\", never \"same_concept\".\n")
	sb.WriteString("When the relation is \"same_concept\", also answer which term is the precise one: \"canonical\":\"query\" when the query term's standard sense is exactly the dimension, \"canonical\":\"target\" when the target term's is, \"canonical\":\"tie\" when neither is more precise. A polysemous term loses to the precise one. Omit \"canonical\" for every other relation.\n\n")
	for i, p := range pairs {
		fmt.Fprintf(&sb, "Pair %d:\n", i)
		fmt.Fprintf(&sb, "  query: %q\n", p.Query)
		fmt.Fprintf(&sb, "  target: %q\n", p.Target)
		sb.WriteString("\n")
	}
	return sb.String()
}

// ParseAdjudicationResponse parses n pair answers. A pair with no parseable relation — a missing entry,
// an unrecognized relation string, or an out-of-range index — counts as missing, so an unanswered pair is
// never invented. An entry with a valid relation but an unrecognized canonical stays present with an empty
// canonical rather than counting as missing, and a well-formed canonical arriving on a non-same_concept
// relation is kept but inert: the fold guards read it only beside same_concept. A bare top-level array is
// accepted as the envelope's value, and an entry without an index is placed by its position in the array.
// A truncated reply returns *IncompleteReplyError alongside the populated results.
func ParseAdjudicationResponse(content string, n int) ([]TagVerdictResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "pairs")
	var parsed struct {
		Pairs []struct {
			Index     *int   `json:"index"`
			Relation  string `json:"relation"`
			Canonical string `json:"canonical"`
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
		relation, err := types.ParseTagRelation(p.Relation)
		if err != nil {
			continue
		}
		results[idx].Relation = relation
		present[idx] = true
		switch p.Canonical {
		case types.TagDirectionQuery, types.TagDirectionTarget, types.TagDirectionTie:
			results[idx].Canonical = p.Canonical
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

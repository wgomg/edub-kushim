package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagRelationCandidate is one fold proposal the relation gate decides: the index the batch response refers
// to, the candidate term, the store anchor the deterministic machinery proposed folding it onto, the
// proposal class that produced the pair, and the matcher similarity when the anchor was ranked. Similarity
// is a pointer so an omitted score is distinguishable from a real zero.
type TagRelationCandidate struct {
	Index      int
	Term       string
	Anchor     string
	Source     string
	Similarity *float64
}

// TagRelationResult is one candidate's relation verdict: true when the pair is semantically related enough
// that a canonical-form judgment is meaningful.
type TagRelationResult struct {
	Index   int
	Related bool
}

// TagRelationSystemMessage is the system prompt of the relation gate: the mechanical pairing is not
// evidence of relatedness, and the similarity estimate, when present, is weak evidence rather than a
// verdict.
const TagRelationSystemMessage = "You are a tag-relation gate. The tag machinery proposed folding a candidate term onto a store anchor for a mechanical reason. Decide only whether the two labels are semantically related enough that a canonical-form decision is meaningful. Answer true when the labels could plausibly be variants, morphological neighbors, or labels for the same or a nearby concept. Answer false only when they are clearly unrelated. Sharing letters, stemming, or sounding alike is not evidence of relatedness. Use the similarity estimate as weak evidence, not as a verdict: a very low estimate suggests unrelatedness, but a high estimate does not make two labels variants. Do not decide whether the fold itself is correct."

// BuildTagRelationPrompt renders the gate's pairs with their proposal class and, where one exists, the
// matcher similarity, under the one-answer-per-pair JSON contract.
func BuildTagRelationPrompt(candidates []TagRelationCandidate) string {
	var sb strings.Builder
	sb.WriteString("For each pair, the proposal source names why the machinery paired the labels; it is not evidence that the fold is correct. An estimated semantic similarity (0-1, rough) is provided when one was available.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"pairs\":[{\"index\":0,\"related\":true},...]}, one entry per pair, no explanations.\n\n")
	for _, c := range candidates {
		fmt.Fprintf(&sb, "Pair %d: candidate=%q anchor=%q source=%q", c.Index, c.Term, c.Anchor, c.Source)
		if c.Similarity != nil {
			fmt.Fprintf(&sb, " estimated_similarity=%.2f", *c.Similarity)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ParseTagRelationResponse parses n relation verdicts. A pair with no parseable answer — a missing entry, a
// non-boolean related field, or an out-of-range index — counts as missing, so an unanswered pair is never
// read as unrelated. A bare top-level array is accepted as the envelope's value, and an entry without an
// index is placed by its position in the array. A truncated reply returns *IncompleteReplyError alongside
// the populated results, so the caller can gate the answered candidates and leave only the missing ones
// pending.
func ParseTagRelationResponse(content string, n int) ([]TagRelationResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "pairs")
	var parsed struct {
		Pairs []struct {
			Index   *int  `json:"index"`
			Related *bool `json:"related"`
		} `json:"pairs"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag relation response: %w", err)
	}
	results := make([]TagRelationResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for pos, p := range parsed.Pairs {
		idx := pos
		if p.Index != nil {
			idx = *p.Index
		}
		if idx < 0 || idx >= n || p.Related == nil {
			continue
		}
		results[idx] = TagRelationResult{Index: idx, Related: *p.Related}
		present[idx] = true
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

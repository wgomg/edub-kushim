package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagConflictVersion pins the conflict arbiter's criteria and prompt. Nothing stores it: an arbitration
// answer is not cached, and the row it decides records the split criteria version and the arbitration
// reason. The arbitrate log line carries this stamp so a prompt change stays traceable, and a prompt change
// that must re-decide stored rows rides the TagSplitVersion bump that re-rolls them.
const TagConflictVersion = "v1"

// TagConflictSystemMessage is the system prompt of the conflict arbiter.
const TagConflictSystemMessage = "You are a tag-composition arbiter. Decide how a compound phrase whose evidence disagrees disposes."

// TagConflictPart is one contested constituent of the compound: its applied name and the parts pass's
// standalone verdict over it.
type TagConflictPart struct {
	Term        string
	Dimensional bool
}

// TagConflictCandidate is one compound the parts and compound passes disagree about: its index in the
// batch, the phrase, the compound pass's own reading, and the constituents whose standalone verdicts
// conflict with that reading.
type TagConflictCandidate struct {
	Index    int
	Compound string
	Class    string
	Parts    []TagConflictPart
}

// TagConflictAction is the arbiter's resolution of one disagreement.
type TagConflictAction string

// the actions an arbitration can return: split the compound into its constituents, keep it whole with the
// qualifying constituents applied alongside, or defer it to review when the evidence still does not settle it
const (
	TagConflictSplit TagConflictAction = "split"
	TagConflictKeep  TagConflictAction = "keep"
	TagConflictDefer TagConflictAction = "defer"
)

// Valid reports whether the action is one of the three resolutions.
func (a TagConflictAction) Valid() bool {
	switch a {
	case TagConflictSplit, TagConflictKeep, TagConflictDefer:
		return true
	}
	return false
}

// TagConflictResult is one compound's arbitration answer.
type TagConflictResult struct {
	Index  int
	Action TagConflictAction
}

// BuildTagConflictPrompt renders the arbitration question over the disagreeing compounds: each item shows
// the phrase, the compound pass's reading, and each constituent with the standalone judgment that conflicts
// with it. Both disagreement shapes reach this call — a pre-coordination whose constituents were withheld,
// and a no-dimension reading over constituents that were granted — so the item itself carries the shape
// instead of the prompt asserting one. Deferring stays available so an unsettled case still reaches review.
func BuildTagConflictPrompt(candidates []TagConflictCandidate) string {
	var sb strings.Builder
	sb.WriteString(TagDefinition + "\n")
	sb.WriteString("For each item below, the phrase classification disagrees with the standalone judgments of its constituents. Decide how the phrase disposes.\n")
	sb.WriteString("- \"split\": the phrase adds nothing to its constituents; the constituents carry the retrieval meaning, so the phrase disposes as those tags.\n")
	sb.WriteString("- \"keep\": the phrase itself is a lexical or named unit that names one tag a searcher would filter by; keep it whole, applying the qualifying constituents alongside.\n")
	sb.WriteString("- \"defer\": the evidence still does not settle it; leave the phrase for review. Defer only when neither reading is supported.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"items\":[{\"index\":0,\"action\":\"keep\"},...]}, one entry per item, no explanations.\n\n")
	for _, c := range candidates {
		fmt.Fprintf(&sb, "Item %d: phrase=%q classification=%s constituents=%s\n", c.Index, c.Compound, c.Class, formatConflictParts(c.Parts))
	}
	return sb.String()
}

// formatConflictParts renders the contested constituents with their standalone verdicts.
func formatConflictParts(parts []TagConflictPart) string {
	rendered := make([]string, 0, len(parts))
	for _, p := range parts {
		verdict := "a tag on its own"
		if !p.Dimensional {
			verdict = "not a tag on its own"
		}
		rendered = append(rendered, fmt.Sprintf("%q (%s)", p.Term, verdict))
	}
	return strings.Join(rendered, ", ")
}

// ParseTagConflictResponse parses n arbitration answers; an entry with a missing or unrecognized action
// counts as missing so an unanswered compound is never invented. A bare top-level array is accepted as
// the envelope's value, and an entry without an index is placed by its position in the array. A truncated
// reply returns *IncompleteReplyError alongside the populated results, so the caller can arbitrate those
// and defer only the missing indices.
func ParseTagConflictResponse(content string, n int) ([]TagConflictResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "items")
	var parsed struct {
		Items []struct {
			Index  *int   `json:"index"`
			Action string `json:"action"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag conflict response: %w", err)
	}
	results := make([]TagConflictResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for pos, item := range parsed.Items {
		idx := pos
		if item.Index != nil {
			idx = *item.Index
		}
		if idx < 0 || idx >= n {
			continue
		}
		action := TagConflictAction(strings.ToLower(strings.TrimSpace(item.Action)))
		if !action.Valid() {
			continue
		}
		results[idx].Action = action
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

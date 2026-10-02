package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagSplitVerdict is the split gate's answer for one tag.
type TagSplitVerdict string

// The two split verdicts: keep the tag whole, or split it into its parts.
const (
	TagSplitKeep  TagSplitVerdict = "keep"
	TagSplitSplit TagSplitVerdict = "split"
)

// TagSplitResult is one tag's split decision: the verdict and, when it is
// split, the components.
type TagSplitResult struct {
	Verdict    TagSplitVerdict
	Components []string
}

// TagSplitVersion pins the split gate's criteria text; the admission
// driver's split log line and every composition row carry it. v1 names
// the generic-head split class, asks one reading per family, keeps named
// units whole, and states that store existence is not the judge's
// criterion. The verdict and response shape are unchanged, and split
// verdicts are in-process only, so no cached verdict is invalidated.
const TagSplitVersion = "v1"

// TagSplitSystemMessage is the system prompt of the split gate.
const TagSplitSystemMessage = "You are a tag-taxonomy classifier. Decide whether a tag's words each name a subject a searcher would filter by on its own, and the tag is just their combination."

// BuildTagSplitPrompt renders the split gate's prompt: for each tag, its
// content words are the parts; the parts are subjects that work as tags on
// their own and the phrase is just their combination (split, including a
// modifier before a generic head), or the phrase names one established
// subject the parts cannot compose (keep). Compounds sharing a head and
// shape take one reading, store existence never decides, and unsettled
// readings keep.
func BuildTagSplitPrompt(tags []string) string {
	var sb strings.Builder
	sb.WriteString("For each tag below, take its content words as its parts; articles, conjunctions, and prepositions are not parts.\n")
	sb.WriteString("Decide whether each part names a subject a searcher would filter by on its own, and whether the phrase is just their combination.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"splits\":[{\"index\":0,\"verdict\":\"split\",\"components\":[\"first part\",\"second part\"]},...]}, one entry per tag, no explanations.\n")
	sb.WriteString("Verdict must be exactly one of:\n")
	sb.WriteString("- \"split\": every part names a subject that works as a tag on its own and the phrase is just their combination ('russian history' → 'russia', 'history'; 'philosophy of mind' → 'philosophy', 'mind'); a modifier before a generic head splits too, even when the phrase is familiar ('labor movement' → 'labor', 'movement'; 'bike repair' → 'bike', 'repair'); list the parts in \"components\"\n")
	sb.WriteString("- \"keep\": the phrase names one established subject its parts cannot compose ('cold war', 'machine learning', 'urban planning')\n")
	sb.WriteString("Judge by class, one reading per family: compounds sharing a head and shape take the same verdict, not one reading per instance ('political education', 'political philosophy', and 'political science' are one family).\n")
	sb.WriteString("Whether the parts already exist in the store is not your criterion; judge only whether they compose the phrase.\n")
	sb.WriteString("When the reading is unsettled, answer \"keep\"; never force a split.\n\n")
	for i, tag := range tags {
		fmt.Fprintf(&sb, "Tag %d: %q\n", i, tag)
	}
	return sb.String()
}

// ParseTagSplitResponse parses n split answers; a split with fewer than
// two components is discarded, and unanswered entries stay zero-valued.
// A bare top-level array is accepted as the envelope's value, and an
// entry without an index is placed by its position in the array.
func ParseTagSplitResponse(content string, n int) ([]TagSplitResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "splits")
	var parsed struct {
		Splits []struct {
			Index      *int     `json:"index"`
			Verdict    string   `json:"verdict"`
			Components []string `json:"components"`
		} `json:"splits"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag split response: %w", err)
	}
	results := make([]TagSplitResult, n)
	for pos, s := range parsed.Splits {
		idx := pos
		if s.Index != nil {
			idx = *s.Index
		}
		if idx < 0 || idx >= n {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(s.Verdict)) {
		case string(TagSplitKeep):
			results[idx].Verdict = TagSplitKeep
		case string(TagSplitSplit):
			components := make([]string, 0, len(s.Components))
			for _, c := range s.Components {
				if c = strings.TrimSpace(c); c != "" {
					components = append(components, c)
				}
			}
			if len(components) < 2 {
				continue
			}
			results[idx].Verdict = TagSplitSplit
			results[idx].Components = components
		}
	}
	return results, nil
}

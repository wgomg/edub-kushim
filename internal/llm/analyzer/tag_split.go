package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagSplitVerdict is the split gate's answer for one tag.
type TagSplitVerdict string

// The two split verdicts: keep the tag whole, or split it into its
// distinct concepts.
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

// TagSplitSystemMessage is the system prompt of the split gate.
const TagSplitSystemMessage = "You are a tag-taxonomy classifier. Decide whether a tag denotes one concept or several distinct concepts."

// BuildTagSplitPrompt renders the split gate's prompt: keep versus
// genuinely distinct concepts, one entry per tag.
func BuildTagSplitPrompt(tags []string) string {
	var sb strings.Builder
	sb.WriteString("For each tag below, decide whether it denotes one single concept or several distinct concepts that must become separate tags.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"splits\":[{\"index\":0,\"verdict\":\"split\",\"components\":[\"first concept\",\"second concept\"]},...]}, one entry per tag, no explanations.\n")
	sb.WriteString("Verdict must be exactly one of:\n")
	sb.WriteString("- \"keep\": the tag denotes one single concept, including established proper names and set phrases\n")
	sb.WriteString("- \"split\": the tag joins distinct concepts and must become separate tags; list the distinct concepts in \"components\"\n")
	sb.WriteString("Use \"split\" only when the joined concepts are genuinely distinct and each deserves its own tag.\n\n")
	for i, tag := range tags {
		fmt.Fprintf(&sb, "Tag %d: %q\n", i, tag)
	}
	return sb.String()
}

// ParseTagSplitResponse parses n split answers; a split with fewer than
// two components is discarded, and unanswered entries stay zero-valued.
func ParseTagSplitResponse(content string, n int) ([]TagSplitResult, error) {
	content = cleanCodeBlock(strings.TrimSpace(content))
	var parsed struct {
		Splits []struct {
			Index      int      `json:"index"`
			Verdict    string   `json:"verdict"`
			Components []string `json:"components"`
		} `json:"splits"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag split response: %w", err)
	}
	results := make([]TagSplitResult, n)
	for _, s := range parsed.Splits {
		if s.Index < 0 || s.Index >= n {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(s.Verdict)) {
		case string(TagSplitKeep):
			results[s.Index].Verdict = TagSplitKeep
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
			results[s.Index].Verdict = TagSplitSplit
			results[s.Index].Components = components
		}
	}
	return results, nil
}

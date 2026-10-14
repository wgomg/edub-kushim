package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagAuditVersion pins the completeness audit's applicability criteria and prompt. A criteria or prompt
// change bumps it so cached verdicts never apply across the change; the cache is keyed by the document's
// content hash and this version, so re-auditing unchanged text reads its verdict instead of re-judging.
const TagAuditVersion = "v1"

// TagAuditSystemMessage fixes the audit judge's criterion — a candidate is a subject the document is about —
// while the per-document question lives in the user prompt.
const TagAuditSystemMessage = "You are a tag-completeness judge. Decide whether each candidate dimension is a subject the document is about."

// TagAuditCandidate is one dimension the audit flagged as a possible coverage gap: its index in the
// document's flag list and the store value.
type TagAuditCandidate struct {
	Index int
	Term  string
}

// TagAuditResult is one flagged dimension's applicability answer and the judge's abstention. The verdict is
// evidence, not a decision — the audit's disposition reads it.
type TagAuditResult struct {
	Index   int
	Applies bool
	Defer   bool
}

// TagAuditCriteria is the persisted wire shape of one dimension's answer. Pointer fields distinguish an
// omitted key from an explicit false; defer is optional, so an omitted defer reads as false.
type TagAuditCriteria struct {
	Applies *bool `json:"applies"`
	Defer   *bool `json:"defer"`
}

// Criteria snapshots the answer for the cache, keeping the pointer fields so an omitted key stays
// distinguishable from an explicit false.
func (r TagAuditResult) Criteria() TagAuditCriteria {
	return TagAuditCriteria{Applies: &r.Applies, Defer: &r.Defer}
}

// Complete reports whether the applies answer was specified.
func (c TagAuditCriteria) Complete() bool {
	return c.Applies != nil
}

// Result reads the persisted criteria back into an answer; the second return is false when the applies
// answer is missing, so a truncated entry never reads as a not-applies rejection.
func (c TagAuditCriteria) Result() (TagAuditResult, bool) {
	if !c.Complete() {
		return TagAuditResult{}, false
	}
	return TagAuditResult{Applies: *c.Applies, Defer: c.Defer != nil && *c.Defer}, true
}

// BuildTagAuditPrompt renders the applicability question over the document and the flagged dimensions: a
// dimension applies when a searcher filtering by it would count the document a correct hit — about-ness, not
// mention. The judge abstains when about-ness is genuinely unclear, deferring to review.
func BuildTagAuditPrompt(text string, candidates []TagAuditCandidate) string {
	var sb strings.Builder
	sb.WriteString("For each candidate dimension below, answer whether the document is about that dimension: a searcher filtering by it would count this document a correct hit. Judge about-ness, not mention — a passing reference is not about-ness.\n")
	sb.WriteString("Also answer defer: set it true when you cannot decide whether the document is about the dimension. Uncertainty defers to review; it never forces a label.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"tags\":[{\"index\":0,\"applies\":true,\"defer\":false},...]}, one entry per candidate, no explanations.\n\n")
	sb.WriteString("Document:\n")
	sb.WriteString(text)
	sb.WriteString("\n\nCandidate dimensions:\n")
	for _, c := range candidates {
		fmt.Fprintf(&sb, "Dimension %d: %q\n", c.Index, c.Term)
	}
	return sb.String()
}

// ParseTagAuditResponse parses n applicability answers; an entry lacking the applies answer is treated as
// missing so a truncated entry is never read as a not-applies rejection. A bare top-level array is accepted
// as the envelope's value, and an entry without an index is placed by its position. A truncated reply
// returns *IncompleteReplyError alongside the populated results.
func ParseTagAuditResponse(content string, n int) ([]TagAuditResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "tags")
	var parsed struct {
		Tags []struct {
			Index *int `json:"index"`
			TagAuditCriteria
		} `json:"tags"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag audit response: %w", err)
	}
	results := make([]TagAuditResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for pos, v := range parsed.Tags {
		idx := pos
		if v.Index != nil {
			idx = *v.Index
		}
		if idx < 0 || idx >= n {
			continue
		}
		r, ok := v.TagAuditCriteria.Result()
		if !ok {
			continue
		}
		r.Index = idx
		results[idx] = r
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

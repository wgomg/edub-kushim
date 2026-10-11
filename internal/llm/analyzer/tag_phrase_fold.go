package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagPhraseFoldVersion pins the phrase-context fold pass's criteria; a criteria or prompt change bumps it.
const TagPhraseFoldVersion = "v1"

// TagPhraseFoldSystemMessage is the system prompt of the phrase-context fold pass.
const TagPhraseFoldSystemMessage = "You are a tag-fold judge. Decide whether a word, in the context of one phrase, denotes the same thing as a candidate anchor value."

// TagPhraseFoldCandidate is one phrase-context fold question: the phrase the part appears in, the part, and
// the anchor store value it might denote. The seam asks it when a multi-base part's primary-sense
// base is not live while another attested base is.
type TagPhraseFoldCandidate struct {
	Index  int
	Phrase string
	Part   string
	Anchor string
}

// TagPhraseFoldResult is one candidate's answer: whether the part denotes the anchor in the phrase, and the
// judge's abstention.
type TagPhraseFoldResult struct {
	Index int
	Fold  bool
	Defer bool
}

// TagPhraseFoldCriteria is the persisted wire shape of one candidate's answer. Pointer fields distinguish an
// omitted key from an explicit false; defer is optional, so an omitted defer reads as false.
type TagPhraseFoldCriteria struct {
	Fold  *bool `json:"fold"`
	Defer *bool `json:"defer"`
}

// Criteria returns the persisted wire shape of the answer.
func (r TagPhraseFoldResult) Criteria() TagPhraseFoldCriteria {
	return TagPhraseFoldCriteria{Fold: &r.Fold, Defer: &r.Defer}
}

// Complete reports whether the fold answer was specified.
func (c TagPhraseFoldCriteria) Complete() bool {
	return c.Fold != nil
}

// Result reads the persisted criteria back into an answer; the second return is false when the fold answer
// is missing.
func (c TagPhraseFoldCriteria) Result() (TagPhraseFoldResult, bool) {
	if !c.Complete() {
		return TagPhraseFoldResult{}, false
	}
	return TagPhraseFoldResult{Fold: *c.Fold, Defer: c.Defer != nil && *c.Defer}, true
}

// String renders the answer for the audit log, treating an omitted answer as false.
func (c TagPhraseFoldCriteria) String() string {
	return fmt.Sprintf("fold=%t defer=%t", c.Fold != nil && *c.Fold, c.Defer != nil && *c.Defer)
}

// BuildTagPhraseFoldPrompt renders the phrase-context fold question over the candidates: for each, whether
// the part, as it is used in the phrase, denotes the same thing as the anchor value.
func BuildTagPhraseFoldPrompt(candidates []TagPhraseFoldCandidate) string {
	var sb strings.Builder
	sb.WriteString("For each item below, decide whether the word denotes the same thing as the anchor value as it is used in the phrase.\n")
	sb.WriteString("Answer fold true only when the word, in this phrase, means the anchor value (a relational adjective that names the anchor entity, e.g. 'german' in 'german literature' denoting germany).\n")
	sb.WriteString("Answer fold false when the word carries its own, different meaning in the phrase (e.g. 'civil' in 'civil war' does not denote a state).\n")
	sb.WriteString("Set defer true when the phrase context does not settle it; never guess.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"items\":[{\"index\":0,\"fold\":true,\"defer\":false},...]}, one entry per item, no explanations.\n\n")
	for i, c := range candidates {
		fmt.Fprintf(&sb, "Item %d: phrase=%q part=%q anchor=%q\n", i, c.Phrase, c.Part, c.Anchor)
	}
	return sb.String()
}

// ParseTagPhraseFoldResponse parses n answers; an entry lacking a fold answer is treated as missing so a
// truncated reply is never read as a rejection. A bare top-level array is accepted as the envelope's value,
// and an entry without an index is placed by its position. A truncated reply returns *IncompleteReplyError
// alongside the populated results.
func ParseTagPhraseFoldResponse(content string, n int) ([]TagPhraseFoldResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "items")
	var parsed struct {
		Items []struct {
			Index *int `json:"index"`
			TagPhraseFoldCriteria
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag phrase fold response: %w", err)
	}
	results := make([]TagPhraseFoldResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for pos, v := range parsed.Items {
		idx := pos
		if v.Index != nil {
			idx = *v.Index
		}
		if idx < 0 || idx >= n {
			continue
		}
		r, ok := v.TagPhraseFoldCriteria.Result()
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

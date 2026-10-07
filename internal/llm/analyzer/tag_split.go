package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagPartDimensionVersion pins the parts pass's criteria: the context-free dimensional question asked of a value on its own, and the policy version
// of its cached verdict rows. A criteria or prompt change bumps it so an older generation's verdicts never apply.
const TagPartDimensionVersion = "v1"

// TagPartSystemMessage is the system prompt of the parts pass.
const TagPartSystemMessage = "You are a tag-dimension classifier. Decide whether each value, on its own, is a subject a searcher would filter by."

// TagPartResult is one value's answer from the parts pass: whether the value is a dimensional value on its
// own, and the judge's abstention.
type TagPartResult struct {
	Index       int
	Dimensional bool
	Defer       bool
}

// TagPartCriteria is the persisted wire shape of one value's answer. Pointer fields distinguish an omitted
// key from an explicit false; defer is optional, so an omitted defer reads as false.
type TagPartCriteria struct {
	Dimensional *bool `json:"dimensional"`
	Defer       *bool `json:"defer"`
}

// Criteria returns the persisted wire shape of the answers.
func (r TagPartResult) Criteria() TagPartCriteria {
	return TagPartCriteria{Dimensional: &r.Dimensional, Defer: &r.Defer}
}

// Complete reports whether the dimensional answer was specified.
func (c TagPartCriteria) Complete() bool {
	return c.Dimensional != nil
}

// Result reads the persisted criteria back into a part answer; the second return is false when the dimensional answer is missing.
func (c TagPartCriteria) Result() (TagPartResult, bool) {
	if !c.Complete() {
		return TagPartResult{}, false
	}
	return TagPartResult{Dimensional: *c.Dimensional, Defer: c.Defer != nil && *c.Defer}, true
}

// String renders the answers for the audit log, treating an omitted answer as false.
func (c TagPartCriteria) String() string {
	return fmt.Sprintf("dimensional=%t defer=%t",
		c.Dimensional != nil && *c.Dimensional,
		c.Defer != nil && *c.Defer)
}

// BuildTagPartPrompt renders the parts pass's context-free dimensional question over the values: each value
// is judged alone, with no phrase, document, or store context, and a country or continent value is a
// legitimate tag like any other.
func BuildTagPartPrompt(values []string) string {
	var sb strings.Builder
	sb.WriteString("For each value below, judge it on its own as a potential search filter over any document collection: does it name a subject a searcher would filter by?\n")
	sb.WriteString("- A broad, stable, reusable dimension of content counts (repair, movement, networking).\n")
	sb.WriteString("- A generic container with no retrieval function does not (air, thought, irony).\n")
	sb.WriteString("- A value that is a legitimate tag on any facet, a whitelisted country or continent included (russia), counts.\n")
	sb.WriteString("Judge the value alone: no phrase or document context. Set defer true when you cannot decide; never guess.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"values\":[{\"index\":0,\"dimensional\":true,\"defer\":false},...]}, one entry per value, no explanations.\n\n")
	for i, value := range values {
		fmt.Fprintf(&sb, "Value %d: %q\n", i, value)
	}
	return sb.String()
}

// ParseTagPartResponse parses n part answers; an entry lacking a complete dimensional answer is treated as
// missing so a truncated value is never read as a rejection. A bare top-level array is accepted as the
// envelope's value, and an entry without an index is placed by its position in the array. A truncated reply
// returns *IncompleteReplyError alongside the populated results, so the caller can judge those and defer
// only the missing indices.
func ParseTagPartResponse(content string, n int) ([]TagPartResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "values")
	var parsed struct {
		Values []struct {
			Index *int `json:"index"`
			TagPartCriteria
		} `json:"values"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag part response: %w", err)
	}
	results := make([]TagPartResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for pos, v := range parsed.Values {
		idx := pos
		if v.Index != nil {
			idx = *v.Index
		}
		if idx < 0 || idx >= n {
			continue
		}
		r, ok := v.TagPartCriteria.Result()
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

// TagCompoundStatus is the compound pass's reading of one phrase: a named unit its parts cannot compose, a
// plain composition of its parts, or a phrase with no retrievable dimension. A judge abstention is carried
// by TagCompoundResult.Defer, not by a status value.
type TagCompoundStatus string

// The compound pass's readings.
const (
	TagCompoundNamedUnit   TagCompoundStatus = "named_unit"
	TagCompoundComposition TagCompoundStatus = "composition"
	TagCompoundNoDimension TagCompoundStatus = "no_dimension"
)

// Valid reports whether the status is one of the three readings.
func (s TagCompoundStatus) Valid() bool {
	switch s {
	case TagCompoundNamedUnit, TagCompoundComposition, TagCompoundNoDimension:
		return true
	}
	return false
}

// TagSplitVersion pins the compound pass's criteria: the context-free named_unit/composition/no_dimension
// trichotomy asked of every compound, and the criteria version every composition row records. A criteria or
// prompt change bumps it so an older generation's verdicts never apply.
const TagSplitVersion = "v2"

// TagCompoundSystemMessage is the system prompt of the compound pass.
const TagCompoundSystemMessage = "You are a tag-taxonomy classifier. Decide what a compound phrase denotes."

// TagCompoundResult is one phrase's answer: its status and the judge's abstention.
type TagCompoundResult struct {
	Index  int
	Status TagCompoundStatus
	Defer  bool
}

// TagCompoundCriteria is the persisted wire shape of one phrase's answer. Pointer fields distinguish an
// omitted key from an explicit false; defer is optional, so an omitted defer reads as false.
type TagCompoundCriteria struct {
	Status *string `json:"status"`
	Defer  *bool   `json:"defer"`
}

// Criteria returns the persisted wire shape of the answers.
func (r TagCompoundResult) Criteria() TagCompoundCriteria {
	status := string(r.Status)
	return TagCompoundCriteria{Status: &status, Defer: &r.Defer}
}

// Complete reports whether a recognized status was specified.
func (c TagCompoundCriteria) Complete() bool {
	if c.Status == nil {
		return false
	}
	return TagCompoundStatus(strings.ToLower(strings.TrimSpace(*c.Status))).Valid()
}

// Result reads the persisted criteria back into a compound answer; the second return is false when the
// status is missing or outside the trichotomy, so an unrecognized reading is never executed.
func (c TagCompoundCriteria) Result() (TagCompoundResult, bool) {
	if !c.Complete() {
		return TagCompoundResult{}, false
	}
	status := TagCompoundStatus(strings.ToLower(strings.TrimSpace(*c.Status)))
	return TagCompoundResult{Status: status, Defer: c.Defer != nil && *c.Defer}, true
}

// String renders the answers for the audit log.
func (c TagCompoundCriteria) String() string {
	status := ""
	if c.Status != nil {
		status = *c.Status
	}
	return fmt.Sprintf("status=%s defer=%t", status, c.Defer != nil && *c.Defer)
}

// BuildTagCompoundPrompt renders the compound pass's context-free trichotomy question over the phrases: each
// phrase is judged alone, independent of sibling compounds and of any document.
func BuildTagCompoundPrompt(candidates []string) string {
	var sb strings.Builder
	sb.WriteString("For each compound below, judge the phrase on its own, independent of sibling compounds and of any document.\n")
	sb.WriteString("- \"named_unit\": the phrase names one subject its parts cannot compose ('internet of things', 'black bloc', 'cold war').\n")
	sb.WriteString("- \"composition\": the phrase is just the combination of its content words, each of which works as a tag on its own ('russian history', 'labor movement', 'computer networking').\n")
	sb.WriteString("- \"no_dimension\": the phrase names no subject a searcher would filter by (a thesis or a compression artifact).\n")
	sb.WriteString("Set defer true when the reading is unsettled; never force one.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"compounds\":[{\"index\":0,\"status\":\"composition\",\"defer\":false},...]}, one entry per compound, no explanations.\n\n")
	for i, candidate := range candidates {
		fmt.Fprintf(&sb, "Compound %d: %q\n", i, candidate)
	}
	return sb.String()
}

// ParseTagCompoundResponse parses n compound answers; an entry lacking a recognized status is treated as
// missing, so an out-of-trichotomy reading is never executed. A bare top-level array is accepted as the
// envelope's value, and an entry without an index is placed by its position in the array. A truncated reply
// returns *IncompleteReplyError alongside the populated results.
func ParseTagCompoundResponse(content string, n int) ([]TagCompoundResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "compounds")
	var parsed struct {
		Compounds []struct {
			Index *int `json:"index"`
			TagCompoundCriteria
		} `json:"compounds"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag compound response: %w", err)
	}
	results := make([]TagCompoundResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for pos, c := range parsed.Compounds {
		idx := pos
		if c.Index != nil {
			idx = *c.Index
		}
		if idx < 0 || idx >= n {
			continue
		}
		r, ok := c.TagCompoundCriteria.Result()
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

package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagTranslationVersion pins the language translation pass's criteria; a criteria or prompt change bumps it.
const TagTranslationVersion = "v1"

// TagTranslationSystemMessage is the system prompt of the language translation pass.
const TagTranslationSystemMessage = "You are a tag-language normalizer. Give each value's English canonical subject term, or return it unchanged when it is already English."

// TagTranslationResult is one value's answer from the translation pass: the English canonical form (the
// input unchanged when it is already English) and the judge's abstention.
type TagTranslationResult struct {
	Index       int
	Translation string
	Defer       bool
}

// TagTranslationCriteria is the wire shape parsed from one value's answer. Pointer fields distinguish an
// omitted key from an explicit empty value; defer is optional, so an omitted defer reads as false.
type TagTranslationCriteria struct {
	Translation *string `json:"translation"`
	Defer       *bool   `json:"defer"`
}

// Complete reports whether a translation was specified.
func (c TagTranslationCriteria) Complete() bool {
	return c.Translation != nil
}

// Result reads the parsed criteria back into a translation answer; the second return is false when the
// translation is missing.
func (c TagTranslationCriteria) Result() (TagTranslationResult, bool) {
	if !c.Complete() {
		return TagTranslationResult{}, false
	}
	return TagTranslationResult{Translation: *c.Translation, Defer: c.Defer != nil && *c.Defer}, true
}

// BuildTagTranslationPrompt renders the language-normalization question over the values: each value is
// returned in its English canonical subject form, or unchanged when it is already English (including
// technical jargon the dictionary may miss); the judge abstains when the English form is uncertain.
func BuildTagTranslationPrompt(values []string) string {
	var sb strings.Builder
	sb.WriteString("For each value below, give its English canonical subject term in house form (base/lemma form, numeric ordinals); a value already in English — including technical jargon — is returned unchanged.\n")
	sb.WriteString("Translate a non-English value to the English term a searcher would use (filosofia → philosophy, 19 век → 19th century, movimiento social → social movement).\n")
	sb.WriteString("Set defer true when you cannot confidently give the English form; never translate a proper noun into a different entity.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"values\":[{\"index\":0,\"translation\":\"philosophy\",\"defer\":false},...]}, one entry per value, no explanations.\n\n")
	for i, value := range values {
		fmt.Fprintf(&sb, "Value %d: %q\n", i, value)
	}
	return sb.String()
}

// ParseTagTranslationResponse parses n translation answers; an entry lacking a translation is treated as
// missing so a truncated value is never read as an empty translation. A bare top-level array is accepted
// as the envelope's value, and an entry without an index is placed by its position. A truncated reply
// returns *IncompleteReplyError alongside the populated results.
func ParseTagTranslationResponse(content string, n int) ([]TagTranslationResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "values")
	var parsed struct {
		Values []struct {
			Index *int `json:"index"`
			TagTranslationCriteria
		} `json:"values"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag translation response: %w", err)
	}
	results := make([]TagTranslationResult, n)
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
		r, ok := v.TagTranslationCriteria.Result()
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

package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagQualityCandidate is one tag up for quality judging: its index, term,
// facet category, and whether it is a whitelisted place.
type TagQualityCandidate struct {
	Index            int
	Term             string
	Category         string
	WhitelistedPlace bool
}

// TagQualityResult is one candidate's rubric answers; Passed requires all
// four criteria.
type TagQualityResult struct {
	Index              int
	SubjectMatter      bool
	StandardTerm       bool
	NotPersonPlaceWork bool
	AppropriatelyBroad bool
}

// Passed reports whether the candidate satisfies all four quality
// criteria.
func (r TagQualityResult) Passed() bool {
	return r.SubjectMatter && r.StandardTerm && r.NotPersonPlaceWork && r.AppropriatelyBroad
}

// TagQualityCriteria is the persisted wire shape of one candidate's rubric
// verdict. Pointer fields distinguish an omitted key from an explicit false.
type TagQualityCriteria struct {
	SubjectMatter      *bool `json:"subject_matter"`
	StandardTerm       *bool `json:"standard_term"`
	NotPersonPlaceWork *bool `json:"not_person_place_work"`
	AppropriatelyBroad *bool `json:"appropriately_broad"`
}

// Criteria returns the persisted wire shape of the answers.
func (r TagQualityResult) Criteria() TagQualityCriteria {
	return TagQualityCriteria{
		SubjectMatter:      &r.SubjectMatter,
		StandardTerm:       &r.StandardTerm,
		NotPersonPlaceWork: &r.NotPersonPlaceWork,
		AppropriatelyBroad: &r.AppropriatelyBroad,
	}
}

// Complete reports whether all four answers were specified.
func (c TagQualityCriteria) Complete() bool {
	return c.SubjectMatter != nil && c.StandardTerm != nil &&
		c.NotPersonPlaceWork != nil && c.AppropriatelyBroad != nil
}

// String renders the answers for the audit log, treating an omitted
// answer as false.
func (c TagQualityCriteria) String() string {
	return fmt.Sprintf("subject_matter=%t standard_term=%t not_person_place_work=%t appropriately_broad=%t",
		c.SubjectMatter != nil && *c.SubjectMatter,
		c.StandardTerm != nil && *c.StandardTerm,
		c.NotPersonPlaceWork != nil && *c.NotPersonPlaceWork,
		c.AppropriatelyBroad != nil && *c.AppropriatelyBroad)
}

// TagQualityVersion pins the quality measurement generation: bump it
// whenever the criteria text or the prompt changes, so cached verdicts
// from an older generation never apply to a newer one.
const TagQualityVersion = "v1"

// TagQualitySystemMessage is the system prompt of the quality judge.
const TagQualitySystemMessage = "You are a tag-quality judge. Decide whether each candidate tag is fit for a shared document vocabulary."

// BuildTagQualityPrompt renders the four-question rubric over the
// document text and the candidate tags.
func BuildTagQualityPrompt(text string, candidates []TagQualityCandidate) string {
	var sb strings.Builder
	sb.WriteString("For each candidate tag below, answer four yes/no questions about its fitness as a tag for the document.\n")
	sb.WriteString("1. subject_matter: is the tag a subject matter of the document, not its form or genre?\n")
	sb.WriteString("2. standard_term: is the tag standard, widely-recognized terminology, not a coinage specific to this document?\n")
	sb.WriteString("3. not_person_place_work: is the tag neither a person, nor a work, nor a place other than a whitelisted country or continent? A whitelisted country or continent is a legitimate place tag and passes this criterion; only a person, a work, or a sub-national place (a city, province, region, or other non-sovereign territory) fails it.\n")
	sb.WriteString("4. appropriately_broad: is the tag appropriately broad for a shared vocabulary, not a per-document detail?\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"tags\":[{\"index\":0,\"subject_matter\":true,\"standard_term\":true,\"not_person_place_work\":true,\"appropriately_broad\":true},...]}, one entry per candidate, no explanations.\n\n")
	sb.WriteString("Document text:\n")
	sb.WriteString(text)
	sb.WriteString("\n\nCandidate tags:\n")
	for _, c := range candidates {
		fmt.Fprintf(&sb, "Tag %d: term=%q category=%s", c.Index, c.Term, c.Category)
		if c.WhitelistedPlace {
			sb.WriteString(" (whitelisted country or continent — a legitimate place tag; answer not_person_place_work: true)")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ParseTagQualityResponse errors when any candidate lacks a fully
// specified verdict: a zero-valued result must never be read as an
// all-criteria-false rejection, or a truncated response would cache a
// spurious permanent rejection.
func ParseTagQualityResponse(content string, n int) ([]TagQualityResult, error) {
	content = cleanCodeBlock(strings.TrimSpace(content))
	var parsed struct {
		Tags []struct {
			Index int `json:"index"`
			TagQualityCriteria
		} `json:"tags"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag quality response: %w", err)
	}
	results := make([]TagQualityResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for _, t := range parsed.Tags {
		if t.Index < 0 || t.Index >= n || !t.Complete() {
			continue
		}
		results[t.Index] = TagQualityResult{
			Index:              t.Index,
			SubjectMatter:      *t.SubjectMatter,
			StandardTerm:       *t.StandardTerm,
			NotPersonPlaceWork: *t.NotPersonPlaceWork,
			AppropriatelyBroad: *t.AppropriatelyBroad,
		}
		present[t.Index] = true
	}
	var missing []int
	for i, ok := range present {
		if !ok {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		return results, fmt.Errorf("incomplete tag quality response: no verdict for candidate indices %v", missing)
	}
	return results, nil
}

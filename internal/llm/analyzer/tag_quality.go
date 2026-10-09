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

// TagQualityResult is one candidate's rubric answers: the five criteria
// plus the judge's abstention. The criteria are features, not a verdict —
// the admission decision combines them.
type TagQualityResult struct {
	Index              int
	SubjectMatter      bool
	Dimension          bool
	NotPersonPlaceWork bool
	Atomic             bool
	NotFormGenre       bool
	Defer              bool
}

// TagQualityCriteria is the persisted wire shape of one candidate's rubric
// verdict. Pointer fields distinguish an omitted key from an explicit
// false; defer is optional, so an omitted defer reads as false.
type TagQualityCriteria struct {
	SubjectMatter      *bool `json:"subject_matter"`
	Dimension          *bool `json:"dimension"`
	NotPersonPlaceWork *bool `json:"not_person_place_work"`
	Atomic             *bool `json:"atomic"`
	NotFormGenre       *bool `json:"not_form_genre"`
	Defer              *bool `json:"defer"`
}

// Criteria returns the persisted wire shape of the answers.
func (r TagQualityResult) Criteria() TagQualityCriteria {
	return TagQualityCriteria{
		SubjectMatter:      &r.SubjectMatter,
		Dimension:          &r.Dimension,
		NotPersonPlaceWork: &r.NotPersonPlaceWork,
		Atomic:             &r.Atomic,
		NotFormGenre:       &r.NotFormGenre,
		Defer:              &r.Defer,
	}
}

// Complete reports whether the four judge-owned criteria were specified. Dimension is optional here: it
// is the parts pass's verdict the gate stamps onto the row (report N5), not a question the quality judge
// answers, so a reply without it is complete.
func (c TagQualityCriteria) Complete() bool {
	return c.SubjectMatter != nil &&
		c.NotPersonPlaceWork != nil && c.Atomic != nil && c.NotFormGenre != nil
}

// Result reads the persisted criteria back into rubric answers; the
// second return is false when any judge-owned criterion is missing. The
// dimension answer is read when present (a stamped row) and reads false
// otherwise, so a caller that has not stamped the parts verdict sees a
// missing dimension as a failing signal, never as an admission.
func (c TagQualityCriteria) Result() (TagQualityResult, bool) {
	if !c.Complete() {
		return TagQualityResult{}, false
	}
	return TagQualityResult{
		SubjectMatter:      *c.SubjectMatter,
		Dimension:          c.Dimension != nil && *c.Dimension,
		NotPersonPlaceWork: *c.NotPersonPlaceWork,
		Atomic:             *c.Atomic,
		NotFormGenre:       *c.NotFormGenre,
		Defer:              c.Defer != nil && *c.Defer,
	}, true
}

// String renders the answers for the audit log, treating an omitted
// answer as false.
func (c TagQualityCriteria) String() string {
	return fmt.Sprintf("subject_matter=%t dimension=%t not_person_place_work=%t atomic=%t not_form_genre=%t defer=%t",
		c.SubjectMatter != nil && *c.SubjectMatter,
		c.Dimension != nil && *c.Dimension,
		c.NotPersonPlaceWork != nil && *c.NotPersonPlaceWork,
		c.Atomic != nil && *c.Atomic,
		c.NotFormGenre != nil && *c.NotFormGenre,
		c.Defer != nil && *c.Defer)
}

// TagQualityVersion pins the quality judge's criteria: the rubric, the prompt, and the policy version of
// its cached verdict rows. A criteria or prompt change bumps it so an older generation's verdicts never
// apply. v4 narrows the document snippet to the subject-matter criterion: the snippet serves question 1,
// criteria 2–5 are judged on the tag as a store-global dimension, and the text stays in the single call —
// the withholding is by prompt framing. v5 makes the parts pass the single owner of the dimension
// question (report N5): the quality judge answers the document-scoped questions and the gate stamps the
// parts verdict as the row's dimension feature; the language guard rides the same policy version (report
// N2). Cache lookup is version-filtered, so v4 verdicts never apply and stored v4 rows stay for the Stage
// E sweep.
const TagQualityVersion = "v5"

// TagQualitySystemMessage is the system prompt of the quality judge.
const TagQualitySystemMessage = "You are a tag-quality judge. Decide whether each candidate tag is a reusable dimension for a shared document vocabulary."

// BuildTagQualityPrompt renders the quality rubric over the document snippet and the candidate tags.
// The snippet serves the subject-matter question (question 1) alone; the remaining criteria are answered
// on the tag as a store-global dimension, and their answers do not vary with the presenting document.
// The dimension question is not asked here: the parts pass owns it and the gate stamps its verdict
// (report N5), so this rubric carries the document-scoped questions only.
func BuildTagQualityPrompt(text string, candidates []TagQualityCandidate) string {
	var sb strings.Builder
	sb.WriteString("For each candidate tag below, answer four yes/no questions. Question 1 asks whether the tag is a subject matter of the document; questions 2-4 ask about the tag on its own, and their answers do not depend on the document.\n")
	sb.WriteString("1. subject_matter: is the tag a subject matter of the document — something this document is about? A specialized document's technical subject counts even when the word is rare or field-specific (a repair guide about spudgers is about spudgers); a generic theme word the text merely mentions or embodies is not (air, love, nature). The document's language is never a subject matter: a language name (spanish, english) is form, not aboutness.\n")
	sb.WriteString("2. not_person_place_work: is the tag neither a person, nor a work, nor a place other than a whitelisted country or continent? A whitelisted country or continent is a legitimate place tag and passes this criterion; only a person, a work, or a sub-national place (a city, province, region, or other non-sovereign territory) fails it.\n")
	sb.WriteString("3. atomic: is the tag one dimension, not a combination whose parts each work as tags on their own? A compound that survives decomposition as a named unit answers true.\n")
	sb.WriteString("4. not_form_genre: is the tag not a publishing format? Publishing formats and containers (book, article, tutorial, manual, report, anthology, collected letters) describe what a document is as a publication, not what it is about — they fail this criterion. A genre or art form (poetry, drama, science fiction, horror, autobiography, memoir) is a subject-side content dimension, not a publishing format: it passes this criterion and is judged on the questions above.\n")
	sb.WriteString("Also answer defer: set it true when you cannot decide whether the tag is a reusable dimension. Uncertainty defers to review; it never forces a label.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"tags\":[{\"index\":0,\"subject_matter\":true,\"not_person_place_work\":true,\"atomic\":true,\"not_form_genre\":true,\"defer\":false},...]}, one entry per candidate, no explanations.\n\n")
	sb.WriteString("Document for question 1:\n")
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

// IncompleteReplyError reports a judged reply that omitted verdicts for some entries: Missing holds the
// entry indices with no verdict. It is the judged parsers' shared incompleteness type — quality, part,
// compound, identity, and adjudication — each of which still returns the entries it did read, so a caller
// can judge those and defer only the missing indices instead of discarding the whole reply.
type IncompleteReplyError struct {
	Missing []int
}

func (e *IncompleteReplyError) Error() string {
	return fmt.Sprintf("incomplete judged response: no verdict for entry indices %v", e.Missing)
}

// MissingSet returns the missing entry indices as a set for membership tests, so a caller salvaging a
// partial reply does not rebuild the lookup at every call site.
func (e *IncompleteReplyError) MissingSet() map[int]bool {
	missing := make(map[int]bool, len(e.Missing))
	for _, idx := range e.Missing {
		missing[idx] = true
	}
	return missing
}

// ParseTagQualityResponse errors when any candidate lacks a fully
// specified verdict: a zero-valued result must never be read as an
// all-criteria-false rejection, or a truncated response would cache a
// spurious permanent rejection. An omitted defer reads as false. A bare
// top-level array is accepted as the envelope's value, and an entry
// without an index is placed by its position in the array. A truncated
// reply returns *IncompleteReplyError alongside the populated results.
func ParseTagQualityResponse(content string, n int) ([]TagQualityResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "tags")
	var parsed struct {
		Tags []struct {
			Index *int `json:"index"`
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
	for pos, t := range parsed.Tags {
		idx := pos
		if t.Index != nil {
			idx = *t.Index
		}
		if idx < 0 || idx >= n || !t.Complete() {
			continue
		}
		results[idx] = TagQualityResult{
			Index:              idx,
			SubjectMatter:      *t.SubjectMatter,
			Dimension:          t.Dimension != nil && *t.Dimension,
			NotPersonPlaceWork: *t.NotPersonPlaceWork,
			Atomic:             *t.Atomic,
			NotFormGenre:       *t.NotFormGenre,
			Defer:              t.Defer != nil && *t.Defer,
		}
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

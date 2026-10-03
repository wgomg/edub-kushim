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

// Complete reports whether all five criteria were specified.
func (c TagQualityCriteria) Complete() bool {
	return c.SubjectMatter != nil && c.Dimension != nil &&
		c.NotPersonPlaceWork != nil && c.Atomic != nil && c.NotFormGenre != nil
}

// Result reads the persisted criteria back into rubric answers; the
// second return is false when any criterion is missing.
func (c TagQualityCriteria) Result() (TagQualityResult, bool) {
	if !c.Complete() {
		return TagQualityResult{}, false
	}
	return TagQualityResult{
		SubjectMatter:      *c.SubjectMatter,
		Dimension:          *c.Dimension,
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

// TagQualityVersion pins the quality measurement generation: the criteria
// text, the prompt, and the policy version of the cached verdict rows. v2
// rewords subject_matter and dimension corpus-relatively and adds the
// not_form_genre criterion; v3 restores the B2 genre/format boundary —
// criterion 5 targets the publishing formats the document_type facet holds
// and genres and art-forms are subject-side, judged on dimension alone —
// and adds the language carve-out to criteria 1–2 (a language name is form,
// never subject matter or a content dimension). Older verdicts never apply:
// the cache lookup is version-filtered and stored v2 rows stay for the
// Stage E sweep. Later criteria or prompt changes bump it so an older
// generation's verdicts never apply.
const TagQualityVersion = "v3"

// TagQualitySystemMessage is the system prompt of the quality judge.
const TagQualitySystemMessage = "You are a tag-quality judge. Decide whether each candidate tag is a reusable dimension for a shared document vocabulary."

// BuildTagQualityPrompt renders the dimension rubric over the document
// text and the candidate tags.
func BuildTagQualityPrompt(text string, candidates []TagQualityCandidate) string {
	var sb strings.Builder
	sb.WriteString("For each candidate tag below, answer five yes/no questions about its fitness as a reusable dimension of the document.\n")
	sb.WriteString("1. subject_matter: is the tag a subject matter of the document — something this document is about? A specialized document's technical subject counts even when the word is rare or corpus-specific (a repair guide about spudgers is about spudgers); a generic theme word the text merely mentions or embodies is not (air, love, nature). The document's language is never a subject matter: a language name (spanish, english) is form, not aboutness.\n")
	sb.WriteString("2. dimension: is the tag a reusable dimension of content — a value a searcher would filter by on its own, expecting other documents like this one to share it — judged relative to the corpus the document belongs to? A value on a tool, part, or operation dimension of a specialized corpus is a dimension (spudger, jack stands, nut driver); a dictionary-generic container word with no retrieval function is not (air, love, nature, life, self). Not a per-document detail, a thesis, or a container word, and not the document's language — a language name is a value of the language facet, never a content dimension (spanish, english).\n")
	sb.WriteString("3. not_person_place_work: is the tag neither a person, nor a work, nor a place other than a whitelisted country or continent? A whitelisted country or continent is a legitimate place tag and passes this criterion; only a person, a work, or a sub-national place (a city, province, region, or other non-sovereign territory) fails it.\n")
	sb.WriteString("4. atomic: is the tag one dimension, not a combination whose parts each work as tags on their own? A compound that survives decomposition as a named unit answers true.\n")
	sb.WriteString("5. not_form_genre: is the tag not a publishing format of the document? Publishing formats and containers (book, article, tutorial, manual, report, anthology, collected letters) describe what the document is as a publication, not what it is about — they belong to the document-type facet and fail this criterion. A genre or art form (poetry, drama, science fiction, horror, autobiography, memoir) is a subject-side content dimension, not a publishing format: it passes this criterion and is judged on the questions above.\n")
	sb.WriteString("Also answer defer: set it true when you cannot decide whether the tag is a reusable dimension. Uncertainty defers to review; it never forces a label.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"tags\":[{\"index\":0,\"subject_matter\":true,\"dimension\":true,\"not_person_place_work\":true,\"atomic\":true,\"not_form_genre\":true,\"defer\":false},...]}, one entry per candidate, no explanations.\n\n")
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
// spurious permanent rejection. An omitted defer reads as false. A bare
// top-level array is accepted as the envelope's value, and an entry
// without an index is placed by its position in the array.
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
			Dimension:          *t.Dimension,
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
		return results, fmt.Errorf("incomplete tag quality response: no verdict for candidate indices %v", missing)
	}
	return results, nil
}

package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/types"
)

// TagQualityCandidate is one tag up for quality judging: its index, term,
// and facet category.
type TagQualityCandidate struct {
	Index    int
	Term     string
	Category string
}

// TagQualityResult is one candidate's rubric answers: the boolean criteria, the person class value, the
// judge's abstention, and whether the composition gate decided to keep the candidate whole. The answers
// are features, not a verdict — the admission decision combines them. A composition-owned result carries
// no dimension answer: the composition decision owns that question.
type TagQualityResult struct {
	Index           int
	SubjectMatter   bool
	Dimension       bool
	PersonClass     string
	Atomic          bool
	NotFormGenre    bool
	Defer           bool
	CompositionKept bool
}

// TagQualityCriteria is the persisted wire shape of one candidate's rubric verdict. Pointer-valued fields
// distinguish an omitted key from an explicit false; defer is optional, so an omitted defer reads as
// false, and the dimension markers are conditional: an ordinary row carries dimension, while an owned row
// carries composition_keep instead.
type TagQualityCriteria struct {
	SubjectMatter *bool `json:"subject_matter"`
	// Dimension is the parts pass's stamped verdict; an absent key means the parts pass did not own the
	// row (a composition-owned keep) or abstained.
	Dimension       *bool   `json:"dimension,omitempty"`
	PersonClass     *string `json:"person_class"`
	Atomic          *bool   `json:"atomic"`
	NotFormGenre    *bool   `json:"not_form_genre"`
	Defer           *bool   `json:"defer"`
	CompositionKept *bool   `json:"composition_keep,omitempty"`
}

// Criteria returns the persisted wire shape of the answers. A composition-owned row carries composition_keep
// and no dimension key, so the stored shape states which decision owns the dimension question; an ordinary
// row's shape is unchanged.
func (r TagQualityResult) Criteria() TagQualityCriteria {
	c := TagQualityCriteria{
		SubjectMatter: &r.SubjectMatter,
		PersonClass:   &r.PersonClass,
		Atomic:        &r.Atomic,
		NotFormGenre:  &r.NotFormGenre,
		Defer:         &r.Defer,
	}
	if r.CompositionKept {
		c.CompositionKept = &r.CompositionKept
	} else {
		c.Dimension = &r.Dimension
	}
	return c
}

// Complete reports whether the judge-owned criteria were specified, the person class included and carrying
// one of the five rubric answers. Dimension is optional here: it is the parts pass's verdict the gate
// stamps onto the row, not a question the quality judge answers, so a reply without it is still complete.
func (c TagQualityCriteria) Complete() bool {
	return c.SubjectMatter != nil &&
		c.PersonClass != nil && types.ValidPersonClass(*c.PersonClass) &&
		c.Atomic != nil && c.NotFormGenre != nil
}

// Result reads the persisted criteria back into rubric answers; the second return is false when any
// judge-owned criterion is missing. The dimension answer is read when present (a stamped row) and reads
// false otherwise, so a caller that has not stamped the parts verdict sees a missing dimension as a
// failing signal, never as an admission. A composition-owned row carries composition_keep instead: the
// admission ladder then reads the dimension as satisfied by the composition decision.
func (c TagQualityCriteria) Result() (TagQualityResult, bool) {
	if !c.Complete() {
		return TagQualityResult{}, false
	}
	return TagQualityResult{
		SubjectMatter:   *c.SubjectMatter,
		Dimension:       c.Dimension != nil && *c.Dimension,
		PersonClass:     *c.PersonClass,
		Atomic:          *c.Atomic,
		NotFormGenre:    *c.NotFormGenre,
		Defer:           c.Defer != nil && *c.Defer,
		CompositionKept: c.CompositionKept != nil && *c.CompositionKept,
	}, true
}

// String renders the answers for the audit log, treating an omitted boolean as false and an omitted class
// as empty. The composition-owned marker is appended only when set, so an ordinary audit line is unchanged.
func (c TagQualityCriteria) String() string {
	personClass := ""
	if c.PersonClass != nil {
		personClass = *c.PersonClass
	}
	line := fmt.Sprintf("subject_matter=%t dimension=%t person_class=%s atomic=%t not_form_genre=%t defer=%t",
		c.SubjectMatter != nil && *c.SubjectMatter,
		c.Dimension != nil && *c.Dimension,
		personClass,
		c.Atomic != nil && *c.Atomic,
		c.NotFormGenre != nil && *c.NotFormGenre,
		c.Defer != nil && *c.Defer)
	if c.CompositionKept != nil && *c.CompositionKept {
		line += " composition_keep=true"
	}
	return line
}

// TagQualityVersion pins the quality judge's criteria: the rubric, the prompt, and the policy version of
// its cached verdict rows. A criteria or prompt change bumps it so an older generation's verdicts never
// apply.
//
// v9 changes what the gate stamps, not the rubric: a compound the composition gate decided to keep whole
// carries `composition_keep` and no `dimension`, and the parts pass's abstention is no longer merged into
// `defer` for it; every other candidate still reads the parts pass's verdict. The judge's questions are
// unchanged, and cache lookup is version-filtered, so v8 verdicts never apply and their stored rows are
// superseded evidence.
const TagQualityVersion = "v9"

// TagQualitySystemMessage is the system prompt of the quality judge.
const TagQualitySystemMessage = "You are a tag-quality judge. Answer the rubric in the user message for each candidate tag."

// BuildTagQualityPrompt renders the quality rubric over the document snippet and the candidate tags.
// The snippet serves the subject-matter question (question 1) alone; the remaining criteria are answered
// on the tag as a store-global dimension, and their answers do not vary with the presenting document.
// The dimension question is not asked here: the parts pass owns it and the gate stamps its verdict,
// so this rubric carries the document-scoped questions only.
func BuildTagQualityPrompt(text string, candidates []TagQualityCandidate) string {
	var sb strings.Builder
	sb.WriteString(TagDefinition + "\n")
	sb.WriteString("For each candidate tag below, answer the rubric against the definition above. Question 1 asks whether the tag is a subject matter of the document; questions 2-4 ask about the tag on its own, and their answers do not depend on the document.\n")
	sb.WriteString("1. subject_matter: is the tag a subject matter of the document — something this document is about? A specialized document's technical subject counts even when the word is rare or field-specific (a repair guide about spudgers is about spudgers); a generic theme word the text merely mentions or embodies is not (air, love, nature). The document's language is never a subject matter: a language name (spanish, english) is form, not aboutness.\n")
	sb.WriteString("2. person_class: does the tag name a person, and if so which kind? Answer exactly one of five classes. not_person: the tag names no person — a place, work, organization, concept, or ordinary word. real_individual: a historically documented individual the string names unambiguously. mythological_or_legendary: a figure of myth, legend, or religious tradition — a deity or legendary figure whose person-hood rests on tradition, not historical record. fictional: a character invented by a work of fiction. uncertain: any other case, including a string that also names a place, organization, work, or concept (tesla, washington, darwin, jesus, buddha), a string naming several people, or a figure whose classification is contested. An individual belongs to the separate people field, so a person's name is never a subject tag.\n")
	sb.WriteString("3. atomic: is the tag one dimension rather than a pre-coordination of dimensions that are each tags on their own? A lexical or named unit whose parts are not dimensions answers true.\n")
	sb.WriteString("4. not_form_genre: is the tag not a publishing format? Publishing formats and containers (book, article, tutorial, manual, report, anthology, collected letters) describe what a document is as a publication, not what it is about — they fail this criterion. A genre or art form (poetry, drama, science fiction, horror, autobiography, memoir) is a subject-side content dimension, not a publishing format: it passes this criterion and is judged on the questions above.\n")
	sb.WriteString("Also answer defer: set it true when you cannot decide whether the tag satisfies the definition. Uncertainty defers to review; it never forces a label.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"tags\":[{\"index\":0,\"subject_matter\":true,\"person_class\":\"not_person\",\"atomic\":true,\"not_form_genre\":true,\"defer\":false},...]}, one entry per candidate, no explanations.\n\n")
	sb.WriteString("Document for question 1:\n")
	sb.WriteString(text)
	sb.WriteString("\n\nCandidate tags:\n")
	for _, c := range candidates {
		fmt.Fprintf(&sb, "Tag %d: term=%q category=%s\n", c.Index, c.Term, c.Category)
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

// ParseTagQualityResponse errors when any candidate lacks a fully specified verdict — a person_class
// outside the five answers counting as missing, since a zero-valued result must never be read as an
// all-criteria-false rejection, or a truncated response would cache a spurious permanent rejection.
// An omitted defer reads as false. A stray composition_keep in a reply is ignored: the judge is never asked
// it, and only the gate stamps composition ownership. A bare top-level array is accepted as the envelope's
// value, and an entry without an index is placed by its position in the array. A truncated reply returns the
// typed *IncompleteReplyError alongside the populated results.
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
			Index:         idx,
			SubjectMatter: *t.SubjectMatter,
			Dimension:     t.Dimension != nil && *t.Dimension,
			PersonClass:   *t.PersonClass,
			Atomic:        *t.Atomic,
			NotFormGenre:  *t.NotFormGenre,
			Defer:         t.Defer != nil && *t.Defer,
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

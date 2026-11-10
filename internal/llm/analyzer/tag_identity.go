package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagIdentityCandidate is one term the identity judge decides: its
// index, the term, and the fold the deterministic machinery proposed
// (empty when none).
type TagIdentityCandidate struct {
	Index        int
	Term         string
	ProposedFold string
}

// TagIdentityResult is one candidate's identity answers: whether to fold to base, whether the fold preserves
// denotation, and whether the term is conventionally written, plus the judge's fold-relation class — an
// informational label that never decides the fold.
type TagIdentityResult struct {
	Index                   int
	FoldToBase              bool
	FoldPreservesDenotation bool
	ConventionalForm        bool
	FoldClass               string
}

// TagIdentityCriteria is the persisted wire shape of one candidate's identity verdict. Pointer fields
// distinguish an omitted key from an explicit false. FoldClass is informational and omitted when unknown.
type TagIdentityCriteria struct {
	FoldToBase              *bool  `json:"fold_to_base"`
	FoldPreservesDenotation *bool  `json:"fold_preserves_denotation"`
	ConventionalForm        *bool  `json:"conventional_form"`
	FoldClass               string `json:"fold_class,omitempty"`
}

// Criteria returns the persisted wire shape of the answers.
func (r TagIdentityResult) Criteria() TagIdentityCriteria {
	return TagIdentityCriteria{
		FoldToBase:              &r.FoldToBase,
		FoldPreservesDenotation: &r.FoldPreservesDenotation,
		ConventionalForm:        &r.ConventionalForm,
		FoldClass:               r.FoldClass,
	}
}

// CriteriaRecord marshals the three answers plus the fold the mechanism
// proposed, for the identity event's criteria column.
func (r TagIdentityResult) CriteriaRecord(proposedFold string) []byte {
	record := struct {
		TagIdentityCriteria
		ProposedFold string `json:"proposed_fold,omitempty"`
	}{TagIdentityCriteria: r.Criteria(), ProposedFold: proposedFold}
	raw, err := json.Marshal(record)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

// Complete reports whether all three answers were specified.
func (c TagIdentityCriteria) Complete() bool {
	return c.FoldToBase != nil && c.FoldPreservesDenotation != nil && c.ConventionalForm != nil
}

// String renders the answers for the log, treating an omitted answer as false and an omitted fold class as
// absent.
func (c TagIdentityCriteria) String() string {
	s := fmt.Sprintf("fold_to_base=%t fold_preserves_denotation=%t conventional_form=%t",
		c.FoldToBase != nil && *c.FoldToBase,
		c.FoldPreservesDenotation != nil && *c.FoldPreservesDenotation,
		c.ConventionalForm != nil && *c.ConventionalForm)
	if c.FoldClass != "" {
		s += " fold_class=" + c.FoldClass
	}
	return s
}

// The fold-relation classes the identity judge labels each candidate with: a relational adjective whose noun
// denotes the same thing, an inflected form, a lexicalized derivation or gerund, or none of these. The label
// is persisted and audited but never read by the fold decision, which rests on the three boolean answers
// alone.
const (
	FoldClassRelationalAdjective = "relational_adjective"
	FoldClassInflection          = "inflection"
	FoldClassDerivation          = "derivation"
	FoldClassNone                = "none"
)

// normalizeFoldClass keeps the four declared fold-relation classes and reads anything else — including an
// omitted answer — as unknown.
func normalizeFoldClass(class string) string {
	switch class {
	case FoldClassRelationalAdjective, FoldClassInflection, FoldClassDerivation, FoldClassNone:
		return class
	}
	return ""
}

// TagIdentitySystemMessage is the system prompt of the identity judge.
const TagIdentitySystemMessage = "You are a tag-identity judge. Decide the canonical written form of each tag term."

// BuildTagIdentityPrompt renders the canonical-form questions, with each
// candidate's proposed fold where one exists. The shared tag definition
// supplies the canonicality property the questions enforce.
func BuildTagIdentityPrompt(candidates []TagIdentityCandidate) string {
	var sb strings.Builder
	sb.WriteString(TagDefinition + "\n")
	sb.WriteString("For each candidate tag term below, answer three yes/no questions about its canonical written form and one fold-relation class question, enforcing the definition's one-canonical-label-per-intent property.\n")
	sb.WriteString("1. fold_to_base: should the term be written in its base form? A relational adjective falls to its noun when it denotes the same thing (chilean -> chile). A plural whose base form exists folds to the base unless the plural is the established name of the sense (railways -> railway; data stands). An inflected form stands when it is the established name of the sense (data, human rights) or denotes a different sense than the base (arms is not arm). Lexicalized derivations and gerunds stand (programming, learning, running); a derived word that names its own concept is never reduced to a root. Answer false when the term already stands or its proposed fold would cross denotation.\n")
	sb.WriteString("2. fold_preserves_denotation: does the proposed fold preserve the term's retrieval meaning, without generalizing, specializing, or merging sibling concepts? Answer true when no fold is proposed.\n")
	sb.WriteString("3. conventional_form: is the term written in the conventional form its domain uses? Spelled-out symbols are not conventional (c plus plus should be c++). Answer true when the term is already conventional.\n")
	fmt.Fprintf(&sb, "4. fold_class: which fold-relation class describes the term? Answer %s for an adjective that names the same thing as its noun base (chilean -> chile); %s for a plural or other inflected form (railways -> railway, data); %s for a lexicalized derivation or gerund that names its own concept (programming, learning); %s when the term is already a base form or no fold relation applies. The class describes the term; it does not decide the fold.\n",
		FoldClassRelationalAdjective, FoldClassInflection, FoldClassDerivation, FoldClassNone)
	sb.WriteString("Answer with ONLY a JSON object: {\"terms\":[{\"index\":0,\"fold_to_base\":false,\"fold_preserves_denotation\":true,\"conventional_form\":true,\"fold_class\":\"none\"},...]}, one entry per candidate, no explanations.\n\n")
	sb.WriteString("Candidate terms:\n")
	for _, c := range candidates {
		fmt.Fprintf(&sb, "Tag %d: term=%q", c.Index, c.Term)
		if c.ProposedFold != "" && c.ProposedFold != c.Term {
			fmt.Fprintf(&sb, " proposed_fold=%q", c.ProposedFold)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ParseTagIdentityResponse treats a candidate lacking a fully specified verdict as missing, so a truncated
// reply is never read as an all-false rejection. A bare top-level array is accepted as the envelope's value,
// and an entry without an index is placed by its position in the array. The informational fold class is
// normalized: an omitted or unrecognized value reads as empty and never affects the missing-entry rule. A
// truncated reply returns *IncompleteReplyError alongside the populated results, so the caller can judge
// those and defer only the missing indices.
func ParseTagIdentityResponse(content string, n int) ([]TagIdentityResult, error) {
	content = wrapBareArray(cleanCodeBlock(strings.TrimSpace(content)), "terms")
	var parsed struct {
		Terms []struct {
			Index *int `json:"index"`
			TagIdentityCriteria
		} `json:"terms"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag identity response: %w", err)
	}
	results := make([]TagIdentityResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for pos, t := range parsed.Terms {
		idx := pos
		if t.Index != nil {
			idx = *t.Index
		}
		if idx < 0 || idx >= n || !t.Complete() {
			continue
		}
		results[idx] = TagIdentityResult{
			Index:                   idx,
			FoldToBase:              *t.FoldToBase,
			FoldPreservesDenotation: *t.FoldPreservesDenotation,
			ConventionalForm:        *t.ConventionalForm,
			FoldClass:               normalizeFoldClass(t.FoldClass),
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

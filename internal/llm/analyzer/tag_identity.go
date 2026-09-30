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

// TagIdentityResult is one candidate's identity answers: whether to fold
// to base, whether the fold preserves denotation, and whether the term is
// conventionally written.
type TagIdentityResult struct {
	Index                   int
	FoldToBase              bool
	FoldPreservesDenotation bool
	ConventionalForm        bool
}

// TagIdentityCriteria is the persisted wire shape of one candidate's
// identity verdict. Pointer fields distinguish an omitted key from an
// explicit false.
type TagIdentityCriteria struct {
	FoldToBase              *bool `json:"fold_to_base"`
	FoldPreservesDenotation *bool `json:"fold_preserves_denotation"`
	ConventionalForm        *bool `json:"conventional_form"`
}

// Criteria returns the persisted wire shape of the answers.
func (r TagIdentityResult) Criteria() TagIdentityCriteria {
	return TagIdentityCriteria{
		FoldToBase:              &r.FoldToBase,
		FoldPreservesDenotation: &r.FoldPreservesDenotation,
		ConventionalForm:        &r.ConventionalForm,
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

// String renders the answers for the log, treating an omitted answer as
// false.
func (c TagIdentityCriteria) String() string {
	return fmt.Sprintf("fold_to_base=%t fold_preserves_denotation=%t conventional_form=%t",
		c.FoldToBase != nil && *c.FoldToBase,
		c.FoldPreservesDenotation != nil && *c.FoldPreservesDenotation,
		c.ConventionalForm != nil && *c.ConventionalForm)
}

// TagIdentitySystemMessage is the system prompt of the identity judge.
const TagIdentitySystemMessage = "You are a tag-identity judge. Decide the canonical written form of each tag term."

// BuildTagIdentityPrompt renders the canonical-form questions, with each
// candidate's proposed fold where one exists.
func BuildTagIdentityPrompt(candidates []TagIdentityCandidate) string {
	var sb strings.Builder
	sb.WriteString("For each candidate tag term below, answer three yes/no questions about its canonical written form.\n")
	sb.WriteString("1. fold_to_base: should the term be written in its base form? A relational adjective falls to its noun when it denotes the same thing (chilean -> chile). A plural whose base form exists folds to the base unless the plural is the established name of the sense (railways -> railway; data stands). An inflected form stands when it is the established name of the sense (data, human rights) or denotes a different sense than the base (arms is not arm). Lexicalized derivations and gerunds stand (programming, learning, running); a derived word that names its own concept is never reduced to a root. Answer false when the term already stands or its proposed fold would cross denotation.\n")
	sb.WriteString("2. fold_preserves_denotation: does the proposed fold preserve the term's denotation, without generalizing, specializing, or merging sibling concepts? Answer true when no fold is proposed.\n")
	sb.WriteString("3. conventional_form: is the term written in the conventional form its domain uses? Spelled-out symbols are not conventional (c plus plus should be c++). Answer true when the term is already conventional.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"terms\":[{\"index\":0,\"fold_to_base\":false,\"fold_preserves_denotation\":true,\"conventional_form\":true},...]}, one entry per candidate, no explanations.\n\n")
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

// ParseTagIdentityResponse errors when any candidate lacks a fully
// specified verdict, so a truncated answer is never read as an all-false
// rejection.
func ParseTagIdentityResponse(content string, n int) ([]TagIdentityResult, error) {
	content = cleanCodeBlock(strings.TrimSpace(content))
	var parsed struct {
		Terms []struct {
			Index int `json:"index"`
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
	for _, t := range parsed.Terms {
		if t.Index < 0 || t.Index >= n || !t.Complete() {
			continue
		}
		results[t.Index] = TagIdentityResult{
			Index:                   t.Index,
			FoldToBase:              *t.FoldToBase,
			FoldPreservesDenotation: *t.FoldPreservesDenotation,
			ConventionalForm:        *t.ConventionalForm,
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
		return results, fmt.Errorf("incomplete tag identity response: no verdict for candidate indices %v", missing)
	}
	return results, nil
}

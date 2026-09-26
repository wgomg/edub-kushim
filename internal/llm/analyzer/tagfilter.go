package analyzer

import (
	"strings"

	"github.com/wgomg/edub-kushim/internal/textnorm"
)

func normalizeCore(s string) string {
	return textnorm.NormalizeTag(s)
}

func normalizeToTokens(s string) []string {
	return strings.Fields(normalizeCore(s))
}

const (
	maxTagWords  = 3
	maxTagLength = 64
)

// TagFilter carries the token sets FilterTags checks a candidate tag against,
// so other tag-producing paths (adjudicated split components) can apply the
// same per-document rules.
type TagFilter struct {
	nameTokens     map[string]struct{}
	knownTokenSets []map[string]struct{}
	docTypeTokens  map[string]struct{}
	titleTokens    map[string]struct{}
}

// NewTagFilter builds the per-document token sets: the analysis people's
// names, the known people, the document type names, and the title.
func NewTagFilter(people []PeopleResult, knownPeopleNormalized []string, title string, docTypeNames []string) *TagFilter {
	nameTokens := make(map[string]struct{})
	for _, p := range people {
		for _, tok := range normalizeToTokens(p.NormalizedName) {
			nameTokens[tok] = struct{}{}
		}
	}

	var knownTokenSets []map[string]struct{}
	for _, name := range knownPeopleNormalized {
		tokens := normalizeToTokens(name)
		if len(tokens) == 0 {
			continue
		}
		set := make(map[string]struct{}, len(tokens))
		for _, tok := range tokens {
			set[tok] = struct{}{}
		}
		knownTokenSets = append(knownTokenSets, set)
	}

	docTypeTokens := make(map[string]struct{})
	for _, name := range docTypeNames {
		for _, tok := range normalizeToTokens(name) {
			docTypeTokens[tok] = struct{}{}
		}
	}

	titleTokens := make(map[string]struct{})
	for _, tok := range normalizeToTokens(title) {
		titleTokens[tok] = struct{}{}
	}

	return &TagFilter{
		nameTokens:     nameTokens,
		knownTokenSets: knownTokenSets,
		docTypeTokens:  docTypeTokens,
		titleTokens:    titleTokens,
	}
}

// Accept reports whether a candidate tag passes the per-document rules:
// length and word-count bounds, no person names or known people, no
// document-type tokens, and not fully contained in the title.
func (f *TagFilter) Accept(tag string) bool {
	tagTokens := strings.Fields(tag)
	if len(tagTokens) > maxTagWords || len(tagTokens) == 0 || len(tag) > maxTagLength {
		return false
	}

	for _, tok := range tagTokens {
		if _, ok := f.nameTokens[tok]; ok {
			return false
		}
	}

	if len(tagTokens) >= 2 {
		for _, kset := range f.knownTokenSets {
			allInKnown := true
			for _, tok := range tagTokens {
				if _, ok := kset[tok]; !ok {
					allInKnown = false
					break
				}
			}
			if allInKnown {
				return false
			}
		}
	}

	for _, tok := range tagTokens {
		if _, ok := f.docTypeTokens[tok]; ok {
			return false
		}
	}

	allInTitle := true
	for _, tok := range tagTokens {
		if _, ok := f.titleTokens[tok]; !ok {
			allInTitle = false
			break
		}
	}
	return !allInTitle
}

// FilterTags normalizes, deduplicates and filters the analysis tags
// through the per-document rules, capped at maxTags.
func FilterTags(tags []string, people []PeopleResult, knownPeopleNormalized []string, title string, docTypeNames []string, maxTags int) []string {
	filter := NewTagFilter(people, knownPeopleNormalized, title, docTypeNames)

	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if !filter.Accept(tag) {
			continue
		}
		result = append(result, tag)
	}

	if len(result) > maxTags {
		result = result[:maxTags]
	}
	return result
}

// SplitComponents normalizes adjudicated split components and keeps the
// ones the per-document filter accepts.
func SplitComponents(raw []string, filter *TagFilter) []string {
	normalized := NormalizeTags(raw)
	result := make([]string, 0, len(normalized))
	for _, c := range normalized {
		if !filter.Accept(c) {
			continue
		}
		result = append(result, c)
	}
	return result
}

// NormalizeTags applies the tag normalization, drops empties and
// deduplicates.
func NormalizeTags(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	result := make([]string, 0, len(raw))
	for _, t := range raw {
		t = normalizeCore(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		result = append(result, t)
	}
	return result
}

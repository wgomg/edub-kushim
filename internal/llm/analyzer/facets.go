package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TagDetail is the model's per-tag annotation: the term, its facet
// category, and whether the term is standard terminology. IsStandardTerm
// is a pointer so an omitted answer stays distinguishable from a false
// one.
type TagDetail struct {
	Term           string `json:"term"`
	Category       string `json:"category"`
	IsStandardTerm *bool  `json:"is_standard_term"`
}

// The tag facet categories the analysis prompt offers; anything the
// model answers outside them parses to other.
const (
	TagCategoryTopic     = "topic"
	TagCategoryFormGenre = "form_genre"
	TagCategoryPlace     = "place"
	TagCategoryPerson    = "person"
	TagCategoryWork      = "work"
	TagCategoryOther     = "other"
)

func normalizeTagCategory(raw string) string {
	c := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(raw)))
	switch c {
	case TagCategoryTopic, TagCategoryFormGenre, TagCategoryPlace, TagCategoryPerson, TagCategoryWork:
		return c
	default:
		return TagCategoryOther
	}
}

type analysisWire struct {
	Title    string          `json:"title"`
	DocType  string          `json:"type"`
	Tags     json.RawMessage `json:"tags"`
	People   []PeopleResult  `json:"people"`
	Language string          `json:"language"`
}

// ParseAnalysisResponse parses the model's JSON answer into the result.
// Tags may arrive as plain strings or as detail objects; categories
// normalize to the vocabulary, with anything unknown becoming other.
func ParseAnalysisResponse(content string) (*AnalysisResult, error) {
	var wire analysisWire
	if err := json.Unmarshal([]byte(content), &wire); err != nil {
		return nil, fmt.Errorf("parse analysis response: %w", err)
	}
	var elements []json.RawMessage
	if len(wire.Tags) > 0 {
		if err := json.Unmarshal(wire.Tags, &elements); err != nil {
			return nil, fmt.Errorf("parse tags: %w", err)
		}
	}
	tags := make([]string, 0, len(elements))
	details := make([]TagDetail, 0, len(elements))
	for _, el := range elements {
		var term string
		if err := json.Unmarshal(el, &term); err == nil {
			tags = append(tags, term)
			details = append(details, TagDetail{Term: term, Category: TagCategoryOther})
			continue
		}
		var d TagDetail
		if err := json.Unmarshal(el, &d); err != nil {
			continue
		}
		if strings.TrimSpace(d.Term) == "" {
			continue
		}
		d.Category = normalizeTagCategory(d.Category)
		tags = append(tags, d.Term)
		details = append(details, d)
	}
	return &AnalysisResult{
		Title:      wire.Title,
		DocType:    wire.DocType,
		Tags:       tags,
		TagDetails: details,
		People:     wire.People,
		Language:   wire.Language,
	}, nil
}

// FacetDrop is one tag the facet separation removed, with its category
// and the reason.
type FacetDrop struct {
	Term     string
	Category string
	Reason   string
}

func classifyCategory(tag, category string, docTypes map[string]struct{}) (string, bool) {
	switch category {
	case TagCategoryFormGenre:
		if _, ok := docTypes[tag]; ok {
			return "matches_document_type", true
		}
		return "model_self_classification", true
	case TagCategoryPerson:
		return "people_facet", true
	case TagCategoryWork:
		return "forbidden_category", true
	case TagCategoryPlace:
		if !IsWhitelistedPlace(tag) {
			return "non_whitelisted_place", true
		}
	}
	return "", false
}

// SeparateFacets drops tags that belong to other facets: people, works,
// non-whitelisted places, and form-or-genre tags that restate the
// document type. Tags without a parsed detail pass through.
func SeparateFacets(tags []string, details []TagDetail, docTypeNames []string) ([]string, []FacetDrop) {
	if len(details) == 0 {
		return tags, nil
	}
	categoryByTerm := make(map[string]string, len(details))
	for _, d := range details {
		categoryByTerm[normalizeCore(d.Term)] = d.Category
	}
	docTypes := make(map[string]struct{}, len(docTypeNames))
	for _, name := range docTypeNames {
		docTypes[normalizeCore(name)] = struct{}{}
	}
	kept := make([]string, 0, len(tags))
	var dropped []FacetDrop
	for _, tag := range tags {
		category, ok := categoryByTerm[tag]
		if !ok {
			kept = append(kept, tag)
			continue
		}
		if reason, drop := classifyCategory(tag, category, docTypes); drop {
			dropped = append(dropped, FacetDrop{Term: tag, Category: category, Reason: reason})
			continue
		}
		kept = append(kept, tag)
	}
	return kept, dropped
}

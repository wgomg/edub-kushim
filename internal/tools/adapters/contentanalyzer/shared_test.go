package contentanalyzer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
)

func TestNormalizeTags_Transforms(t *testing.T) {
	tests := []struct {
		name string
		raw  []string
		want []string
	}{
		{"single word", []string{"anarchism"}, []string{"anarchism"}},
		{"hyphens to spaces", []string{"social-justice"}, []string{"social justice"}},
		{"underscores to spaces", []string{"self_ownership"}, []string{"self ownership"}},
		{"already space-separated", []string{"social justice"}, []string{"social justice"}},
		{"lowercased", []string{"Anarchism"}, []string{"anarchism"}},
		{"mixed case hyphens", []string{"Post-Left-Anarchism"}, []string{"post left anarchism"}},
		{"strips non-alpha", []string{"c++"}, []string{"c"}},
		{"strips digits", []string{"c2"}, []string{"c"}},
		{"collapses spaces", []string{"social   justice"}, []string{"social justice"}},
		{"trims whitespace", []string{"  anarchism  "}, []string{"anarchism"}},
		{"multiple tags", []string{"anarchism", "social justice"}, []string{"anarchism", "social justice"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeTags(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizeTags(%v) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestBuildPrompt_DefaultTemplate(t *testing.T) {
	docTypes := []database.DocumentType{
		{Name: "article", Description: "A written composition"},
	}
	peopleTypes := []database.PeopleType{
		{Name: "author", Description: "Writer of the document"},
	}
	tagSuggestions := []string{"ai", "ml"}

	result := BuildPrompt("sample text", docTypes, peopleTypes, tagSuggestions, "")

	if !strings.Contains(result, "sample text") {
		t.Error("expected output to contain the input text")
	}
	if !strings.Contains(result, "article") {
		t.Error("expected output to contain document type name")
	}
	if !strings.Contains(result, "author") {
		t.Error("expected output to contain people type name")
	}
	if !strings.Contains(result, "ai") {
		t.Error("expected output to contain tag suggestion")
	}
	if !strings.Contains(result, "Analyze the excerpts") {
		t.Error("expected output to start with default prompt header")
	}
	if !strings.Contains(result, "At most 8 thematic tags") {
		t.Error("expected prompt to contain 'At most 8 thematic tags' from RequestedTags field")
	}
}

func TestBuildPrompt_CustomTemplate(t *testing.T) {
	custom := "Custom prompt: {{.Text}} and {{.DocTypePrompt}}"
	result := BuildPrompt("hello world", nil, nil, nil, custom)

	expected := "Custom prompt: hello world and - Document type: choose one of the following:\n"
	if result != expected {
		t.Errorf("custom template output = %q, want %q", result, expected)
	}
}

func TestBuildPrompt_MalformedTemplateFallsBack(t *testing.T) {
	result := BuildPrompt("text", nil, nil, nil, "{{.Invalid")

	if !strings.Contains(result, "text") {
		t.Error("expected fallback to default template with input text")
	}
	if !strings.Contains(result, "Analyze the excerpts") {
		t.Error("expected fallback to default template header")
	}
}

func TestBuildPrompt_ExecutionErrorFallsBack(t *testing.T) {
	result := BuildPrompt("text", nil, nil, nil, "{{.Nonexistent}}")

	if !strings.Contains(result, "text") {
		t.Error("expected fallback to default template with input text")
	}
}

func TestBuildPrompt_WhitespaceCustomTemplateUsesDefault(t *testing.T) {
	result := BuildPrompt("text", nil, nil, nil, "   \n  ")

	if !strings.Contains(result, "Analyze the excerpts") {
		t.Error("expected whitespace-only template to use default")
	}
}

func TestNormalizeTags_AccentFolding(t *testing.T) {
	tests := []struct {
		name string
		raw  []string
		want []string
	}{
		{"folds cafe", []string{"café"}, []string{"cafe"}},
		{"folds muller", []string{"Müller"}, []string{"muller"}},
		{"folds poincare", []string{"Poincaré"}, []string{"poincare"}},
		{"dedup after fold", []string{"café", "cafe"}, []string{"cafe"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeTags(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizeTags(%v) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestFilterTags(t *testing.T) {
	tests := []struct {
		name          string
		tags          []string
		people        []PeopleResult
		title         string
		docTypeNames  []string
		want          []string
	}{
		{
			name: "clean tags pass through",
			tags: []string{"physics", "machine learning"},
			want: []string{"physics", "machine learning"},
		},
		{
			name: "drops tags exceeding three words",
			tags: []string{"artificial intelligence and robotics", "physics"},
			want: []string{"physics"},
		},
		{
			name: "drops tag sharing token with person name",
			tags: []string{"turing", "machine learning"},
			people: []PeopleResult{
				{Name: "Alan Turing", NormalizedName: "alan turing"},
			},
			want: []string{"machine learning"},
		},
		{
			name: "drops tag sharing token with romanized name",
			tags: []string{"muller", "physics"},
			people: []PeopleResult{
				{Name: "Müller", NameRomanized: "muller", NormalizedName: "muller"},
			},
			want: []string{"physics"},
		},
		{
			name:  "drops tag that is subset of title tokens",
			tags:  []string{"machine learning", "deep learning"},
			title: "machine learning applications",
			want:  []string{"deep learning"},
		},
		{
			name:  "keeps tag with partial title overlap",
			tags:  []string{"deep architecture", "neural networks"},
			title: "deep learning applications",
			want:  []string{"deep architecture", "neural networks"},
		},
		{
			name:   "applies all rules together",
			tags:   []string{"turing", "artificial intelligence and robots", "machine learning", "physics"},
			people: []PeopleResult{{Name: "Alan Turing", NormalizedName: "alan turing"}},
			title:  "machine learning",
			want:   []string{"physics"},
		},
		{
			name: "caps at maxTags preserving order",
			tags: []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"},
			want: []string{"alpha", "bravo", "charlie", "delta", "echo"},
		},
		{
			name: "nil people and empty title",
			tags: []string{"physics"},
			want: []string{"physics"},
		},
		{
			name:         "drops tag matching doc-type name token",
			tags:         []string{"letter", "physics"},
			docTypeNames: []string{"letter"},
			want:         []string{"physics"},
		},
		{
			name:         "keeps tag not matching doc-type name token",
			tags:         []string{"physics", "mathematics"},
			docTypeNames: []string{"letter"},
			want:         []string{"physics", "mathematics"},
		},
		{
			name:   "empty tags",
			tags:   []string{},
			people: []PeopleResult{{Name: "Turing", NormalizedName: "turing"}},
			title:  "physics",
			want:   []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterTags(tt.tags, tt.people, tt.title, tt.docTypeNames)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FilterTags(%v, %v, %q, %v) = %v, want %v",
					tt.tags, tt.people, tt.title, tt.docTypeNames, got, tt.want)
			}
		})
	}
}

func TestNormalizeTags_DeduplicatesAndDropsEmpty(t *testing.T) {
	tests := []struct {
		name string
		raw  []string
		want []string
	}{
		{"empty input", []string{}, []string{}},
		{"nil input", nil, []string{}},
		{"dedup after normalize", []string{"Anarchism", "anarchism"}, []string{"anarchism"}},
		{"dedup hyphen vs space", []string{"social-justice", "social justice"}, []string{"social justice"}},
		{"drops all-special tags", []string{"!!!"}, []string{}},
		{"drops empty after strip", []string{"123"}, []string{}},
		{"mixed keep and drop", []string{"anarchism", "!!!"}, []string{"anarchism"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeTags(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizeTags(%v) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

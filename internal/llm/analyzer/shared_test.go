package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/types"
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
		{"keeps symbols", []string{"c++"}, []string{"c++"}},
		{"keeps digits", []string{"c2"}, []string{"c2"}},
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
	docTypes := []TypeRef{
		{Name: "article", Description: "A written composition"},
	}
	peopleTypes := []TypeRef{
		{Name: "author", Description: "Writer of the document"},
	}
	tagSuggestions := []string{"ai", "ml"}

	result := BuildPrompt("sample text", docTypes, peopleTypes, tagSuggestions, "", 3, 5)

	if !strings.Contains(result, "sample text") {
		t.Error("expected output to contain the input text")
	}
	if !strings.Contains(result, "article") {
		t.Error("expected output to contain document type name")
	}
	if !strings.Contains(result, "article (A written composition)") {
		t.Error("expected output to render document type description beside its name")
	}
	if !strings.Contains(result, "author") {
		t.Error("expected output to contain people type name")
	}
	if !strings.Contains(result, "author (Writer of the document)") {
		t.Error("expected output to render people type description beside its name")
	}
	if !strings.Contains(result, "ai") {
		t.Error("expected output to contain tag suggestion")
	}
	if !strings.Contains(result, "Analyze the excerpts") {
		t.Error("expected output to start with default prompt header")
	}
	if !strings.Contains(result, "At least 3 tags") {
		t.Error("expected prompt to contain 'At least 3 tags' from the MinTags field")
	}
	if strings.Contains(defaultPromptTemplate, "{{.MaxTags}}") {
		t.Error("default template must not render the upper tag bound")
	}
}

func TestDefaultPromptTemplate_PublishingFormatBoundary(t *testing.T) {
	wants := []string{
		"what the document is as a publication — its publishing format (a book, article, tutorial, report) — belongs in the document type field",
		"A genre or art form (poetry, science fiction, autobiography) is subject matter and may be a tag",
		"form_genre (the document's publishing format)",
	}
	for _, want := range wants {
		if !strings.Contains(defaultPromptTemplate, want) {
			t.Errorf("default template missing %q", want)
		}
	}
	if strings.Contains(defaultPromptTemplate, "its form or genre") {
		t.Error("default template still carries the superseded form-or-genre clause")
	}
}

func TestBuildPrompt_CustomTemplate(t *testing.T) {
	custom := "Custom prompt: {{.Text}} and {{.DocTypePrompt}}"
	result := BuildPrompt("hello world", nil, nil, nil, custom, 3, 5)

	expected := "Custom prompt: hello world and - Document type: choose one of the following:\n"
	if result != expected {
		t.Errorf("custom template output = %q, want %q", result, expected)
	}
}

func TestBuildPrompt_MalformedTemplateFallsBack(t *testing.T) {
	result := BuildPrompt("text", nil, nil, nil, "{{.Invalid", 3, 5)

	if !strings.Contains(result, "text") {
		t.Error("expected fallback to default template with input text")
	}
	if !strings.Contains(result, "Analyze the excerpts") {
		t.Error("expected fallback to default template header")
	}
}

func TestBuildPrompt_ExecutionErrorFallsBack(t *testing.T) {
	result := BuildPrompt("text", nil, nil, nil, "{{.Nonexistent}}", 3, 5)

	if !strings.Contains(result, "text") {
		t.Error("expected fallback to default template with input text")
	}
}

func TestTagsPrompt_EmptyListReturnsEmpty(t *testing.T) {
	got := tagsPrompt(nil)
	if got != "" {
		t.Errorf("tagsPrompt(nil) = %q, want empty", got)
	}
	got = tagsPrompt([]string{})
	if got != "" {
		t.Errorf("tagsPrompt([]) = %q, want empty", got)
	}
}

func TestBuildPrompt_EmptyTagSuggestions(t *testing.T) {
	docTypes := []TypeRef{
		{Name: "article", Description: "A written composition"},
	}
	result := BuildPrompt("sample text", docTypes, nil, nil, "", 3, 5)

	if !strings.Contains(result, "sample text") {
		t.Error("expected output to contain the input text")
	}
	// Empty tag list must not produce a malformed clause with empty quotes.
	if strings.Contains(result, "''") {
		t.Error("empty tag suggestions must not produce empty quotes in the prompt")
	}
}

func TestBuildPrompt_WhitespaceCustomTemplateUsesDefault(t *testing.T) {
	result := BuildPrompt("text", nil, nil, nil, "   \n  ", 3, 5)

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

// Regression guard for the shared-transform.Chain data race: normalizeCore
// is reached from enrich workers via NormalizeTags/FilterTags, and a shared
// stateful chain panicked with "slice bounds out of range" under overlap.
// Run with -race; each goroutine also pins exact output so corruption (not
// just a panic) fails the test.
func TestNormalizeCore_Concurrent(t *testing.T) {
	inputs := []string{"José García", "Müller", "Poincaré", "Post-Left-Anarchism"}
	wants := []string{"jose garcia", "muller", "poincare", "post left anarchism"}

	const goroutines = 8
	const iterations = 1000

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				for j, in := range inputs {
					if got := normalizeCore(in); got != wants[j] {
						t.Errorf("normalizeCore(%q) = %q, want %q", in, got, wants[j])
						return
					}
				}
			}
		})
	}
	wg.Wait()
}

func TestExtractHeadTailWords(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		headWords int
		tailWords int
		wantHead  string
		wantTail  string
		wantSep   bool
	}{
		{
			name:      "enough words for head and tail",
			content:   "a b c d e f g h i j",
			headWords: 3,
			tailWords: 2,
			wantHead:  "a b c",
			wantTail:  "i j",
			wantSep:   true,
		},
		{
			name:      "content shorter than head+tail",
			content:   "a b c",
			headWords: 5,
			tailWords: 5,
			wantHead:  "a b c",
			wantTail:  "",
			wantSep:   false,
		},
		{
			name:      "empty content",
			content:   "",
			headWords: 10,
			tailWords: 10,
			wantHead:  "",
			wantTail:  "",
			wantSep:   false,
		},
		{
			name:      "zero tail words",
			content:   "a b c d e",
			headWords: 3,
			tailWords: 0,
			wantHead:  "a b c",
			wantTail:  "",
			wantSep:   false,
		},
		{
			name:      "exact head coverage",
			content:   "a b c",
			headWords: 3,
			tailWords: 2,
			wantHead:  "a b c",
			wantTail:  "",
			wantSep:   false,
		},
		{
			name:      "head covers all, tail overlaps",
			content:   "a b c d",
			headWords: 4,
			tailWords: 3,
			wantHead:  "a b c d",
			wantTail:  "",
			wantSep:   false,
		},
		{
			name:      "negative head_words treated as zero",
			content:   "a b c d e",
			headWords: -5,
			tailWords: 2,
			wantHead:  "",
			wantTail:  "d e",
			wantSep:   true,
		},
		{
			name:      "negative tail_words treated as zero",
			content:   "a b c d e",
			headWords: 3,
			tailWords: -2,
			wantHead:  "a b c",
			wantTail:  "",
			wantSep:   false,
		},
		{
			name:      "both negative produces head-only empty output",
			content:   "a b c d e",
			headWords: -1,
			tailWords: -1,
			wantHead:  "",
			wantTail:  "",
			wantSep:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractHeadTailWords(tt.content, tt.headWords, tt.tailWords)
			if !strings.Contains(got, tt.wantHead) {
				t.Errorf("expected head %q in output %q", tt.wantHead, got)
			}
			if tt.wantSep {
				if !strings.Contains(got, "[...]") {
					t.Error("expected separator in output")
				}
			} else {
				if strings.Contains(got, "[...]") {
					t.Error("unexpected separator in output")
				}
			}
			if tt.wantTail != "" && !strings.Contains(got, tt.wantTail) {
				t.Errorf("expected tail %q in output %q", tt.wantTail, got)
			}
		})
	}
}

func TestBuildDocTypePrompt(t *testing.T) {
	docTypes := []TypeRef{
		{Name: "article", Description: "Scholarly article"},
		{Name: "invoice", Description: "Commercial invoice"},
	}
	headTail := "first line\n\n[...]\n\nlast line"

	t.Run("with metadata", func(t *testing.T) {
		metadata := DocMetadata{WordCount: 15234, PageCount: 42, MimeType: "application/pdf"}
		prompt := BuildDocTypePrompt(headTail, docTypes, metadata)

		if !strings.Contains(prompt, "article") {
			t.Error("expected prompt to contain document type name")
		}
		if !strings.Contains(prompt, "article (Scholarly article)") {
			t.Error("expected prompt to render document type description beside its name")
		}
		if !strings.Contains(prompt, "invoice") {
			t.Error("expected prompt to contain second document type name")
		}
		if !strings.Contains(prompt, headTail) {
			t.Error("expected prompt to contain head+tail text")
		}
		if !strings.Contains(prompt, "re-evaluate") {
			t.Error("expected prompt to contain re-evaluation instruction")
		}
		if !strings.Contains(prompt, "Document context:") {
			t.Error("expected prompt to contain Document context when metadata is present")
		}
		if !strings.Contains(prompt, "15234 total words") {
			t.Error("expected prompt to contain word count metadata")
		}
		if !strings.Contains(prompt, "42 pages") {
			t.Error("expected prompt to contain page count metadata")
		}
		if !strings.Contains(prompt, "application/pdf") {
			t.Error("expected prompt to contain mime type metadata")
		}
	})

	t.Run("without metadata", func(t *testing.T) {
		metadata := DocMetadata{}
		prompt := BuildDocTypePrompt(headTail, docTypes, metadata)

		if !strings.Contains(prompt, "article") {
			t.Error("expected prompt to contain document type name")
		}
		if !strings.Contains(prompt, "invoice") {
			t.Error("expected prompt to contain second document type name")
		}
		if !strings.Contains(prompt, headTail) {
			t.Error("expected prompt to contain head+tail text")
		}
		if !strings.Contains(prompt, "re-evaluate") {
			t.Error("expected prompt to contain re-evaluation instruction")
		}
		if strings.Contains(prompt, "Document context:") {
			t.Error("expected no Document context when metadata is zero-valued")
		}
	})
}

func TestDocMetadata_Format(t *testing.T) {
	tests := []struct {
		name     string
		metadata DocMetadata
		want     string
	}{
		{
			name:     "all fields populated",
			metadata: DocMetadata{WordCount: 15234, PageCount: 42, MimeType: "application/pdf"},
			want:     "15234 total words, 42 pages, application/pdf",
		},
		{
			name:     "only word count",
			metadata: DocMetadata{WordCount: 3200},
			want:     "3200 total words",
		},
		{
			name:     "only page count",
			metadata: DocMetadata{PageCount: 5},
			want:     "5 pages",
		},
		{
			name:     "only mime type",
			metadata: DocMetadata{MimeType: "text/plain"},
			want:     "text/plain",
		},
		{
			name:     "all zero",
			metadata: DocMetadata{},
			want:     "",
		},
		{
			name:     "word count and mime type",
			metadata: DocMetadata{WordCount: 1000, MimeType: "application/pdf"},
			want:     "1000 total words, application/pdf",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.metadata.Format()
			if got != tt.want {
				t.Errorf("DocMetadata.Format() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFilterTags(t *testing.T) {
	tests := []struct {
		name            string
		tags            []string
		people          []PeopleResult
		knownNormalized []string
		title           string
		docTypeNames    []string
		want            []string
	}{
		{
			name:            "clean tags pass through",
			tags:            []string{"physics", "machine learning"},
			knownNormalized: nil,
			want:            []string{"physics", "machine learning"},
		},
		{
			name:            "drops tags exceeding three words",
			tags:            []string{"artificial intelligence and robotics", "physics"},
			knownNormalized: nil,
			want:            []string{"physics"},
		},
		{
			name: "drops tag sharing token with person name",
			tags: []string{"turing", "machine learning"},
			people: []PeopleResult{
				{Name: "Alan Turing", NormalizedName: "alan turing"},
			},
			knownNormalized: nil,
			want:            []string{"machine learning"},
		},
		{
			name: "drops tag sharing token with romanized name",
			tags: []string{"muller", "physics"},
			people: []PeopleResult{
				{Name: "Müller", NameRomanized: "muller", NormalizedName: "muller"},
			},
			knownNormalized: nil,
			want:            []string{"physics"},
		},
		{
			name:            "drops tag that is subset of title tokens",
			tags:            []string{"machine learning", "deep learning"},
			knownNormalized: nil,
			title:           "machine learning applications",
			want:            []string{"deep learning"},
		},
		{
			name:            "keeps tag with partial title overlap",
			tags:            []string{"deep architecture", "neural networks"},
			knownNormalized: nil,
			title:           "deep learning applications",
			want:            []string{"deep architecture", "neural networks"},
		},
		{
			name:            "applies all rules together",
			tags:            []string{"turing", "artificial intelligence and robots", "machine learning", "physics"},
			people:          []PeopleResult{{Name: "Alan Turing", NormalizedName: "alan turing"}},
			knownNormalized: nil,
			title:           "machine learning",
			want:            []string{"physics"},
		},
		{
			name:            "keeps every tag preserving order",
			tags:            []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"},
			knownNormalized: nil,
			want:            []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"},
		},
		{
			name:            "nil people and empty title",
			tags:            []string{"physics"},
			knownNormalized: nil,
			want:            []string{"physics"},
		},
		{
			name:            "drops tag matching doc-type name token",
			tags:            []string{"letter", "physics"},
			knownNormalized: nil,
			docTypeNames:    []string{"letter"},
			want:            []string{"physics"},
		},
		{
			name:            "keeps tag not matching doc-type name token",
			tags:            []string{"physics", "mathematics"},
			knownNormalized: nil,
			docTypeNames:    []string{"letter"},
			want:            []string{"physics", "mathematics"},
		},
		{
			name:            "empty tags",
			tags:            []string{},
			people:          []PeopleResult{{Name: "Turing", NormalizedName: "turing"}},
			knownNormalized: nil,
			title:           "physics",
			want:            []string{},
		},
		{
			name:            "drops tag that equals a subset of a known person name",
			tags:            []string{"john doe", "physics"},
			knownNormalized: []string{"john doe smith"},
			want:            []string{"physics"},
		},
		{
			name:            "keeps single-word tag sharing token with longer known name",
			tags:            []string{"young", "activism"},
			knownNormalized: []string{"andrew young"},
			want:            []string{"young", "activism"},
		},
		{
			name:            "drops multi-word tag that exactly matches a known name",
			tags:            []string{"rosa luxemburg", "marxism"},
			knownNormalized: []string{"rosa luxemburg"},
			want:            []string{"marxism"},
		},
		{
			name:            "keeps tag with known people present that does not match",
			tags:            []string{"algebra", "topology"},
			knownNormalized: []string{"alan turing", "andrew young"},
			want:            []string{"algebra", "topology"},
		},
		{
			name:            "drops 3-word tag that is subset of a known name",
			tags:            []string{"john doe smith", "physics"},
			knownNormalized: []string{"john doe smith jones"},
			want:            []string{"physics"},
		},
		{
			name:            "keeps multi-word tag whose tokens are split across different known people",
			tags:            []string{"john smith", "physics"},
			knownNormalized: []string{"john doe", "smith jones"},
			want:            []string{"john smith", "physics"},
		},
		{
			name:            "skips empty-string entries in knownNormalized",
			tags:            []string{"john doe", "physics"},
			knownNormalized: []string{"", "john doe smith"},
			want:            []string{"physics"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterTags(tt.tags, NewTagFilter(tt.people, tt.knownNormalized, tt.title, tt.docTypeNames))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FilterTags(%v, %v, %v, %q, %v) = %v, want %v",
					tt.tags, tt.people, tt.knownNormalized, tt.title, tt.docTypeNames, got, tt.want)
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
		{"keeps digits-only", []string{"123"}, []string{"123"}},
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

func TestCheckContentTooLarge(t *testing.T) {
	tests := []struct {
		name    string
		caps    *llm.ModelCapability
		prompt  string
		wantNil bool
	}{
		{"nil caps", nil, "hello world", true},
		{"zero max input tokens", &llm.ModelCapability{MaxInputTokens: 0}, "hello world", true},
		{"negative max input tokens", &llm.ModelCapability{MaxInputTokens: -1}, "hello world", true},
		{"prompt within limit", &llm.ModelCapability{MaxInputTokens: 1000000}, "short prompt", true},
		{"prompt exceeds limit", &llm.ModelCapability{MaxInputTokens: 5}, "this is a much longer prompt that exceeds the limit", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkContentTooLarge(tt.caps, tt.prompt)
			if tt.wantNil && err != nil {
				t.Errorf("checkContentTooLarge() = %v, want nil", err)
			}
			if !tt.wantNil && err == nil {
				t.Error("checkContentTooLarge() = nil, want error")
			}
			if !tt.wantNil {
				if _, ok := errors.AsType[*ContentTooLargeError](err); !ok {
					t.Errorf("expected ContentTooLargeError, got %T", err)
				}
			}
		})
	}
}

func TestParseInsufficientCreditsError(t *testing.T) {
	tests := []struct {
		name       string
		body       []byte
		statusCode int
		provider   string
		wantNil    bool
		wantStatus int
		wantProv   string
	}{
		{"402 any provider", []byte("Payment Required"), 402, "openai", false, 402, "openai"},
		{"429 any provider", []byte("rate limited"), 429, "anthropic", false, 429, "anthropic"},
		{"400 qwen arrearage", []byte(`{"error":{"code":"Arrearage"}}`), 400, "qwen", false, 400, "qwen"},
		{"400 qwen arrearage case-insensitive", []byte(`{"error":"Arrearage"}`), 400, "qwen", false, 400, "qwen"},
		{"400 non-qwen ignores arrearage", []byte(`{"error":"Arrearage"}`), 400, "openai", true, 0, ""},
		{"400 qwen no arrearage", []byte(`{"error":"bad request"}`), 400, "qwen", true, 0, ""},
		{"401 unauthorized", []byte("Unauthorized"), 401, "openai", true, 0, ""},
		{"500 server error", []byte("Internal Server Error"), 500, "openai", true, 0, ""},
		{"200 ok", []byte(`{"choices":[]}`), 200, "openai", true, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseInsufficientCreditsError(tt.body, tt.statusCode, tt.provider)
			if tt.wantNil {
				if err != nil {
					t.Errorf("parseInsufficientCreditsError() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("parseInsufficientCreditsError() = nil, want error")
			}
			var credErr *InsufficientCreditsError
			if !errors.As(err, &credErr) {
				t.Fatalf("expected InsufficientCreditsError, got %T: %v", err, err)
			}
			if credErr.HTTPStatus != tt.wantStatus {
				t.Errorf("HTTPStatus = %d, want %d", credErr.HTTPStatus, tt.wantStatus)
			}
			if credErr.Provider != tt.wantProv {
				t.Errorf("Provider = %q, want %q", credErr.Provider, tt.wantProv)
			}
			if credErr.RawBody != string(tt.body) {
				t.Errorf("RawBody = %q, want %q", credErr.RawBody, string(tt.body))
			}
		})
	}
}

func TestInsufficientCreditsError_Message(t *testing.T) {
	err := &InsufficientCreditsError{Provider: "openai", HTTPStatus: 402}
	want := "insufficient credits (openai, HTTP 402): the provider account has run out of credits or has billing issues"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestParseTokenLimitError(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		wantNil bool
		wantMax int
		wantReq int
	}{
		{"no match", []byte("something went wrong"), true, 0, 0},
		{"empty body", nil, true, 0, 0},
		{"valid openai format", []byte("maximum context length is 200000 tokens but you requested 250000 tokens"), false, 200000, 250000},
		{"valid with about prefix", []byte("maximum context length is 100000 tokens but you requested about 150000 tokens"), false, 100000, 150000},
		{"zero max tokens", []byte("maximum context length is 0 tokens but you requested 100 tokens"), true, 0, 0},
		{"zero requested tokens", []byte("maximum context length is 100 tokens but you requested 0 tokens"), true, 0, 0},
		{"partial match only first number", []byte("maximum context length is 100 tokens but no request info"), true, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseTokenLimitError(tt.body)
			if tt.wantNil && err != nil {
				t.Errorf("parseTokenLimitError() = %v, want nil", err)
			}
			if !tt.wantNil && err == nil {
				t.Error("parseTokenLimitError() = nil, want error")
			}
			if !tt.wantNil {
				var tle *TokenLimitError
				if !errors.As(err, &tle) {
					t.Fatalf("expected TokenLimitError, got %T", err)
				}
				if tle.MaxTokens != tt.wantMax {
					t.Errorf("MaxTokens = %d, want %d", tle.MaxTokens, tt.wantMax)
				}
				if tle.RequestedTokens != tt.wantReq {
					t.Errorf("RequestedTokens = %d, want %d", tle.RequestedTokens, tt.wantReq)
				}
			}
		})
	}
}

func TestSleepAfterRequest_Disabled(t *testing.T) {
	for _, delay := range []float64{0, -1} {
		start := time.Now()
		sleepAfterRequest(context.Background(), delay)
		if elapsed := time.Since(start); elapsed >= 100*time.Millisecond {
			t.Errorf("sleepAfterRequest(%v) slept %v, want immediate return", delay, elapsed)
		}
	}
}

func TestSleepAfterRequest_Sleeps(t *testing.T) {
	start := time.Now()
	sleepAfterRequest(context.Background(), 0.1)
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("sleepAfterRequest(0.1) returned after %v, want >= 100ms", elapsed)
	}
}

func TestSleepAfterRequest_CancelledContextAbortsEarly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	sleepAfterRequest(ctx, 1)
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Errorf("sleepAfterRequest slept %v after cancellation, want early abort", elapsed)
	}
}

func TestParseAdjudicationResponse(t *testing.T) {
	same := types.TagVerdicts.Same
	variant := types.TagVerdicts.Variant

	tests := []struct {
		name    string
		content string
		n       int
		want    []TagVerdictResult
	}{
		{
			name:    "clean JSON with all valid verdicts",
			content: `{"pairs":[{"index":0,"verdict":"same"},{"index":1,"verdict":"variant"}]}`,
			n:       2,
			want: []TagVerdictResult{
				{Index: 0, Verdict: same},
				{Index: 1, Verdict: variant},
			},
		},
		{
			name:    "code-fenced JSON",
			content: "```json\n{\"pairs\":[{\"index\":0,\"verdict\":\"same\"}]}\n```",
			n:       1,
			want:    []TagVerdictResult{{Index: 0, Verdict: same}},
		},
		{
			name:    "unknown verdict string is dropped",
			content: `{"pairs":[{"index":0,"verdict":"foobar"}]}`,
			n:       1,
			want:    []TagVerdictResult{{Index: 0, Verdict: ""}},
		},
		{
			name:    "out-of-range index is dropped",
			content: `{"pairs":[{"index":5,"verdict":"same"}]}`,
			n:       2,
			want: []TagVerdictResult{
				{Index: 0, Verdict: ""},
				{Index: 1, Verdict: ""},
			},
		},
		{
			name:    "short slice leaves trailing entries ambiguous",
			content: `{"pairs":[{"index":0,"verdict":"same"}]}`,
			n:       3,
			want: []TagVerdictResult{
				{Index: 0, Verdict: same},
				{Index: 1, Verdict: ""},
				{Index: 2, Verdict: ""},
			},
		},
		{
			name:    "bare top-level array accepted as the envelope",
			content: `[{"index":0,"verdict":"same"},{"index":1,"verdict":"variant"}]`,
			n:       2,
			want: []TagVerdictResult{
				{Index: 0, Verdict: same},
				{Index: 1, Verdict: variant},
			},
		},
		{
			name:    "entries without index placed by position",
			content: `{"pairs":[{"verdict":"same","direction":"query"},{"verdict":"variant"}]}`,
			n:       2,
			want: []TagVerdictResult{
				{Index: 0, Verdict: same, Direction: TagDirectionQuery},
				{Index: 1, Verdict: variant},
			},
		},
		{
			name:    "mixed indexed and unindexed entries",
			content: `{"pairs":[{"verdict":"same"},{"index":2,"verdict":"variant"}]}`,
			n:       3,
			want: []TagVerdictResult{
				{Index: 0, Verdict: same},
				{Index: 1, Verdict: ""},
				{Index: 2, Verdict: variant},
			},
		},
		{
			name:    "unindexed entries beyond n dropped",
			content: `{"pairs":[{"verdict":"same"},{"verdict":"variant"},{"verdict":"same"}]}`,
			n:       2,
			want: []TagVerdictResult{
				{Index: 0, Verdict: same},
				{Index: 1, Verdict: variant},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAdjudicationResponse(tt.content, tt.n)
			if err != nil {
				t.Fatalf("ParseAdjudicationResponse: unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseAdjudicationResponse = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseAdjudicationResponse_InvalidJSON(t *testing.T) {
	_, err := ParseAdjudicationResponse("not json", 2)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestBuildTagSplitPrompt(t *testing.T) {
	result := BuildTagSplitPrompt([]string{"supply and demand", "physics"})
	if !strings.Contains(result, `"supply and demand"`) {
		t.Error("prompt should contain first tag")
	}
	if !strings.Contains(result, `"physics"`) {
		t.Error("prompt should contain second tag")
	}
	if !strings.Contains(result, "Tag 0") || !strings.Contains(result, "Tag 1") {
		t.Error("prompt should contain indexed tag entries")
	}
	if !strings.Contains(result, "split") || !strings.Contains(result, "keep") {
		t.Error("prompt should mention both verdict options")
	}
	if !strings.Contains(result, "never force a split") {
		t.Error("split criteria must keep unsettled readings instead of forcing a split")
	}
}

func TestParseTagSplitResponse(t *testing.T) {
	tests := []struct {
		name    string
		content string
		n       int
		want    []TagSplitResult
	}{
		{
			name:    "keep and split",
			content: `{"splits":[{"index":0,"verdict":"keep"},{"index":1,"verdict":"split","components":["supply","demand"]}]}`,
			n:       2,
			want: []TagSplitResult{
				{Verdict: TagSplitKeep},
				{Verdict: TagSplitSplit, Components: []string{"supply", "demand"}},
			},
		},
		{
			name:    "all keep",
			content: `{"splits":[{"index":0,"verdict":"keep"},{"index":1,"verdict":"keep"}]}`,
			n:       2,
			want: []TagSplitResult{
				{Verdict: TagSplitKeep},
				{Verdict: TagSplitKeep},
			},
		},
		{
			name:    "out of range index ignored",
			content: `{"splits":[{"index":5,"verdict":"split","components":["a","b"]}]}`,
			n:       2,
			want:    []TagSplitResult{{}, {}},
		},
		{
			name:    "single component treated as ambiguous",
			content: `{"splits":[{"index":0,"verdict":"split","components":["only one"]}]}`,
			n:       1,
			want:    []TagSplitResult{{}},
		},
		{
			name:    "empty components treated as ambiguous",
			content: `{"splits":[{"index":0,"verdict":"split","components":[]}]}`,
			n:       1,
			want:    []TagSplitResult{{}},
		},
		{
			name:    "unknown verdict treated as ambiguous",
			content: `{"splits":[{"index":0,"verdict":"maybe"}]}`,
			n:       1,
			want:    []TagSplitResult{{}},
		},
		{
			name:    "empty splits returns all defaults",
			content: `{"splits":[]}`,
			n:       2,
			want:    []TagSplitResult{{}, {}},
		},
		{
			name:    "code block stripped",
			content: "```json\n{\"splits\":[{\"index\":0,\"verdict\":\"keep\"}]}\n```",
			n:       1,
			want: []TagSplitResult{
				{Verdict: TagSplitKeep},
			},
		},
		{
			name:    "components trimmed of whitespace",
			content: `{"splits":[{"index":0,"verdict":"split","components":["  supply  ","  demand  "]}]}`,
			n:       1,
			want: []TagSplitResult{
				{Verdict: TagSplitSplit, Components: []string{"supply", "demand"}},
			},
		},
		{
			name:    "empty components filtered",
			content: `{"splits":[{"index":0,"verdict":"split","components":["supply","","demand"]}]}`,
			n:       1,
			want: []TagSplitResult{
				{Verdict: TagSplitSplit, Components: []string{"supply", "demand"}},
			},
		},
		{
			name:    "bare top-level array accepted as the envelope",
			content: `[{"index":0,"verdict":"keep"},{"index":1,"verdict":"split","components":["supply","demand"]}]`,
			n:       2,
			want: []TagSplitResult{
				{Verdict: TagSplitKeep},
				{Verdict: TagSplitSplit, Components: []string{"supply", "demand"}},
			},
		},
		{
			name:    "entries without index placed by position",
			content: `{"splits":[{"verdict":"keep"},{"verdict":"split","components":["supply","demand"]}]}`,
			n:       2,
			want: []TagSplitResult{
				{Verdict: TagSplitKeep},
				{Verdict: TagSplitSplit, Components: []string{"supply", "demand"}},
			},
		},
		{
			name:    "unindexed entries beyond n dropped",
			content: `{"splits":[{"verdict":"keep"},{"verdict":"keep"},{"verdict":"split","components":["a","b"]}]}`,
			n:       2,
			want: []TagSplitResult{
				{Verdict: TagSplitKeep},
				{Verdict: TagSplitKeep},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTagSplitResponse(tt.content, tt.n)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i].Verdict != tt.want[i].Verdict {
					t.Errorf("[%d].Verdict = %q, want %q", i, got[i].Verdict, tt.want[i].Verdict)
				}
				if !reflect.DeepEqual(got[i].Components, tt.want[i].Components) {
					t.Errorf("[%d].Components = %v, want %v", i, got[i].Components, tt.want[i].Components)
				}
			}
		})
	}
}

func TestParseTagSplitResponse_InvalidJSON(t *testing.T) {
	_, err := ParseTagSplitResponse("not json", 1)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestSplitComponents(t *testing.T) {
	filter := NewTagFilter(nil, nil, "", nil)
	tests := []struct {
		name string
		raw  []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"dedup", []string{"supply", "supply"}, []string{"supply"}},
		{"drops empty", []string{"supply", "", "demand"}, []string{"supply", "demand"}},
		{"normalizes", []string{"  Supply  ", "DEMAND"}, []string{"supply", "demand"}},
		{"drops four words", []string{"one two three four"}, []string{}},
		{"drops too long", []string{strings.Repeat("x", maxTagLength+1)}, []string{}},
		{"keeps three words", []string{"supply chain management"}, []string{"supply chain management"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitComponents(tt.raw, filter)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitComponents(%v) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSplitComponents_AppliesDocumentFilter(t *testing.T) {
	people := []PeopleResult{{NormalizedName: "John Smith"}}
	filter := NewTagFilter(people, nil, "machine learning applications", []string{"research article"})
	got := SplitComponents([]string{"John Smith", "machine learning", "physics", "research article"}, filter)
	want := []string{"machine learning", "physics"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SplitComponents = %v, want %v", got, want)
	}
}

func TestParseAnalysisResponse(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    *AnalysisResult
		wantErr bool
	}{
		{
			name:    "invalid JSON",
			content: "not json",
			wantErr: true,
		},
		{
			name:    "non-array tags",
			content: `{"title":"t","tags":"bad"}`,
			wantErr: true,
		},
		{
			name:    "null tags",
			content: `{"title":"t","tags":null}`,
			want:    &AnalysisResult{Title: "t", Tags: []string{}, TagDetails: []TagDetail{}},
		},
		{
			name:    "empty tags array",
			content: `{"title":"t","tags":[]}`,
			want:    &AnalysisResult{Title: "t", Tags: []string{}, TagDetails: []TagDetail{}},
		},
		{
			name:    "missing tags key",
			content: `{"title":"t"}`,
			want:    &AnalysisResult{Title: "t", Tags: []string{}, TagDetails: []TagDetail{}},
		},
		{
			name:    "string tags",
			content: `{"title":"t","tags":["opioid crisis","physics"]}`,
			want: &AnalysisResult{
				Title:      "t",
				Tags:       []string{"opioid crisis", "physics"},
				TagDetails: []TagDetail{},
			},
		},
		{
			name:    "object tags",
			content: `{"title":"t","tags":[{"term":"opioid crisis","category":"topic"}]}`,
			want: &AnalysisResult{
				Title: "t",
				Tags:  []string{"opioid crisis"},
				TagDetails: []TagDetail{
					{Term: "opioid crisis", Category: TagCategoryTopic},
				},
			},
		},
		{
			name:    "object tags unknown category",
			content: `{"tags":[{"term":"x","category":"banana"}]}`,
			want: &AnalysisResult{
				Tags:       []string{"x"},
				TagDetails: []TagDetail{{Term: "x", Category: TagCategoryOther}},
			},
		},
		{
			name:    "mixed string and object tags",
			content: `{"tags":["simple",{"term":"typed","category":"place"}]}`,
			want: &AnalysisResult{
				Tags: []string{"simple", "typed"},
				TagDetails: []TagDetail{
					{Term: "typed", Category: TagCategoryPlace},
				},
			},
		},
		{
			name:    "object without term skipped",
			content: `{"tags":[{"category":"topic"},"valid"]}`,
			want: &AnalysisResult{
				Tags:       []string{"valid"},
				TagDetails: []TagDetail{},
			},
		},
		{
			name:    "object with empty term skipped",
			content: `{"tags":[{"term":"  ","category":"topic"},"valid"]}`,
			want: &AnalysisResult{
				Tags:       []string{"valid"},
				TagDetails: []TagDetail{},
			},
		},
		{
			name:    "malformed element in array skipped",
			content: `{"tags":[123,"valid"]}`,
			want: &AnalysisResult{
				Tags:       []string{"valid"},
				TagDetails: []TagDetail{},
			},
		},
		{
			name:    "category folding",
			content: `{"tags":[{"term":"a","category":"Form Genre"},{"term":"b","category":"form-genre"},{"term":"c","category":"  Topic  "}]}`,
			want: &AnalysisResult{
				Tags: []string{"a", "b", "c"},
				TagDetails: []TagDetail{
					{Term: "a", Category: TagCategoryFormGenre},
					{Term: "b", Category: TagCategoryFormGenre},
					{Term: "c", Category: TagCategoryTopic},
				},
			},
		},
		{
			name:    "title docType people language",
			content: `{"title":"My Doc","type":"article","people":[{"name":"Jane","type":"author"}],"language":"eng"}`,
			want: &AnalysisResult{
				Title:      "My Doc",
				DocType:    "article",
				People:     []PeopleResult{{Name: "Jane", Type: "author"}},
				Language:   "eng",
				Tags:       []string{},
				TagDetails: []TagDetail{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAnalysisResponse(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Title != tt.want.Title {
				t.Errorf("Title = %q, want %q", got.Title, tt.want.Title)
			}
			if got.DocType != tt.want.DocType {
				t.Errorf("DocType = %q, want %q", got.DocType, tt.want.DocType)
			}
			if !reflect.DeepEqual(got.Tags, tt.want.Tags) {
				t.Errorf("Tags = %v, want %v", got.Tags, tt.want.Tags)
			}
			if !reflect.DeepEqual(got.TagDetails, tt.want.TagDetails) {
				t.Errorf("TagDetails = %+v, want %+v", got.TagDetails, tt.want.TagDetails)
			}
			if !reflect.DeepEqual(got.People, tt.want.People) {
				t.Errorf("People = %+v, want %+v", got.People, tt.want.People)
			}
			if got.Language != tt.want.Language {
				t.Errorf("Language = %q, want %q", got.Language, tt.want.Language)
			}
		})
	}
}

func TestSeparateFacets(t *testing.T) {
	tests := []struct {
		name         string
		tags         []string
		details      []TagDetail
		docTypeNames []string
		wantKept     []string
		wantDropped  []FacetDrop
	}{
		{
			name:     "nil details passthrough",
			tags:     []string{"physics"},
			details:  nil,
			wantKept: []string{"physics"},
		},
		{
			name:     "empty details passthrough",
			tags:     []string{"physics"},
			details:  []TagDetail{},
			wantKept: []string{"physics"},
		},
		{
			name:        "form_genre dropped, topic kept",
			tags:        []string{"physics", "literary anthology"},
			details:     []TagDetail{{Term: "physics", Category: TagCategoryTopic}, {Term: "literary anthology", Category: TagCategoryFormGenre}},
			wantKept:    []string{"physics"},
			wantDropped: []FacetDrop{{Term: "literary anthology", Category: TagCategoryFormGenre, Reason: "model_self_classification"}},
		},
		{
			name:        "topic and whitelisted place kept, sub-national place dropped",
			tags:        []string{"physics", "palestine", "newfoundland"},
			details:     []TagDetail{{Term: "physics", Category: TagCategoryTopic}, {Term: "palestine", Category: TagCategoryPlace}, {Term: "newfoundland", Category: TagCategoryPlace}},
			wantKept:    []string{"physics", "palestine"},
			wantDropped: []FacetDrop{{Term: "newfoundland", Category: TagCategoryPlace, Reason: "non_whitelisted_place"}},
		},
		{
			name:         "matches_document_type when term matches docTypeNames",
			tags:         []string{"physics"},
			details:      []TagDetail{{Term: "physics", Category: TagCategoryFormGenre}},
			docTypeNames: []string{"physics"},
			wantKept:     []string{},
			wantDropped:  []FacetDrop{{Term: "physics", Category: TagCategoryFormGenre, Reason: "matches_document_type"}},
		},
		{
			name:         "model_self_classification when term does not match docTypeNames",
			tags:         []string{"physics"},
			details:      []TagDetail{{Term: "physics", Category: TagCategoryFormGenre}},
			docTypeNames: []string{"letter"},
			wantKept:     []string{},
			wantDropped:  []FacetDrop{{Term: "physics", Category: TagCategoryFormGenre, Reason: "model_self_classification"}},
		},
		{
			name:        "normalization aligns detail and tag",
			tags:        []string{"form genre"},
			details:     []TagDetail{{Term: "Form-Genre", Category: TagCategoryFormGenre}},
			wantKept:    []string{},
			wantDropped: []FacetDrop{{Term: "form genre", Category: TagCategoryFormGenre, Reason: "model_self_classification"}},
		},
		{
			name:        "person dropped",
			tags:        []string{"physics", "marie curie"},
			details:     []TagDetail{{Term: "physics", Category: TagCategoryTopic}, {Term: "marie curie", Category: TagCategoryPerson}},
			wantKept:    []string{"physics"},
			wantDropped: []FacetDrop{{Term: "marie curie", Category: TagCategoryPerson, Reason: "people_facet"}},
		},
		{
			name:        "work dropped",
			tags:        []string{"physics", "origin of species"},
			details:     []TagDetail{{Term: "physics", Category: TagCategoryTopic}, {Term: "origin of species", Category: TagCategoryWork}},
			wantKept:    []string{"physics"},
			wantDropped: []FacetDrop{{Term: "origin of species", Category: TagCategoryWork, Reason: "forbidden_category"}},
		},
		{
			name:     "topic and other always survive",
			tags:     []string{"physics", "misc"},
			details:  []TagDetail{{Term: "physics", Category: TagCategoryTopic}, {Term: "misc", Category: TagCategoryOther}},
			wantKept: []string{"physics", "misc"},
		},
		{
			name:     "untyped tag without detail row kept",
			tags:     []string{"physics", "orphan"},
			details:  []TagDetail{{Term: "physics", Category: TagCategoryTopic}},
			wantKept: []string{"physics", "orphan"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKept, gotDropped := SeparateFacets(tt.tags, tt.details, tt.docTypeNames)
			if !reflect.DeepEqual(gotKept, tt.wantKept) {
				t.Errorf("kept = %v, want %v", gotKept, tt.wantKept)
			}
			if !reflect.DeepEqual(gotDropped, tt.wantDropped) {
				t.Errorf("dropped = %+v, want %+v", gotDropped, tt.wantDropped)
			}
		})
	}
}

func TestIsWhitelistedPlace(t *testing.T) {
	tests := []struct {
		name string
		term string
		want bool
	}{
		// Countries.
		{"spain", "spain", true},
		{"united states", "united states", true},
		{"palestine", "palestine", true},
		{"taiwan", "taiwan", true},
		{"kosovo", "kosovo", true},
		// Continents.
		{"europe", "europe", true},
		{"africa", "africa", true},
		{"north america", "north america", true},
		// Aliases.
		{"usa", "usa", true},
		{"uk", "uk", true},
		{"uae", "uae", true},
		{"drc", "drc", true},
		{"great britain", "great britain", true},
		{"czech republic", "czech republic", true},
		{"holland", "holland", true},
		{"cape verde", "cape verde", true},
		{"swaziland", "swaziland", true},
		// Dotted forms.
		{"u.s.a.", "u.s.a.", true},
		{"u.k.", "u.k.", true},
		{"u.s.", "u.s.", true},
		// Article forms.
		{"the philippines", "the philippines", true},
		{"the gambia", "the gambia", true},
		{"the netherlands", "the netherlands", true},
		{"the bahamas", "the bahamas", true},
		{"the united states", "the united states", true},
		{"the united kingdom", "the united kingdom", true},
		// Case folding.
		{"Spain", "Spain", true},
		{"USA", "USA", true},
		{"The Philippines", "The Philippines", true},
		// Punctuation folding.
		{"Cote d'Ivoire", "Cote d'Ivoire", true},
		{"Timor-Leste", "Timor-Leste", true},
		// Sub-national misses.
		{"newfoundland", "newfoundland", false},
		{"the hague", "the hague", false},
		{"london", "london", false},
		{"california", "california", false},
		// Empty/odd.
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsWhitelistedPlace(tt.term)
			if got != tt.want {
				t.Errorf("IsWhitelistedPlace(%q) = %v, want %v", tt.term, got, tt.want)
			}
		})
	}
}

func TestPlaceWhitelist_Hygiene(t *testing.T) {
	seen := make(map[string]string, len(placeWhitelist))
	for _, raw := range allWhitelistEntries() {
		key := placeWhitelistKey(raw)
		if prev, dup := seen[key]; dup {
			t.Errorf("duplicate normalized key %q from entries %q and %q", key, prev, raw)
		}
		seen[key] = raw
	}
	for _, raw := range allWhitelistEntries() {
		if !IsWhitelistedPlace(raw) {
			t.Errorf("whitelist entry %q does not survive its own lookup", raw)
		}
	}
}

// allWhitelistEntries returns all raw entries (continents + countries).
func allWhitelistEntries() []string {
	all := make([]string, 0, len(continentNames)+len(countryNames))
	all = append(all, continentNames...)
	all = append(all, countryNames...)
	return all
}

func TestNormalizeTagCategory(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"topic", "topic", TagCategoryTopic},
		{"form_genre", "form_genre", TagCategoryFormGenre},
		{"place", "place", TagCategoryPlace},
		{"person", "person", TagCategoryPerson},
		{"work", "work", TagCategoryWork},
		{"case fold", "Topic", TagCategoryTopic},
		{"hyphen variant", "form-genre", TagCategoryFormGenre},
		{"space variant", "form genre", TagCategoryFormGenre},
		{"whitespace trimmed", "  Topic  ", TagCategoryTopic},
		{"unknown category", "banana", TagCategoryOther},
		{"empty string", "", TagCategoryOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeTagCategory(tt.raw)
			if got != tt.want {
				t.Errorf("normalizeTagCategory(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestBuildTagQualityPrompt(t *testing.T) {
	candidates := []TagQualityCandidate{
		{Index: 0, Term: "quantum computing", Category: "topic"},
		{Index: 1, Term: "spain", Category: "place", WhitelistedPlace: true},
		{Index: 2, Term: "John Smith", Category: "person"},
	}
	prompt := BuildTagQualityPrompt("A text about quantum physics", candidates)

	// Must include the document text.
	if !strings.Contains(prompt, "A text about quantum physics") {
		t.Error("prompt missing document text")
	}
	// Must list each candidate with its term and category.
	for _, c := range candidates {
		if !strings.Contains(prompt, fmt.Sprintf("Tag %d: term=%q category=%s", c.Index, c.Term, c.Category)) {
			t.Errorf("prompt missing candidate %d", c.Index)
		}
	}
	// Whitelisted place must include the carve-out note with the expected verdict.
	if !strings.Contains(prompt, "answer not_person_place_work: true") {
		t.Error("prompt missing expected verdict in whitelisted place note")
	}
	// Criterion 3 must name the passing class (whitelisted) and the failing classes (sub-national) explicitly.
	if !strings.Contains(prompt, "passes this criterion") {
		t.Error("prompt missing whitelisted-passes clause in criterion 3")
	}
	if !strings.Contains(prompt, "sub-national place") {
		t.Error("prompt missing sub-national-place clause in criterion 3")
	}
	// Must contain the five criteria, the abstention and the response format.
	for _, kw := range []string{"subject_matter", "dimension", "not_person_place_work", "atomic", "not_form_genre", "defer", "JSON"} {
		if !strings.Contains(prompt, kw) {
			t.Errorf("prompt missing keyword %q", kw)
		}
	}
	// The rubric must ask five numbered questions, not four plus the key
	// appearing only in the JSON example.
	if !strings.Contains(prompt, "five yes/no questions") {
		t.Error("prompt missing the five-question count")
	}
	if !strings.Contains(prompt, "5. not_form_genre") {
		t.Error("prompt missing the fifth numbered question")
	}
}

func TestBuildTagQualityPrompt_RestoresGenreAndLanguageBoundary(t *testing.T) {
	prompt := BuildTagQualityPrompt("A text about poetry", []TagQualityCandidate{{Index: 0, Term: "poetry", Category: "topic"}})

	for _, want := range []string{
		"5. not_form_genre: is the tag not a publishing format of the document?",
		"not a publishing format: it passes this criterion and is judged on the questions above",
		"The document's language is never a subject matter",
		"never a content dimension (spanish, english)",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("quality prompt missing %q", want)
		}
	}
	for _, gone := range []string{"neither a form nor a genre", "not its form or genre"} {
		if strings.Contains(prompt, gone) {
			t.Errorf("superseded wording %q still present in the quality prompt", gone)
		}
	}
}

func TestBuildAdjudicationPrompt_SameReachableForMeaningMatches(t *testing.T) {
	prompt := BuildAdjudicationPrompt([]TagPairEvidence{{Query: "labour", Target: "labor", Sim: 0.93}})

	for _, want := range []string{
		`Use "same" exactly when faithful replacement holds`,
		`spelling and morphographic variants of one term (labour/labor, modelling/modeling)`,
		`Reserve "variant" and "related" for genuinely distinct concepts`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("adjudication prompt missing %q", want)
		}
	}
	if strings.Contains(prompt, "when in doubt") {
		t.Error("adjudication prompt still carries the doubt clause that closes the same path")
	}
}

func TestParseTagQualityResponse_RoundTrip(t *testing.T) {
	candidates := []TagQualityCandidate{
		{Index: 0, Term: "foo", Category: "topic"},
		{Index: 1, Term: "bar", Category: "topic"},
	}

	// Simulate a well-formed LLM response.
	allTrue := `{"tags":[
		{"index":0,"subject_matter":true,"dimension":true,"not_person_place_work":true,"atomic":true,"not_form_genre":true,"defer":false},
		{"index":1,"subject_matter":false,"dimension":true,"not_person_place_work":true,"atomic":true,"not_form_genre":true,"defer":false}
	]}`
	results, err := ParseTagQualityResponse(allTrue, len(candidates))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !results[0].SubjectMatter || !results[0].Dimension || !results[0].NotPersonPlaceWork || !results[0].Atomic || !results[0].NotFormGenre || results[0].Defer {
		t.Errorf("candidate 0 answers = %+v, want all criteria true and no defer", results[0])
	}
	if results[1].SubjectMatter || !results[1].Dimension {
		t.Errorf("candidate 1 answers = %+v, want subject_matter=false", results[1])
	}

	// Prompt builder should produce a non-empty prompt.
	prompt := BuildTagQualityPrompt("doc text", candidates)
	if prompt == "" {
		t.Error("prompt is empty")
	}
}

func TestParseTagQualityResponse_MissingIndex(t *testing.T) {
	// Only 1 of 2 candidates returned — must error.
	partial := `{"tags":[{"index":0,"subject_matter":true,"dimension":true,"not_person_place_work":true,"atomic":true,"not_form_genre":true}]}`
	results, err := ParseTagQualityResponse(partial, 2)
	if err == nil {
		t.Fatal("expected error for missing index, got nil")
	}
	incomplete, ok := errors.AsType[*IncompleteReplyError](err)
	if !ok {
		t.Fatalf("error type = %T, want *IncompleteReplyError carrying the missing indices", err)
	}
	if len(incomplete.Missing) != 1 || incomplete.Missing[0] != 1 {
		t.Errorf("Missing = %v, want [1]", incomplete.Missing)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results even on error, got %d", len(results))
	}
	// Index 0 should be populated, index 1 should be zero-valued.
	if !results[0].SubjectMatter || !results[0].Dimension {
		t.Errorf("index 0 answers = %+v, want them populated", results[0])
	}
	if results[1].SubjectMatter || results[1].Dimension || results[1].NotPersonPlaceWork || results[1].Atomic || results[1].NotFormGenre || results[1].Defer {
		t.Errorf("index 1 answers = %+v, want zero-valued (all false)", results[1])
	}
}

func TestParseTagQualityResponse_IncompleteVerdict(t *testing.T) {
	// One criterion omitted (nil) — must error.
	incomplete := `{"tags":[{"index":0,"subject_matter":true,"dimension":true,"not_person_place_work":true}]}`
	_, err := ParseTagQualityResponse(incomplete, 1)
	if err == nil {
		t.Fatal("expected error for incomplete verdict, got nil")
	}
}

func TestParseTagQualityResponse_DeferKey(t *testing.T) {
	abstained := `{"tags":[{"index":0,"subject_matter":true,"dimension":true,"not_person_place_work":true,"atomic":true,"not_form_genre":true,"defer":true}]}`
	results, err := ParseTagQualityResponse(abstained, 1)
	if err != nil {
		t.Fatalf("parse abstained: %v", err)
	}
	if !results[0].Defer {
		t.Error("defer=true must read as the judge's abstention")
	}

	omitted := `{"tags":[{"index":0,"subject_matter":true,"dimension":true,"not_person_place_work":true,"atomic":true,"not_form_genre":true}]}`
	results, err = ParseTagQualityResponse(omitted, 1)
	if err != nil {
		t.Fatalf("parse omitted defer: %v", err)
	}
	if results[0].Defer {
		t.Error("an omitted defer reads as false, not abstention")
	}
}

func TestParseTagQualityResponse_InvalidJSON(t *testing.T) {
	_, err := ParseTagQualityResponse("not json", 1)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseTagQualityResponse_BareArrayPositional(t *testing.T) {
	bare := `[
		{"subject_matter":true,"dimension":true,"not_person_place_work":true,"atomic":true,"not_form_genre":true},
		{"subject_matter":false,"dimension":true,"not_person_place_work":true,"atomic":true,"not_form_genre":true},
		{"subject_matter":true,"dimension":true,"not_person_place_work":true,"atomic":true,"not_form_genre":true}
	]`
	results, err := ParseTagQualityResponse(bare, 2)
	if err != nil {
		t.Fatalf("parse bare array without indexes: %v", err)
	}
	if !results[0].SubjectMatter || !results[0].Dimension {
		t.Errorf("position 0 answers = %+v, want them populated", results[0])
	}
	if results[1].SubjectMatter || !results[1].Dimension {
		t.Errorf("position 1 answers = %+v, want subject_matter=false", results[1])
	}

	// a bare array shorter than n must still fail rather than read as an
	// all-false rejection
	truncated := `[{"subject_matter":true,"dimension":true,"not_person_place_work":true,"atomic":true,"not_form_genre":true}]`
	if _, err := ParseTagQualityResponse(truncated, 2); err == nil {
		t.Fatal("expected incomplete-response error, got nil")
	}
}

func TestTagDetail_PresenceSemantics(t *testing.T) {
	// String-shape tag: no annotation, so no detail entry.
	stringTag := `{"title":"t","tags":["hello"]}`
	result, err := ParseAnalysisResponse(stringTag)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(result.TagDetails) != 0 {
		t.Fatalf("expected no detail for a string-shape tag, got %+v", result.TagDetails)
	}

	// Object without the retired key: the detail carries only term and
	// category.
	omitTag := `{"title":"t","tags":[{"term":"baz","category":"topic"}]}`
	result, err = ParseAnalysisResponse(omitTag)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.TagDetails[0].Term != "baz" || result.TagDetails[0].Category != TagCategoryTopic {
		t.Errorf("object: detail = %+v, want term baz and category topic", result.TagDetails[0])
	}
}

func TestParseTagIdentityResponse(t *testing.T) {
	complete := `{"terms":[
		{"index":0,"fold_to_base":true,"fold_preserves_denotation":true,"conventional_form":true},
		{"index":1,"fold_to_base":false,"fold_preserves_denotation":false,"conventional_form":false}
	]}`
	fenced := "```json\n{\"terms\":[{\"index\":0,\"fold_to_base\":false,\"fold_preserves_denotation\":true,\"conventional_form\":true}]}\n```"
	// one criterion omitted: a zero-valued result must never read as an all-false rejection
	incomplete := `{"terms":[{"index":0,"fold_to_base":true,"conventional_form":true}]}`
	// only 1 of 2 candidates returned
	missingIndex := `{"terms":[{"index":0,"fold_to_base":true,"fold_preserves_denotation":true,"conventional_form":true}]}`

	tests := []struct {
		name    string
		content string
		n       int
		wantErr bool
		want0   *TagIdentityResult
	}{
		{
			name:    "complete JSON parses both candidates",
			content: complete,
			n:       2,
			wantErr: false,
			want0: &TagIdentityResult{
				Index: 0, FoldToBase: true, FoldPreservesDenotation: true, ConventionalForm: true,
			},
		},
		{
			name:    "code-fenced JSON",
			content: fenced,
			n:       1,
			wantErr: false,
			want0: &TagIdentityResult{
				Index: 0, FoldToBase: false, FoldPreservesDenotation: true, ConventionalForm: true,
			},
		},
		{
			name:    "incomplete verdict must error",
			content: incomplete,
			n:       1,
			wantErr: true,
		},
		{
			name:    "missing index must error",
			content: missingIndex,
			n:       2,
			wantErr: true,
		},
		{
			name:    "invalid JSON must error",
			content: "not json",
			n:       1,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := ParseTagIdentityResponse(tt.content, tt.n)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(results) != tt.n {
				t.Fatalf("results = %d, want %d", len(results), tt.n)
			}
			if tt.want0 != nil && !reflect.DeepEqual(results[0], *tt.want0) {
				t.Errorf("results[0] = %+v, want %+v", results[0], *tt.want0)
			}
		})
	}
}

func TestParseTagIdentityResponse_BareArrayPositional(t *testing.T) {
	bare := `[
		{"fold_to_base":true,"fold_preserves_denotation":true,"conventional_form":true},
		{"fold_to_base":false,"fold_preserves_denotation":false,"conventional_form":false},
		{"fold_to_base":true,"fold_preserves_denotation":true,"conventional_form":true}
	]`
	results, err := ParseTagIdentityResponse(bare, 2)
	if err != nil {
		t.Fatalf("parse bare array without indexes: %v", err)
	}
	if !results[0].FoldToBase || !results[0].FoldPreservesDenotation || !results[0].ConventionalForm {
		t.Errorf("position 0 answers = %+v, want all true", results[0])
	}
	if results[1].FoldToBase || results[1].FoldPreservesDenotation || results[1].ConventionalForm {
		t.Errorf("position 1 answers = %+v, want all false", results[1])
	}

	// a bare array shorter than n must still fail rather than read as an
	// all-false rejection
	truncated := `[{"fold_to_base":true,"fold_preserves_denotation":true,"conventional_form":true}]`
	if _, err := ParseTagIdentityResponse(truncated, 2); err == nil {
		t.Fatal("expected incomplete-response error, got nil")
	}
}

func TestTagIdentityResult_CriteriaRecord(t *testing.T) {
	result := TagIdentityResult{
		FoldToBase: true, FoldPreservesDenotation: true, ConventionalForm: false,
	}

	raw := result.CriteriaRecord("buildings")
	var stored TagIdentityCriteria
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("record does not round-trip the criteria: %v", err)
	}
	if !stored.Complete() {
		t.Fatalf("record lost a criterion: %s", stored)
	}
	if stored.FoldToBase == nil || !*stored.FoldToBase {
		t.Error("fold_to_base = false, want true")
	}
	if stored.ConventionalForm == nil || *stored.ConventionalForm {
		t.Error("conventional_form = true, want false")
	}
	var envelope struct {
		ProposedFold string `json:"proposed_fold"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if envelope.ProposedFold != "buildings" {
		t.Errorf("proposed_fold = %q, want buildings", envelope.ProposedFold)
	}

	empty := result.CriteriaRecord("")
	if strings.Contains(string(empty), "proposed_fold") {
		t.Errorf("fold-less record should omit proposed_fold: %s", empty)
	}
}

func TestParseAdjudicationResponse_Direction(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "query direction",
			content: `{"pairs":[{"index":0,"verdict":"same","direction":"query"}]}`,
			want:    TagDirectionQuery,
		},
		{
			name:    "target direction",
			content: `{"pairs":[{"index":0,"verdict":"same","direction":"target"}]}`,
			want:    TagDirectionTarget,
		},
		{
			name:    "tie direction",
			content: `{"pairs":[{"index":0,"verdict":"same","direction":"tie"}]}`,
			want:    TagDirectionTie,
		},
		{
			name:    "unknown direction is dropped",
			content: `{"pairs":[{"index":0,"verdict":"same","direction":"sideways"}]}`,
			want:    "",
		},
		{
			name:    "absent direction stays empty",
			content: `{"pairs":[{"index":0,"verdict":"same"}]}`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := ParseAdjudicationResponse(tt.content, 1)
			if err != nil {
				t.Fatalf("ParseAdjudicationResponse: %v", err)
			}
			if results[0].Verdict != types.TagVerdicts.Same {
				t.Errorf("verdict = %q, want same", results[0].Verdict)
			}
			if results[0].Direction != tt.want {
				t.Errorf("direction = %q, want %q", results[0].Direction, tt.want)
			}
		})
	}
}

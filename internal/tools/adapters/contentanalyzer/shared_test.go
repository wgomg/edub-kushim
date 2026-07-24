package contentanalyzer

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm"
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
	docTypes := []database.DocumentType{
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
		name                 string
		tags                 []string
		people               []PeopleResult
		knownNormalized      []string
		title                string
		docTypeNames         []string
		want                 []string
	}{
		{
			name: "clean tags pass through",
			tags: []string{"physics", "machine learning"},
			knownNormalized: nil,
			want: []string{"physics", "machine learning"},
		},
		{
			name: "drops tags exceeding three words",
			tags: []string{"artificial intelligence and robotics", "physics"},
			knownNormalized: nil,
			want: []string{"physics"},
		},
		{
			name: "drops tag sharing token with person name",
			tags: []string{"turing", "machine learning"},
			people: []PeopleResult{
				{Name: "Alan Turing", NormalizedName: "alan turing"},
			},
			knownNormalized: nil,
			want: []string{"machine learning"},
		},
		{
			name: "drops tag sharing token with romanized name",
			tags: []string{"muller", "physics"},
			people: []PeopleResult{
				{Name: "Müller", NameRomanized: "muller", NormalizedName: "muller"},
			},
			knownNormalized: nil,
			want: []string{"physics"},
		},
		{
			name:  "drops tag that is subset of title tokens",
			tags:  []string{"machine learning", "deep learning"},
			knownNormalized: nil,
			title: "machine learning applications",
			want:  []string{"deep learning"},
		},
		{
			name:  "keeps tag with partial title overlap",
			tags:  []string{"deep architecture", "neural networks"},
			knownNormalized: nil,
			title: "deep learning applications",
			want:  []string{"deep architecture", "neural networks"},
		},
		{
			name:   "applies all rules together",
			tags:   []string{"turing", "artificial intelligence and robots", "machine learning", "physics"},
			people: []PeopleResult{{Name: "Alan Turing", NormalizedName: "alan turing"}},
			knownNormalized: nil,
			title:  "machine learning",
			want:   []string{"physics"},
		},
		{
			name: "caps at maxTags preserving order",
			tags: []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"},
			knownNormalized: nil,
			want: []string{"alpha", "bravo", "charlie", "delta", "echo"},
		},
		{
			name: "nil people and empty title",
			tags: []string{"physics"},
			knownNormalized: nil,
			want: []string{"physics"},
		},
		{
			name:         "drops tag matching doc-type name token",
			tags:         []string{"letter", "physics"},
			knownNormalized: nil,
			docTypeNames: []string{"letter"},
			want:         []string{"physics"},
		},
		{
			name:         "keeps tag not matching doc-type name token",
			tags:         []string{"physics", "mathematics"},
			knownNormalized: nil,
			docTypeNames: []string{"letter"},
			want:         []string{"physics", "mathematics"},
		},
		{
			name:   "empty tags",
			tags:   []string{},
			people: []PeopleResult{{Name: "Turing", NormalizedName: "turing"}},
			knownNormalized: nil,
			title:  "physics",
			want:   []string{},
		},
		{
			name:   "drops tag that equals a subset of a known person name",
			tags:   []string{"john doe", "physics"},
			knownNormalized: []string{"john doe smith"},
			want:   []string{"physics"},
		},
		{
			name:   "keeps single-word tag sharing token with longer known name",
			tags:   []string{"young", "activism"},
			knownNormalized: []string{"andrew young"},
			want:   []string{"young", "activism"},
		},
		{
			name:   "drops multi-word tag that exactly matches a known name",
			tags:   []string{"rosa luxemburg", "marxism"},
			knownNormalized: []string{"rosa luxemburg"},
			want:   []string{"marxism"},
		},
		{
			name:   "keeps tag with known people present that does not match",
			tags:   []string{"algebra", "topology"},
			knownNormalized: []string{"alan turing", "andrew young"},
			want:   []string{"algebra", "topology"},
		},
		{
			name:   "drops 3-word tag that is subset of a known name",
			tags:   []string{"john doe smith", "physics"},
			knownNormalized: []string{"john doe smith jones"},
			want:   []string{"physics"},
		},
		{
			name:   "keeps multi-word tag whose tokens are split across different known people",
			tags:   []string{"john smith", "physics"},
			knownNormalized: []string{"john doe", "smith jones"},
			want:   []string{"john smith", "physics"},
		},
		{
			name:   "skips empty-string entries in knownNormalized",
			tags:   []string{"john doe", "physics"},
			knownNormalized: []string{"", "john doe smith"},
			want:   []string{"physics"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterTags(tt.tags, tt.people, tt.knownNormalized, tt.title, tt.docTypeNames)
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
				var ctle *ContentTooLargeError
				if !errors.As(err, &ctle) {
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

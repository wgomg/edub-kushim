package contentanalyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"unicode"

	"github.com/wgomg/edub-kushim/internal/database"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var nonAlphaKeepSpaces = regexp.MustCompile(`[^a-z ]`)
var multiSpaceRE = regexp.MustCompile(` +`)

const (
	maxTags          = 5
	tagRequestBuffer = 3
)

const requestedTagCount = maxTags + tagRequestBuffer // 8

const systemMessage = "You are a helpful assistant specialized in document analysis and metadata extraction"

type tokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func buildTokenUsageStats(prompt, completion, total int) *json.RawMessage {
	stats := tokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
	}
	data, err := json.Marshal(stats)
	if err != nil {
		return nil
	}
	rm := json.RawMessage(data)
	return &rm
}

const defaultPromptTemplate = `Analyze the excerpts of a document provided below and extract the following data:
- Document title: In excerpts language, truncate to 127 characters if longer
{{.DocTypePrompt}}
- Tags: At most {{.RequestedTags}} thematic tags describing the document's topics and domains. English only. Each tag is one to three words naming a recognizable subject, field, or period. Tags describe facets the title does not already state. For names containing symbols (e.g., C++, C#), use the conventional spelled-out form (e.g., c plus plus, c sharp). Tags are conceptual categories — fields, domains, disciplines, periods, methods. Tags are never specific people, works, or places. Use only widely-recognized standard terminology a general educated audience would know. If an existing suggestion tag captures the concept adequately, prefer it over inventing a narrower label.{{.TagsPrompt}}
- People: People associated with the document. For each person provide: name (the person's name in its native script/language; if you cannot determine the native form, use the name exactly as it appears in the document), name_romanized (a Latin-script/romanized form of the name — always provide this, even when name is already in Latin script), and a type from the list below. Only include individuals who play a substantive role in the document's creation, execution, or primary subject matter — exclude incidental mentions. Note: names captured here must NOT be re-used as tags.
{{.PeoplePrompt}}- Language: 3-letter ISO 639-2 code (e.g. 'eng','spa','jpn','fra','deu','zho','kor','ara','por','rus'). Detect the primary language even from noisy or mixed text. Only use 'und' as a last resort if the text is truly too short or ambiguous to determine.
Return ONLY a json string without any explanations, numbers, additional text, text formatting or text/code blocks, with keys: title, type, tags, people (array of objects with keys: name, name_romanized, type), language.

Document Excerpts: {{.Text}}`

type promptData struct {
	DocTypePrompt string
	TagsPrompt    string
	PeoplePrompt  string
	Text          string
	RequestedTags int
}

func BuildPrompt(text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string, customTemplate string) string {
	docTypePrompt := documentTypePrompt(docTypes)
	peoplePrompt := peopleTypePrompt(peopleTypes)
	tagsPrompt := tagsPrompt(tagSuggestions)

	tmplStr := strings.TrimSpace(customTemplate)
	if tmplStr == "" {
		tmplStr = defaultPromptTemplate
	}

	tmpl, err := template.New("prompt").Parse(tmplStr)
	if err != nil {
		tmpl, _ = template.New("prompt").Parse(defaultPromptTemplate)
	}

	data := promptData{
		DocTypePrompt: docTypePrompt,
		TagsPrompt:    tagsPrompt,
		PeoplePrompt:  peoplePrompt,
		Text:          text,
		RequestedTags: requestedTagCount,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		buf.Reset()
		fallback, _ := template.New("prompt").Parse(defaultPromptTemplate)
		fallback.Execute(&buf, data)
	}

	return buf.String()
}

func peopleTypePrompt(types []database.PeopleType) string {
	var sb strings.Builder
	sb.WriteString("  Available types:\n")
	for _, t := range types {
		fmt.Fprintf(&sb, "    - %s (%s)\n", t.Name, t.Description)
	}
	return sb.String()
}

func documentTypePrompt(types []database.DocumentType) string {
	var sb strings.Builder
	sb.WriteString("- Document type: choose one of the following:\n")
	for _, t := range types {
		fmt.Fprintf(&sb, "  - %s (%s)\n", t.Name, t.Description)
	}
	return sb.String()
}

func tagsPrompt(tags []string) string {
	return fmt.Sprintf(
		" Prefer tags from the following list if thematically related to document excerpts: '%s'",
		strings.Join(tags, ","),
	)
}

var accentFolder transform.Transformer = transform.Chain(
	norm.NFD,
	runes.Remove(runes.In(unicode.Mn)),
	norm.NFC,
)

func foldAccents(s string) string {
	out, _, err := transform.String(accentFolder, s)
	if err != nil {
		return s
	}
	return out
}

func normalizeCore(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = foldAccents(s)
	s = nonAlphaKeepSpaces.ReplaceAllString(s, "")
	s = multiSpaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func normalizeToTokens(s string) []string {
	return strings.Fields(normalizeCore(s))
}

func FilterTags(tags []string, people []PeopleResult, title string, docTypeNames []string) []string {
	nameTokens := make(map[string]struct{})
	for _, p := range people {
		for _, tok := range normalizeToTokens(p.NormalizedName) {
			nameTokens[tok] = struct{}{}
		}
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

	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tagTokens := strings.Fields(tag)
		if len(tagTokens) > 3 || len(tagTokens) == 0 {
			continue
		}

		matchesName := false
		for _, tok := range tagTokens {
			if _, ok := nameTokens[tok]; ok {
				matchesName = true
				break
			}
		}
		if matchesName {
			continue
		}

		matchesDocType := false
		for _, tok := range tagTokens {
			if _, ok := docTypeTokens[tok]; ok {
				matchesDocType = true
				break
			}
		}
		if matchesDocType {
			continue
		}

		allInTitle := true
		for _, tok := range tagTokens {
			if _, ok := titleTokens[tok]; !ok {
				allInTitle = false
				break
			}
		}
		if allInTitle {
			continue
		}

		result = append(result, tag)
	}

	if len(result) > maxTags {
		result = result[:maxTags]
	}
	return result
}

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

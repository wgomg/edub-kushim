package contentanalyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/wgomg/edub-kushim/internal/database"
)

var nonAlphaKeepSpaces = regexp.MustCompile(`[^a-z ]`)
var multiSpaceRE = regexp.MustCompile(` +`)

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
- Tags: At most five thematic tags. English only, lowercase. Prefer single words. If a concept requires multiple words, separate them with spaces. At most 3 words per tag. For names containing symbols (e.g., C++, C#), use the conventional spelled-out form (e.g., c plus plus, c sharp). Do not use people's names and/or surnames as tags — authors, historical figures, and individuals belong in the document's people metadata, not in tags. Use only widely-recognized, standard terminology. Do not coin novel compound terms or use highly specialized jargon that would be unfamiliar to a general educated audience. If an existing suggestion tag captures the concept adequately, prefer it over inventing a narrower label.{{.TagsPrompt}}
- People: People associated with the document. For each people, provide a name and a type from the list below. If the name contains non-Latin characters (e.g. Korean, Arabic, Cyrillic, Hebrew, etc.), also provide a name_romanized field with the romanized/Latin-script version of the name. Only include individuals who play a substantive role in the document's creation, execution, or primary subject matter — exclude incidental mentions.
{{.PeoplePrompt}}- Language: 3-letter ISO 639-2 code (e.g. 'eng','spa','jpn','fra','deu','zho','kor','ara','por','rus'). Detect the primary language even from noisy or mixed text. Only use 'und' as a last resort if the text is truly too short or ambiguous to determine.
Return ONLY a json string without any explanations, numbers, additional text, text formatting or text/code blocks, with keys: title, type, tags, people (array of objects with keys: name, name_romanized, type), language.

Document Excerpts: {{.Text}}`

type promptData struct {
	DocTypePrompt string
	TagsPrompt    string
	PeoplePrompt  string
	Text          string
}

func BuildPrompt(text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string, customTemplate string) string {
	docTypePrompt := documentTypePrompt(docTypes)
	peoplePrompt := peopleTypePrompt(peopleTypes)
	tagsPrompt := tagsPrompt(tagSuggestions)

	var docTypesNames []string
	for _, dt := range docTypes {
		docTypesNames = append(docTypesNames, dt.Name)
	}

	if len(docTypes) > 0 {
		tagsPrompt += fmt.Sprintf(
			" DO NOT use words from the following list as tags: '%s'",
			strings.Join(docTypesNames, ","),
		)
	}

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

func NormalizeTags(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	result := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.TrimSpace(strings.ToLower(t))
		t = strings.ReplaceAll(t, "-", " ")
		t = strings.ReplaceAll(t, "_", " ")
		t = nonAlphaKeepSpaces.ReplaceAllString(t, "")
		t = multiSpaceRE.ReplaceAllString(t, " ")
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		result = append(result, t)
	}
	return result
}

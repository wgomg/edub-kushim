package contentanalyzer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/database"
)

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

func BuildPrompt(text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) string {
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

	return fmt.Sprintf(
		"Analyze the excerpts of a document provided below and extract the following data: \n- Document title: In excerpts language, truncate to 127 characters if longer\n%s\n- Tags: At most five thematic tags. English only, lowercase, prefer single word, if two or more words join them with hyphens.%s\n- People: People associated with the document. For each people, provide a name and a type from the list below. If the name contains non-Latin characters (e.g. Korean, Arabic, Cyrillic, Hebrew, etc.), also provide a name_romanized field with the romanized/Latin-script version of the name. Only include individuals who play a substantive role in the document's creation, execution, or primary subject matter — exclude incidental mentions.\n%s- Language: 3-letter ISO 639-2 code (e.g. 'eng','spa','jpn','fra','deu','zho','kor','ara','por','rus'). Detect the primary language even from noisy or mixed text. Only use 'und' as a last resort if the text is truly too short or ambiguous to determine.\nReturn ONLY a json string without any explanations, numbers, additional text, text formatting or text/code blocks, with keys: title, type, tags, people (array of objects with keys: name, name_romanized, type), language.\n\nDocument Excerpts: %s",
		docTypePrompt,
		tagsPrompt,
		peoplePrompt,
		text,
	)
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

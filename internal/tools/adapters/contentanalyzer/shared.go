package contentanalyzer

import (
	"encoding/json"
	"fmt"
	"strings"
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

func BuildPrompt(text string, docTypes []string, tagSuggestions []string) string {
	docTypePrompt := documentTypePrompt(docTypes)
	tagsPrompt := tagsPrompt(tagSuggestions)

	if len(docTypes) > 0 {
		tagsPrompt += fmt.Sprintf(
			" DO NOT use words from the following list as tags: '%s'",
			tagsPrompt,
		)
	}

	return fmt.Sprintf(
		"Analyze the excerpts of a document provided below and extract the following data: \n- Document title: In excerpts language, truncate to 127 characters if longer\n%s\n- Tags: At most five thematic tags. English only, lowercase, prefer single word, if two or more words join them with hyphens.%s\n- Authors: Full names of the author(s), correspondent(s) or incumbent(s).\n- Language: 3 letters code, set as 'und' if unable to identify.\nReturn ONLY a json string without any explanations, numbers, additional text, text formatting or text/code blocks, with keys: title, type, tags, authors, language.\n\nDocument Excerpts: %s",
		docTypePrompt,
		tagsPrompt,
		text,
	)
}

func documentTypePrompt(types []string) string {
	return fmt.Sprintf("- Document type: choose one of '%s'.", strings.Join(types, ","))
}

func tagsPrompt(tags []string) string {
	return fmt.Sprintf(
		" Prefer tags from the following list if thematically related to document excerpts: '%s'",
		strings.Join(tags, ","),
	)
}

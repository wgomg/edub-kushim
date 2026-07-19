package contentanalyzer

import (
	"context"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type PeopleResult struct {
	Name           string `json:"name"`
	NameRomanized  string `json:"name_romanized,omitempty"`
	Type           string `json:"type"`
	NormalizedName string `json:"-"`
}

type ContentAnalyzer interface {
	Analyze(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error)
	AnalyzeDocType(ctx context.Context, prevResult *AnalysisResult, headTailText string, docTypes []database.DocumentType) (string, error)
	Name() string
}

type AnalysisResult struct {
	Title       string           `json:"title"`
	DocType     string           `json:"type"`
	Tags        []string         `json:"tags"`
	People      []PeopleResult   `json:"people"`
	Language    string           `json:"language"`
	Stats       *json.RawMessage `json:"stats"`
	Prompt      string           `json:"prompt"`
	PassContext *json.RawMessage `json:"-"`
}

func NewContentAnalyzer(logger *utils.Logger, cfg config.ToolConfig, llmCfg *config.LlmToolsConfig, promptTemplate string) (ContentAnalyzer, error) {
	switch cfg.Command {
	case config.ContentAnalyzer.OpenAI:
		ca, err := NewLlmOpenAi(logger, cfg, llmCfg.OpenAI, promptTemplate)
		return ca, err
	case config.ContentAnalyzer.Anthropic:
		ca, err := NewLlmAnthropic(logger, cfg, llmCfg.Anthropic, promptTemplate)
		return ca, err
	case config.ContentAnalyzer.DeepSeek:
		ca, err := NewLlmDeepSeek(logger, cfg, llmCfg.DeepSeek, promptTemplate)
		return ca, err
	case config.ContentAnalyzer.Ollama:
		ca, err := NewLlmOllama(logger, cfg, llmCfg.Ollama, promptTemplate)
		return ca, err
	default:
		return NewLlmOpenAi(logger, cfg, llmCfg.OpenAI, promptTemplate)
	}
}

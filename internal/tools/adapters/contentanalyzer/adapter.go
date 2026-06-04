package contentanalyzer

import (
	"context"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type ContentAnalyzer interface {
	Analyze(ctx context.Context, text string, docTypes []string, tagSuggestions []string) (*AnalysisResult, error)
	Name() string
}

type AnalysisResult struct {
	Title    string           `json:"title"`
	DocType  string           `json:"type"`
	Tags     []string         `json:"tags"`
	Authors  []string         `json:"authors"`
	Language string           `json:"language"`
	Stats    *json.RawMessage `json:"stats"`
	Prompt   string           `json:"prompt"`
}

func NewContentAnalyzer(logger *utils.Logger, cfg config.ToolConfig, llmCfg *config.LlmToolsConfig) (ContentAnalyzer, error) {
	switch cfg.Command {
	case "llmopenai":
		ca, err := NewLlmOpenAi(logger, cfg, llmCfg.OpenAI)
		return ca, err
	case "llmanthropic":
		ca, err := NewLlmAnthropic(logger, cfg, llmCfg.Anthropic)
		return ca, err
	case "llmdeepseek":
		ca, err := NewLlmDeepSeek(logger, cfg, llmCfg.DeepSeek)
		return ca, err
	case "llmollama":
		ca, err := NewLlmOllama(logger, cfg, llmCfg.Ollama)
		return ca, err
	default:
		return NewLlmOpenAi(logger, cfg, llmCfg.OpenAI)
	}
}

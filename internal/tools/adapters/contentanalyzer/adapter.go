package contentanalyzer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm"
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
	AnalyzeDocType(ctx context.Context, prevResult *AnalysisResult, headTailText string, docTypes []database.DocumentType, metadata DocMetadata) (string, error)
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

func NewContentAnalyzer(logger *utils.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, promptTemplate string, reg *llm.Registry) (ContentAnalyzer, error) {
	if reg == nil {
		return nil, fmt.Errorf("registry is required for %q adapter", llmCfg.Adapter)
	}
	caps := reg.Lookup(llmCfg.Provider, llmCfg.Model)

	switch llmCfg.Adapter {
	case "anthropic":
		return NewLlmAnthropic(logger, cfg, llmCfg, caps, promptTemplate, reg)
	default:
		return NewLlmOpenAiCompatible(logger, cfg, llmCfg, caps, promptTemplate, reg)
	}
}

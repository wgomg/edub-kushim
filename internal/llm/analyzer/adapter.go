package analyzer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/log"
)

type PeopleResult struct {
	Name           string `json:"name"`
	NameRomanized  string `json:"name_romanized,omitempty"`
	Type           string `json:"type"`
	NormalizedName string `json:"-"`
}

// TypeRef is one option of a type list offered to the model, carrying the
// fields the prompts render: the type's name and its description.
type TypeRef struct {
	Name        string
	Description string
}

type ContentAnalyzer interface {
	Analyze(ctx context.Context, text string, docTypes []TypeRef, peopleTypes []TypeRef, tagSuggestions []string) (*AnalysisResult, error)
	AnalyzeDocType(ctx context.Context, prevResult *AnalysisResult, headTailText string, docTypes []TypeRef, metadata DocMetadata) (string, error)
	Adjudicate(ctx context.Context, pairs []TagPairEvidence) ([]TagVerdictResult, error)
	AdjudicateTagSplits(ctx context.Context, tags []string) ([]TagSplitResult, error)
	JudgeTagQuality(ctx context.Context, text string, candidates []TagQualityCandidate) ([]TagQualityResult, error)
	JudgeTagIdentity(ctx context.Context, candidates []TagIdentityCandidate) ([]TagIdentityResult, error)
	Name() string
}

type AnalysisResult struct {
	Title       string           `json:"title"`
	DocType     string           `json:"type"`
	Tags        []string         `json:"tags"`
	TagDetails  []TagDetail      `json:"tag_details,omitempty"`
	People      []PeopleResult   `json:"people"`
	Language    string           `json:"language"`
	Stats       *json.RawMessage `json:"stats"`
	Prompt      string           `json:"prompt"`
	PassContext *json.RawMessage `json:"-"`
}

func NewContentAnalyzer(logger *log.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, promptTemplate string, minTags, maxTags int, reg *llm.Registry) (ContentAnalyzer, error) {
	if reg == nil {
		return nil, fmt.Errorf("registry is required for %q adapter", llmCfg.Adapter)
	}
	caps := reg.Lookup(llmCfg.Provider, llmCfg.Model)

	switch llmCfg.Adapter {
	case "anthropic":
		return NewLlmAnthropic(logger, cfg, llmCfg, caps, promptTemplate, minTags, maxTags, reg)
	default:
		return NewLlmOpenAiCompatible(logger, cfg, llmCfg, caps, promptTemplate, minTags, maxTags, reg)
	}
}

package contentanalyzer

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/llm"
)

func TestApplyReasoning_Disabled(t *testing.T) {
	l := &LlmAnthropic{
		llmCfg: &config.LlmConfig{Reasoning: false},
		caps:   &llm.ModelCapability{SupportsReasoning: true, ReasoningEffortLevels: []string{"low", "high"}},
	}
	reqBody := &anthropicRequest{}
	l.applyReasoning(reqBody, 64000)

	if reqBody.Thinking == nil || reqBody.Thinking.Type != "disabled" {
		t.Errorf("expected thinking disabled, got %+v", reqBody.Thinking)
	}
	if reqBody.OutputConfig != nil {
		t.Errorf("expected no output_config, got %+v", reqBody.OutputConfig)
	}
}

func TestApplyReasoning_EffortCapableModel(t *testing.T) {
	l := &LlmAnthropic{
		llmCfg: &config.LlmConfig{Model: "claude-opus-4-8", Reasoning: true, ReasoningEffort: "medium"},
		caps:   &llm.ModelCapability{SupportsReasoning: true, ReasoningEffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
	}
	reqBody := &anthropicRequest{}
	l.applyReasoning(reqBody, 64000)

	if reqBody.OutputConfig == nil || reqBody.OutputConfig.Effort != "medium" {
		t.Errorf("expected output_config.effort=medium, got %+v", reqBody.OutputConfig)
	}
	if reqBody.Thinking == nil || reqBody.Thinking.Type != "adaptive" {
		t.Errorf("expected adaptive thinking, got %+v", reqBody.Thinking)
	}
	if reqBody.Thinking.BudgetTokens != 0 {
		t.Errorf("expected no budget_tokens for adaptive thinking, got %d", reqBody.Thinking.BudgetTokens)
	}
}

func TestApplyReasoning_EffortDefaultsToHigh(t *testing.T) {
	l := &LlmAnthropic{
		llmCfg: &config.LlmConfig{Model: "claude-sonnet-5", Reasoning: true, ReasoningEffort: ""},
		caps:   &llm.ModelCapability{SupportsReasoning: true, ReasoningEffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
	}
	reqBody := &anthropicRequest{}
	l.applyReasoning(reqBody, 64000)

	if reqBody.OutputConfig == nil || reqBody.OutputConfig.Effort != "high" {
		t.Errorf("expected output_config.effort=high default, got %+v", reqBody.OutputConfig)
	}
}

func TestApplyReasoning_ManualThinkingModel(t *testing.T) {
	l := &LlmAnthropic{
		llmCfg: &config.LlmConfig{Model: "claude-opus-4-5", Reasoning: true, ReasoningEffort: "high"},
		caps:   &llm.ModelCapability{SupportsReasoning: true, ReasoningEffortLevels: []string{"low", "medium", "high"}},
	}
	reqBody := &anthropicRequest{}
	l.applyReasoning(reqBody, 64000)

	if reqBody.OutputConfig == nil || reqBody.OutputConfig.Effort != "high" {
		t.Errorf("expected output_config.effort=high, got %+v", reqBody.OutputConfig)
	}
	if reqBody.Thinking == nil || reqBody.Thinking.Type != "enabled" || reqBody.Thinking.BudgetTokens == 0 {
		t.Errorf("expected manual thinking with a budget, got %+v", reqBody.Thinking)
	}
}

func TestApplyReasoning_BudgetOnlyModel(t *testing.T) {
	l := &LlmAnthropic{
		llmCfg: &config.LlmConfig{Model: "claude-sonnet-4-5", Reasoning: true},
		caps:   &llm.ModelCapability{SupportsReasoning: true, ReasoningEffortLevels: nil},
	}
	reqBody := &anthropicRequest{}
	l.applyReasoning(reqBody, 64000)

	if reqBody.OutputConfig != nil {
		t.Errorf("expected no output_config for a budget-only model, got %+v", reqBody.OutputConfig)
	}
	if reqBody.Thinking == nil || reqBody.Thinking.Type != "enabled" || reqBody.Thinking.BudgetTokens < minThinkingBudgetTokens {
		t.Errorf("expected manual thinking with a valid budget, got %+v", reqBody.Thinking)
	}
}

func TestApplyReasoning_BudgetOnlyModel_TooFewMaxTokens(t *testing.T) {
	l := &LlmAnthropic{
		llmCfg: &config.LlmConfig{Model: "claude-haiku-4-5", Reasoning: true},
		caps:   &llm.ModelCapability{SupportsReasoning: true, ReasoningEffortLevels: nil},
	}
	reqBody := &anthropicRequest{}
	l.applyReasoning(reqBody, 256)

	if reqBody.Thinking != nil {
		t.Errorf("expected thinking to be skipped when maxTokens is too small, got %+v", reqBody.Thinking)
	}
}

func TestThinkingBudget(t *testing.T) {
	tests := []struct {
		maxTokens int
		want      int
	}{
		{256, 0},
		{2000, 0},
		{2048, 1024},
		{4000, 2000},
		{100000, maxThinkingBudgetTokens},
	}
	for _, tt := range tests {
		if got := thinkingBudget(tt.maxTokens); got != tt.want {
			t.Errorf("thinkingBudget(%d) = %d, want %d", tt.maxTokens, got, tt.want)
		}
	}
}

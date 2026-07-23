package contentanalyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type LlmAnthropic struct {
	logger         *utils.Logger
	config         config.ToolConfig
	llmCfg         *config.LlmConfig
	caps           *llm.ModelCapability
	promptTemplate string
	client         *http.Client
	reg            *llm.Registry
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type anthropicOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type cacheControlEphemeral struct {
	Type string `json:"type"`
}

type anthropicRequest struct {
	Model        string                   `json:"model"`
	MaxTokens    int                      `json:"max_tokens"`
	Messages     []anthropicMessage       `json:"messages"`
	System       string                   `json:"system,omitempty"`
	Thinking     *anthropicThinkingConfig `json:"thinking,omitempty"`
	OutputConfig *anthropicOutputConfig   `json:"output_config,omitempty"`
	StopSeq      any                      `json:"stop_sequences,omitempty"`
	Stream       bool                     `json:"stream,omitempty"`
	Temperature  *float64                 `json:"temperature,omitempty"`
	TopK         int                      `json:"top_k,omitempty"`
	CacheControl *cacheControlEphemeral   `json:"cache_control,omitempty"`
}

var anthropicManualThinkingModels = map[string]bool{
	"claude-opus-4-5": true,
}

const (
	minThinkingBudgetTokens = 1024
	maxThinkingBudgetTokens = 32000
)

func thinkingBudget(maxTokens int) int {
	budget := min(maxTokens/2, maxThinkingBudgetTokens)
	if budget < minThinkingBudgetTokens {
		return 0
	}
	return budget
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []anthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   string                  `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence"`
	Usage        anthropicUsage          `json:"usage"`
}

func NewLlmAnthropic(logger *utils.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, caps *llm.ModelCapability, promptTemplate string, reg *llm.Registry) (*LlmAnthropic, error) {
	return &LlmAnthropic{
		logger:         logger,
		config:         cfg,
		llmCfg:         llmCfg,
		caps:           caps,
		promptTemplate: promptTemplate,
		client:         &http.Client{},
		reg:            reg,
	}, nil
}

type anthropicPassContext struct {
	System     string `json:"system"`
	UserPrompt string `json:"user_prompt"`
}

func (l *LlmAnthropic) baseURL() string {
	return l.reg.ProviderDefaultURL(l.llmCfg.Provider)
}

func (l *LlmAnthropic) defaultMaxTokens() int {
	if l.caps != nil && l.caps.MaxOutputTokens > 0 {
		return l.caps.MaxOutputTokens
	}
	return 4096
}

func (l *LlmAnthropic) applyReasoning(reqBody *anthropicRequest, maxTokens int) {
	reasoningOn := l.llmCfg.Reasoning && l.caps != nil && l.caps.SupportsReasoning
	if !reasoningOn {
		reqBody.Thinking = &anthropicThinkingConfig{Type: "disabled"}
		return
	}

	if len(l.caps.ReasoningEffortLevels) == 0 {
		if budget := thinkingBudget(maxTokens); budget > 0 {
			reqBody.Thinking = &anthropicThinkingConfig{Type: "enabled", BudgetTokens: budget}
		}
		return
	}

	effort := l.llmCfg.ReasoningEffort
	if effort == "" {
		effort = "high"
	}
	reqBody.OutputConfig = &anthropicOutputConfig{Effort: effort}

	if anthropicManualThinkingModels[l.llmCfg.Model] {
		if budget := thinkingBudget(maxTokens); budget > 0 {
			reqBody.Thinking = &anthropicThinkingConfig{Type: "enabled", BudgetTokens: budget}
		}
		return
	}

	reqBody.Thinking = &anthropicThinkingConfig{Type: "adaptive"}
}

func (l *LlmAnthropic) applyTemperature(reqBody *anthropicRequest, temp float64) {
	if l.caps != nil && l.caps.SupportsTemperature {
		reqBody.Temperature = &temp
	}
}

func (l *LlmAnthropic) Analyze(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error) {
	prompt := BuildPrompt(text, docTypes, peopleTypes, tagSuggestions, l.promptTemplate)

	if err := checkContentTooLarge(l.caps, SystemMessage+"\n"+prompt); err != nil {
		return nil, err
	}

	reqBody := anthropicRequest{
		Model:     l.llmCfg.Model,
		MaxTokens: l.defaultMaxTokens(),
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
		System:       SystemMessage,
		Stream:       false,
		CacheControl: &cacheControlEphemeral{Type: "ephemeral"},
	}
	l.applyReasoning(&reqBody, reqBody.MaxTokens)
	l.applyTemperature(&reqBody, l.llmCfg.Temperature)

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", l.baseURL()+"/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if l.llmCfg.Token != "" {
		httpReq.Header.Set("x-api-key", l.llmCfg.Token)
	}
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		if tokErr := parseTokenLimitError(body); tokErr != nil {
			return nil, tokErr
		}
		if credErr := parseInsufficientCreditsError(body, resp.StatusCode, "anthropic"); credErr != nil {
			return nil, credErr
		}
		return nil, fmt.Errorf("API error: status %s: %s", resp.Status, string(body))
	}

	var anthResp anthropicResponse
	if err := json.Unmarshal(body, &anthResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(anthResp.Content) == 0 {
		return nil, fmt.Errorf("empty response from LLM")
	}

	var responseText strings.Builder
	for _, block := range anthResp.Content {
		if block.Type == "text" && block.Text != "" {
			responseText.WriteString(block.Text)
		}
	}

	if responseText.Len() == 0 {
		return nil, fmt.Errorf("no text content in response")
	}

	responseContent := strings.TrimSpace(responseText.String())
	responseContent = utils.CleanCodeBlock(responseContent)

	var analysisResult AnalysisResult
	if err := json.Unmarshal([]byte(responseContent), &analysisResult); err != nil {
		return nil, fmt.Errorf("LLM returned invalid JSON: %w\nraw: %s", err, responseContent)
	}

	analysisResult.Stats = buildTokenUsageStats(
		anthResp.Usage.InputTokens,
		anthResp.Usage.OutputTokens,
		anthResp.Usage.InputTokens+anthResp.Usage.OutputTokens,
	)
	analysisResult.Prompt = prompt

	passCtxObj := anthropicPassContext{System: SystemMessage, UserPrompt: prompt}
	ctxJSON, _ := json.Marshal(passCtxObj)
	rm := json.RawMessage(ctxJSON)
	analysisResult.PassContext = &rm

	return &analysisResult, nil
}

func (l *LlmAnthropic) AnalyzeDocType(ctx context.Context, prevResult *AnalysisResult, headTailText string, docTypes []database.DocumentType) (string, error) {
	if prevResult.PassContext == nil {
		return "", fmt.Errorf("pass context is nil")
	}

	var passCtx anthropicPassContext
	if err := json.Unmarshal(*prevResult.PassContext, &passCtx); err != nil {
		return "", fmt.Errorf("unmarshal pass context: %w", err)
	}

	assistantJSON, _ := json.Marshal(struct {
		Title    string         `json:"title"`
		Type     string         `json:"type"`
		Tags     []string       `json:"tags"`
		People   []PeopleResult `json:"people"`
		Language string         `json:"language"`
	}{
		Title:    prevResult.Title,
		Type:     prevResult.DocType,
		Tags:     prevResult.Tags,
		People:   prevResult.People,
		Language: prevResult.Language,
	})

	docTypePrompt := BuildDocTypePrompt(headTailText, docTypes)

	if err := checkContentTooLarge(l.caps, passCtx.System+"\n"+passCtx.UserPrompt+"\n"+string(assistantJSON)+"\n"+docTypePrompt); err != nil {
		return "", err
	}

	reqBody := anthropicRequest{
		Model:     l.llmCfg.Model,
		MaxTokens: 256,
		Messages: []anthropicMessage{
			{Role: "user", Content: passCtx.UserPrompt},
			{Role: "assistant", Content: string(assistantJSON)},
			{Role: "user", Content: docTypePrompt},
		},
		System:       passCtx.System,
		Stream:       false,
		CacheControl: &cacheControlEphemeral{Type: "ephemeral"},
	}
	l.applyReasoning(&reqBody, reqBody.MaxTokens)
	l.applyTemperature(&reqBody, 0)

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", l.baseURL()+"/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	if l.llmCfg.Token != "" {
		httpReq.Header.Set("x-api-key", l.llmCfg.Token)
	}
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		if tokErr := parseTokenLimitError(body); tokErr != nil {
			return "", tokErr
		}
		if credErr := parseInsufficientCreditsError(body, resp.StatusCode, "anthropic"); credErr != nil {
			return "", credErr
		}
		return "", fmt.Errorf("doc type refinement: status %s: %s", resp.Status, string(body))
	}

	var anthResp anthropicResponse
	if err := json.Unmarshal(body, &anthResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(anthResp.Content) == 0 {
		return "", fmt.Errorf("empty response from LLM")
	}

	var responseText strings.Builder
	for _, block := range anthResp.Content {
		if block.Type == "text" && block.Text != "" {
			responseText.WriteString(block.Text)
		}
	}

	if responseText.Len() == 0 {
		return "", fmt.Errorf("no text content in response")
	}

	responseContent := strings.TrimSpace(responseText.String())
	responseContent = utils.CleanCodeBlock(responseContent)

	var result struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(responseContent), &result); err != nil {
		return "", fmt.Errorf("LLM returned invalid JSON: %w\nraw: %s", err, responseContent)
	}
	if result.Type == "" {
		var full AnalysisResult
		if err := json.Unmarshal([]byte(responseContent), &full); err == nil {
			return full.DocType, nil
		}
	}
	return result.Type, nil
}

func (l *LlmAnthropic) Name() string {
	return "anthropic"
}

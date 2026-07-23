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

type LlmOpenAiCompatible struct {
	logger         *utils.Logger
	config         config.ToolConfig
	llmCfg         *config.LlmConfig
	caps           *llm.ModelCapability
	promptTemplate string
	client         *http.Client
	reg            *llm.Registry
}

type openaiChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponseFormat struct {
	Type string `json:"type"`
}

type openaiChoice struct {
	FinishReason string            `json:"finish_reason"`
	Index        int               `json:"index"`
	Message      openaiChatMessage `json:"message"`
}

type openaiUsage struct {
	CompletionTokens int `json:"completion_tokens"`
	PromptTokens     int `json:"prompt_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openaiResponse struct {
	ID      string         `json:"id"`
	Choices []openaiChoice `json:"choices"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Object  string         `json:"object"`
	Usage   openaiUsage    `json:"usage"`
}

func NewLlmOpenAiCompatible(logger *utils.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, caps *llm.ModelCapability, promptTemplate string, reg *llm.Registry) (*LlmOpenAiCompatible, error) {
	return &LlmOpenAiCompatible{
		logger:         logger,
		config:         cfg,
		llmCfg:         llmCfg,
		caps:           caps,
		promptTemplate: promptTemplate,
		client:         &http.Client{},
		reg:            reg,
	}, nil
}

type openaiPassContext struct {
	SystemMessage string `json:"system_message"`
	UserPrompt    string `json:"user_prompt"`
}

func (l *LlmOpenAiCompatible) baseURL() string {
	return l.reg.ProviderDefaultURL(l.llmCfg.Provider)
}

func (l *LlmOpenAiCompatible) buildRequestBody(system, user, assistant string, temp float64) map[string]any {
	messages := []openaiChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	if assistant != "" {
		messages = append(messages, openaiChatMessage{Role: "assistant", Content: assistant})
	}

	body := map[string]any{
		"model":    l.llmCfg.Model,
		"messages": messages,
		"stream":   false,
	}

	if l.caps != nil && l.caps.SupportsTemperature {
		body["temperature"] = temp
		body["top_p"] = 1
	}

	if l.caps != nil && l.caps.SupportsStructuredOutput {
		body["response_format"] = &openaiResponseFormat{Type: "json_object"}
	}

	l.addThinkingConfig(body)
	return body
}

func (l *LlmOpenAiCompatible) addThinkingConfig(body map[string]any) {
	if l.caps == nil || !l.caps.SupportsReasoning {
		return
	}

	if !l.llmCfg.Reasoning {
		l.disableThinking(body)
		return
	}

	effort := l.llmCfg.ReasoningEffort
	if effort == "" {
		effort = "high"
	}

	switch l.llmCfg.Provider {
	case "deepseek":
		body["thinking"] = map[string]string{"type": "enabled"}
		body["reasoning_effort"] = effort

	case "qwen":
		body["enable_thinking"] = true

	case "zhipu":
		body["thinking"] = map[string]string{"type": "enabled"}

	default:
		body["reasoning_effort"] = effort
	}
}

func (l *LlmOpenAiCompatible) disableThinking(body map[string]any) {
	switch l.llmCfg.Provider {
	case "deepseek", "zhipu":
		body["thinking"] = map[string]string{"type": "disabled"}
	case "qwen":
		body["enable_thinking"] = false
	default:
		body["reasoning_effort"] = "none"
	}
}

func (l *LlmOpenAiCompatible) doRequest(ctx context.Context, reqBody map[string]any) (*openaiResponse, error) {
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", l.baseURL()+"/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if l.llmCfg.Token != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", l.llmCfg.Token))
	}
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
		if credErr := parseInsufficientCreditsError(body, resp.StatusCode, l.llmCfg.Provider); credErr != nil {
			return nil, credErr
		}
		return nil, fmt.Errorf("API error: status %s: %s", resp.Status, string(body))
	}

	var chatResp openaiResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &chatResp, nil
}

func (l *LlmOpenAiCompatible) Analyze(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error) {
	prompt := BuildPrompt(text, docTypes, peopleTypes, tagSuggestions, l.promptTemplate)

	if err := checkContentTooLarge(l.caps, SystemMessage+"\n"+prompt); err != nil {
		return nil, err
	}

	reqBody := l.buildRequestBody(SystemMessage, prompt, "", l.llmCfg.Temperature)

	chatResp, err := l.doRequest(ctx, reqBody)
	if err != nil {
		return nil, fmt.Errorf("content analyzer: %w", err)
	}

	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("empty response from LLM")
	}

	responseContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	responseContent = utils.CleanCodeBlock(responseContent)

	var analysisResult AnalysisResult
	if err := json.Unmarshal([]byte(responseContent), &analysisResult); err != nil {
		return nil, fmt.Errorf("LLM returned invalid JSON: %w\nraw: %s", err, responseContent)
	}

	analysisResult.Stats = buildTokenUsageStats(
		chatResp.Usage.PromptTokens,
		chatResp.Usage.CompletionTokens,
		chatResp.Usage.TotalTokens,
	)
	analysisResult.Prompt = prompt

	passCtxObj := openaiPassContext{SystemMessage: SystemMessage, UserPrompt: prompt}
	ctxJSON, _ := json.Marshal(passCtxObj)
	rm := json.RawMessage(ctxJSON)
	analysisResult.PassContext = &rm

	return &analysisResult, nil
}

func (l *LlmOpenAiCompatible) AnalyzeDocType(ctx context.Context, prevResult *AnalysisResult, headTailText string, docTypes []database.DocumentType) (string, error) {
	if prevResult.PassContext == nil {
		return "", fmt.Errorf("pass context is nil")
	}

	var passCtx openaiPassContext
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

	if err := checkContentTooLarge(l.caps, passCtx.SystemMessage+"\n"+passCtx.UserPrompt+"\n"+string(assistantJSON)+"\n"+docTypePrompt); err != nil {
		return "", err
	}

	messages := l.buildRequestMessages(passCtx.SystemMessage, passCtx.UserPrompt, string(assistantJSON))
	messages = append(messages, openaiChatMessage{Role: "user", Content: docTypePrompt})

	reqBody := map[string]any{
		"model":    l.llmCfg.Model,
		"messages": messages,
		"stream":   false,
	}

	if l.caps != nil && l.caps.SupportsStructuredOutput {
		reqBody["response_format"] = &openaiResponseFormat{Type: "json_object"}
	}
	if l.caps != nil && l.caps.SupportsTemperature {
		reqBody["temperature"] = 0
		reqBody["top_p"] = 1
	}
	l.addThinkingConfig(reqBody)

	chatResp, err := l.doRequest(ctx, reqBody)
	if err != nil {
		return "", fmt.Errorf("doc type refinement: %w", err)
	}

	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("empty response from LLM")
	}

	responseContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
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

func (l *LlmOpenAiCompatible) buildRequestMessages(system, user, assistant string) []openaiChatMessage {
	messages := []openaiChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	if assistant != "" {
		messages = append(messages, openaiChatMessage{Role: "assistant", Content: assistant})
	}
	return messages
}

func (l *LlmOpenAiCompatible) Name() string {
	return "openai-compatible"
}

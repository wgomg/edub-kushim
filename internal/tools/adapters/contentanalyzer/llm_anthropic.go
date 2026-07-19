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
	"github.com/wgomg/edub-kushim/internal/utils"
)

type LlmAnthropic struct {
	logger         *utils.Logger
	config         config.ToolConfig
	llmCfg         config.LlmToolConfig
	promptTemplate string
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicThinkingConfig struct {
	Type string `json:"type"`
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
	StopSeq      any                      `json:"stop_sequences,omitempty"`
	Stream       bool                     `json:"stream,omitempty"`
	TopK         int                      `json:"top_k,omitempty"`
	CacheControl *cacheControlEphemeral   `json:"cache_control,omitempty"`
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

func NewLlmAnthropic(logger *utils.Logger, cfg config.ToolConfig, llmCfg config.LlmToolConfig, promptTemplate string) (*LlmAnthropic, error) {
	return &LlmAnthropic{logger: logger, config: cfg, llmCfg: llmCfg, promptTemplate: promptTemplate}, nil
}

type anthropicPassContext struct {
	System     string `json:"system"`
	UserPrompt string `json:"user_prompt"`
}

func (l *LlmAnthropic) Analyze(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error) {
	maxTokens := 0

	prompt := BuildPrompt(text, docTypes, peopleTypes, tagSuggestions, l.promptTemplate)

	reqBody := anthropicRequest{
		Model:     l.llmCfg.Model,
		MaxTokens: maxTokens,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
		System:       systemMessage,
		Thinking:     &anthropicThinkingConfig{Type: "disabled"},
		Stream:       false,
		CacheControl: &cacheControlEphemeral{Type: "ephemeral"},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", l.llmCfg.BaseURL+"/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", l.llmCfg.Token)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed content analyzer: status %s: %s", resp.Status, string(body))
	}

	var anthResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
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

	passCtxObj := anthropicPassContext{System: systemMessage, UserPrompt: prompt}
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

	reqBody := anthropicRequest{
		Model:     l.llmCfg.Model,
		MaxTokens: 256,
		Messages: []anthropicMessage{
			{Role: "user", Content: passCtx.UserPrompt},
			{Role: "assistant", Content: string(assistantJSON)},
			{Role: "user", Content: docTypePrompt},
		},
		System:       passCtx.System,
		Thinking:     &anthropicThinkingConfig{Type: "disabled"},
		Stream:       false,
		CacheControl: &cacheControlEphemeral{Type: "ephemeral"},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", l.llmCfg.BaseURL+"/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", l.llmCfg.Token)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("doc type refinement: status %s: %s", resp.Status, string(body))
	}

	var anthResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
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
	return config.ContentAnalyzer.Anthropic
}

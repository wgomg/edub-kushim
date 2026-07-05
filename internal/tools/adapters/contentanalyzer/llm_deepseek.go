package contentanalyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type LlmDeepSeek struct {
	logger         *utils.Logger
	config         config.ToolConfig
	llmCfg         config.LlmToolConfig
	promptTemplate string
}

type deepSeekMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type deepSeekThinkingConfig struct {
	Type string `json:"type"`
}

type deepSeekRequest struct {
	Messages        []deepSeekMessage       `json:"messages"`
	Model           string                  `json:"model"`
	MaxTokens       int                     `json:"max_tokens,omitempty"`
	Thinking        *deepSeekThinkingConfig `json:"thinking,omitempty"`
	ReasoningEffort string                  `json:"reasoning_effort,omitempty"`
	ResponseFormat  *ResponseFormat         `json:"response_format,omitempty"`
	Stop            any                     `json:"stop,omitempty"`
	Stream          bool                    `json:"stream"`
	Temperature     float64                 `json:"temperature,omitempty"`
	TopP            float64                 `json:"top_p,omitempty"`
}

type deepSeekChoice struct {
	FinishReason string          `json:"finish_reason"`
	Index        int             `json:"index"`
	Message      deepSeekMessage `json:"message"`
}

type deepSeekUsage struct {
	CompletionTokens int `json:"completion_tokens"`
	PromptTokens     int `json:"prompt_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type deepSeekResponse struct {
	ID      string           `json:"id"`
	Choices []deepSeekChoice `json:"choices"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Object  string           `json:"object"`
	Usage   deepSeekUsage    `json:"usage"`
}

func NewLlmDeepSeek(logger *utils.Logger, cfg config.ToolConfig, llmCfg config.LlmToolConfig, promptTemplate string) (*LlmDeepSeek, error) {
	return &LlmDeepSeek{logger: logger, config: cfg, llmCfg: llmCfg, promptTemplate: promptTemplate}, nil
}

func (l *LlmDeepSeek) Analyze(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error) {
	maxTokens := 0
	temp := 0.0

	prompt := BuildPrompt(text, docTypes, peopleTypes, tagSuggestions, l.promptTemplate)

	reqBody := deepSeekRequest{
		Messages: []deepSeekMessage{
			{Role: "system", Content: systemMessage},
			{Role: "user", Content: prompt},
		},
		Model:          l.llmCfg.Model,
		MaxTokens:      maxTokens,
		Thinking:       &deepSeekThinkingConfig{Type: "disabled"},
		ResponseFormat: &ResponseFormat{Type: "json_object"},
		Stream:         false,
		Temperature:    temp,
		TopP:           1,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", l.llmCfg.BaseURL+"/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", l.llmCfg.Token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed content analyzer: %v", resp)
	}

	var dsResp deepSeekResponse
	if err := json.NewDecoder(resp.Body).Decode(&dsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(dsResp.Choices) == 0 || dsResp.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("empty response from LLM")
	}

	responseContent := strings.TrimSpace(dsResp.Choices[0].Message.Content)
	responseContent = utils.CleanCodeBlock(responseContent)

	var analysisResult AnalysisResult
	if err := json.Unmarshal([]byte(responseContent), &analysisResult); err != nil {
		return nil, fmt.Errorf("LLM returned invalid JSON: %w\nraw: %s", err, responseContent)
	}

	analysisResult.Stats = buildTokenUsageStats(
		dsResp.Usage.PromptTokens,
		dsResp.Usage.CompletionTokens,
		dsResp.Usage.TotalTokens,
	)
	analysisResult.Prompt = prompt

	return &analysisResult, nil
}

func (l *LlmDeepSeek) Name() string {
	return config.ContentAnalyzer.DeepSeek
}

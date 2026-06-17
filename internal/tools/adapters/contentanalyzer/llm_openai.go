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

type LlmOpenAi struct {
	logger *utils.Logger
	config config.ToolConfig
	llmCfg config.LlmToolConfig
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ResponseFormat struct {
	Type string `json:"type"`
}

type LlmOpenAiRequest struct {
	Messages         []ChatMessage  `json:"messages"`
	Model            string         `json:"model"`
	ReasoningEffort  string         `json:"reasoning_effort,omitempty"`
	FrequencyPenalty float64        `json:"frequency_penalty,omitempty"`
	MaxTokens        int            `json:"max_completion_tokens,omitempty"`
	PresencePenalty  float64        `json:"presence_penalty,omitempty"`
	ResponseFormat   ResponseFormat `json:"response_format"`
	Stop             any            `json:"stop,omitempty"`
	Stream           bool           `json:"stream"`
	Temperature      float64        `json:"temperature,omitempty"`
	TopP             float64        `json:"top_p,omitempty"`
}

type openaiMessage struct {
	Content string `json:"content"`
	Role    string `json:"role"`
}

type Choice struct {
	FinishReason string        `json:"finish_reason"`
	Index        int           `json:"index"`
	Message      openaiMessage `json:"message"`
}

type Usage struct {
	CompletionTokens int `json:"completion_tokens"`
	PromptTokens     int `json:"prompt_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Object  string   `json:"object"`
	Usage   Usage    `json:"usage"`
}

func NewLlmOpenAi(logger *utils.Logger, cfg config.ToolConfig, llmCfg config.LlmToolConfig) (*LlmOpenAi, error) {
	return &LlmOpenAi{logger: logger, config: cfg, llmCfg: llmCfg}, nil
}

func (l *LlmOpenAi) Analyze(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error) {
	freqPen := 0.0
	maxTokens := 0
	prescPen := 0.0
	temp := 0.0

	prompt := BuildPrompt(text, docTypes, peopleTypes, tagSuggestions)

	reqBody := LlmOpenAiRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: systemMessage},
			{Role: "user", Content: prompt},
		},
		Model:            l.llmCfg.Model,
		ReasoningEffort:  "low",
		FrequencyPenalty: freqPen,
		MaxTokens:        maxTokens,
		PresencePenalty:  prescPen,
		ResponseFormat:   ResponseFormat{Type: "json_object"},
		Stream:           false,
		Temperature:      temp,
		TopP:             1,
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

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("empty response from LLM")
	}

	responseContent := chatResp.Choices[0].Message.Content
	responseContent = strings.TrimSpace(responseContent)
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

	return &analysisResult, nil
}

func (l *LlmOpenAi) Name() string {
	return config.ContentAnalyzer.OpenAI
}

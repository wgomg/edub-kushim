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

type anthropicRequest struct {
	Model     string                   `json:"model"`
	MaxTokens int                      `json:"max_tokens"`
	Messages  []anthropicMessage       `json:"messages"`
	System    string                   `json:"system,omitempty"`
	Thinking  *anthropicThinkingConfig `json:"thinking,omitempty"`
	StopSeq   any                      `json:"stop_sequences,omitempty"`
	Stream    bool                     `json:"stream,omitempty"`
	TopK      int                      `json:"top_k,omitempty"`
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

func (l *LlmAnthropic) Analyze(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error) {
	maxTokens := 0

	prompt := BuildPrompt(text, docTypes, peopleTypes, tagSuggestions, l.promptTemplate)

	reqBody := anthropicRequest{
		Model:     l.llmCfg.Model,
		MaxTokens: maxTokens,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
		System:   systemMessage,
		Thinking: &anthropicThinkingConfig{Type: "disabled"},
		Stream:   false,
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
		return nil, fmt.Errorf("failed content analyzer: %v", resp)
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

	return &analysisResult, nil
}

func (l *LlmAnthropic) Name() string {
	return config.ContentAnalyzer.Anthropic
}

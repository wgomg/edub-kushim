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

type LlmOllama struct {
	logger *utils.Logger
	config config.ToolConfig
	llmCfg config.LlmToolConfig
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
}

type ollamaRequest struct {
	Model     string          `json:"model"`
	Messages  []ollamaMessage `json:"messages"`
	Stream    bool            `json:"stream"`
	Format    string          `json:"format,omitempty"`
	Options   *ollamaOptions  `json:"options,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
}

type ollamaResponseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaResponse struct {
	Model           string                `json:"model"`
	CreatedAt       string                `json:"created_at"`
	Message         ollamaResponseMessage `json:"message"`
	Done            bool                  `json:"done"`
	DoneReason      string                `json:"done_reason,omitempty"`
	TotalDuration   int64                 `json:"total_duration,omitempty"`
	LoadDuration    int64                 `json:"load_duration,omitempty"`
	PromptEvalCount int                   `json:"prompt_eval_count,omitempty"`
	EvalCount       int                   `json:"eval_count,omitempty"`
}

func NewLlmOllama(logger *utils.Logger, cfg config.ToolConfig, llmCfg config.LlmToolConfig) (*LlmOllama, error) {
	return &LlmOllama{logger: logger, config: cfg, llmCfg: llmCfg}, nil
}

func (l *LlmOllama) Analyze(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error) {
	temp := 0.0

	prompt := BuildPrompt(text, docTypes, peopleTypes, tagSuggestions)

	reqBody := ollamaRequest{
		Model: l.llmCfg.Model,
		Messages: []ollamaMessage{
			{Role: "system", Content: systemMessage},
			{Role: "user", Content: prompt},
		},
		Stream:    false,
		Format:    "json",
		Options:   &ollamaOptions{Temperature: temp, TopP: 1},
		KeepAlive: "5m",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", l.llmCfg.BaseURL+"/api/chat", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed content analyzer: %v", resp)
	}

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if ollamaResp.Message.Content == "" {
		return nil, fmt.Errorf("empty response from LLM")
	}

	responseContent := strings.TrimSpace(ollamaResp.Message.Content)
	responseContent = utils.CleanCodeBlock(responseContent)

	var analysisResult AnalysisResult
	if err := json.Unmarshal([]byte(responseContent), &analysisResult); err != nil {
		return nil, fmt.Errorf("LLM returned invalid JSON: %w\nraw: %s", err, responseContent)
	}

	analysisResult.Stats = buildTokenUsageStats(
		ollamaResp.PromptEvalCount,
		ollamaResp.EvalCount,
		ollamaResp.PromptEvalCount+ollamaResp.EvalCount,
	)
	analysisResult.Prompt = prompt

	return &analysisResult, nil
}

func (l *LlmOllama) Name() string {
	return "llmollama"
}

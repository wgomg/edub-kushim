package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/llm"
)

// sleepAfterRequest paces requests toward rate-limited providers. Aborts early
// when the batch context is cancelled.
func sleepAfterRequest(ctx context.Context, delaySeconds float64) {
	if delaySeconds <= 0 {
		return
	}
	select {
	case <-time.After(time.Duration(delaySeconds * float64(time.Second))):
	case <-ctx.Done():
	}
}

// ContentTooLargeError is raised before a call when the prompt's estimated
// token count exceeds the model's cataloged input limit. The runner never
// retries it on a fallback.
type ContentTooLargeError struct {
	EstimatedTokens int
	MaxInputTokens  int
}

func (e *ContentTooLargeError) Error() string {
	return fmt.Sprintf("estimated %d tokens exceeds model limit of %d", e.EstimatedTokens, e.MaxInputTokens)
}

// TokenLimitError is a provider-side context-length rejection, parsed
// from the response body.
type TokenLimitError struct {
	MaxTokens       int
	RequestedTokens int
	RawBody         string
}

func (e *TokenLimitError) Error() string {
	return fmt.Sprintf("token limit: requested %d tokens, model max is %d", e.RequestedTokens, e.MaxTokens)
}

// InsufficientCreditsError means the provider account cannot pay: HTTP
// 402, a 429 the caller treats as billing, or the qwen arrearage quirk.
type InsufficientCreditsError struct {
	Provider   string
	HTTPStatus int
	RawBody    string // retained for debugging; not surfaced in Error()
}

func (e *InsufficientCreditsError) Error() string {
	return fmt.Sprintf("insufficient credits (%s, HTTP %d): the provider account has run out of credits or has billing issues", e.Provider, e.HTTPStatus)
}

func parseInsufficientCreditsError(body []byte, statusCode int, provider string) error {
	if statusCode == 402 {
		return &InsufficientCreditsError{Provider: provider, HTTPStatus: statusCode, RawBody: string(body)}
	}
	if statusCode == 429 {
		return &InsufficientCreditsError{Provider: provider, HTTPStatus: statusCode, RawBody: string(body)}
	}
	if provider == "qwen" && statusCode == 400 && strings.Contains(strings.ToLower(string(body)), "arrearage") {
		return &InsufficientCreditsError{Provider: provider, HTTPStatus: statusCode, RawBody: string(body)}
	}
	return nil
}

var tokenLimitRE = regexp.MustCompile(
	`maximum context length is (\d+) tokens.*?you requested (?:about )?(\d+) tokens`,
)

func checkContentTooLarge(caps *llm.ModelCapability, fullPrompt string) error {
	if caps == nil || caps.MaxInputTokens <= 0 {
		return nil
	}
	estimated := estimateTokens(fullPrompt)
	if estimated > caps.MaxInputTokens {
		return &ContentTooLargeError{EstimatedTokens: estimated, MaxInputTokens: caps.MaxInputTokens}
	}
	return nil
}

func parseTokenLimitError(body []byte) error {
	matches := tokenLimitRE.FindSubmatch(body)
	if matches == nil {
		return nil
	}
	max, _ := strconv.Atoi(string(matches[1]))
	req, _ := strconv.Atoi(string(matches[2]))
	if max == 0 || req == 0 {
		return nil
	}
	return &TokenLimitError{MaxTokens: max, RequestedTokens: req, RawBody: string(body)}
}

type tokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func buildTokenUsageStats(prompt, completion, total int) *json.RawMessage {
	stats := tokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
	}
	data, err := json.Marshal(stats)
	if err != nil {
		return nil
	}
	rm := json.RawMessage(data)
	return &rm
}

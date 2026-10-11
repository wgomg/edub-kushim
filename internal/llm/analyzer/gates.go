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

// Output budgets per call type: the analysis family answers with one record per document, judged steps
// answer with one small record per candidate. The catalog's max_output_tokens is only a ceiling — the
// effective bound is min(budget, ceiling) — and a budget applies even with no catalog entry. The constants
// are loose: observed judge entries run ~40-80 tokens per candidate. The quality judge carries the widest
// per-candidate envelope (index, term, four criteria, defer) and runs reasoning models whose reasoning
// tokens share the output bound, so it gets its own larger budget; a 40-candidate batch must fit without
// truncation.
const (
	analysisOutputBudget = 4096
	judgedOutputBase     = 512
	judgedOutputPerItem  = 128
	qualityOutputBase    = 2048
	qualityOutputPerItem = 256
)

// judgedOutputBudget is the one-field judged steps' budget: adjudication, parts, compounds, phrase folds,
// identity, and translation.
func judgedOutputBudget(n int) int {
	return judgedOutputBase + judgedOutputPerItem*n
}

// qualityOutputBudget is the quality judge's budget: the wider rubric envelope, with headroom for the
// reasoning tokens a reasoning model spends against the same output bound.
func qualityOutputBudget(n int) int {
	return qualityOutputBase + qualityOutputPerItem*n
}

// clampOutputBudget caps budget at the cataloged model's true maximum; with
// nil caps or no cataloged maximum the budget passes through unchanged. A
// model that cannot disable reasoning runs its default path at an effort
// level, so its reasoning tokens share the output bound and the budget is
// doubled first; unused headroom is never billed.
func clampOutputBudget(caps *llm.ModelCapability, budget int) int {
	if caps.RunsAtEffortLevel() {
		budget *= 2
	}
	if caps != nil && caps.MaxOutputTokens > 0 && budget > caps.MaxOutputTokens {
		return caps.MaxOutputTokens
	}
	return budget
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

// OutputCapError reports that generation stopped at the request's output
// bound: the reply is truncated at Cap tokens. Judged calls never retry it.
type OutputCapError struct {
	Model string
	Cap   int
}

func (e *OutputCapError) Error() string {
	return fmt.Sprintf("model %s hit the %d-token output cap, reply truncated", e.Model, e.Cap)
}

// replyParsePrefixBytes bounds the raw text a ReplyParseError carries.
const replyParsePrefixBytes = 2048

// ReplyParseError reports a judged reply that could not be parsed. It
// carries the step that failed, the model, the raw reply's total length and
// a bounded prefix of it, so a degenerate multi-thousand-line reply cannot
// flood the log.
type ReplyParseError struct {
	Step      string
	Model     string
	RawLen    int
	RawPrefix string
}

func (e *ReplyParseError) Error() string {
	return fmt.Sprintf("%s: model %s returned unparseable JSON (%d bytes)\nraw: %s", e.Step, e.Model, e.RawLen, e.RawPrefix)
}

// newReplyParseError captures a judged reply's parse failure with the raw
// text truncated to the diagnostic prefix.
func newReplyParseError(step, model, raw string) *ReplyParseError {
	prefix := raw
	if len(prefix) > replyParsePrefixBytes {
		prefix = prefix[:replyParsePrefixBytes]
	}
	return &ReplyParseError{Step: step, Model: model, RawLen: len(raw), RawPrefix: prefix}
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

package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/log"
)

// LlmOpenAiCompatible is the OpenAI-compatible chat-completions
// implementation of ContentAnalyzer, gated by the model catalog's
// capabilities (thinking, temperature, structured output).
type LlmOpenAiCompatible struct {
	logger         *log.Logger
	config         config.ToolConfig
	llmCfg         *config.LlmConfig
	caps           *llm.ModelCapability
	promptTemplate string
	minTags        int
	maxTags        int
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

// NewLlmOpenAiCompatible wires the adapter; caps may be nil, in which
// case the capability gates are skipped.
func NewLlmOpenAiCompatible(logger *log.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, caps *llm.ModelCapability, promptTemplate string, minTags, maxTags int, reg *llm.Registry) (*LlmOpenAiCompatible, error) {
	return &LlmOpenAiCompatible{
		logger:         logger,
		config:         cfg,
		llmCfg:         llmCfg,
		caps:           caps,
		promptTemplate: promptTemplate,
		minTags:        minTags,
		maxTags:        maxTags,
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
	return l.buildRequest(l.buildRequestMessages(system, user, assistant), temp)
}

// buildRequest assembles the body every compatible-API call shares: model,
// messages, the stream flag, and the capability-gated envelope under the
// model catalog's caps; any body-level field is set here, never at call
// sites.
func (l *LlmOpenAiCompatible) buildRequest(messages []openaiChatMessage, temp float64) map[string]any {
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
	if h := l.reg.ProviderSessionHeader(l.llmCfg.Provider); h != "" {
		httpReq.Header.Set(h, llm.SessionID())
	}

	resp, err := l.client.Do(httpReq)
	defer sleepAfterRequest(ctx, l.llmCfg.RequestDelay)
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

// Analyze runs the document analysis call against the compatible API.
func (l *LlmOpenAiCompatible) Analyze(ctx context.Context, text string, docTypes []TypeRef, peopleTypes []TypeRef, tagSuggestions []string) (*AnalysisResult, error) {
	prompt := BuildPrompt(text, docTypes, peopleTypes, tagSuggestions, l.promptTemplate, l.minTags, l.maxTags)

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
	responseContent = cleanCodeBlock(responseContent)

	analysisResult, err := ParseAnalysisResponse(responseContent)
	if err != nil {
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

	return analysisResult, nil
}

// AnalyzeDocType refines the document type against the compatible API.
func (l *LlmOpenAiCompatible) AnalyzeDocType(ctx context.Context, prevResult *AnalysisResult, headTailText string, docTypes []TypeRef, metadata DocMetadata) (string, error) {
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

	docTypePrompt := BuildDocTypePrompt(headTailText, docTypes, metadata)

	if err := checkContentTooLarge(l.caps, passCtx.SystemMessage+"\n"+passCtx.UserPrompt+"\n"+string(assistantJSON)+"\n"+docTypePrompt); err != nil {
		return "", err
	}

	messages := l.buildRequestMessages(passCtx.SystemMessage, passCtx.UserPrompt, string(assistantJSON))
	messages = append(messages, openaiChatMessage{Role: "user", Content: docTypePrompt})

	reqBody := l.buildRequest(messages, 0)

	chatResp, err := l.doRequest(ctx, reqBody)
	if err != nil {
		return "", fmt.Errorf("doc type refinement: %w", err)
	}

	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("empty response from LLM")
	}

	responseContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	responseContent = cleanCodeBlock(responseContent)

	var result struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(responseContent), &result); err != nil {
		return "", fmt.Errorf("LLM returned invalid JSON: %w\nraw: %s", err, responseContent)
	}
	if result.Type == "" {
		if full, perr := ParseAnalysisResponse(responseContent); perr == nil {
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

// judgedOpenAI runs one judged step against the compatible API: the size
// gate, the request envelope, the labeled transport call, the empty
// response check, the bare-array drift log and the parser. A parse failure
// carries the model name and the raw reply.
func judgedOpenAI[T any](ctx context.Context, l *LlmOpenAiCompatible, system, prompt, label string, n int, parse func(string, int) (T, error)) (T, error) {
	var zero T

	if err := checkContentTooLarge(l.caps, system+"\n"+prompt); err != nil {
		return zero, err
	}

	reqBody := l.buildRequestBody(system, prompt, "", 0)

	chatResp, err := l.doRequest(ctx, reqBody)
	if err != nil {
		return zero, fmt.Errorf("%s: %w", label, err)
	}

	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message.Content == "" {
		return zero, fmt.Errorf("empty response from LLM")
	}

	raw := chatResp.Choices[0].Message.Content
	content := cleanCodeBlock(strings.TrimSpace(raw))
	if isBareArray(content) {
		l.logger.Debug("", "%s: model %s replied with a bare top-level array, wrapped", label, l.llmCfg.Model)
	}
	results, err := parse(content, n)
	if err != nil {
		return zero, fmt.Errorf("%w (model %s)\nraw: %s", err, l.llmCfg.Model, raw)
	}
	return results, nil
}

// Adjudicate answers the same-intent pairs through the compatible API.
func (l *LlmOpenAiCompatible) Adjudicate(ctx context.Context, pairs []TagPairEvidence) ([]TagVerdictResult, error) {
	return judgedOpenAI(ctx, l, AdjudicationSystemMessage, BuildAdjudicationPrompt(pairs), "tag adjudication", len(pairs), ParseAdjudicationResponse)
}

// AdjudicateTagSplits decides splits through the compatible API.
func (l *LlmOpenAiCompatible) AdjudicateTagSplits(ctx context.Context, tags []string) ([]TagSplitResult, error) {
	return judgedOpenAI(ctx, l, TagSplitSystemMessage, BuildTagSplitPrompt(tags), "tag split adjudication", len(tags), ParseTagSplitResponse)
}

// JudgeTagQuality scores candidates through the compatible API.
func (l *LlmOpenAiCompatible) JudgeTagQuality(ctx context.Context, text string, candidates []TagQualityCandidate) ([]TagQualityResult, error) {
	return judgedOpenAI(ctx, l, TagQualitySystemMessage, BuildTagQualityPrompt(text, candidates), "tag quality judge", len(candidates), ParseTagQualityResponse)
}

// JudgeTagIdentity decides canonical forms through the compatible API.
func (l *LlmOpenAiCompatible) JudgeTagIdentity(ctx context.Context, candidates []TagIdentityCandidate) ([]TagIdentityResult, error) {
	return judgedOpenAI(ctx, l, TagIdentitySystemMessage, BuildTagIdentityPrompt(candidates), "tag identity judge", len(candidates), ParseTagIdentityResponse)
}

// Name identifies the OpenAI-compatible implementation.
func (l *LlmOpenAiCompatible) Name() string {
	return "openai-compatible"
}

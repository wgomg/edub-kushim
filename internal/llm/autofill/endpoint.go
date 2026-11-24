package autofill

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	probeTimeout     = 10 * time.Second
	probeMaxBytes    = 8 << 20
	maxListedModels  = 2000
	anthropicVersion = "2023-06-01"
)

// endpointModel is the subset of one provider model-list entry this package reads; the key names cover
// the OpenAI-compatible shape and the documented Anthropic and DeepSeek extensions.
type endpointModel struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Name          string `json:"name"`
	MaxInput      int    `json:"max_input_tokens"`
	ContextWindow int    `json:"context_window"`
	MaxOutput     int    `json:"max_output_tokens"`
	MaxTokens     int    `json:"max_tokens"`
	Effort        *struct {
		SupportedLevels []string `json:"supported_levels"`
	} `json:"effort"`
	Capabilities *struct {
		StructuredOutputs *struct {
			Supported bool `json:"supported"`
		} `json:"structured_outputs"`
		Thinking *struct {
			Supported bool `json:"supported"`
			Types     struct {
				Disabled *struct {
					Supported bool `json:"supported"`
				} `json:"disabled"`
			} `json:"types"`
		} `json:"thinking"`
		Effort map[string]json.RawMessage `json:"effort"`
	} `json:"capabilities"`
}

// probeResult is one model-list probe: the classified status and, when it succeeded, the entries.
type probeResult struct {
	status EndpointStatus
	models []endpointModel
}

// probeEndpoint asks a provider for its model list. The request mirrors the adapter the catalog will
// use: the same auth header, and the Anthropic page size that returns the whole list in one call. It
// never follows redirects.
func (c *Client) probeEndpoint(ctx context.Context, base, adapter, token string) probeResult {
	raw := strings.TrimRight(base, "/") + "/models"
	if adapter == adapterAnthropic {
		raw += "?limit=1000"
	}
	result := probeResult{status: EndpointStatus{URL: raw, Status: statusUnreachable}}

	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		result.status.Status = statusInvalidResponse
		result.status.Detail = "not an absolute http(s) URL"
		return result
	}

	attemptCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, raw, nil)
	if err != nil {
		result.status.Status = statusInvalidResponse
		return result
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		if adapter == adapterAnthropic {
			req.Header.Set("x-api-key", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	if adapter == adapterAnthropic {
		req.Header.Set("anthropic-version", anthropicVersion)
	}

	resp, err := c.probe.Do(req)
	if err != nil {
		result.status.Detail = transportDetail(attemptCtx, err)
		return result
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		result.status.Status = statusRedirected
		if location, err := resp.Location(); err == nil && location.Host != "" {
			result.status.Detail = location.Host
		}
		return result
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		result.status.Status = statusUnauthorized
		result.status.Detail = resp.Status
		return result
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
		result.status.Status = statusNotFound
		result.status.Detail = resp.Status
		return result
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		result.status.Detail = resp.Status
		return result
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, probeMaxBytes+1))
	if err != nil {
		result.status.Detail = transportDetail(attemptCtx, err)
		return result
	}
	if len(body) > probeMaxBytes {
		result.status.Status = statusInvalidResponse
		result.status.Detail = "response too large"
		return result
	}

	var list struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil || list.Data == nil {
		result.status.Status = statusInvalidResponse
		result.status.Detail = "not a model list"
		return result
	}

	models := make([]endpointModel, 0, len(list.Data))
	for _, rawModel := range list.Data {
		var model endpointModel
		if err := json.Unmarshal(rawModel, &model); err != nil || model.ID == "" {
			continue
		}
		models = append(models, model)
	}
	result.status.Status = statusOK
	result.models = models
	return result
}

// transportDetail reports a failed probe without echoing any body: the context state when the attempt
// ended, else the transport error itself, whose URL carries no credential.
func transportDetail(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timed out"
	case errors.Is(ctx.Err(), context.Canceled):
		return "canceled"
	default:
		return err.Error()
	}
}

// endpointFields maps one model-list entry onto the form's fields, skipping zero limits and anything the
// endpoint did not publish.
func endpointFields(model endpointModel) ModelFields {
	var fields ModelFields

	switch {
	case model.DisplayName != "":
		fields.DisplayName = model.DisplayName
	case model.Name != "":
		fields.DisplayName = model.Name
	}
	if value := firstPositive(model.MaxInput, model.ContextWindow); value > 0 {
		fields.MaxInputTokens = &value
	}
	if value := firstPositive(model.MaxOutput, model.MaxTokens); value > 0 {
		fields.MaxOutputTokens = &value
	}
	if levels := endpointEfforts(model); len(levels) > 0 {
		fields.ReasoningEfforts = &levels
	}
	if thinking := model.Capabilities; thinking != nil && thinking.Thinking != nil &&
		thinking.Thinking.Supported && thinking.Thinking.Types.Disabled != nil {
		value := thinking.Thinking.Types.Disabled.Supported
		fields.DisableReasoning = &value
	}
	if capabilities := model.Capabilities; capabilities != nil && capabilities.StructuredOutputs != nil {
		value := capabilities.StructuredOutputs.Supported
		fields.SupportsStructuredOutput = &value
	}
	return fields
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// endpointEfforts reads the effort levels a model-list entry publishes, from DeepSeek's supported_levels
// list or from Anthropic's per-level support map.
func endpointEfforts(model endpointModel) []string {
	if model.Effort != nil && len(model.Effort.SupportedLevels) > 0 {
		return sortEfforts(model.Effort.SupportedLevels)
	}
	if model.Capabilities == nil || model.Capabilities.Effort == nil {
		return nil
	}

	var levels []string
	for level, raw := range model.Capabilities.Effort {
		if level == "supported" {
			continue
		}
		var entry struct {
			Supported bool `json:"supported"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil || !entry.Supported {
			continue
		}
		levels = append(levels, level)
	}
	if len(levels) == 0 {
		return nil
	}
	return sortEfforts(levels)
}

// findEndpointModel looks the requested id up in the probed list, exact match first.
func findEndpointModel(models []endpointModel, id string) (endpointModel, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	for _, model := range models {
		if strings.EqualFold(model.ID, id) {
			return model, true
		}
	}
	return endpointModel{}, false
}

// listedModels renders the probed list for the model-id suggestions, sorted and capped.
func listedModels(models []endpointModel) []ModelRef {
	out := make([]ModelRef, 0, len(models))
	for _, model := range models {
		name := model.DisplayName
		if name == "" {
			name = model.Name
		}
		out = append(out, ModelRef{ID: model.ID, DisplayName: name})
	}
	slices.SortFunc(out, func(a, b ModelRef) int { return strings.Compare(a.ID, b.ID) })
	if len(out) > maxListedModels {
		out = out[:maxListedModels]
	}
	return out
}

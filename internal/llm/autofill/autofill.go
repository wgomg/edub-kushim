// Package autofill proposes values for the catalog editor's provider and model forms. The provider's own
// model-list endpoint supplies the model ids the configured host serves, models.dev supplies capability
// metadata, and the embedded default catalog supplies the vendor-specific reasoning request shape that
// neither external source publishes. Nothing here writes a catalog file.
package autofill

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/llm"
)

// ErrUnknownProvider reports a models_dev_provider id that the models.dev snapshot does not contain.
var ErrUnknownProvider = errors.New("unknown models.dev provider")

const (
	adapterOpenAICompatible = "openai-compatible"
	adapterAnthropic        = "anthropic"

	sourceModelsDev = "models.dev"
	sourceEndpoint  = "endpoint"
	sourceDefaults  = "defaults"

	statusOK              = "ok"
	statusUnauthorized    = "unauthorized"
	statusNotFound        = "not_found"
	statusRedirected      = "redirected"
	statusUnreachable     = "unreachable"
	statusInvalidResponse = "invalid_response"
)

// AutofillRequest is one autofill lookup: the provider form's current values, the model being edited when
// the model form asks, an optional models.dev provider id picked from an earlier response's candidates,
// and the optional provider token used only for the model-list probe.
type AutofillRequest struct {
	Provider          string `json:"provider,omitempty"`
	Adapter           string `json:"adapter,omitempty"`
	OfficialURL       string `json:"official_url,omitempty"`
	ModelID           string `json:"model_id,omitempty"`
	ModelsDevProvider string `json:"models_dev_provider,omitempty"`
	Token             string `json:"token,omitempty"`
}

// AutofillResponse is what the lookup found. An absent field means the sources had nothing for it, so
// the editor leaves the form value untouched; the notes explain every source that failed or disagreed.
type AutofillResponse struct {
	Provider           *ProviderSuggestion `json:"provider,omitempty"`
	ProviderCandidates []ProviderCandidate `json:"provider_candidates,omitempty"`
	Endpoint           *EndpointStatus     `json:"endpoint,omitempty"`
	Models             []ModelRef          `json:"models,omitempty"`
	Model              *ModelFields        `json:"model,omitempty"`
	Filled             []FilledField       `json:"filled,omitempty"`
	Notes              []string            `json:"notes,omitempty"`
}

// ProviderSuggestion is what the provider form takes from models.dev: the endpoint base of the protocol
// the adapter speaks, and the adapter when the current one cannot work.
type ProviderSuggestion struct {
	OfficialURL string `json:"official_url,omitempty"`
	Adapter     string `json:"adapter,omitempty"`
}

// ProviderCandidate is one plausible models.dev provider when the request matched several; the editor
// repeats the lookup with ModelsDevProvider set to the chosen id.
type ProviderCandidate struct {
	ModelsDevProvider string `json:"models_dev_provider"`
	Name              string `json:"name"`
}

// EndpointStatus reports how the provider's model-list probe ended: URL is the probed address, Status
// one of ok, unauthorized, not_found, redirected, unreachable or invalid_response, and Detail a short
// classification that never carries the response body.
type EndpointStatus struct {
	URL    string `json:"url"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// ModelRef is one model id the provider's endpoint listed, with its display name when the endpoint does
// publish one.
type ModelRef struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
}

// ModelFields is the model form's suggestion set: every key is the ModelInput JSON name of the same
// field, and a present key means a source reported it. The two reasoning request fields come from the
// embedded default catalog only.
type ModelFields struct {
	DisplayName              string               `json:"display_name,omitempty"`
	MaxInputTokens           *int                 `json:"max_input_tokens,omitempty"`
	MaxOutputTokens          *int                 `json:"max_output_tokens,omitempty"`
	DisableReasoning         *bool                `json:"disable_reasoning,omitempty"`
	ReasoningEfforts         *[]string            `json:"reasoning_efforts,omitempty"`
	SupportsStructuredOutput *bool                `json:"supports_structured_output,omitempty"`
	SupportsTemperature      *bool                `json:"supports_temperature,omitempty"`
	SupportsPromptCaching    *bool                `json:"supports_prompt_caching,omitempty"`
	UsesMaxCompletionTokens  *bool                `json:"uses_max_completion_tokens,omitempty"`
	UsesResponsesEndpoint    *bool                `json:"uses_responses_endpoint,omitempty"`
	ReasoningToggle          *llm.ReasoningToggle `json:"reasoning_toggle,omitempty"`
	ReasoningEffortField     *string              `json:"reasoning_effort_field,omitempty"`
}

// FilledField names one field the lookup filled and the source that supplied it.
type FilledField struct {
	Field  string `json:"field"`
	Source string `json:"source"`
}

// Client performs autofill lookups and caches the models.dev snapshot for its lifetime.
type Client struct {
	mu           sync.Mutex
	modelsDevURL string
	snapshot     map[string]*providerIndex
	fetchedAt    time.Time
	modelsDev    *http.Client
	probe        *http.Client
	now          func() time.Time
}

// NewClient returns a client for the production endpoints. The first models.dev lookup downloads the
// snapshot; later ones reuse it until its 24-hour lifetime expires.
func NewClient() *Client {
	return &Client{
		modelsDevURL: modelsDevSnapshotURL,
		modelsDev:    &http.Client{},
		probe: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		now: time.Now,
	}
}

// Fill resolves what it can for one form submission. A source being unavailable is reported in Notes,
// never as an error; ErrUnknownProvider is returned only for an explicit ModelsDevProvider id that the
// snapshot does not contain.
func (c *Client) Fill(ctx context.Context, req AutofillRequest) (AutofillResponse, error) {
	var out AutofillResponse
	adapter := req.Adapter
	if adapter == "" {
		adapter = adapterOpenAICompatible
	}

	providers, snapshotNote := c.providerSnapshot(ctx)
	if snapshotNote != "" {
		out.Notes = append(out.Notes, snapshotNote)
	}

	var matched *providerIndex
	if req.ModelsDevProvider != "" {
		matched = providers[strings.ToLower(strings.TrimSpace(req.ModelsDevProvider))]
		if matched == nil {
			return AutofillResponse{}, fmt.Errorf("%w: %q", ErrUnknownProvider, req.ModelsDevProvider)
		}
	} else if providers != nil {
		matched, out.ProviderCandidates = matchProvider(providers, req.Provider, req.OfficialURL)
	}

	var suggestion *endpointSuggestion
	if matched != nil {
		suggestion = suggestEndpoint(matched, adapter)
		if suggestion == nil {
			out.Notes = append(out.Notes, fmt.Sprintf(
				"models.dev publishes %s only over %s; there is no endpoint for the current adapter.",
				matched.id, strings.Join(matched.protocols, ", ")))
		}
	}

	base := strings.TrimSpace(req.OfficialURL)
	if base == "" && suggestion != nil {
		base = suggestion.url
	}

	var models []endpointModel
	if base != "" {
		result := c.probeEndpoint(ctx, base, adapter, req.Token)
		out.Endpoint = &result.status
		models = result.models
	} else {
		out.Notes = append(out.Notes, "enter a provider URL")
	}

	if matched != nil && suggestion != nil {
		provider := &ProviderSuggestion{Adapter: suggestion.adapter}
		if req.OfficialURL == "" || (out.Endpoint != nil && out.Endpoint.Status == statusNotFound) {
			provider.OfficialURL = suggestion.url
		}
		out.Provider = provider
	}

	if req.ModelID != "" {
		out.Model, out.Filled, out.Notes = modelSuggestions(req, adapter, matched, out.Endpoint, models, out.Notes)
	}

	if out.Endpoint != nil && out.Endpoint.Status == statusOK {
		out.Models = listedModels(models)
	}

	return out, nil
}

// modelSuggestions merges the model-level sources: models.dev first, the provider endpoint over it, and
// the embedded default catalog for the two reasoning request fields.
func modelSuggestions(
	req AutofillRequest,
	adapter string,
	matched *providerIndex,
	endpoint *EndpointStatus,
	models []endpointModel,
	notes []string,
) (*ModelFields, []FilledField, []string) {
	fields := ModelFields{}
	sources := map[string]string{}

	if matched != nil {
		if m, ok := findModel(matched.provider, req.ModelID); ok {
			if m.Status == "deprecated" {
				notes = append(notes, fmt.Sprintf("models.dev marks %s as deprecated.", m.ID))
			}
			suggested, devNotes := modelsDevFields(m, adapter)
			notes = append(notes, devNotes...)
			mergeModelFields(&fields, suggested, sources, sourceModelsDev)
		} else {
			notes = append(notes, fmt.Sprintf("%s is not in models.dev under %s.", req.ModelID, matched.id))
		}
	}

	if endpoint != nil && endpoint.Status == statusOK {
		entry, ok := findEndpointModel(models, req.ModelID)
		if !ok {
			notes = append(notes, fmt.Sprintf("%s is not listed by this endpoint.", req.ModelID))
		} else {
			mergeModelFields(&fields, endpointFields(entry), sources, sourceEndpoint)
		}
	}

	if adapter != adapterAnthropic {
		knob, ok, err := llm.DefaultReasoningKnob(strings.TrimSpace(req.Provider))
		switch {
		case err != nil:
			notes = append(notes, fmt.Sprintf("the embedded default catalog could not be read: %v", err))
		case ok:
			if knob.Toggle != nil {
				fields.ReasoningToggle = knob.Toggle
				sources["reasoning_toggle"] = sourceDefaults
			}
			fields.ReasoningEffortField = &knob.Effort
			sources["reasoning_effort_field"] = sourceDefaults
		}
	}

	if len(sources) == 0 {
		notes = append(notes, fmt.Sprintf("no catalog values found for %s.", req.ModelID))
		return nil, nil, notes
	}
	return &fields, filledFields(sources), notes
}

// mergeModelFields copies every field a source reported, recording the source that supplied it.
func mergeModelFields(dst *ModelFields, src ModelFields, sources map[string]string, source string) {
	if src.DisplayName != "" {
		dst.DisplayName = src.DisplayName
		sources["display_name"] = source
	}
	if src.MaxInputTokens != nil {
		dst.MaxInputTokens = src.MaxInputTokens
		sources["max_input_tokens"] = source
	}
	if src.MaxOutputTokens != nil {
		dst.MaxOutputTokens = src.MaxOutputTokens
		sources["max_output_tokens"] = source
	}
	if src.DisableReasoning != nil {
		dst.DisableReasoning = src.DisableReasoning
		sources["disable_reasoning"] = source
	}
	if src.ReasoningEfforts != nil {
		dst.ReasoningEfforts = src.ReasoningEfforts
		sources["reasoning_efforts"] = source
	}
	if src.SupportsStructuredOutput != nil {
		dst.SupportsStructuredOutput = src.SupportsStructuredOutput
		sources["supports_structured_output"] = source
	}
	if src.SupportsTemperature != nil {
		dst.SupportsTemperature = src.SupportsTemperature
		sources["supports_temperature"] = source
	}
	if src.SupportsPromptCaching != nil {
		dst.SupportsPromptCaching = src.SupportsPromptCaching
		sources["supports_prompt_caching"] = source
	}
	if src.UsesMaxCompletionTokens != nil {
		dst.UsesMaxCompletionTokens = src.UsesMaxCompletionTokens
		sources["uses_max_completion_tokens"] = source
	}
	if src.UsesResponsesEndpoint != nil {
		dst.UsesResponsesEndpoint = src.UsesResponsesEndpoint
		sources["uses_responses_endpoint"] = source
	}
}

var filledFieldOrder = []string{
	"display_name",
	"max_input_tokens",
	"max_output_tokens",
	"disable_reasoning",
	"reasoning_efforts",
	"supports_structured_output",
	"supports_temperature",
	"supports_prompt_caching",
	"uses_max_completion_tokens",
	"uses_responses_endpoint",
	"reasoning_toggle",
	"reasoning_effort_field",
}

// filledFields reports the sources in a stable field order.
func filledFields(sources map[string]string) []FilledField {
	out := make([]FilledField, 0, len(sources))
	for _, field := range filledFieldOrder {
		if source, ok := sources[field]; ok {
			out = append(out, FilledField{Field: field, Source: source})
		}
	}
	return out
}

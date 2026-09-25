package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/wgomg/edub-kushim/internal/llm"
)

// LlmHandler serves the model catalog for the UI.
type LlmHandler struct {
	registry  *llm.Registry
	cached    *LlmModelsResponse
}

// NewLlmHandler wires the handler to the model registry.
func NewLlmHandler(registry *llm.Registry) *LlmHandler {
	h := &LlmHandler{registry: registry}
	h.cached = h.buildResponse()
	return h
}

func (h *LlmHandler) buildResponse() *LlmModelsResponse {
	adapters := make(map[string][]string)
	for _, adapter := range h.registry.Adapters() {
		providers := h.registry.ProvidersForAdapter(adapter)
		if providers == nil {
			providers = []string{}
		}
		adapters[adapter] = providers
	}

	providers := make(map[string][]LlmModelEntry)
	for _, adapter := range h.registry.Adapters() {
		for _, provider := range h.registry.ProvidersForAdapter(adapter) {
			models := h.registry.ModelsForProvider(provider)
			entries := make([]LlmModelEntry, 0, len(models))
			for _, m := range models {
				entry := LlmModelEntry{
					ID: m.ID,
				}
				entry.Capabilities.SupportsReasoning = m.Capabilities.SupportsReasoning
				entry.Capabilities.ReasoningEfforts = m.Capabilities.ReasoningEffortLevels
				entry.Capabilities.MaxInputTokens = m.Capabilities.MaxInputTokens
				entry.Capabilities.MaxOutputTokens = m.Capabilities.MaxOutputTokens
				entry.Capabilities.SupportsTemperature = m.Capabilities.SupportsTemperature
				entry.Capabilities.SupportsResponseSchema = m.Capabilities.SupportsStructuredOutput
				entries = append(entries, entry)
			}
			if len(entries) > 0 {
				providers[provider] = entries
			}
		}
	}

	return &LlmModelsResponse{
		Adapters:  adapters,
		Providers: providers,
	}
}

// ListModels serves the catalog's adapters, providers and models.
func (h *LlmHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(h.cached)
}

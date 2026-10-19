package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/log"
)

// LlmHandler serves and edits the model catalog for the settings page: the read views the UI loads and
// the per-entry mutations it applies. The registry is nil when the catalog failed to load at startup, in
// which case every endpoint but the reset reports 503; a successful reset installs a fresh registry, so
// the catalog endpoints recover without a restart.
type LlmHandler struct {
	registry   atomic.Pointer[llm.Registry]
	catalogDir string
	getConfig  func() *config.Config
	logger     *log.Logger
}

// NewLlmHandler wires the handler to the model registry (nil when its load failed), the catalog directory
// the reset writes, the live configuration for its reference checks, and the logger.
func NewLlmHandler(
	registry *llm.Registry,
	catalogDir string,
	getConfig func() *config.Config,
	logger *log.Logger,
) *LlmHandler {
	h := &LlmHandler{catalogDir: catalogDir, getConfig: getConfig, logger: logger}
	h.registry.Store(registry)
	return h
}

// ListModels serves the catalog's adapters, providers and models.
func (h *LlmHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	reg := h.requireRegistry(w)
	if reg == nil {
		return
	}
	h.reloadQuiet(reg)
	writeJSON(w, http.StatusOK, h.buildModelsResponse(reg))
}

// GetCatalog serves the full editable catalog: every provider and model with all their fields.
func (h *LlmHandler) GetCatalog(w http.ResponseWriter, r *http.Request) {
	reg := h.requireRegistry(w)
	if reg == nil {
		return
	}
	h.reloadQuiet(reg)
	writeJSON(w, http.StatusOK, reg.Catalog())
}

// AddProvider creates one provider from the submitted fields.
func (h *LlmHandler) AddProvider(w http.ResponseWriter, r *http.Request) {
	reg := h.requireRegistry(w)
	if reg == nil {
		return
	}
	var in llm.ProviderInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	sanitizeProviderInput(&in)
	if err := reg.AddProvider(in); err != nil {
		writeCatalogError(w, h.logger, err)
		return
	}
	h.writeCatalog(w, reg)
}

// SetProvider rewrites a provider's adapter, URL and session header; the name is its immutable key.
func (h *LlmHandler) SetProvider(w http.ResponseWriter, r *http.Request) {
	reg := h.requireRegistry(w)
	if reg == nil {
		return
	}
	var in llm.ProviderInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	sanitizeProviderInput(&in)
	if err := reg.SetProvider(r.PathValue("name"), in); err != nil {
		writeCatalogError(w, h.logger, err)
		return
	}
	h.writeCatalog(w, reg)
}

// RemoveProvider drops a provider; the cascade query parameter also deletes its models.
func (h *LlmHandler) RemoveProvider(w http.ResponseWriter, r *http.Request) {
	reg := h.requireRegistry(w)
	if reg == nil {
		return
	}
	cascade, err := cascadeParam(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := reg.RemoveProvider(r.PathValue("name"), cascade, h.getConfig()); err != nil {
		writeCatalogError(w, h.logger, err)
		return
	}
	h.writeCatalog(w, reg)
}

// AddModel creates one model from the submitted fields.
func (h *LlmHandler) AddModel(w http.ResponseWriter, r *http.Request) {
	reg := h.requireRegistry(w)
	if reg == nil {
		return
	}
	var in llm.ModelInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	sanitizeModelInput(&in)
	if err := reg.AddModel(in); err != nil {
		writeCatalogError(w, h.logger, err)
		return
	}
	h.writeCatalog(w, reg)
}

// SetModel rewrites a model's display name, limits and capability flags; its key is immutable.
func (h *LlmHandler) SetModel(w http.ResponseWriter, r *http.Request) {
	reg := h.requireRegistry(w)
	if reg == nil {
		return
	}
	var in llm.ModelInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	sanitizeModelInput(&in)
	if err := reg.SetModel(r.PathValue("provider"), r.PathValue("model"), in); err != nil {
		writeCatalogError(w, h.logger, err)
		return
	}
	h.writeCatalog(w, reg)
}

// RemoveModel drops a model that no enricher configuration references.
func (h *LlmHandler) RemoveModel(w http.ResponseWriter, r *http.Request) {
	reg := h.requireRegistry(w)
	if reg == nil {
		return
	}
	if err := reg.RemoveModel(r.PathValue("provider"), r.PathValue("model"), h.getConfig()); err != nil {
		writeCatalogError(w, h.logger, err)
		return
	}
	h.writeCatalog(w, reg)
}

// ResetCatalogs restores the embedded defaults for the catalogs named in the request body. It is the
// recovery path when the registry failed to load: it rewrites the files, loads a fresh registry and
// installs it, so every catalog endpoint serves again without a restart.
func (h *LlmHandler) ResetCatalogs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Providers bool `json:"providers"`
		Models    bool `json:"models"`
	}
	if !decodeJSONBody(w, r, &body) {
		return
	}
	if !body.Providers && !body.Models {
		http.Error(w, "select at least one of the provider or model catalogs", http.StatusBadRequest)
		return
	}
	if reg := h.registry.Load(); reg != nil {
		if err := reg.ResetCatalogs(body.Providers, body.Models); err != nil {
			writeCatalogError(w, h.logger, err)
			return
		}
		h.writeCatalog(w, reg)
		return
	}
	if err := llm.ResetCatalogFiles(h.catalogDir, body.Providers, body.Models); err != nil {
		writeCatalogError(w, h.logger, err)
		return
	}
	reg, err := llm.NewRegistry(h.catalogDir)
	if err != nil {
		writeCatalogError(w, h.logger, err)
		return
	}
	h.registry.Store(reg)
	writeJSON(w, http.StatusOK, reg.Catalog())
}

// requireRegistry answers 503 when the catalog has not loaded, telling the caller to restore the defaults,
// and otherwise returns the live registry, nil after answering.
func (h *LlmHandler) requireRegistry(w http.ResponseWriter) *llm.Registry {
	reg := h.registry.Load()
	if reg != nil {
		return reg
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "the model catalog failed to load; restore the default catalogs to recover",
	})
	return nil
}

// writeCatalog answers a successful mutation with the freshly updated catalog, so the UI refreshes in
// the same round trip.
func (h *LlmHandler) writeCatalog(w http.ResponseWriter, reg *llm.Registry) {
	writeJSON(w, http.StatusOK, reg.Catalog())
}

// reloadQuiet re-reads the catalogs from disk so a view served by this process reflects edits made by
// another one; a failed reload keeps the current catalogs and is logged.
func (h *LlmHandler) reloadQuiet(reg *llm.Registry) {
	if err := reg.Reload(); err != nil {
		h.logger.Warn("", "reload model catalogs: %v", err)
	}
}

func (h *LlmHandler) buildModelsResponse(reg *llm.Registry) *LlmModelsResponse {
	adapters := make(map[string][]string)
	for _, adapter := range reg.Adapters() {
		providers := reg.ProvidersForAdapter(adapter)
		if providers == nil {
			providers = []string{}
		}
		adapters[adapter] = providers
	}

	providers := make(map[string][]LlmModelEntry)
	for _, adapter := range reg.Adapters() {
		for _, provider := range reg.ProvidersForAdapter(adapter) {
			models := reg.ModelsForProvider(provider)
			entries := make([]LlmModelEntry, 0, len(models))
			for _, m := range models {
				entry := LlmModelEntry{ID: m.ID}
				entry.Capabilities.CanDisableReasoning = m.Capabilities.CanDisableReasoning
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

// decodeJSONBody decodes the request body into dst; a malformed body is answered and reports false.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return false
	}
	return true
}

// cascadeParam reads the cascade query parameter; absent means false and a malformed value is an error.
func cascadeParam(r *http.Request) (bool, error) {
	raw := r.URL.Query().Get("cascade")
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, errors.New("cascade must be a boolean")
	}
	return v, nil
}

// writeCatalogError maps a catalog mutation error to its HTTP status and the user-facing message.
func writeCatalogError(w http.ResponseWriter, logger *log.Logger, err error) {
	status := http.StatusInternalServerError
	message := "internal error"
	switch errs.KindOf(err) {
	case errs.KindNotFound:
		status, message = http.StatusNotFound, catalogErrorMessage(err)
	case errs.KindConflict:
		status, message = http.StatusConflict, catalogErrorMessage(err)
	case errs.KindInvalid:
		status, message = http.StatusBadRequest, catalogErrorMessage(err)
	default:
		logger.Error("", "llm catalog: %v", err)
	}
	writeJSON(w, status, map[string]string{"error": message})
}

// catalogErrorMessage returns the user-facing cause of a kinded catalog error.
func catalogErrorMessage(err error) string {
	if e, ok := errors.AsType[*errs.Error](err); ok {
		return e.Cause.Error()
	}
	return err.Error()
}

// sanitizeProviderInput strips markup from every submitted provider field.
func sanitizeProviderInput(in *llm.ProviderInput) {
	in.Provider = stripTags(in.Provider)
	in.Adapter = stripTags(in.Adapter)
	in.OfficialURL = stripTags(in.OfficialURL)
	in.SessionHeader = stripTags(in.SessionHeader)
}

// sanitizeModelInput strips markup from every submitted model field.
func sanitizeModelInput(in *llm.ModelInput) {
	in.Provider = stripTags(in.Provider)
	in.ModelID = stripTags(in.ModelID)
	in.DisplayName = stripTags(in.DisplayName)
	for i := range in.ReasoningEfforts {
		in.ReasoningEfforts[i] = stripTags(in.ReasoningEfforts[i])
	}
}

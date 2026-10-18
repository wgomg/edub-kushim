// Package llm is the model catalog: the embedded JSON catalogs of providers and their models, extendable
// from the config directory, plus the process-stable session identifier that session-gated providers
// expect. The catalogs drive what the LLM adapters may send (reasoning, temperature, caching, structured
// output) and what the API and CLI display.
package llm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
)

//go:embed provider_catalog.json
var embeddedProviderCatalog []byte

//go:embed model_catalog.json
var embeddedModelCatalog []byte

const (
	providerCatalogFile = "provider_catalog.json"
	modelCatalogFile    = "model_catalog.json"
)

var (
	sessionOnce sync.Once
	sessionID   string
)

// SessionID returns the process-stable identifier for providers whose catalog
// entry declares a session_header; gateways use it for request routing and
// prompt caching, so it must stay constant across requests.
func SessionID() string {
	sessionOnce.Do(func() { sessionID = uuid.NewString() })
	return sessionID
}

// ModelCapability is what a cataloged model allows: reasoning control and
// effort levels, temperature, prompt caching, structured output, the output
// bound's request field, the request dialect, and the context and output
// limits.
type ModelCapability struct {
	// CanDisableReasoning reports that the provider's request body accepts a
	// flag to disable thinking/reasoning; when false the adapters send the
	// lowest cataloged effort instead.
	CanDisableReasoning      bool
	ReasoningEffortLevels    []string
	SupportsTemperature      bool
	SupportsPromptCaching    bool
	SupportsStructuredOutput bool
	MaxInputTokens           int
	MaxOutputTokens          int

	// UsesMaxCompletionTokens selects the Chat Completions output bound's
	// field: OpenAI's GPT-5 family rejects max_tokens and requires
	// max_completion_tokens; every other cataloged provider takes max_tokens.
	UsesMaxCompletionTokens bool

	// UsesResponsesEndpoint selects the Responses-API dialect (endpoint path,
	// request body, response shape) inside the OpenAI-compatible adapter.
	UsesResponsesEndpoint bool
}

// RunsAtEffortLevel reports that a model with no disable flag falls back to a
// cataloged effort level: its reasoning tokens then share the output bound, so
// callers may widen the budget for them.
func (c *ModelCapability) RunsAtEffortLevel() bool {
	return c != nil && !c.CanDisableReasoning && len(c.ReasoningEffortLevels) > 0
}

// ProviderModel is one cataloged model of a provider, as exposed to the API surface. Overridden marks a
// model whose entry comes from the config directory instead of the embedded catalog.
type ProviderModel struct {
	ID           string          `json:"id"`
	DisplayName  string          `json:"display_name,omitempty"`
	Overridden   bool            `json:"overridden,omitempty"`
	Capabilities ModelCapability `json:"capabilities"`
}

type providerEntry struct {
	Provider      string `json:"provider"`
	Adapter       string `json:"adapter"`
	OfficialURL   string `json:"official_url"`
	SessionHeader string `json:"session_header,omitempty"`
}

type modelEntry struct {
	Provider                 string   `json:"provider"`
	ModelID                  string   `json:"model_id"`
	DisplayName              string   `json:"display_name"`
	MaxInputTokens           int      `json:"max_input_tokens"`
	MaxOutputTokens          int      `json:"max_output_tokens"`
	DisableReasoning         bool     `json:"disable_reasoning,omitempty"`
	ReasoningEfforts         []string `json:"reasoning_efforts,omitempty"`
	SupportsStructuredOutput bool     `json:"supports_structured_output"`
	SupportsTemperature      bool     `json:"supports_temperature"`
	SupportsPromptCaching    bool     `json:"supports_prompt_caching"`
	UsesMaxCompletionTokens  bool     `json:"uses_max_completion_tokens,omitempty"`
	UsesResponsesEndpoint    bool     `json:"uses_responses_endpoint,omitempty"`
	Overridden               bool     `json:"-"`
}

// Registry is the loaded provider and model catalogs, with model entries indexed by provider/model. All
// methods are safe for concurrent use; Reload swaps both catalogs atomically.
type Registry struct {
	mu         sync.RWMutex
	catalogDir string
	providers  map[string]providerEntry
	models     []modelEntry
	index      map[string]int
	adapters   map[string][]string
}

// NewRegistry loads the embedded provider and model catalogs, overlaying the config directory's copies
// when present; an empty catalogDir uses the embedded catalogs only. A user entry duplicating an embedded
// key replaces it, and duplicates within one user file follow array order. A user model must name a
// provider defined in the provider catalog. Files that cannot be read or parsed are an error.
func NewRegistry(catalogDir string) (*Registry, error) {
	r := &Registry{catalogDir: catalogDir}
	if err := r.load(); err != nil {
		return nil, err
	}
	r.buildIndex()
	return r, nil
}

// load parses both embedded catalogs, overlays the user copies when present, and assigns the merged
// result, leaving the registry untouched on failure.
func (r *Registry) load() error {
	providers, err := loadProviders(r.catalogDir)
	if err != nil {
		return err
	}
	models, err := loadModels(r.catalogDir, providers)
	if err != nil {
		return err
	}
	r.providers = indexProviders(providers)
	r.models = models
	return nil
}

func loadProviders(catalogDir string) ([]providerEntry, error) {
	base, err := parseProviders(embeddedProviderCatalog, "embedded "+providerCatalogFile)
	if err != nil {
		return nil, err
	}
	data, ok, err := readUserCatalog(catalogDir, providerCatalogFile)
	if err != nil {
		return nil, err
	}
	if !ok {
		return base, nil
	}
	user, err := parseProviders(data, filepath.Join(catalogDir, providerCatalogFile))
	if err != nil {
		return nil, err
	}
	return mergeProviders(base, user), nil
}

func loadModels(catalogDir string, providers []providerEntry) ([]modelEntry, error) {
	base, err := parseModels(embeddedModelCatalog, "embedded "+modelCatalogFile)
	if err != nil {
		return nil, err
	}
	data, ok, err := readUserCatalog(catalogDir, modelCatalogFile)
	if err != nil {
		return nil, err
	}
	if !ok {
		return base, nil
	}
	source := filepath.Join(catalogDir, modelCatalogFile)
	user, err := parseModels(data, source)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(providers))
	for _, p := range providers {
		known[p.Provider] = true
	}
	for i, e := range user {
		if !known[e.Provider] {
			return nil, fmt.Errorf("%s: entry %d: provider %q is not defined in %s", source, i, e.Provider, providerCatalogFile)
		}
	}
	return mergeModels(base, user), nil
}

func readUserCatalog(catalogDir, name string) ([]byte, bool, error) {
	if catalogDir == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(filepath.Join(catalogDir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", name, err)
	}
	return data, true, nil
}

func parseProviders(data []byte, source string) ([]providerEntry, error) {
	var entries []providerEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	for i, e := range entries {
		if e.Provider == "" || e.Adapter == "" || e.OfficialURL == "" {
			return nil, fmt.Errorf("%s: entry %d: provider, adapter and official_url are required", source, i)
		}
	}
	return entries, nil
}

func parseModels(data []byte, source string) ([]modelEntry, error) {
	var entries []modelEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	for i, e := range entries {
		if e.Provider == "" || e.ModelID == "" {
			return nil, fmt.Errorf("%s: entry %d: provider and model_id are required", source, i)
		}
	}
	return entries, nil
}

// mergeProviders overlays user provider entries on the embedded catalog: a duplicate provider replaces
// its base entry in place, new providers append. The replace/append and last-wins rules mirror mergeModels
// and must stay in sync with it.
func mergeProviders(base, overlay []providerEntry) []providerEntry {
	merged := slices.Clone(base)
	index := make(map[string]int, len(merged))
	for i, e := range merged {
		index[e.Provider] = i
	}
	for _, e := range overlay {
		if i, ok := index[e.Provider]; ok {
			merged[i] = e
			continue
		}
		index[e.Provider] = len(merged)
		merged = append(merged, e)
	}
	return merged
}

// mergeModels overlays user model entries on the embedded catalog: a duplicate provider/model replaces
// its base entry in place and is marked as an override, new models append. Duplicates inside the overlay
// itself follow array order, the last entry winning, and never carry the override mark. The replace/append
// and last-wins rules mirror mergeProviders and must stay in sync with it.
func mergeModels(base, overlay []modelEntry) []modelEntry {
	merged := slices.Clone(base)
	index := make(map[string]int, len(merged))
	baseKeys := make(map[string]bool, len(merged))
	for i, e := range merged {
		key := modelKey(e.Provider, e.ModelID)
		index[key] = i
		baseKeys[key] = true
	}
	for _, e := range overlay {
		key := modelKey(e.Provider, e.ModelID)
		if i, ok := index[key]; ok {
			e.Overridden = baseKeys[key]
			merged[i] = e
			continue
		}
		index[key] = len(merged)
		merged = append(merged, e)
	}
	return merged
}

func indexProviders(entries []providerEntry) map[string]providerEntry {
	providers := make(map[string]providerEntry, len(entries))
	for _, e := range entries {
		providers[e.Provider] = e
	}
	return providers
}

func modelKey(provider, modelID string) string {
	return provider + "/" + modelID
}

// Reload re-reads both catalogs and rebuilds the indexes; a failure leaves the previous catalogs in place.
func (r *Registry) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.load(); err != nil {
		return err
	}
	r.buildIndex()
	return nil
}

func (r *Registry) buildIndex() {
	r.index = make(map[string]int, len(r.models))
	for i, e := range r.models {
		r.index[modelKey(e.Provider, e.ModelID)] = i
	}

	adapterProviders := make(map[string][]string)
	seen := make(map[string]bool)
	for _, e := range r.models {
		p, ok := r.providers[e.Provider]
		if !ok {
			continue
		}
		pk := p.Adapter + ":" + e.Provider
		if seen[pk] {
			continue
		}
		seen[pk] = true
		adapterProviders[p.Adapter] = append(adapterProviders[p.Adapter], e.Provider)
	}

	r.adapters = make(map[string][]string, len(adapterProviders))
	for adapter := range adapterProviders {
		provs := adapterProviders[adapter]
		slices.Sort(provs)
		r.adapters[adapter] = provs
	}
}

func entryToCapability(e *modelEntry) *ModelCapability {
	return &ModelCapability{
		CanDisableReasoning:      e.DisableReasoning,
		ReasoningEffortLevels:    e.ReasoningEfforts,
		SupportsTemperature:      e.SupportsTemperature,
		SupportsPromptCaching:    e.SupportsPromptCaching,
		SupportsStructuredOutput: e.SupportsStructuredOutput,
		MaxInputTokens:           e.MaxInputTokens,
		MaxOutputTokens:          e.MaxOutputTokens,
		UsesMaxCompletionTokens:  e.UsesMaxCompletionTokens,
		UsesResponsesEndpoint:    e.UsesResponsesEndpoint,
	}
}

// Lookup returns the capability of a provider/model pair, nil when the
// pair is not cataloged.
func (r *Registry) Lookup(provider, modelID string) *ModelCapability {
	r.mu.RLock()
	defer r.mu.RUnlock()

	i, ok := r.index[modelKey(provider, modelID)]
	if !ok {
		return nil
	}
	return entryToCapability(&r.models[i])
}

// Adapters lists the distinct adapter keys in the catalog, sorted.
func (r *Registry) Adapters() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, 0, len(r.adapters))
	for a := range r.adapters {
		result = append(result, a)
	}
	slices.Sort(result)
	return result
}

// ProvidersForAdapter lists the providers an adapter serves, sorted;
// empty for an unknown adapter.
func (r *Registry) ProvidersForAdapter(adapter string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provs := r.adapters[adapter]
	if provs == nil {
		return []string{}
	}
	return provs
}

// ModelsForProvider lists a provider's catalog models with their
// capabilities, ordered by model id.
func (r *Registry) ModelsForProvider(provider string) []ProviderModel {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []ProviderModel
	for i := range r.models {
		e := &r.models[i]
		if e.Provider != provider {
			continue
		}
		result = append(result, ProviderModel{
			ID:           e.ModelID,
			DisplayName:  e.DisplayName,
			Overridden:   e.Overridden,
			Capabilities: *entryToCapability(e),
		})
	}
	slices.SortFunc(result, func(a, b ProviderModel) int {
		return strings.Compare(a.ID, b.ID)
	})
	return result
}

// ProviderDefaultURL returns the provider's official URL from the provider catalog, empty when unknown.
func (r *Registry) ProviderDefaultURL(provider string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providers[provider].OfficialURL
}

// ProviderSessionHeader returns the header a provider expects its client
// session identifier in, or "" when the provider does not ask for one.
func (r *Registry) ProviderSessionHeader(provider string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providers[provider].SessionHeader
}

// ProviderAdapter returns the provider's adapter kind, custom when the provider catalog does not name one.
func (r *Registry) ProviderAdapter(provider string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[provider]
	if !ok {
		return "custom"
	}
	return p.Adapter
}

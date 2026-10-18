// Package llm is the model catalog: the provider and model catalogs read from the config directory,
// seeded with the embedded defaults when a file is absent, plus the process-stable session identifier
// that session-gated providers expect. The catalogs drive what the LLM adapters may send (reasoning,
// temperature, caching, structured output) and what the API and CLI display.
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
var defaultProviderCatalog []byte

//go:embed model_catalog.json
var defaultModelCatalog []byte

const (
	providerCatalogFile = "provider_catalog.json"
	modelCatalogFile    = "model_catalog.json"

	// catalogVersion is the format version both catalog files must declare to load; the loader rejects any
	// other or missing value, so bump it alongside a shape change.
	catalogVersion = 1
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

// ProviderModel is one cataloged model of a provider, as exposed to the API surface.
type ProviderModel struct {
	ID           string          `json:"id"`
	DisplayName  string          `json:"display_name,omitempty"`
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
}

// providerCatalogDoc is the on-disk provider catalog: the format version, the provider entries, and
// an ignored `_comment` hint key.
type providerCatalogDoc struct {
	Version   int             `json:"version"`
	Providers []providerEntry `json:"providers"`
}

// modelCatalogDoc is the on-disk model catalog: the format version, the model entries, and an ignored
// `_comment` hint key.
type modelCatalogDoc struct {
	Version int          `json:"version"`
	Models  []modelEntry `json:"models"`
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

// NewRegistry loads the provider and model catalogs from the config directory: a file that does not
// exist yet resolves from its embedded default and is seeded to disk only after the combined result
// validates, so a failing load never writes files; an existing file is never overwritten, so user
// edits are authoritative. Each file must declare version 1 and pass entry validation; duplicate keys
// inside one file follow array order, the array's last entry winning. A model must name a provider
// defined in the provider catalog. Unreadable, unparseable or invalid files are an error, as is an
// empty catalogDir.
func NewRegistry(catalogDir string) (*Registry, error) {
	if catalogDir == "" {
		return nil, fmt.Errorf("catalog directory is required")
	}
	r := &Registry{catalogDir: catalogDir}
	if err := r.load(); err != nil {
		return nil, err
	}
	r.buildIndex()
	return r, nil
}

// load resolves both catalogs — reading each file when present and using its embedded default when
// the file is missing — validates them, seeds the defaults for missing files, and assigns the result,
// leaving the registry untouched on failure.
func (r *Registry) load() error {
	providers, providerMissing, err := resolveProviders(r.catalogDir)
	if err != nil {
		return err
	}
	models, modelMissing, err := resolveModels(r.catalogDir, providers)
	if err != nil {
		return err
	}
	if providerMissing {
		if err := seedCatalog(r.catalogDir, providerCatalogFile, defaultProviderCatalog); err != nil {
			return err
		}
	}
	if modelMissing {
		if err := seedCatalog(r.catalogDir, modelCatalogFile, defaultModelCatalog); err != nil {
			return err
		}
	}
	r.providers = indexProviders(providers)
	r.models = models
	return nil
}

// catalogSource returns the catalog file's bytes with the file name as the error label, falling back
// to the embedded default labeled "default <name>" when the file does not exist yet; missing reports
// that the fallback was used.
func catalogSource(catalogDir, name string, def []byte) ([]byte, string, bool, error) {
	data, err := os.ReadFile(filepath.Join(catalogDir, name))
	if err == nil {
		return data, name, false, nil
	}
	if !os.IsNotExist(err) {
		return nil, "", false, fmt.Errorf("read %s: %w", name, err)
	}
	return def, "default " + name, true, nil
}

// seedCatalog creates name in catalogDir from the embedded default when it does not exist yet.
// Creation is exclusive (O_CREATE|O_EXCL), so an existing file — or a symlink at the path — is never
// overwritten or followed, even when it appeared since the read that loaded the catalog.
func seedCatalog(catalogDir, name string, def []byte) error {
	path := filepath.Join(catalogDir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("seed %s: %w", name, err)
	}
	// a partial file would be adopted as user content on the next load (existing files are never
	// overwritten), so roll this create back before reporting the failure
	if _, err := f.Write(def); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("seed %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("seed %s: %w", name, err)
	}
	return nil
}

// resolveProviders resolves the provider catalog from catalogDir and validates its entries,
// collapsing duplicate provider keys with the last entry winning; missing reports that the file does
// not exist yet and its embedded default was used.
func resolveProviders(catalogDir string) ([]providerEntry, bool, error) {
	data, source, missing, err := catalogSource(catalogDir, providerCatalogFile, defaultProviderCatalog)
	if err != nil {
		return nil, false, err
	}
	var doc providerCatalogDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("parse %s: expected an object with version and providers fields: %w", source, err)
	}
	if doc.Version != catalogVersion {
		return nil, false, fmt.Errorf("%s: unsupported catalog version %d, want %d", source, doc.Version, catalogVersion)
	}
	for i, e := range doc.Providers {
		if e.Provider == "" || e.Adapter == "" || e.OfficialURL == "" {
			return nil, false, fmt.Errorf("%s: entry %d: provider, adapter and official_url are required", source, i)
		}
	}
	return dedupeProviders(doc.Providers), missing, nil
}

// resolveModels resolves the model catalog from catalogDir, validating its entries against the
// provider keys and collapsing duplicate provider/model keys with the last entry winning; missing
// reports that the file does not exist yet and its embedded default was used.
func resolveModels(catalogDir string, providers []providerEntry) ([]modelEntry, bool, error) {
	data, source, missing, err := catalogSource(catalogDir, modelCatalogFile, defaultModelCatalog)
	if err != nil {
		return nil, false, err
	}
	var doc modelCatalogDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("parse %s: expected an object with version and models fields: %w", source, err)
	}
	if doc.Version != catalogVersion {
		return nil, false, fmt.Errorf("%s: unsupported catalog version %d, want %d", source, doc.Version, catalogVersion)
	}
	known := make(map[string]bool, len(providers))
	for _, p := range providers {
		known[p.Provider] = true
	}
	for i, e := range doc.Models {
		if e.Provider == "" || e.ModelID == "" {
			return nil, false, fmt.Errorf("%s: entry %d: provider and model_id are required", source, i)
		}
		if !known[e.Provider] {
			return nil, false, fmt.Errorf("%s: entry %d: provider %q is not defined in %s", source, i, e.Provider, providerCatalogFile)
		}
	}
	return dedupeModels(doc.Models), missing, nil
}

// dedupeProviders collapses duplicate provider keys, the last entry winning, so the indexed catalog
// never resolves a key twice.
func dedupeProviders(entries []providerEntry) []providerEntry {
	out := make([]providerEntry, 0, len(entries))
	index := make(map[string]int, len(entries))
	for _, e := range entries {
		if i, ok := index[e.Provider]; ok {
			out[i] = e
			continue
		}
		index[e.Provider] = len(out)
		out = append(out, e)
	}
	return out
}

// dedupeModels collapses duplicate provider/model keys, the last entry winning, so the indexed catalog
// never resolves a key twice.
func dedupeModels(entries []modelEntry) []modelEntry {
	out := make([]modelEntry, 0, len(entries))
	index := make(map[string]int, len(entries))
	for _, e := range entries {
		key := modelKey(e.Provider, e.ModelID)
		if i, ok := index[key]; ok {
			out[i] = e
			continue
		}
		index[key] = len(out)
		out = append(out, e)
	}
	return out
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

// Reload re-reads both catalogs — seeding any missing file — and rebuilds the indexes; a failure leaves
// the previous catalogs in place.
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
		p := r.providers[e.Provider]
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

package llm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
)

//go:embed model_catalog.json
var embeddedCatalog []byte

type ModelCapability struct {
	SupportsReasoning        bool
	ReasoningEffortLevels    []string
	SupportsTemperature      bool
	SupportsPromptCaching    bool
	SupportsStructuredOutput bool
	MaxInputTokens           int
	MaxOutputTokens          int
	InputCostPerToken        float64
	OutputCostPerToken       float64
}

type ProviderModel struct {
	ID           string          `json:"id"`
	DisplayName  string          `json:"display_name,omitempty"`
	Capabilities ModelCapability `json:"capabilities"`
}

type catalogEntry struct {
	Provider                string   `json:"provider"`
	Adapter                 string   `json:"adapter"`
	ModelID                 string   `json:"model_id"`
	DisplayName             string   `json:"display_name"`
	OfficialURL             string   `json:"official_url"`
	MaxInputTokens          int      `json:"max_input_tokens"`
	MaxOutputTokens         int      `json:"max_output_tokens"`
	Reasoning               bool     `json:"reasoning"`
	ReasoningEfforts        []string `json:"reasoning_efforts,omitempty"`
	SupportsStructuredOutput bool    `json:"supports_structured_output"`
	SupportsTemperature     bool     `json:"supports_temperature"`
	SupportsPromptCaching   bool     `json:"supports_prompt_caching"`
}

type Registry struct {
	mu         sync.RWMutex
	catalogPath string
	entries    []catalogEntry
	index      map[string]int
	adapters   map[string][]string

	providerAdapters map[string]string
	providerURLs     map[string]string
}

func NewRegistry(catalogPath string) (*Registry, error) {
	r := &Registry{
		catalogPath:      catalogPath,
		index:            make(map[string]int),
		adapters:         make(map[string][]string),
		providerAdapters: make(map[string]string),
		providerURLs:     make(map[string]string),
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	r.buildIndex()
	return r, nil
}

func (r *Registry) load() error {
	data, err := os.ReadFile(r.catalogPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read model catalog: %w", err)
		}
		data = embeddedCatalog
	}

	var entries []catalogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse model catalog: %w", err)
	}

	r.entries = entries

	for _, e := range entries {
		if _, ok := r.providerAdapters[e.Provider]; !ok {
			r.providerAdapters[e.Provider] = e.Adapter
		}
		if _, ok := r.providerURLs[e.Provider]; !ok {
			r.providerURLs[e.Provider] = e.OfficialURL
		}
	}
	return nil
}

func (r *Registry) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providerAdapters = make(map[string]string)
	r.providerURLs = make(map[string]string)
	if err := r.load(); err != nil {
		return err
	}
	r.index = make(map[string]int)
	r.adapters = make(map[string][]string)
	r.buildIndex()
	return nil
}

func (r *Registry) buildIndex() {
	r.index = make(map[string]int)
	for i, entry := range r.entries {
		key := entry.Provider + "/" + entry.ModelID
		r.index[key] = i
	}

	adapterProviders := make(map[string][]string)
	seen := make(map[string]bool)
	for _, entry := range r.entries {
		adapter := r.providerAdapters[entry.Provider]
		if adapter == "" {
			continue
		}
		pk := adapter + ":" + entry.Provider
		if seen[pk] {
			continue
		}
		seen[pk] = true
		adapterProviders[adapter] = append(adapterProviders[adapter], entry.Provider)
	}

	for adapter := range adapterProviders {
		provs := adapterProviders[adapter]
		slices.Sort(provs)
		r.adapters[adapter] = provs
	}
}

func entryToCapability(e *catalogEntry) *ModelCapability {
	return &ModelCapability{
		SupportsReasoning:        e.Reasoning,
		ReasoningEffortLevels:    e.ReasoningEfforts,
		SupportsTemperature:      e.SupportsTemperature,
		SupportsPromptCaching:    e.SupportsPromptCaching,
		SupportsStructuredOutput: e.SupportsStructuredOutput,
		MaxInputTokens:           e.MaxInputTokens,
		MaxOutputTokens:          e.MaxOutputTokens,
		InputCostPerToken:        0,
		OutputCostPerToken:       0,
	}
}

func (r *Registry) Lookup(provider, modelID string) *ModelCapability {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := provider + "/" + modelID
	i, ok := r.index[key]
	if !ok {
		return nil
	}

	entry := &r.entries[i]
	return entryToCapability(entry)
}

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

func (r *Registry) ProvidersForAdapter(adapter string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provs := r.adapters[adapter]
	if provs == nil {
		return []string{}
	}
	return provs
}

func (r *Registry) ModelsForProvider(provider string) []ProviderModel {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []ProviderModel
	for _, entry := range r.entries {
		if entry.Provider != provider {
			continue
		}
		caps := entryToCapability(&entry)
		result = append(result, ProviderModel{
			ID:           entry.ModelID,
			DisplayName:  entry.DisplayName,
			Capabilities: *caps,
		})
	}
	slices.SortFunc(result, func(a, b ProviderModel) int {
		return strings.Compare(a.ID, b.ID)
	})
	return result
}

func (r *Registry) ProviderDefaultURL(provider string) string {
	return r.providerURLs[provider]
}

func (r *Registry) ProviderAdapter(provider string) string {
	a := r.providerAdapters[provider]
	if a == "" {
		return "custom"
	}
	return a
}

func (r *Registry) CatalogPath() string {
	return r.catalogPath
}

package autofill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/version"
)

const (
	modelsDevSnapshotURL = "https://models.dev/experimental/v2.0/api.json?type=chat"
	modelsDevTTL         = 24 * time.Hour
	modelsDevTimeout     = 30 * time.Second
	modelsDevMaxBytes    = 64 << 20
)

// modelsDevProvider is one provider entry of the models.dev v2 snapshot.
type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	Models map[string]modelsDevModel `json:"models"`
}

// modelsDevModel is the subset of a models.dev v2 model entry this package reads.
type modelsDevModel struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	RecommendedAPI string `json:"recommended_api"`
	Limit          struct {
		Context int `json:"context"`
		Input   int `json:"input"`
		Output  int `json:"output"`
	} `json:"limit"`
	Capabilities struct {
		Reasoning struct {
			Supported bool     `json:"supported"`
			Toggle    bool     `json:"toggle"`
			Effort    []string `json:"effort"`
		} `json:"reasoning"`
		Cache            json.RawMessage `json:"cache"`
		StructuredOutput *bool           `json:"structured_output"`
		Temperature      *bool           `json:"temperature"`
	} `json:"capabilities"`
	API map[string]modelsDevAPI `json:"api"`
}

// modelsDevAPI is one protocol entry of a models.dev v2 model.
type modelsDevAPI struct {
	BaseURL string `json:"base_url"`
	SDK     struct {
		AISDK string `json:"aisdk"`
	} `json:"sdk"`
	Compatibility struct {
		MaxTokensField string `json:"max_tokens_field"`
	} `json:"compatibility"`
}

// providerIndex is the matching view of one models.dev provider: its identity, the concrete base URLs
// its chat models publish, and the base URL each protocol resolves to.
type providerIndex struct {
	provider      *modelsDevProvider
	id            string
	name          string
	baseURLs      map[string]struct{}
	hosts         map[string]struct{}
	protocolBases map[string]string
	protocols     []string
}

// endpointSuggestion is the endpoint the provider form should carry for a requested adapter.
type endpointSuggestion struct {
	adapter string
	url     string
}

// providerSnapshot returns the cached models.dev index, refreshing it when its lifetime has expired; a
// refresh failure keeps the stale snapshot and reports why in the note.
func (c *Client) providerSnapshot(ctx context.Context) (map[string]*providerIndex, string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.snapshot != nil && now.Sub(c.fetchedAt) < modelsDevTTL {
		return c.snapshot, ""
	}

	providers, err := c.fetchSnapshot(ctx)
	if err != nil {
		if c.snapshot != nil {
			return c.snapshot, "models.dev is unreachable; using the last snapshot."
		}
		return nil, "models.dev is unreachable; only the provider endpoint was consulted."
	}
	c.snapshot = providers
	c.fetchedAt = now
	return providers, ""
}

// fetchSnapshot downloads and indexes the models.dev v2 chat catalog.
func (c *Client) fetchSnapshot(ctx context.Context) (map[string]*providerIndex, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, modelsDevTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, c.modelsDevURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create models.dev request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "edub-kushim/"+version.Version)

	resp, err := c.modelsDev.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models.dev: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch models.dev: status %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, modelsDevMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read models.dev snapshot: %w", err)
	}
	if len(data) > modelsDevMaxBytes {
		return nil, fmt.Errorf("models.dev snapshot exceeds %d bytes", modelsDevMaxBytes)
	}

	var doc map[string]modelsDevProvider
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decode models.dev snapshot: %w", err)
	}
	return indexSnapshot(doc), nil
}

// indexSnapshot builds the per-provider matching view once per fetch.
func indexSnapshot(doc map[string]modelsDevProvider) map[string]*providerIndex {
	out := make(map[string]*providerIndex, len(doc))
	for key := range doc {
		entry := doc[key]
		id := entry.ID
		if id == "" {
			id = key
		}
		counts := make(map[string]map[string]int)
		index := &providerIndex{
			provider:      &entry,
			id:            id,
			name:          entry.Name,
			baseURLs:      make(map[string]struct{}),
			hosts:         make(map[string]struct{}),
			protocolBases: make(map[string]string),
		}
		for _, model := range entry.Models {
			for protocol, api := range model.API {
				full, host, ok := normalizeURL(api.BaseURL)
				if !ok {
					continue
				}
				index.baseURLs[full] = struct{}{}
				index.hosts[host] = struct{}{}
				if counts[protocol] == nil {
					counts[protocol] = make(map[string]int)
				}
				counts[protocol][full]++
			}
		}
		for protocol, urls := range counts {
			best, bestCount := "", 0
			for base, n := range urls {
				if n > bestCount || (n == bestCount && (best == "" || base < best)) {
					best, bestCount = base, n
				}
			}
			index.protocolBases[protocol] = best
			index.protocols = append(index.protocols, protocol)
		}
		slices.Sort(index.protocols)
		out[id] = index
	}
	return out
}

// normalizeURL lower-cases the scheme and host and trims the path's trailing slashes, returning the full
// form and the scheme-and-host prefix; false for anything but an absolute http(s) URL.
func normalizeURL(raw string) (full, host string, ok bool) {
	parsed, ok := llm.ParseEndpointURL(raw)
	if !ok {
		return "", "", false
	}
	host = strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
	return host + strings.TrimRight(parsed.Path, "/"), host, true
}

// normalizeName keeps the letters and digits of a provider key or display name, lower-cased.
func normalizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// prefixMatch reports whether two normalized names share a prefix of at least three characters.
func prefixMatch(a, b string) bool {
	if len(a) < 3 || len(b) < 3 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// matchProvider scores every provider that publishes a concrete base URL and returns the unique best
// match, or the candidates when several providers tie.
func matchProvider(
	snapshot map[string]*providerIndex,
	key string,
	officialURL string,
) (*providerIndex, []ProviderCandidate) {
	key = normalizeName(key)
	full, host, hasURL := "", "", false
	if officialURL != "" {
		full, host, hasURL = normalizeURL(officialURL)
	}

	type scored struct {
		provider *providerIndex
		score    int
	}
	var matches []scored
	for _, provider := range snapshot {
		if len(provider.baseURLs) == 0 {
			continue
		}
		score := 0
		if key != "" && key == normalizeName(provider.id) {
			score = 5
		}
		if hasURL {
			if _, ok := provider.baseURLs[full]; ok && score < 4 {
				score = 4
			}
			if _, ok := provider.hosts[host]; ok && score < 3 {
				score = 3
			}
		}
		if key != "" && key == normalizeName(provider.name) && score < 2 {
			score = 2
		}
		if key != "" && score < 1 &&
			(prefixMatch(key, normalizeName(provider.id)) || prefixMatch(key, normalizeName(provider.name))) {
			score = 1
		}
		if score > 0 {
			matches = append(matches, scored{provider: provider, score: score})
		}
	}

	slices.SortFunc(matches, func(a, b scored) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.provider.id, b.provider.id)
	})

	switch {
	case len(matches) == 0:
		return nil, nil
	case len(matches) == 1 || matches[0].score > matches[1].score:
		return matches[0].provider, nil
	}

	candidates := make([]ProviderCandidate, 0, min(5, len(matches)))
	for _, match := range matches[:min(5, len(matches))] {
		candidates = append(candidates, ProviderCandidate{
			ModelsDevProvider: match.provider.id,
			Name:              match.provider.name,
		})
	}
	return nil, candidates
}

// suggestEndpoint picks the endpoint the request adapter should use for a provider: the request's own
// adapter when the provider publishes a protocol it speaks, else the other supported adapter, else nil.
func suggestEndpoint(provider *providerIndex, requestAdapter string) *endpointSuggestion {
	chat := provider.protocolBases["chat-completions"]
	responses := provider.protocolBases["responses"]
	messages := provider.protocolBases["messages"]

	if requestAdapter == adapterAnthropic {
		if messages != "" {
			return &endpointSuggestion{adapter: adapterAnthropic, url: messages}
		}
		if chat != "" {
			return &endpointSuggestion{adapter: adapterOpenAICompatible, url: chat}
		}
		return nil
	}
	if chat != "" {
		return &endpointSuggestion{adapter: adapterOpenAICompatible, url: chat}
	}
	if responses != "" {
		return &endpointSuggestion{adapter: adapterOpenAICompatible, url: responses}
	}
	if messages != "" {
		return &endpointSuggestion{adapter: adapterAnthropic, url: messages}
	}
	return nil
}

// findModel looks a model id up in a provider: exact key first, then a case-insensitive match, then the
// single vendor-prefixed or vendor-stripped sibling.
func findModel(provider *modelsDevProvider, modelID string) (*modelsDevModel, bool) {
	if model, ok := provider.Models[modelID]; ok {
		return &model, true
	}
	for key, model := range provider.Models {
		if strings.EqualFold(key, modelID) || strings.EqualFold(model.ID, modelID) {
			return &model, true
		}
	}

	want := strings.ToLower(modelID)
	var matches []*modelsDevModel
	for key, model := range provider.Models {
		key = strings.ToLower(key)
		if strings.HasSuffix(key, "/"+want) || strings.HasSuffix(want, "/"+key) {
			matches = append(matches, &model)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return nil, false
}

// modelsDevFields maps a models.dev model onto the form's fields, skipping anything it did not publish.
func modelsDevFields(model *modelsDevModel, adapter string) (ModelFields, []string) {
	var fields ModelFields
	var notes []string

	if model.Name != "" {
		fields.DisplayName = model.Name
	}
	switch {
	case model.Limit.Input > 0:
		value := model.Limit.Input
		fields.MaxInputTokens = &value
	case model.Limit.Context > 0:
		value := model.Limit.Context
		fields.MaxInputTokens = &value
	}
	if model.Limit.Output > 0 {
		value := model.Limit.Output
		fields.MaxOutputTokens = &value
	}
	if model.Capabilities.Reasoning.Supported {
		levels := sortEfforts(model.Capabilities.Reasoning.Effort)
		fields.ReasoningEfforts = &levels
		toggle := model.Capabilities.Reasoning.Toggle
		fields.DisableReasoning = &toggle
	}
	if model.Capabilities.StructuredOutput != nil {
		fields.SupportsStructuredOutput = model.Capabilities.StructuredOutput
	}
	if model.Capabilities.Temperature != nil {
		fields.SupportsTemperature = model.Capabilities.Temperature
	}
	if len(bytes.TrimSpace(model.Capabilities.Cache)) > 0 {
		value := !bytes.Equal(bytes.TrimSpace(model.Capabilities.Cache), []byte("false"))
		fields.SupportsPromptCaching = &value
	}

	if adapter != adapterAnthropic {
		if api, ok := model.API["chat-completions"]; ok {
			switch api.Compatibility.MaxTokensField {
			case "max_tokens":
				value := false
				fields.UsesMaxCompletionTokens = &value
			case "max_completion_tokens":
				value := true
				fields.UsesMaxCompletionTokens = &value
			default:
				switch api.SDK.AISDK {
				case "@ai-sdk/openai", "@ai-sdk/azure", "@ai-sdk/xai":
					value := true
					fields.UsesMaxCompletionTokens = &value
				}
			}
		}
		switch model.RecommendedAPI {
		case "responses":
			value := true
			fields.UsesResponsesEndpoint = &value
		case "chat-completions":
			value := false
			fields.UsesResponsesEndpoint = &value
		case "":
		default:
			notes = append(notes, fmt.Sprintf(
				"models.dev recommends %s for this model; this build speaks only chat-completions and responses.",
				model.RecommendedAPI))
		}
	}
	return fields, notes
}

// effortRank orders the effort levels the catalog and the adapters recognize; the adapters treat the
// first list entry as the lowest level.
var effortRank = map[string]int{
	"none":    0,
	"minimal": 1,
	"low":     2,
	"medium":  3,
	"high":    4,
	"xhigh":   5,
	"max":     6,
}

// sortEfforts drops the placeholder "default" level, removes duplicates and sorts by rank, with unknown
// levels after the known ones in name order.
func sortEfforts(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, effort := range in {
		effort = strings.TrimSpace(effort)
		if effort == "" || effort == "default" || seen[effort] {
			continue
		}
		seen[effort] = true
		out = append(out, effort)
	}
	slices.SortFunc(out, func(a, b string) int {
		rankA, knownA := effortRank[a]
		rankB, knownB := effortRank[b]
		switch {
		case knownA && knownB && rankA != rankB:
			return rankA - rankB
		case knownA != knownB:
			if knownA {
				return -1
			}
			return 1
		default:
			return strings.Compare(a, b)
		}
	})
	return out
}

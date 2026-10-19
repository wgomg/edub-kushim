package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/errs"
)

// catalogLockFile is the cross-process lock file serializing catalog read-modify-write cycles; the API
// server and the CLI may both edit the catalogs.
const catalogLockFile = "catalog.lock"

// headerTokenRE matches an HTTP field-name token, the shape a provider's session header must have.
var headerTokenRE = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// ProviderInput is a provider's editable catalog fields: the name (its key), the adapter kind, the endpoint
// base URL, and the optional session header the provider expects its client session identifier in.
type ProviderInput struct {
	Provider      string `json:"provider"`
	Adapter       string `json:"adapter"`
	OfficialURL   string `json:"official_url"`
	SessionHeader string `json:"session_header,omitempty"`
}

// ModelInput is a model's editable catalog fields, mirroring the on-disk entry: its key (provider and
// model id), display name, token limits and capability flags.
type ModelInput struct {
	Provider                 string   `json:"provider"`
	ModelID                  string   `json:"model_id"`
	DisplayName              string   `json:"display_name,omitempty"`
	MaxInputTokens           int      `json:"max_input_tokens,omitempty"`
	MaxOutputTokens          int      `json:"max_output_tokens,omitempty"`
	DisableReasoning         bool     `json:"disable_reasoning,omitempty"`
	ReasoningEfforts         []string `json:"reasoning_efforts,omitempty"`
	SupportsStructuredOutput bool     `json:"supports_structured_output,omitempty"`
	SupportsTemperature      bool     `json:"supports_temperature,omitempty"`
	SupportsPromptCaching    bool     `json:"supports_prompt_caching,omitempty"`
	UsesMaxCompletionTokens  bool     `json:"uses_max_completion_tokens,omitempty"`
	UsesResponsesEndpoint    bool     `json:"uses_responses_endpoint,omitempty"`
}

// Catalog is the full editable catalog: every provider, including the ones with no models, and every
// model entry, both ordered by name.
type Catalog struct {
	Providers []ProviderInput `json:"providers"`
	Models    []ModelInput    `json:"models"`
}

// catalogState is one resolved read of both catalog files: the validated entries, each file's preserved
// hint comment, and whether a file was missing and resolved from its embedded default. Neither entry
// slice is nil.
type catalogState struct {
	providers       []providerEntry
	models          []modelEntry
	providerComment string
	modelComment    string
	providerMissing bool
	modelMissing    bool
}

// catalogChange is one mutation's resulting state: a non-nil entry slice marks its catalog file for
// rewrite with the accompanying comment.
type catalogChange struct {
	providers       []providerEntry
	providerComment string
	models          []modelEntry
	modelComment    string
}

// Catalog returns a snapshot of every catalog entry for display and editing.
func (r *Registry) Catalog() Catalog {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := Catalog{
		Providers: make([]ProviderInput, 0, len(r.providers)),
		Models:    make([]ModelInput, 0, len(r.models)),
	}
	for _, p := range r.providers {
		out.Providers = append(out.Providers, providerInputFrom(p))
	}
	for _, m := range r.models {
		out.Models = append(out.Models, modelInputFrom(m))
	}
	slices.SortFunc(out.Providers, func(a, b ProviderInput) int {
		return strings.Compare(a.Provider, b.Provider)
	})
	slices.SortFunc(out.Models, func(a, b ModelInput) int {
		if c := strings.Compare(a.Provider, b.Provider); c != 0 {
			return c
		}
		return strings.Compare(a.ModelID, b.ModelID)
	})
	return out
}

// AddProvider appends a provider, rejecting a name already cataloged.
func (r *Registry) AddProvider(in ProviderInput) error {
	entry, err := providerEntryFrom(in.Provider, in)
	if err != nil {
		return errs.EInvalid("add provider", err)
	}
	return r.applyMutation("add provider", func(st catalogState) (catalogChange, error) {
		if slices.ContainsFunc(st.providers, func(p providerEntry) bool { return p.Provider == entry.Provider }) {
			return catalogChange{}, errs.EConflict("add provider",
				fmt.Errorf("provider %q already exists", entry.Provider))
		}
		return catalogChange{
			providers:       append(slices.Clone(st.providers), entry),
			providerComment: st.providerComment,
		}, nil
	})
}

// SetProvider rewrites a provider's adapter, URL and session header; name is the provider's immutable
// key and the input's own name field is ignored.
func (r *Registry) SetProvider(name string, in ProviderInput) error {
	entry, err := providerEntryFrom(name, in)
	if err != nil {
		return errs.EInvalid("set provider", err)
	}
	return r.applyMutation("set provider", func(st catalogState) (catalogChange, error) {
		idx := slices.IndexFunc(st.providers, func(p providerEntry) bool { return p.Provider == entry.Provider })
		if idx < 0 {
			return catalogChange{}, errs.ENotFound("set provider",
				fmt.Errorf("provider %q is not cataloged", entry.Provider))
		}
		next := slices.Clone(st.providers)
		next[idx] = entry
		return catalogChange{providers: next, providerComment: st.providerComment}, nil
	})
}

// RemoveProvider drops a provider and, with cascade, its models; a provider that still owns models when
// cascade is false, or that cfg's LLM endpoints use, is rejected.
func (r *Registry) RemoveProvider(name string, cascade bool, cfg *config.Config) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errs.EInvalid("remove provider", errors.New("provider name is required"))
	}
	return r.applyMutation("remove provider", func(st catalogState) (catalogChange, error) {
		idx := slices.IndexFunc(st.providers, func(p providerEntry) bool { return p.Provider == name })
		if idx < 0 {
			return catalogChange{}, errs.ENotFound("remove provider",
				fmt.Errorf("provider %q is not cataloged", name))
		}
		if llmInUse(cfg, name, "") {
			return catalogChange{}, errs.EConflict("remove provider",
				fmt.Errorf("provider %q is in use by the enricher LLM configuration", name))
		}
		owned := 0
		for _, m := range st.models {
			if m.Provider == name {
				owned++
			}
		}
		if owned > 0 && !cascade {
			return catalogChange{}, errs.EConflict("remove provider", fmt.Errorf(
				"provider %q still has %d models; pass cascade to remove them together", name, owned))
		}
		change := catalogChange{
			providers:       slices.Delete(slices.Clone(st.providers), idx, idx+1),
			providerComment: st.providerComment,
		}
		if owned > 0 {
			change.models = slices.DeleteFunc(slices.Clone(st.models), func(m modelEntry) bool {
				return m.Provider == name
			})
			change.modelComment = st.modelComment
		}
		return change, nil
	})
}

// AddModel appends a model, rejecting a provider/model key already cataloged and a provider that is
// not in the provider catalog.
func (r *Registry) AddModel(in ModelInput) error {
	entry, err := modelEntryFrom(in.Provider, in.ModelID, in)
	if err != nil {
		return errs.EInvalid("add model", err)
	}
	return r.applyMutation("add model", func(st catalogState) (catalogChange, error) {
		if !slices.ContainsFunc(st.providers, func(p providerEntry) bool { return p.Provider == entry.Provider }) {
			return catalogChange{}, errs.EInvalid("add model",
				fmt.Errorf("provider %q is not cataloged", entry.Provider))
		}
		if slices.ContainsFunc(st.models, func(m modelEntry) bool {
			return m.Provider == entry.Provider && m.ModelID == entry.ModelID
		}) {
			return catalogChange{}, errs.EConflict("add model",
				fmt.Errorf("model %s/%s already exists", entry.Provider, entry.ModelID))
		}
		return catalogChange{
			models:       append(slices.Clone(st.models), entry),
			modelComment: st.modelComment,
		}, nil
	})
}

// SetModel rewrites a model's display name, limits and capability flags; provider and model id are its
// immutable key and the input's own key fields are ignored.
func (r *Registry) SetModel(provider, modelID string, in ModelInput) error {
	entry, err := modelEntryFrom(provider, modelID, in)
	if err != nil {
		return errs.EInvalid("set model", err)
	}
	return r.applyMutation("set model", func(st catalogState) (catalogChange, error) {
		idx := slices.IndexFunc(st.models, func(m modelEntry) bool {
			return m.Provider == entry.Provider && m.ModelID == entry.ModelID
		})
		if idx < 0 {
			return catalogChange{}, errs.ENotFound("set model",
				fmt.Errorf("model %s/%s is not cataloged", entry.Provider, entry.ModelID))
		}
		next := slices.Clone(st.models)
		next[idx] = entry
		return catalogChange{models: next, modelComment: st.modelComment}, nil
	})
}

// RemoveModel drops a model, rejecting a key that is not cataloged and one cfg's LLM endpoints use.
func (r *Registry) RemoveModel(provider, modelID string, cfg *config.Config) error {
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	if provider == "" || modelID == "" {
		return errs.EInvalid("remove model", errors.New("provider and model_id are required"))
	}
	return r.applyMutation("remove model", func(st catalogState) (catalogChange, error) {
		idx := slices.IndexFunc(st.models, func(m modelEntry) bool {
			return m.Provider == provider && m.ModelID == modelID
		})
		if idx < 0 {
			return catalogChange{}, errs.ENotFound("remove model",
				fmt.Errorf("model %s/%s is not cataloged", provider, modelID))
		}
		if llmInUse(cfg, provider, modelID) {
			return catalogChange{}, errs.EConflict("remove model",
				fmt.Errorf("model %s/%s is in use by the enricher LLM configuration", provider, modelID))
		}
		return catalogChange{
			models:       slices.Delete(slices.Clone(st.models), idx, idx+1),
			modelComment: st.modelComment,
		}, nil
	})
}

// ResetCatalogs restores the embedded defaults for the selected catalogs and reloads the full in-memory
// state; at least one of the provider or model catalogs must be selected.
func (r *Registry) ResetCatalogs(providers, models bool) error {
	if err := ResetCatalogFiles(r.catalogDir, providers, models); err != nil {
		return err
	}
	return r.Reload()
}

// ResetCatalogFiles rewrites the selected catalog files from the embedded defaults, atomically, and
// under the catalog lock, without requiring a loadable registry — it is the recovery path for a mangled
// catalog. A partial reset validates its result against the catalog left in place and fails when the
// two no longer agree.
func ResetCatalogFiles(catalogDir string, providers, models bool) error {
	if catalogDir == "" {
		return errs.EInternal("reset catalogs", errors.New("catalog directory is required"))
	}
	if !providers && !models {
		return errs.EInvalid("reset catalogs", errors.New("select at least one of the provider or model catalogs"))
	}

	unlock, err := lockCatalogDir(catalogDir)
	if err != nil {
		return errs.EInternal("reset catalogs", err)
	}
	defer unlock()

	if providers && models {
		providerDoc, err := decodeProviderDoc(defaultProviderCatalog)
		if err != nil {
			return errs.EInternal("reset catalogs", err)
		}
		modelDoc, err := decodeModelDoc(defaultModelCatalog)
		if err != nil {
			return errs.EInternal("reset catalogs", err)
		}
		if err := writeCatalogPair(catalogDir, providerDoc, modelDoc); err != nil {
			return errs.EInternal("reset catalogs", err)
		}
		return nil
	}

	if providers {
		doc, err := decodeProviderDoc(defaultProviderCatalog)
		if err != nil {
			return errs.EInternal("reset catalogs", err)
		}
		models, _, _, err := parseModels(catalogDir)
		if err != nil {
			return errs.EInternal("reset catalogs", err)
		}
		if missing := missingProvider(doc.Providers, models); missing != "" {
			return errs.EConflict("reset catalogs", fmt.Errorf(
				"model entries reference provider %q, which the default provider catalog does not define; reset both catalogs or remove those models first",
				missing))
		}
		if err := writeProviderCatalog(catalogDir, doc.Comment, doc.Providers); err != nil {
			return errs.EInternal("reset catalogs", err)
		}
	}
	if models {
		doc, err := decodeModelDoc(defaultModelCatalog)
		if err != nil {
			return errs.EInternal("reset catalogs", err)
		}
		providers, _, _, err := resolveProviders(catalogDir)
		if err != nil {
			return errs.EInternal("reset catalogs", err)
		}
		if missing := missingProvider(providers, doc.Models); missing != "" {
			return errs.EConflict("reset catalogs", fmt.Errorf(
				"default model entries reference provider %q, which the provider catalog does not define; reset both catalogs to recover",
				missing))
		}
		if err := writeModelCatalog(catalogDir, doc.Comment, doc.Models); err != nil {
			return errs.EInternal("reset catalogs", err)
		}
	}
	return nil
}

// applyMutation serializes one catalog mutation: it takes the cross-process catalog lock first, so a
// wait on another writer never blocks in-process readers, then the in-memory write lock, re-reads the
// catalogs from disk, applies m to that fresh state, persists the change and swaps the result in. A
// single-file change is atomic; a change touching both files stages them before replacing either and
// restores the model file when the provider rename fails, so an error never leaves the pair split.
func (r *Registry) applyMutation(op string, m func(st catalogState) (catalogChange, error)) error {
	unlock, err := lockCatalogDir(r.catalogDir)
	if err != nil {
		return errs.EInternal(op, err)
	}
	defer unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	st, err := readCatalog(r.catalogDir)
	if err != nil {
		return errs.EInternal(op, err)
	}

	change, err := m(st)
	if err != nil {
		return err
	}

	switch {
	case change.providers != nil && change.models != nil:
		providerDoc := providerCatalogDoc{
			Comment:   change.providerComment,
			Version:   catalogVersion,
			Providers: change.providers,
		}
		modelDoc := modelCatalogDoc{
			Comment: change.modelComment,
			Version: catalogVersion,
			Models:  change.models,
		}
		if err := writeCatalogPair(r.catalogDir, providerDoc, modelDoc); err != nil {
			return errs.EInternal(op, err)
		}
		st.providers, st.models = change.providers, change.models
	case change.providers != nil:
		if err := writeProviderCatalog(r.catalogDir, change.providerComment, change.providers); err != nil {
			return errs.EInternal(op, err)
		}
		st.providers = change.providers
	case change.models != nil:
		if err := writeModelCatalog(r.catalogDir, change.modelComment, change.models); err != nil {
			return errs.EInternal(op, err)
		}
		st.models = change.models
	}

	r.providers = indexProviders(st.providers)
	r.models = st.models
	r.buildIndex()
	return nil
}

// providerEntryFrom builds and validates a provider entry: a name, an adapter and an absolute http(s)
// official_url are required, the name must avoid "." and "/" because both delimit catalog keys, and
// the session header, when set, must be a valid HTTP header name.
func providerEntryFrom(name string, in ProviderInput) (providerEntry, error) {
	name = strings.TrimSpace(name)
	adapter := strings.TrimSpace(in.Adapter)
	officialURL := strings.TrimSpace(in.OfficialURL)
	header := strings.TrimSpace(in.SessionHeader)

	if name == "" || adapter == "" || officialURL == "" {
		return providerEntry{}, errors.New("provider, adapter and official_url are required")
	}
	if strings.ContainsAny(name, "./") {
		return providerEntry{}, fmt.Errorf("provider %q must not contain %q or %q, both catalog key delimiters", name, ".", "/")
	}
	parsed, err := url.Parse(officialURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return providerEntry{}, fmt.Errorf("official_url %q must be an absolute http(s) URL", officialURL)
	}
	if header != "" && !headerTokenRE.MatchString(header) {
		return providerEntry{}, fmt.Errorf("session_header %q is not a valid HTTP header name", header)
	}
	return providerEntry{Provider: name, Adapter: adapter, OfficialURL: officialURL, SessionHeader: header}, nil
}

// modelEntryFrom builds and validates a model entry: provider and model_id are required, token limits
// must not be negative, and reasoning efforts are trimmed, emptied entries dropped and duplicates collapsed.
func modelEntryFrom(provider, modelID string, in ModelInput) (modelEntry, error) {
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	if provider == "" || modelID == "" {
		return modelEntry{}, errors.New("provider and model_id are required")
	}
	if in.MaxInputTokens < 0 || in.MaxOutputTokens < 0 {
		return modelEntry{}, errors.New("token limits must not be negative")
	}

	efforts := make([]string, 0, len(in.ReasoningEfforts))
	for _, e := range in.ReasoningEfforts {
		e = strings.TrimSpace(e)
		if e == "" || slices.Contains(efforts, e) {
			continue
		}
		efforts = append(efforts, e)
	}

	return modelEntry{
		Provider:                 provider,
		ModelID:                  modelID,
		DisplayName:              strings.TrimSpace(in.DisplayName),
		MaxInputTokens:           in.MaxInputTokens,
		MaxOutputTokens:          in.MaxOutputTokens,
		DisableReasoning:         in.DisableReasoning,
		ReasoningEfforts:         efforts,
		SupportsStructuredOutput: in.SupportsStructuredOutput,
		SupportsTemperature:      in.SupportsTemperature,
		SupportsPromptCaching:    in.SupportsPromptCaching,
		UsesMaxCompletionTokens:  in.UsesMaxCompletionTokens,
		UsesResponsesEndpoint:    in.UsesResponsesEndpoint,
	}, nil
}

// providerInputFrom maps a catalog entry to its editable view.
func providerInputFrom(p providerEntry) ProviderInput {
	return ProviderInput{
		Provider:      p.Provider,
		Adapter:       p.Adapter,
		OfficialURL:   p.OfficialURL,
		SessionHeader: p.SessionHeader,
	}
}

// modelInputFrom maps a catalog entry to its editable view.
func modelInputFrom(m modelEntry) ModelInput {
	return ModelInput{
		Provider:                 m.Provider,
		ModelID:                  m.ModelID,
		DisplayName:              m.DisplayName,
		MaxInputTokens:           m.MaxInputTokens,
		MaxOutputTokens:          m.MaxOutputTokens,
		DisableReasoning:         m.DisableReasoning,
		ReasoningEfforts:         m.ReasoningEfforts,
		SupportsStructuredOutput: m.SupportsStructuredOutput,
		SupportsTemperature:      m.SupportsTemperature,
		SupportsPromptCaching:    m.SupportsPromptCaching,
		UsesMaxCompletionTokens:  m.UsesMaxCompletionTokens,
		UsesResponsesEndpoint:    m.UsesResponsesEndpoint,
	}
}

// llmInUse reports whether any configured LLM endpoint — the analyzer, a fallback, the tag adjudicator
// or the tag quality judge — names provider and, when model is non-empty, that exact model; an empty
// model matches any model of the provider, and a nil config counts as not in use.
func llmInUse(cfg *config.Config, provider, model string) bool {
	if cfg == nil || provider == "" {
		return false
	}
	if llmEndpointMatches(cfg.Enricher.ContentAnalyzer.Llm, provider, model) {
		return true
	}
	for _, fb := range cfg.Enricher.ContentAnalyzer.Fallbacks {
		if llmEndpointMatches(fb.Llm, provider, model) {
			return true
		}
	}
	if llmEndpointMatches(cfg.Enricher.TagAdjudicator.Llm, provider, model) {
		return true
	}
	return llmEndpointMatches(cfg.Enricher.TagQuality.Llm, provider, model)
}

// llmEndpointMatches reports whether one configured endpoint names the provider/model pair.
func llmEndpointMatches(endpoint config.LlmConfig, provider, model string) bool {
	return endpoint.Provider == provider && (model == "" || endpoint.Model == model)
}

// decodeProviderDoc parses an embedded provider catalog.
func decodeProviderDoc(data []byte) (providerCatalogDoc, error) {
	var doc providerCatalogDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return providerCatalogDoc{}, fmt.Errorf("parse embedded provider catalog: %w", err)
	}
	return doc, nil
}

// decodeModelDoc parses an embedded model catalog.
func decodeModelDoc(data []byte) (modelCatalogDoc, error) {
	var doc modelCatalogDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return modelCatalogDoc{}, fmt.Errorf("parse embedded model catalog: %w", err)
	}
	return doc, nil
}

// providerCatalogData renders a provider catalog document, never leaving its entries nil.
func providerCatalogData(comment string, entries []providerEntry) ([]byte, error) {
	if entries == nil {
		entries = []providerEntry{}
	}
	return marshalCatalog(providerCatalogDoc{Comment: comment, Version: catalogVersion, Providers: entries})
}

// modelCatalogData renders a model catalog document, never leaving its entries nil.
func modelCatalogData(comment string, entries []modelEntry) ([]byte, error) {
	if entries == nil {
		entries = []modelEntry{}
	}
	return marshalCatalog(modelCatalogDoc{Comment: comment, Version: catalogVersion, Models: entries})
}

// marshalCatalog renders one catalog document as the on-disk JSON, newline-terminated.
func marshalCatalog(doc any) ([]byte, error) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// writeProviderCatalog replaces the provider catalog file with the given entries, preserving that file's
// `_comment` hint.
func writeProviderCatalog(catalogDir, comment string, entries []providerEntry) error {
	data, err := providerCatalogData(comment, entries)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", providerCatalogFile, err)
	}
	return writeCatalogBytes(catalogDir, providerCatalogFile, data)
}

// writeModelCatalog replaces the model catalog file with the given model entries, preserving that file's
// `_comment` hint.
func writeModelCatalog(catalogDir, comment string, entries []modelEntry) error {
	data, err := modelCatalogData(comment, entries)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", modelCatalogFile, err)
	}
	return writeCatalogBytes(catalogDir, modelCatalogFile, data)
}

// writeCatalogBytes atomically installs one catalog file: the bytes go to a temp file beside it and a
// rename replaces the file, so a reader never sees a partial file and a failure leaves it intact.
func writeCatalogBytes(catalogDir, name string, data []byte) error {
	tmp, err := stageCatalogBytes(catalogDir, name, data)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(catalogDir, name)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

// writeCatalogPair installs both catalog files as one pair: both temp files are staged before either
// rename, so every write error surfaces before a file is replaced; the model file is installed first,
// which keeps a pruning mutation's intermediate pair referentially valid, and a failed provider rename
// restores the model file from its previous bytes, so an error never leaves the pair split.
func writeCatalogPair(catalogDir string, providerDoc providerCatalogDoc, modelDoc modelCatalogDoc) error {
	if providerDoc.Providers == nil {
		providerDoc.Providers = []providerEntry{}
	}
	if modelDoc.Models == nil {
		modelDoc.Models = []modelEntry{}
	}
	providerData, err := marshalCatalog(providerDoc)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", providerCatalogFile, err)
	}
	modelData, err := marshalCatalog(modelDoc)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", modelCatalogFile, err)
	}

	modelPrev, modelExisted, err := readCatalogBytes(catalogDir, modelCatalogFile)
	if err != nil {
		return err
	}

	providerTmp, err := stageCatalogBytes(catalogDir, providerCatalogFile, providerData)
	if err != nil {
		return err
	}
	modelTmp, err := stageCatalogBytes(catalogDir, modelCatalogFile, modelData)
	if err != nil {
		_ = os.Remove(providerTmp)
		return err
	}

	if err := os.Rename(modelTmp, filepath.Join(catalogDir, modelCatalogFile)); err != nil {
		_ = os.Remove(providerTmp)
		_ = os.Remove(modelTmp)
		return fmt.Errorf("write %s: %w", modelCatalogFile, err)
	}
	if err := os.Rename(providerTmp, filepath.Join(catalogDir, providerCatalogFile)); err != nil {
		_ = os.Remove(providerTmp)
		if restoreErr := restoreCatalogBytes(catalogDir, modelCatalogFile, modelPrev, modelExisted); restoreErr != nil {
			return fmt.Errorf("write %s: %w (restoring %s failed: %v)",
				providerCatalogFile, err, modelCatalogFile, restoreErr)
		}
		return fmt.Errorf("write %s: %w", providerCatalogFile, err)
	}
	return nil
}

// restoreCatalogBytes puts one catalog file back to its previous bytes, removing it when it had not
// existed before the write it is rolling back.
func restoreCatalogBytes(catalogDir, name string, prev []byte, existed bool) error {
	if !existed {
		return os.Remove(filepath.Join(catalogDir, name))
	}
	return writeCatalogBytes(catalogDir, name, prev)
}

// stageCatalogBytes writes data to a temp file beside the catalog file it will become.
func stageCatalogBytes(catalogDir, name string, data []byte) (string, error) {
	tmp, err := os.CreateTemp(catalogDir, name+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	// CreateTemp creates the file 0600; the catalogs are world-readable
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	return tmpPath, nil
}

// readCatalogBytes reads one catalog file's raw bytes; existed reports whether it was there.
func readCatalogBytes(catalogDir, name string) ([]byte, bool, error) {
	data, err := os.ReadFile(filepath.Join(catalogDir, name))
	if err == nil {
		return data, true, nil
	}
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("read %s: %w", name, err)
}

// lockCatalogDir takes the cross-process exclusive lock that serializes the catalog read-modify-write
// cycles and returns its release.
func lockCatalogDir(catalogDir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(catalogDir, catalogLockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open catalog lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock catalog: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

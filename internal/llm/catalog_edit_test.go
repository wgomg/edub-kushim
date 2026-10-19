package llm

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/errs"
)

func newMutableRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("seed catalogs: %v", err)
	}
	return r
}

// TestMutationsPersistAcrossReload pins the add/set round trip: a mutated catalog survives a fresh load
// from disk, a duplicate key is rejected as a conflict, and provider or model keys fail validation.
func TestMutationsPersistAcrossReload(t *testing.T) {
	r := newMutableRegistry(t)

	provider := ProviderInput{
		Provider:    "groq",
		Adapter:     "openai-compatible",
		OfficialURL: "https://api.groq.com/openai/v1",
	}
	if err := r.AddProvider(provider); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if err := r.AddModel(ModelInput{Provider: "groq", ModelID: "llama-4-scout", DisplayName: "Llama 4 Scout",
		MaxInputTokens: 131072}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	if err := r.SetModel("groq", "llama-4-scout", ModelInput{Provider: "groq", ModelID: "llama-4-scout",
		DisplayName: "Scout", SupportsTemperature: true}); err != nil {
		t.Fatalf("SetModel: %v", err)
	}

	if err := r.AddProvider(provider); errs.KindOf(err) != errs.KindConflict {
		t.Errorf("duplicate AddProvider: got %v, want a conflict", err)
	}
	if err := r.AddProvider(ProviderInput{Provider: "bad.provider", Adapter: "openai-compatible",
		OfficialURL: "https://example.test"}); errs.KindOf(err) != errs.KindInvalid {
		t.Errorf("dotted provider name: got %v, want an invalid error", err)
	}
	if err := r.AddModel(ModelInput{Provider: "ghost", ModelID: "x"}); errs.KindOf(err) != errs.KindInvalid {
		t.Errorf("model under an unknown provider: got %v, want an invalid error", err)
	}

	reloaded, err := NewRegistry(r.catalogDir)
	if err != nil {
		t.Fatalf("reload after mutations: %v", err)
	}
	catalog := reloaded.Catalog()
	if !slices.ContainsFunc(catalog.Providers, func(p ProviderInput) bool { return p.Provider == "groq" }) {
		t.Errorf("providers = %v, want groq persisted", catalog.Providers)
	}
	idx := slices.IndexFunc(catalog.Models, func(m ModelInput) bool { return m.ModelID == "llama-4-scout" })
	if idx < 0 {
		t.Fatalf("models = %v, want llama-4-scout persisted", catalog.Models)
	}
	if got := catalog.Models[idx]; got.DisplayName != "Scout" || !got.SupportsTemperature || got.MaxInputTokens != 0 {
		t.Errorf("persisted model = %+v, want the SetModel replacement (Scout, temperature, no limits)", got)
	}
}

// TestRemoveProviderOrphanAndCascade pins the cascade contract: a provider that still owns models is
// rejected without cascade, and a cascaded removal leaves a pair that still loads.
func TestRemoveProviderOrphanAndCascade(t *testing.T) {
	r := newMutableRegistry(t)

	err := r.RemoveProvider("zhipu", false, nil)
	if errs.KindOf(err) != errs.KindConflict || !strings.Contains(err.Error(), "models") {
		t.Fatalf("remove without cascade: got %v, want an orphan-model conflict", err)
	}
	if err := r.RemoveProvider("zhipu", true, nil); err != nil {
		t.Fatalf("remove with cascade: %v", err)
	}

	reloaded, err := NewRegistry(r.catalogDir)
	if err != nil {
		t.Fatalf("pair must stay loadable after cascade: %v", err)
	}
	catalog := reloaded.Catalog()
	if slices.ContainsFunc(catalog.Providers, func(p ProviderInput) bool { return p.Provider == "zhipu" }) {
		t.Errorf("providers still contain zhipu: %v", catalog.Providers)
	}
	if slices.ContainsFunc(catalog.Models, func(m ModelInput) bool { return m.Provider == "zhipu" }) {
		t.Errorf("cascade left zhipu models behind: %v", catalog.Models)
	}
}

// TestRemoveInUseGuard pins the config reference guard: a provider or model the enricher configuration
// names cannot be removed, and a nil config removes the protection entirely.
func TestRemoveInUseGuard(t *testing.T) {
	r := newMutableRegistry(t)
	inUse := &config.Config{Enricher: config.EnricherConfig{ContentAnalyzer: config.ContentAnalyzerConfig{
		Llm: config.LlmConfig{Provider: "zhipu", Model: "glm-5"},
	}}}

	err := r.RemoveModel("zhipu", "glm-5", inUse)
	if errs.KindOf(err) != errs.KindConflict || !strings.Contains(err.Error(), "in use") {
		t.Errorf("remove referenced model: got %v, want an in-use conflict", err)
	}
	err = r.RemoveProvider("zhipu", true, inUse)
	if errs.KindOf(err) != errs.KindConflict || !strings.Contains(err.Error(), "in use") {
		t.Errorf("remove referenced provider: got %v, want an in-use conflict", err)
	}
	if err := r.RemoveModel("zhipu", "glm-5", nil); err != nil {
		t.Errorf("remove with a nil config: %v, want the guard lifted", err)
	}
}

// TestResetRecoversMangledCatalogs pins the recovery contract: a corrupt file fails the load, and each
// reset scope repairs its own file — the partial reset must not parse the file it is about to replace.
func TestResetRecoversMangledCatalogs(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewRegistry(dir); err != nil {
		t.Fatalf("seed catalogs: %v", err)
	}
	mangle := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not json"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	assertUnusable := func(stage string) {
		t.Helper()
		if _, err := NewRegistry(dir); err == nil {
			t.Fatalf("%s: want the mangled catalog to fail the load", stage)
		}
	}
	assertUsable := func(stage string) {
		t.Helper()
		if _, err := NewRegistry(dir); err != nil {
			t.Fatalf("%s: catalogs must load, got %v", stage, err)
		}
	}

	mangle(providerCatalogFile)
	assertUnusable("before any reset")
	if err := ResetCatalogFiles(dir, true, false); err != nil {
		t.Fatalf("providers-only reset must repair the mangled provider file: %v", err)
	}
	assertUsable("after the providers-only reset")

	mangle(modelCatalogFile)
	assertUnusable("with a mangled model file")
	if err := ResetCatalogFiles(dir, false, true); err != nil {
		t.Fatalf("models-only reset must repair the mangled model file: %v", err)
	}
	assertUsable("after the models-only reset")

	mangle(providerCatalogFile)
	mangle(modelCatalogFile)
	if err := ResetCatalogFiles(dir, true, true); err != nil {
		t.Fatalf("full reset: %v", err)
	}
	assertUsable("after the full reset")
}

// TestSetProviderPreservesComment pins the round trip of the `_comment` hint the catalog files carry.
func TestSetProviderPreservesComment(t *testing.T) {
	dir := t.TempDir()
	providerDoc := `{"_comment":"custom hint","version":1,"providers":[` +
		`{"provider":"acme","adapter":"openai-compatible","official_url":"https://acme.test/v1"}]}`
	if err := os.WriteFile(filepath.Join(dir, providerCatalogFile), []byte(providerDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, modelCatalogFile), []byte(`{"version":1,"models":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := r.SetProvider("acme", ProviderInput{Provider: "acme", Adapter: "anthropic",
		OfficialURL: "https://acme.test/v1"}); err != nil {
		t.Fatalf("SetProvider: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, providerCatalogFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "custom hint") {
		t.Errorf("_comment lost by the rewrite:\n%s", data)
	}
	if !strings.Contains(string(data), "anthropic") {
		t.Errorf("mutation not written:\n%s", data)
	}
}

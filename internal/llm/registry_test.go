package llm

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fixtureDir(t *testing.T) string {
	return "testdata"
}

func TestNewRegistry(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
}

func TestNewRegistry_SeedsMissingCatalogs(t *testing.T) {
	dir := t.TempDir()
	r, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("expected the embedded defaults to be seeded, got error: %v", err)
	}
	for _, name := range []string{providerCatalogFile, modelCatalogFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to be seeded on disk: %v", name, err)
		}
	}
	if caps := r.Lookup("openai", "gpt-5.6-sol"); caps == nil {
		t.Error("expected the seeded catalog to be loaded")
	}
}

func TestNewRegistry_EmptyPathFails(t *testing.T) {
	if _, err := NewRegistry(""); err == nil {
		t.Fatal("expected an error for an empty catalog directory")
	}
}

func TestNewRegistry_UserModelCatalog(t *testing.T) {
	models := `{"version":1,"models":[
		{"provider":"openai","model_id":"gpt-5.6-sol","display_name":"User Sol","max_input_tokens":1000,"max_output_tokens":1234,"disable_reasoning":true,"reasoning_efforts":["low"],"supports_structured_output":true,"supports_temperature":true,"supports_prompt_caching":false},
		{"provider":"deepseek","model_id":"user-added-model","display_name":"User Added","max_input_tokens":2000,"max_output_tokens":567,"disable_reasoning":true,"reasoning_efforts":["high"],"supports_structured_output":true,"supports_temperature":true,"supports_prompt_caching":false}
	]}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, modelCatalogFile), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	sol := r.Lookup("openai", "gpt-5.6-sol")
	if sol == nil {
		t.Fatal("expected the on-disk model to be cataloged")
	}
	if sol.MaxOutputTokens != 1234 || !sol.SupportsTemperature || !slices.Equal(sol.ReasoningEffortLevels, []string{"low"}) {
		t.Errorf("on-disk entry not loaded verbatim: got max_output_tokens=%d supports_temperature=%v efforts=%v, want 1234/true/[low]",
			sol.MaxOutputTokens, sol.SupportsTemperature, sol.ReasoningEffortLevels)
	}
	if added := r.Lookup("deepseek", "user-added-model"); added == nil {
		t.Fatal("expected the added model to be cataloged")
	}
	if r.Lookup("openai", "gpt-5.5") != nil {
		t.Error("a model absent from the on-disk catalog must not be cataloged")
	}
}

func TestNewRegistry_UserProviderCatalog(t *testing.T) {
	providers := `{"version":1,"providers":[{"provider":"openai","adapter":"openai-compatible","official_url":"https://proxy.example/v1"}]}`
	models := `{"version":1,"models":[{"provider":"openai","model_id":"gpt-5.6-sol","max_input_tokens":1,"max_output_tokens":1,"supports_structured_output":true,"supports_temperature":true,"supports_prompt_caching":false}]}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, providerCatalogFile), []byte(providers), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, modelCatalogFile), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	if got := r.ProviderDefaultURL("openai"); got != "https://proxy.example/v1" {
		t.Errorf("ProviderDefaultURL(openai) = %q, want the on-disk endpoint", got)
	}
	if r.Lookup("openai", "gpt-5.6-sol") == nil {
		t.Error("expected the on-disk model to be cataloged")
	}
	if got := r.ProviderDefaultURL("anthropic"); got != "" {
		t.Errorf("ProviderDefaultURL(anthropic) = %q, want empty for a provider absent from the on-disk catalog", got)
	}
}

func TestNewRegistry_UserModelWithoutProviderFails(t *testing.T) {
	models := `{"version":1,"models":[{"provider":"no-such-provider","model_id":"m","max_input_tokens":1,"max_output_tokens":1}]}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, modelCatalogFile), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewRegistry(dir); err == nil {
		t.Fatal("expected a load error for a model whose provider has no provider-catalog entry")
	} else if !strings.Contains(err.Error(), "no-such-provider") || !strings.Contains(err.Error(), providerCatalogFile) {
		t.Errorf("error must name the missing provider and the provider catalog, got %v", err)
	}
}

func TestNewRegistry_UnsupportedVersion(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"missing version", `{"providers":[]}`, "unsupported catalog version 0"},
		{"wrong version", `{"version":2,"providers":[]}`, "unsupported catalog version 2"},
		{"legacy bare array", `[{"provider":"p","adapter":"a","official_url":"u"}]`, "expected an object with version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, providerCatalogFile), []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := NewRegistry(dir)
			if err == nil {
				t.Fatal("expected a load error for an unsupported catalog format")
			}
			if !strings.Contains(err.Error(), providerCatalogFile) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error must name the file and %q, got %v", tt.want, err)
			}
		})
	}
}

func TestNewRegistry_FailedLoadLeavesConfigDirUntouched(t *testing.T) {
	invalid := `[{"provider":"openai"}]`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, providerCatalogFile), []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewRegistry(dir); err == nil {
		t.Fatal("expected a load error for an invalid provider catalog")
	}

	got, err := os.ReadFile(filepath.Join(dir, providerCatalogFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != invalid {
		t.Errorf("a failing load must not overwrite the existing catalog: got %q, want %q", got, invalid)
	}
	if _, err := os.Stat(filepath.Join(dir, modelCatalogFile)); !os.IsNotExist(err) {
		t.Error("a failing load must not seed the other catalog file")
	}
}

func TestLookup_KnownModel(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("deepseek", "deepseek-v4-flash")
	if caps == nil {
		t.Fatal("expected capabilities for deepseek-v4-flash")
	}
	if caps.MaxInputTokens != 1000000 {
		t.Errorf("expected 1000000 max input tokens, got %d", caps.MaxInputTokens)
	}
	if caps.MaxOutputTokens != 8192 {
		t.Errorf("expected 8192 max output tokens, got %d", caps.MaxOutputTokens)
	}
	if !caps.CanDisableReasoning {
		t.Error("expected disable_reasoning")
	}
	if !slices.Equal(caps.ReasoningEffortLevels, []string{"high", "max"}) {
		t.Errorf("expected reasoning efforts [high max], got %v", caps.ReasoningEffortLevels)
	}
	if !caps.SupportsTemperature {
		t.Error("expected supports_temperature")
	}
	if !caps.SupportsStructuredOutput {
		t.Error("expected supports_structured_output")
	}
}

func TestLookup_DeepSeekPro(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("deepseek", "deepseek-v4-pro")
	if caps == nil {
		t.Fatal("expected capabilities for deepseek-v4-pro")
	}
	if !caps.CanDisableReasoning {
		t.Error("expected supports_reasoning")
	}
	if !slices.Equal(caps.ReasoningEffortLevels, []string{"high", "max"}) {
		t.Errorf("expected reasoning efforts [high max], got %v", caps.ReasoningEffortLevels)
	}
}

func TestLookup_MissingModel(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("openai", "nonexistent-model-v99")
	if caps != nil {
		t.Fatal("expected nil for nonexistent model")
	}
}

func TestLookup_MissingProvider(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("unknown_provider", "gpt-4o")
	if caps != nil {
		t.Fatal("expected nil for nonexistent provider")
	}
}

func TestLookup_MistralSmall(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("mistral", "mistral-small-latest")
	if caps == nil {
		t.Fatal("expected capabilities for mistral-small-latest")
	}
	if caps.MaxInputTokens != 128000 {
		t.Errorf("expected 128000 max input, got %d", caps.MaxInputTokens)
	}
	if caps.CanDisableReasoning {
		t.Error("expected mistral-small to run at an effort level, not a disable flag")
	}
}

func TestLookup_AnthropicModel(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("anthropic", "claude-sonnet-5")
	if caps == nil {
		t.Fatal("expected capabilities for claude-sonnet-5")
	}
	if !caps.CanDisableReasoning {
		t.Error("expected reasoning support")
	}
	if len(caps.ReasoningEffortLevels) == 0 {
		t.Error("expected non-empty reasoning efforts")
	}
	if !caps.SupportsPromptCaching {
		t.Error("expected prompt caching support")
	}
}

func TestAdapters(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	adapters := r.Adapters()
	if len(adapters) == 0 {
		t.Fatal("expected at least one adapter")
	}
	if !slices.Contains(adapters, "openai-compatible") {
		t.Error("expected openai-compatible adapter")
	}
	if !slices.Contains(adapters, "anthropic") {
		t.Error("expected anthropic adapter")
	}
}

func TestProvidersForAdapter_OpenAICompatible(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	providers := r.ProvidersForAdapter("openai-compatible")
	if len(providers) == 0 {
		t.Fatal("expected at least one provider for openai-compatible")
	}
	if !slices.Contains(providers, "deepseek") {
		t.Error("expected deepseek")
	}
}

func TestProvidersForAdapter_Anthropic(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	providers := r.ProvidersForAdapter("anthropic")
	if len(providers) == 0 {
		t.Fatal("expected at least one provider for anthropic")
	}
	if !slices.Contains(providers, "anthropic") {
		t.Error("expected anthropic")
	}
}

func TestProvidersForAdapter_Gemini(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	providers := r.ProvidersForAdapter("gemini")
	if providers == nil {
		t.Fatal("expected non-nil slice")
	}
	if len(providers) != 0 {
		t.Errorf("expected 0 providers for gemini (no model in fixture), got %d", len(providers))
	}
}

func TestProvidersForAdapter_Unknown(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	providers := r.ProvidersForAdapter("nonexistent")
	if providers == nil {
		t.Fatal("expected empty slice, not nil")
	}
	if len(providers) != 0 {
		t.Errorf("expected 0 providers for nonexistent adapter, got %d", len(providers))
	}
}

func TestModelsForProvider_DeepSeek(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	models := r.ModelsForProvider("deepseek")
	if len(models) == 0 {
		t.Fatal("expected at least one model for deepseek")
	}

	var foundFlash, foundPro bool
	for _, m := range models {
		if m.ID == "deepseek-v4-flash" {
			foundFlash = true
			if !m.Capabilities.CanDisableReasoning {
				t.Error("expected deepseek-v4-flash to have reasoning")
			}
		}
		if m.ID == "deepseek-v4-pro" {
			foundPro = true
		}
	}
	if !foundFlash {
		t.Error("expected deepseek-v4-flash in model list")
	}
	if !foundPro {
		t.Error("expected deepseek-v4-pro in model list")
	}
}

func TestModelsForProvider_Mistral(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	models := r.ModelsForProvider("mistral")
	if len(models) == 0 {
		t.Fatal("expected at least one mistral model")
	}
	for _, m := range models {
		if m.ID == "" {
			t.Error("model ID should not be empty")
		}
	}
}

func TestModelsForProvider_Unknown(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	models := r.ModelsForProvider("unknown_provider")
	if len(models) != 0 {
		t.Errorf("expected 0 models for unknown provider, got %d", len(models))
	}
}

func TestModelsForProvider_Anthropic(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	models := r.ModelsForProvider("anthropic")
	if len(models) == 0 {
		t.Fatal("expected at least one anthropic model")
	}
	for _, m := range models {
		if m.ID == "" {
			t.Error("model ID should not be empty")
		}
	}
}

func TestModelsForProvider_Sorted(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	models := r.ModelsForProvider("deepseek")
	if len(models) < 2 {
		t.Skip("need at least 2 models to test sorting")
	}
	for i := 1; i < len(models); i++ {
		if models[i].ID < models[i-1].ID {
			t.Errorf("models not sorted: %s > %s", models[i-1].ID, models[i].ID)
		}
	}
}

func TestDefaultURL(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		provider string
		want     string
	}{
		{"deepseek", "https://api.deepseek.com/v1"},
		{"anthropic", "https://api.anthropic.com/v1"},
	}
	for _, tt := range tests {
		got := r.ProviderDefaultURL(tt.provider)
		if got != tt.want {
			t.Errorf("ProviderDefaultURL(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

func TestDefaultURL_Unknown(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	got := r.ProviderDefaultURL("unknown")
	if got != "" {
		t.Errorf("expected empty for unknown, got %q", got)
	}
}

func TestProviderAdapter(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		provider string
		want     string
	}{
		{"deepseek", "openai-compatible"},
		{"anthropic", "anthropic"},
		{"unknown", "custom"},
	}
	for _, tt := range tests {
		got := r.ProviderAdapter(tt.provider)
		if got != tt.want {
			t.Errorf("ProviderAdapter(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

func TestReasoningEffortDefaults(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("anthropic", "claude-sonnet-5")
	if caps == nil {
		t.Fatal("expected capabilities")
	}
	if !caps.CanDisableReasoning {
		t.Error("expected reasoning")
	}
	if len(caps.ReasoningEffortLevels) == 0 {
		t.Errorf("expected non-empty reasoning efforts, got %v", caps.ReasoningEffortLevels)
	}
}

func TestReasoningEffort_EmptyStaysEmpty(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("anthropic", "claude-haiku-4-5")
	if caps == nil {
		t.Fatal("expected capabilities")
	}
	if !caps.CanDisableReasoning {
		t.Error("expected reasoning")
	}
	if len(caps.ReasoningEffortLevels) != 0 {
		t.Errorf("expected no fabricated reasoning efforts for a model with none, got %v", caps.ReasoningEffortLevels)
	}
}

func TestReload_PicksUpEdits(t *testing.T) {
	dir := t.TempDir()
	r, err := NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}

	edited := `{"version":1,"models":[{"provider":"deepseek","model_id":"deepseek-v4-flash","display_name":"Edited","max_input_tokens":100,"max_output_tokens":200,"disable_reasoning":true,"reasoning_efforts":["high"],"supports_structured_output":true,"supports_temperature":true,"supports_prompt_caching":false}]}`
	if err := os.WriteFile(filepath.Join(dir, modelCatalogFile), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if caps := r.Lookup("deepseek", "deepseek-v4-flash"); caps == nil || caps.MaxOutputTokens != 200 {
		t.Fatalf("expected the edited catalog after reload, got %+v", caps)
	}

	if err := os.WriteFile(filepath.Join(dir, modelCatalogFile), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(); err == nil {
		t.Fatal("expected reload to fail on an unparseable catalog")
	}
	if caps := r.Lookup("deepseek", "deepseek-v4-flash"); caps == nil || caps.MaxOutputTokens != 200 {
		t.Fatalf("a failed reload must keep the previous catalogs, got %+v", caps)
	}
}

func TestConcurrentAccess(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan bool, 10)
	for range 10 {
		go func() {
			_ = r.Lookup("deepseek", "deepseek-v4-flash")
			_ = r.Adapters()
			_ = r.ProvidersForAdapter("openai-compatible")
			_ = r.ModelsForProvider("deepseek")
			done <- true
		}()
	}

	for range 10 {
		<-done
	}
}

func TestConcurrentReloadAndProviderReads(t *testing.T) {
	r, err := NewRegistry(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}

	const readers = 16
	done := make(chan bool, readers)
	for range readers {
		go func() {
			for range 50 {
				_ = r.ProviderDefaultURL("deepseek")
				_ = r.ProviderAdapter("deepseek")
				_ = r.ProviderSessionHeader("deepseek")
			}
			done <- true
		}()
	}

	for range 50 {
		if err := r.Reload(); err != nil {
			t.Errorf("reload failed: %v", err)
			break
		}
	}

	for range readers {
		<-done
	}
}

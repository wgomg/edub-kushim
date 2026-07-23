package llm

import (
	"path/filepath"
	"slices"
	"testing"
)

func fixturePath(t *testing.T) string {
	return filepath.Join("testdata", "catalog_fixture.json")
}

func TestNewRegistry(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
}

func TestNewRegistry_FallsBackToEmbeddedCatalog(t *testing.T) {
	r, err := NewRegistry("/nonexistent/path.json")
	if err != nil {
		t.Fatalf("expected fallback to embedded catalog, got error: %v", err)
	}
	if caps := r.Lookup("openai", "gpt-5.6-sol"); caps == nil {
		t.Error("expected the embedded production catalog to be loaded")
	}
}

func TestNewRegistry_EmptyPathUsesEmbeddedCatalog(t *testing.T) {
	r, err := NewRegistry("")
	if err != nil {
		t.Fatalf("expected fallback to embedded catalog, got error: %v", err)
	}
	if caps := r.Lookup("openai", "gpt-5.6-sol"); caps == nil {
		t.Error("expected the embedded production catalog to be loaded")
	}
}

func TestLookup_KnownModel(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
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
	if !caps.SupportsReasoning {
		t.Error("expected supports_reasoning")
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
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("deepseek", "deepseek-v4-pro")
	if caps == nil {
		t.Fatal("expected capabilities for deepseek-v4-pro")
	}
	if !caps.SupportsReasoning {
		t.Error("expected supports_reasoning")
	}
	if !slices.Equal(caps.ReasoningEffortLevels, []string{"high", "max"}) {
		t.Errorf("expected reasoning efforts [high max], got %v", caps.ReasoningEffortLevels)
	}
}

func TestLookup_MissingModel(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("openai", "nonexistent-model-v99")
	if caps != nil {
		t.Fatal("expected nil for nonexistent model")
	}
}

func TestLookup_MissingProvider(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("unknown_provider", "gpt-4o")
	if caps != nil {
		t.Fatal("expected nil for nonexistent provider")
	}
}

func TestLookup_MistralSmall(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
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
	if !caps.SupportsReasoning {
		t.Error("expected reasoning support")
	}
}

func TestLookup_AnthropicModel(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("anthropic", "claude-sonnet-5")
	if caps == nil {
		t.Fatal("expected capabilities for claude-sonnet-5")
	}
	if !caps.SupportsReasoning {
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
	r, err := NewRegistry(fixturePath(t))
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
	r, err := NewRegistry(fixturePath(t))
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
	r, err := NewRegistry(fixturePath(t))
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
	r, err := NewRegistry(fixturePath(t))
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
	r, err := NewRegistry(fixturePath(t))
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
	r, err := NewRegistry(fixturePath(t))
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
			if !m.Capabilities.SupportsReasoning {
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
	r, err := NewRegistry(fixturePath(t))
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
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}

	models := r.ModelsForProvider("unknown_provider")
	if len(models) != 0 {
		t.Errorf("expected 0 models for unknown provider, got %d", len(models))
	}
}

func TestModelsForProvider_Anthropic(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
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
	r, err := NewRegistry(fixturePath(t))
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
	r, err := NewRegistry(fixturePath(t))
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
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	got := r.ProviderDefaultURL("unknown")
	if got != "" {
		t.Errorf("expected empty for unknown, got %q", got)
	}
}

func TestProviderAdapter(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
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
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("anthropic", "claude-sonnet-5")
	if caps == nil {
		t.Fatal("expected capabilities")
	}
	if !caps.SupportsReasoning {
		t.Error("expected reasoning")
	}
	if len(caps.ReasoningEffortLevels) == 0 {
		t.Errorf("expected non-empty reasoning efforts, got %v", caps.ReasoningEffortLevels)
	}
}

func TestReasoningEffort_EmptyStaysEmpty(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("anthropic", "claude-haiku-4-5")
	if caps == nil {
		t.Fatal("expected capabilities")
	}
	if !caps.SupportsReasoning {
		t.Error("expected reasoning")
	}
	if len(caps.ReasoningEffortLevels) != 0 {
		t.Errorf("expected no fabricated reasoning efforts for a model with none, got %v", caps.ReasoningEffortLevels)
	}
}

func TestReload(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}

	caps := r.Lookup("deepseek", "deepseek-v4-flash")
	if caps == nil {
		t.Fatal("expected capabilities before reload")
	}

	if err := r.Reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	caps = r.Lookup("deepseek", "deepseek-v4-flash")
	if caps == nil {
		t.Fatal("expected capabilities after reload")
	}
}

func TestCatalogPath(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.CatalogPath() != fixturePath(t) {
		t.Errorf("expected %q, got %q", fixturePath(t), r.CatalogPath())
	}
}

func TestConcurrentAccess(t *testing.T) {
	r, err := NewRegistry(fixturePath(t))
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

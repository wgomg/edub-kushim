package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/container"
)

const llmBaseConfig = `consumer:
  ocr:
    languages:
      - eng
`

const llmInUseConfig = llmBaseConfig + `enricher:
  contentanalyzer:
    llm:
      adapter: openai-compatible
      provider: zhipu
      model: glm-5
`

func llmTestRunner(t *testing.T, yaml string) (*CommandRunner, string) {
	t.Helper()
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	c := container.NewContainer(cfg, log.NewLogger("error"))
	t.Cleanup(func() { c.Close() })
	return NewCommandRunner(c, "cli"), configDir
}

// TestLLMVerbFieldSetPersists pins the config-grammar contract: one key and value reach the catalog file
// on disk for both string and boolean fields, and dump and get run over the same seeded catalogs.
func TestLLMVerbFieldSetPersists(t *testing.T) {
	runner, configDir := llmTestRunner(t, llmBaseConfig)

	if err := runner.ExecuteCommand("llm", nil); err != nil {
		t.Errorf("dump: %v", err)
	}
	if err := runner.ExecuteCommand("llm", []string{"providers.zhipu.adapter"}); err != nil {
		t.Errorf("get: %v", err)
	}
	if err := runner.ExecuteCommand("llm", []string{"providers.zhipu.adapter", "custom-adapter"}); err != nil {
		t.Fatalf("set a string field: %v", err)
	}
	if err := runner.ExecuteCommand("llm",
		[]string{"models.zhipu/glm-5.supports_temperature", "false"}); err != nil {
		t.Fatalf("set a boolean field: %v", err)
	}

	providerData, err := os.ReadFile(filepath.Join(configDir, "provider_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(providerData), "custom-adapter") {
		t.Errorf("string set did not reach the provider catalog:\n%s", providerData)
	}

	modelData, err := os.ReadFile(filepath.Join(configDir, "model_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Models []struct {
			ModelID             string `json:"model_id"`
			SupportsTemperature bool   `json:"supports_temperature"`
		} `json:"models"`
	}
	if err := json.Unmarshal(modelData, &doc); err != nil {
		t.Fatalf("decode model catalog: %v", err)
	}
	for _, m := range doc.Models {
		if m.ModelID == "glm-5" {
			if m.SupportsTemperature {
				t.Errorf("boolean set did not reach glm-5: supports_temperature = true")
			}
			return
		}
	}
	t.Fatalf("glm-5 missing from %s", modelData)
}

// TestLLMVerbUnsetGuards pins both removal guards through the verb: an enricher reference blocks the
// unset, and a provider that still owns models is refused until --cascade drops them together.
func TestLLMVerbUnsetGuards(t *testing.T) {
	referenced, _ := llmTestRunner(t, llmInUseConfig)

	err := referenced.ExecuteCommand("llm", []string{"--unset", "providers.zhipu"})
	if err == nil || !strings.Contains(err.Error(), "in use") {
		t.Errorf("unset a referenced provider: got %v, want an in-use error", err)
	}
	err = referenced.ExecuteCommand("llm", []string{"--unset", "models.zhipu/glm-5"})
	if err == nil || !strings.Contains(err.Error(), "in use") {
		t.Errorf("unset a referenced model: got %v, want an in-use error", err)
	}

	plain, configDir := llmTestRunner(t, llmBaseConfig)
	err = plain.ExecuteCommand("llm", []string{"--unset", "providers.zhipu"})
	if err == nil || !strings.Contains(err.Error(), "models") {
		t.Errorf("unset a provider with models: got %v, want an orphan-model error", err)
	}
	if err := plain.ExecuteCommand("llm", []string{"--unset", "providers.zhipu", "--cascade"}); err != nil {
		t.Fatalf("cascade unset: %v", err)
	}
	for _, name := range []string{"provider_catalog.json", "model_catalog.json"} {
		data, err := os.ReadFile(filepath.Join(configDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "zhipu") {
			t.Errorf("cascade left zhipu in %s:\n%s", name, data)
		}
	}
}

// TestLLMVerbResetRecoversMangledCatalog pins the recovery verb: a corrupt catalog fails a load, and
// --reset restores both files from the embedded defaults so the next load succeeds.
func TestLLMVerbResetRecoversMangledCatalog(t *testing.T) {
	runner, configDir := llmTestRunner(t, llmBaseConfig)
	if err := os.WriteFile(filepath.Join(configDir, "provider_catalog.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := llm.NewRegistry(configDir); err == nil {
		t.Fatal("the mangled catalog must fail the load before the reset")
	}
	if err := runner.ExecuteCommand("llm", []string{"--reset"}); err != nil {
		t.Fatalf("--reset: %v", err)
	}
	if _, err := llm.NewRegistry(configDir); err != nil {
		t.Errorf("catalogs must load after --reset: %v", err)
	}
}

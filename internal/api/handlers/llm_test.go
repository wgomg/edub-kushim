package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/llm"
)

// TestListModels_CapabilityContract pins the keys the catalog view exposes: clients read `disable_reasoning`
// (the catalog's two-state reasoning flag), not the renamed-away `supports_reasoning`, and `overridden` marks
// a model whose entry came from the config directory's catalog; the capability wiring is checked through
// the handler rather than the struct literal.
func TestListModels_CapabilityContract(t *testing.T) {
	providers := `[
		{"provider":"prov","adapter":"openai-compatible","official_url":"https://example.test/v1"}
	]`
	models := `[
		{"provider":"prov","model_id":"m1","display_name":"M1","max_input_tokens":1000,"max_output_tokens":100,"disable_reasoning":true,"reasoning_efforts":["high"],"supports_structured_output":false,"supports_temperature":false,"supports_prompt_caching":false},
		{"provider":"prov","model_id":"m2","display_name":"M2","max_input_tokens":1000,"max_output_tokens":100,"supports_structured_output":false,"supports_temperature":false,"supports_prompt_caching":false},
		{"provider":"openai","model_id":"gpt-5.6-sol","display_name":"User Sol","max_input_tokens":1000,"max_output_tokens":1234,"disable_reasoning":true,"reasoning_efforts":["low"],"supports_structured_output":true,"supports_temperature":true,"supports_prompt_caching":false}
	]`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "provider_catalog.json"), []byte(providers), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model_catalog.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := llm.NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	rec := httptest.NewRecorder()
	NewLlmHandler(reg).ListModels(rec, httptest.NewRequest(http.MethodGet, "/api/v1/llm/models", nil))

	var resp struct {
		Providers map[string][]struct {
			ID           string         `json:"id"`
			Overridden   bool           `json:"overridden"`
			Capabilities map[string]any `json:"capabilities"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	byID := make(map[string]map[string]any, len(resp.Providers["prov"]))
	for _, entry := range resp.Providers["prov"] {
		byID[entry.ID] = entry.Capabilities
	}
	m1, ok := byID["m1"]
	if !ok {
		t.Fatalf("model m1 missing from %v", byID)
	}
	if m1["disable_reasoning"] != true {
		t.Errorf("m1 disable_reasoning = %v, want true", m1["disable_reasoning"])
	}
	if efforts, _ := m1["reasoning_efforts"].([]any); len(efforts) != 1 || efforts[0] != "high" {
		t.Errorf("m1 reasoning_efforts = %v, want [high]", m1["reasoning_efforts"])
	}
	if _, present := m1["supports_reasoning"]; present {
		t.Error("supports_reasoning still emitted, want the renamed disable_reasoning key")
	}

	m2, ok := byID["m2"]
	if !ok {
		t.Fatalf("model m2 missing from %v", byID)
	}
	if m2["disable_reasoning"] != false {
		t.Errorf("m2 disable_reasoning = %v, want false for a model with no disable flag", m2["disable_reasoning"])
	}

	provFlags := make(map[string]bool, len(resp.Providers["prov"]))
	for _, entry := range resp.Providers["prov"] {
		provFlags[entry.ID] = entry.Overridden
	}
	if provFlags["m1"] {
		t.Error("a user model that does not duplicate a default must not carry overridden=true")
	}
	var solFound bool
	for _, entry := range resp.Providers["openai"] {
		if entry.ID != "gpt-5.6-sol" {
			continue
		}
		solFound = true
		if !entry.Overridden {
			t.Error("a model duplicated by the user catalog must carry overridden=true on the wire")
		}
	}
	if !solFound {
		t.Error("duplicated embedded model gpt-5.6-sol missing from the response")
	}
}

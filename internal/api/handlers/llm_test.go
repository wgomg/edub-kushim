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

// TestListModels_CapabilityContract pins the keys the catalog view exposes:
// clients read `disable_reasoning` (the catalog's two-state reasoning flag),
// not the renamed-away `supports_reasoning`, and the capability wiring is
// checked through the handler rather than the struct literal.
func TestListModels_CapabilityContract(t *testing.T) {
	catalog := `[
		{"provider":"prov","adapter":"openai-compatible","model_id":"m1","display_name":"M1","official_url":"https://example.test/v1","max_input_tokens":1000,"max_output_tokens":100,"disable_reasoning":true,"reasoning_efforts":["high"],"supports_structured_output":false,"supports_temperature":false,"supports_prompt_caching":false},
		{"provider":"prov","adapter":"openai-compatible","model_id":"m2","display_name":"M2","official_url":"https://example.test/v1","max_input_tokens":1000,"max_output_tokens":100,"supports_structured_output":false,"supports_temperature":false,"supports_prompt_caching":false}
	]`
	path := filepath.Join(t.TempDir(), "model_catalog.json")
	if err := os.WriteFile(path, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := llm.NewRegistry(path)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	rec := httptest.NewRecorder()
	NewLlmHandler(reg).ListModels(rec, httptest.NewRequest(http.MethodGet, "/api/v1/llm/models", nil))

	var resp struct {
		Providers map[string][]struct {
			ID           string         `json:"id"`
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
}

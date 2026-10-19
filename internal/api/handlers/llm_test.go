package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/log"
)

// TestListModels_CapabilityContract pins the keys the catalog view exposes: clients read `disable_reasoning`
// (the catalog's two-state reasoning flag), not the renamed-away `supports_reasoning`, and the on-disk
// catalog is the whole catalog surfaced through the handler; the capability wiring is checked through
// the handler rather than the struct literal.
func TestListModels_CapabilityContract(t *testing.T) {
	providers := `{"version":1,"providers":[
		{"provider":"prov","adapter":"openai-compatible","official_url":"https://example.test/v1"}
	]}`
	models := `{"version":1,"models":[
		{"provider":"prov","model_id":"m1","display_name":"M1","max_input_tokens":1000,"max_output_tokens":100,"disable_reasoning":true,"reasoning_efforts":["high"],"supports_structured_output":false,"supports_temperature":false,"supports_prompt_caching":false},
		{"provider":"prov","model_id":"m2","display_name":"M2","max_input_tokens":1000,"max_output_tokens":100,"supports_structured_output":false,"supports_temperature":false,"supports_prompt_caching":false}
	]}`
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
	handler := NewLlmHandler(reg, dir, func() *config.Config { return nil }, log.NewLogger("silent"))
	handler.ListModels(rec, httptest.NewRequest(http.MethodGet, "/api/v1/llm/models", nil))

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
	if len(resp.Providers) != 1 {
		t.Fatalf("providers = %v, want only the on-disk provider", resp.Providers)
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

// TestCatalogMutationEndpoints pins the status contract of the catalog CRUD: a valid write answers with
// the updated catalog, a duplicate conflicts, an unknown entry is not found, and invalid input is rejected
// before anything is written.
func TestCatalogMutationEndpoints(t *testing.T) {
	dir := t.TempDir()
	reg, err := llm.NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	handler := NewLlmHandler(reg, dir, func() *config.Config { return nil }, log.NewLogger("silent"))

	rec := httptest.NewRecorder()
	handler.AddProvider(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers",
		strings.NewReader(`{"provider":"zhipu","adapter":"openai-compatible","official_url":"https://z.test"}`)))
	if rec.Code != http.StatusConflict {
		t.Errorf("duplicate provider: status = %d, want 409", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.AddProvider(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers",
		strings.NewReader(`{"provider":"bad.name","adapter":"openai-compatible","official_url":"https://x.test"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid provider name: status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.AddProvider(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers",
		strings.NewReader(`{"provider":"groq","adapter":"openai-compatible","official_url":"https://api.groq.com/openai/v1"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("add provider: status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "groq") {
		t.Errorf("add response does not carry the updated catalog: %s", rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/llm/providers/nope",
		strings.NewReader(`{"adapter":"anthropic","official_url":"https://x.test"}`))
	req.SetPathValue("name", "nope")
	rec = httptest.NewRecorder()
	handler.SetProvider(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("set an unknown provider: status = %d, want 404", rec.Code)
	}
}

// TestRemoveModelInUseConflict pins the enricher reference guard at the endpoint: a referenced model is
// answered as a conflict whose message names the condition.
func TestRemoveModelInUseConflict(t *testing.T) {
	dir := t.TempDir()
	reg, err := llm.NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	cfg := &config.Config{Enricher: config.EnricherConfig{ContentAnalyzer: config.ContentAnalyzerConfig{
		Llm: config.LlmConfig{Provider: "zhipu", Model: "glm-5"},
	}}}
	handler := NewLlmHandler(reg, dir, func() *config.Config { return cfg }, log.NewLogger("silent"))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/llm/models/zhipu/glm-5", nil)
	req.SetPathValue("provider", "zhipu")
	req.SetPathValue("model", "glm-5")
	rec := httptest.NewRecorder()
	handler.RemoveModel(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("remove a referenced model: status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "in use") {
		t.Errorf("body = %s, want the in-use message", rec.Body.String())
	}
}

// TestNilRegistryRecoversThroughReset pins the recovery path: with no loaded registry the catalog reads
// answer 503, and a reset installs a fresh registry so the reads serve again without a restart.
func TestNilRegistryRecoversThroughReset(t *testing.T) {
	handler := NewLlmHandler(nil, t.TempDir(), func() *config.Config { return nil }, log.NewLogger("silent"))

	rec := httptest.NewRecorder()
	handler.GetCatalog(rec, httptest.NewRequest(http.MethodGet, "/api/v1/llm/catalog", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("get with no registry: status = %d, want 503", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ResetCatalogs(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/reset",
		strings.NewReader(`{"providers":true,"models":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.GetCatalog(rec, httptest.NewRequest(http.MethodGet, "/api/v1/llm/catalog", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("get after reset: status = %d body = %s, want 200 without a restart", rec.Code, rec.Body.String())
	}
}

package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/types"
)

// judgedFixture serves adapter traffic through an httptest server by
// cataloging a test provider whose official URL points at it.
type judgedFixture struct {
	reg *llm.Registry
}

func newJudgedFixture(t *testing.T, respond http.HandlerFunc) *judgedFixture {
	t.Helper()

	srv := httptest.NewServer(respond)
	t.Cleanup(srv.Close)

	catalog := fmt.Sprintf(
		`[{"provider":"testprovider","adapter":"openai-compatible","model_id":"test-model","display_name":"Test Model","official_url":%q,"max_input_tokens":100000,"max_output_tokens":4096,"reasoning":false,"supports_structured_output":true,"supports_temperature":true}]`,
		srv.URL,
	)
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := llm.NewRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	return &judgedFixture{reg: reg}
}

func (f *judgedFixture) catalogedCaps(t *testing.T) *llm.ModelCapability {
	t.Helper()
	caps := f.reg.Lookup("testprovider", "test-model")
	if caps == nil {
		t.Fatal("test model missing from fixture catalog")
	}
	return caps
}

func (f *judgedFixture) openAI(t *testing.T, caps *llm.ModelCapability) *LlmOpenAiCompatible {
	t.Helper()
	a, err := NewLlmOpenAiCompatible(nil, config.ToolConfig{}, &config.LlmConfig{
		Provider: "testprovider",
		Model:    "test-model",
	}, caps, "", 0, 0, f.reg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *judgedFixture) anthropic(t *testing.T, caps *llm.ModelCapability) *LlmAnthropic {
	t.Helper()
	a, err := NewLlmAnthropic(nil, config.ToolConfig{}, &config.LlmConfig{
		Provider: "testprovider",
		Model:    "test-model",
	}, caps, "", 0, 0, f.reg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func writeTestJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func testPairs() []TagPairEvidence {
	return []TagPairEvidence{{Query: "urban planning", Target: "city planning", Sim: 0.92}}
}

const fencedVerdictResponse = "```json\n{\"pairs\":[{\"index\":0,\"verdict\":\"same\",\"direction\":\"query\"}]}\n```"

func TestJudgedOpenAI_EnvelopeAndFencedParse(t *testing.T) {
	gotReq := make(chan []byte, 1)
	f := newJudgedFixture(t, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		} else {
			gotReq <- raw
		}
		writeTestJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": fencedVerdictResponse}}},
		})
	})

	results, err := f.openAI(t, f.catalogedCaps(t)).Adjudicate(context.Background(), testPairs())
	if err != nil {
		t.Fatalf("Adjudicate() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Verdict != types.TagVerdicts.Same {
		t.Errorf("Verdict = %q, want %q", results[0].Verdict, types.TagVerdicts.Same)
	}
	if results[0].Direction != TagDirectionQuery {
		t.Errorf("Direction = %q, want %q", results[0].Direction, TagDirectionQuery)
	}

	var body map[string]any
	if err := json.Unmarshal(<-gotReq, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "test-model" {
		t.Errorf("model = %v, want test-model", body["model"])
	}
	if body["stream"] != false {
		t.Errorf("stream = %v, want false", body["stream"])
	}
	if body["temperature"] != float64(0) {
		t.Errorf("temperature = %v, want 0", body["temperature"])
	}
	if body["top_p"] != float64(1) {
		t.Errorf("top_p = %v, want 1", body["top_p"])
	}
	if rf, ok := body["response_format"].(map[string]any); !ok || rf["type"] != "json_object" {
		t.Errorf("response_format = %v, want json_object", body["response_format"])
	}
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %v, want two entries", body["messages"])
	}
	sysMsg, _ := msgs[0].(map[string]any)
	if sysMsg["role"] != "system" || sysMsg["content"] != AdjudicationSystemMessage {
		t.Errorf("system message = %v, want %q", sysMsg, AdjudicationSystemMessage)
	}
	userMsg, _ := msgs[1].(map[string]any)
	if userMsg["role"] != "user" {
		t.Errorf("second message role = %v, want user", userMsg["role"])
	}
}

func TestJudgedOpenAI_OmitsUnsupportedFields(t *testing.T) {
	gotReq := make(chan []byte, 1)
	f := newJudgedFixture(t, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		} else {
			gotReq <- raw
		}
		writeTestJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": fencedVerdictResponse}}},
		})
	})

	// Caps without temperature or structured-output support must strip the
	// gated keys while the base body stays intact.
	a := f.openAI(t, &llm.ModelCapability{MaxInputTokens: 100000})
	if _, err := a.Adjudicate(context.Background(), testPairs()); err != nil {
		t.Fatalf("Adjudicate() error = %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(<-gotReq, &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"temperature", "top_p", "response_format"} {
		if _, present := body[key]; present {
			t.Errorf("%s sent without capability support", key)
		}
	}
	if body["model"] != "test-model" {
		t.Errorf("model = %v, want test-model", body["model"])
	}
	if _, present := body["messages"]; !present {
		t.Error("messages missing from base body")
	}
}

func TestJudgedOpenAI_StepErrorLabels(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	caps := f.catalogedCaps(t)

	tests := []struct {
		name  string
		label string
		call  func(a *LlmOpenAiCompatible) error
	}{
		{"adjudicate", "tag adjudication", func(a *LlmOpenAiCompatible) error {
			_, err := a.Adjudicate(context.Background(), testPairs())
			return err
		}},
		{"adjudicate tag splits", "tag split adjudication", func(a *LlmOpenAiCompatible) error {
			_, err := a.AdjudicateTagSplits(context.Background(), []string{"climate change"})
			return err
		}},
		{"judge tag quality", "tag quality judge", func(a *LlmOpenAiCompatible) error {
			_, err := a.JudgeTagQuality(context.Background(), "document text", []TagQualityCandidate{{Index: 0, Term: "test", Category: TagCategoryTopic}})
			return err
		}},
		{"judge tag identity", "tag identity judge", func(a *LlmOpenAiCompatible) error {
			_, err := a.JudgeTagIdentity(context.Background(), []TagIdentityCandidate{{Index: 0, Term: "test"}})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(f.openAI(t, caps))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.HasPrefix(err.Error(), tt.label+": ") {
				t.Errorf("error = %q, want prefix %q", err.Error(), tt.label+": ")
			}
		})
	}
}

func TestJudgedOpenAI_EmptyResponse(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": ""}}},
		})
	})

	_, err := f.openAI(t, f.catalogedCaps(t)).Adjudicate(context.Background(), testPairs())
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "empty response from LLM" {
		t.Errorf("error = %q, want %q", err.Error(), "empty response from LLM")
	}
}

func TestJudgedOpenAI_ContentGateBeforeRequest(t *testing.T) {
	var hits atomic.Int32
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	a := f.openAI(t, &llm.ModelCapability{MaxInputTokens: 1})
	_, err := a.Adjudicate(context.Background(), testPairs())
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := errors.AsType[*ContentTooLargeError](err); !ok {
		t.Errorf("error type = %T, want *ContentTooLargeError", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("requests sent = %d, want 0", n)
	}
}

func TestJudgedAnthropic_EnvelopeAndFencedParse(t *testing.T) {
	gotReq := make(chan []byte, 1)
	f := newJudgedFixture(t, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		} else {
			gotReq <- raw
		}
		writeTestJSON(w, http.StatusOK, map[string]any{
			"content": []any{map[string]any{"type": "text", "text": fencedVerdictResponse}},
		})
	})

	results, err := f.anthropic(t, f.catalogedCaps(t)).Adjudicate(context.Background(), testPairs())
	if err != nil {
		t.Fatalf("Adjudicate() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Verdict != types.TagVerdicts.Same {
		t.Errorf("Verdict = %q, want %q", results[0].Verdict, types.TagVerdicts.Same)
	}
	if results[0].Direction != TagDirectionQuery {
		t.Errorf("Direction = %q, want %q", results[0].Direction, TagDirectionQuery)
	}

	var req anthropicRequest
	if err := json.Unmarshal(<-gotReq, &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "test-model" {
		t.Errorf("model = %q, want test-model", req.Model)
	}
	if req.MaxTokens != 4096 {
		t.Errorf("max_tokens = %d, want the cataloged 4096", req.MaxTokens)
	}
	if req.System != AdjudicationSystemMessage {
		t.Errorf("system = %q, want %q", req.System, AdjudicationSystemMessage)
	}
	if req.Stream {
		t.Error("stream = true, want false")
	}
	if req.CacheControl == nil || req.CacheControl.Type != "ephemeral" {
		t.Errorf("cache_control = %+v, want ephemeral", req.CacheControl)
	}
	if req.Temperature == nil || *req.Temperature != 0 {
		t.Errorf("temperature = %v, want 0", req.Temperature)
	}
	if req.Thinking == nil || req.Thinking.Type != "disabled" {
		t.Errorf("thinking = %+v, want disabled for a non-reasoning config", req.Thinking)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content == "" {
		t.Errorf("messages = %+v, want one non-empty user message", req.Messages)
	}
}

func TestJudgedAnthropic_StepErrorLabels(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	caps := f.catalogedCaps(t)

	tests := []struct {
		name  string
		label string
		call  func(a *LlmAnthropic) error
	}{
		{"adjudicate", "tag adjudication", func(a *LlmAnthropic) error {
			_, err := a.Adjudicate(context.Background(), testPairs())
			return err
		}},
		{"adjudicate tag splits", "tag split adjudication", func(a *LlmAnthropic) error {
			_, err := a.AdjudicateTagSplits(context.Background(), []string{"climate change"})
			return err
		}},
		{"judge tag quality", "tag quality judge", func(a *LlmAnthropic) error {
			_, err := a.JudgeTagQuality(context.Background(), "document text", []TagQualityCandidate{{Index: 0, Term: "test", Category: TagCategoryTopic}})
			return err
		}},
		{"judge tag identity", "tag identity judge", func(a *LlmAnthropic) error {
			_, err := a.JudgeTagIdentity(context.Background(), []TagIdentityCandidate{{Index: 0, Term: "test"}})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(f.anthropic(t, caps))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.HasPrefix(err.Error(), tt.label+": ") {
				t.Errorf("error = %q, want prefix %q", err.Error(), tt.label+": ")
			}
		})
	}
}

func TestJudgedAnthropic_ContentGateBeforeRequest(t *testing.T) {
	var hits atomic.Int32
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	a := f.anthropic(t, &llm.ModelCapability{MaxInputTokens: 1})
	_, err := a.Adjudicate(context.Background(), testPairs())
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := errors.AsType[*ContentTooLargeError](err); !ok {
		t.Errorf("error type = %T, want *ContentTooLargeError", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("requests sent = %d, want 0", n)
	}
}

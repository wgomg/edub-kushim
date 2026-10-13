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
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// judgedFixture serves adapter traffic through an httptest server by
// cataloging a test provider whose official URL points at it.
type judgedFixture struct {
	reg *llm.Registry
}

// newFixtureWithCatalog lets a test choose the cataloged capabilities of the
// testprovider entry; newJudgedFixture supplies the default shape.
func newFixtureWithCatalog(t *testing.T, capsJSON string, respond http.HandlerFunc) *judgedFixture {
	t.Helper()

	srv := httptest.NewServer(respond)
	t.Cleanup(srv.Close)

	catalog := fmt.Sprintf(
		`[{"provider":"testprovider","adapter":"openai-compatible","model_id":"test-model","display_name":"Test Model","official_url":%q,%s}]`,
		srv.URL, capsJSON,
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

func newJudgedFixture(t *testing.T, respond http.HandlerFunc) *judgedFixture {
	t.Helper()
	return newFixtureWithCatalog(t,
		`"max_input_tokens":100000,"max_output_tokens":4096,"disable_reasoning":false,"supports_structured_output":true,"supports_temperature":true`,
		respond)
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
	a, err := NewLlmOpenAiCompatible(log.NewDiscardLogger(), config.ToolConfig{}, &config.LlmConfig{
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
	a, err := NewLlmAnthropic(log.NewDiscardLogger(), config.ToolConfig{}, &config.LlmConfig{
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
	return []TagPairEvidence{{Query: "urban planning", Target: "city planning"}}
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
		{"judge tag parts", "tag part judge", func(a *LlmOpenAiCompatible) error {
			_, err := a.JudgeTagParts(context.Background(), []string{"russia"})
			return err
		}},
		{"judge tag compounds", "tag compound judge", func(a *LlmOpenAiCompatible) error {
			_, err := a.JudgeTagCompounds(context.Background(), []string{"climate change"})
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
	if req.MaxTokens != 640 {
		t.Errorf("max_tokens = %d, want the judged budget 640", req.MaxTokens)
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
		{"judge tag parts", "tag part judge", func(a *LlmAnthropic) error {
			_, err := a.JudgeTagParts(context.Background(), []string{"russia"})
			return err
		}},
		{"judge tag compounds", "tag compound judge", func(a *LlmAnthropic) error {
			_, err := a.JudgeTagCompounds(context.Background(), []string{"climate change"})
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

func TestJudgedOpenAI_BareArrayReply(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": `[{"index":0,"verdict":"same","direction":"query"}]`}}},
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
}

func TestJudgedOpenAI_ParseErrorCarriesModelAndRaw(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "not json"}}},
		})
	})

	_, err := f.openAI(t, f.catalogedCaps(t)).Adjudicate(context.Background(), testPairs())
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"tag adjudication", "test-model", "raw: not json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestJudgedAnthropic_BareArrayReply(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"content": []any{map[string]any{"type": "text", "text": `[{"index":0,"verdict":"same","direction":"query"}]`}},
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
}

func TestJudgedAnthropic_ParseErrorCarriesModelAndRaw(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "not json"}},
		})
	})

	_, err := f.anthropic(t, f.catalogedCaps(t)).Adjudicate(context.Background(), testPairs())
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"tag adjudication", "test-model", "raw: not json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want substring %q", err.Error(), want)
		}
	}
}

// partialQualityResponse answers the first of two candidates: the reply is
// valid JSON with a hole, the omission class a capped model produces.
const partialQualityResponse = `{"tags":[{"index":0,"subject_matter":true,"dimension":true,"not_person":true,"atomic":true,"not_form_genre":true,"defer":false}]}`

func twoQualityCandidates() []TagQualityCandidate {
	return []TagQualityCandidate{
		{Index: 0, Term: "wave mechanics", Category: TagCategoryTopic},
		{Index: 1, Term: "spin lattice", Category: TagCategoryTopic},
	}
}

func assertSalvagedQualityReply(t *testing.T, results []TagQualityResult, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("an incomplete reply must still report its missing verdict")
	}
	if !strings.HasPrefix(err.Error(), "tag quality judge: ") {
		t.Errorf("error = %q, want the step label prefix", err.Error())
	}
	incomplete, ok := errors.AsType[*IncompleteReplyError](err)
	if !ok {
		t.Fatalf("error type = %T, want *IncompleteReplyError", err)
	}
	if len(incomplete.Missing) != 1 || incomplete.Missing[0] != 1 {
		t.Errorf("Missing = %v, want [1]", incomplete.Missing)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want the 2 entries the parser read", len(results))
	}
	if !results[0].SubjectMatter || !results[0].NotFormGenre || results[0].Defer {
		t.Errorf("index 0 answers = %+v, want the parsed verdict kept", results[0])
	}
	if results[1].SubjectMatter || results[1].Dimension || results[1].Atomic {
		t.Errorf("index 1 answers = %+v, want the missing entry zero-valued", results[1])
	}
}

func TestJudgedOpenAI_PartialQualityReplyKeepsResults(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": partialQualityResponse}}},
		})
	})

	results, err := f.openAI(t, f.catalogedCaps(t)).JudgeTagQuality(context.Background(), "document text", twoQualityCandidates())

	assertSalvagedQualityReply(t, results, err)
}

func TestJudgedAnthropic_PartialQualityReplyKeepsResults(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"content": []any{map[string]any{"type": "text", "text": partialQualityResponse}},
		})
	})

	results, err := f.anthropic(t, f.catalogedCaps(t)).JudgeTagQuality(context.Background(), "document text", twoQualityCandidates())

	assertSalvagedQualityReply(t, results, err)
}

const analysisWireResponse = `{"title":"sample","type":"article","tags":["tag one"],"people":[],"language":"eng"}`

func TestJudgedOpenAI_OutputBudget(t *testing.T) {
	tests := []struct {
		name      string
		caps      *llm.ModelCapability
		wantField string
		wantOther string
		want      float64
	}{
		{"cataloged model sends the judged budget", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 4096}, "max_tokens", "max_completion_tokens", 640},
		{"budget clamps to the cataloged maximum", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 300}, "max_tokens", "max_completion_tokens", 300},
		{"catalog entry without an output ceiling keeps the budget", &llm.ModelCapability{MaxInputTokens: 100000}, "max_tokens", "max_completion_tokens", 640},
		{"uncataloged model keeps the budget", nil, "max_tokens", "max_completion_tokens", 640},
		{"completion-token capability swaps the field", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 4096, UsesMaxCompletionTokens: true}, "max_completion_tokens", "max_tokens", 640},
		{"model that cannot disable reasoning doubles the budget", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 4096, ReasoningEffortLevels: []string{"low"}}, "max_tokens", "max_completion_tokens", 1280},
		{"disableable reasoning model keeps the budget", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 4096, CanDisableReasoning: true, ReasoningEffortLevels: []string{"high"}}, "max_tokens", "max_completion_tokens", 640},
		{"doubled budget still clamps to the cataloged maximum", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 1000, ReasoningEffortLevels: []string{"low"}}, "max_tokens", "max_completion_tokens", 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			if _, err := f.openAI(t, tt.caps).Adjudicate(context.Background(), testPairs()); err != nil {
				t.Fatalf("Adjudicate() error = %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(<-gotReq, &body); err != nil {
				t.Fatal(err)
			}
			if body[tt.wantField] != tt.want {
				t.Errorf("%s = %v, want %v", tt.wantField, body[tt.wantField], tt.want)
			}
			if _, present := body[tt.wantOther]; present {
				t.Errorf("%s sent alongside %s", tt.wantOther, tt.wantField)
			}
		})
	}
}

func TestOpenAI_AnalyzeOutputBudget(t *testing.T) {
	tests := []struct {
		name string
		caps *llm.ModelCapability
		want float64
	}{
		{"cataloged model gets the analysis budget", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 4096}, 4096},
		{"analysis budget clamps to the cataloged maximum", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 2048}, 2048},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReq := make(chan []byte, 1)
			f := newJudgedFixture(t, func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request: %v", err)
				} else {
					gotReq <- raw
				}
				writeTestJSON(w, http.StatusOK, map[string]any{
					"choices": []any{map[string]any{"message": map[string]any{"content": analysisWireResponse}}},
				})
			})

			if _, err := f.openAI(t, tt.caps).Analyze(context.Background(), "text", nil, nil, nil); err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(<-gotReq, &body); err != nil {
				t.Fatal(err)
			}
			if body["max_tokens"] != tt.want {
				t.Errorf("max_tokens = %v, want %v", body["max_tokens"], tt.want)
			}
		})
	}
}

func TestJudgedAnthropic_OutputBudget(t *testing.T) {
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

	// the cataloged 640-token judged budget is asserted by the envelope test;
	// this covers the clamp against a smaller cataloged maximum
	a := f.anthropic(t, &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 300})
	if _, err := a.Adjudicate(context.Background(), testPairs()); err != nil {
		t.Fatalf("Adjudicate() error = %v", err)
	}
	var req anthropicRequest
	if err := json.Unmarshal(<-gotReq, &req); err != nil {
		t.Fatal(err)
	}
	if req.MaxTokens != 300 {
		t.Errorf("max_tokens = %d, want the clamp to 300", req.MaxTokens)
	}
}

func TestAnthropic_AnalyzeOutputBudget(t *testing.T) {
	tests := []struct {
		name string
		caps *llm.ModelCapability
		want int
	}{
		{"cataloged model gets the analysis budget", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 4096}, 4096},
		{"analysis budget clamps to the cataloged maximum", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 2048}, 2048},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReq := make(chan []byte, 1)
			f := newJudgedFixture(t, func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request: %v", err)
				} else {
					gotReq <- raw
				}
				writeTestJSON(w, http.StatusOK, map[string]any{
					"content": []any{map[string]any{"type": "text", "text": analysisWireResponse}},
				})
			})

			if _, err := f.anthropic(t, tt.caps).Analyze(context.Background(), "text", nil, nil, nil); err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}
			var req anthropicRequest
			if err := json.Unmarshal(<-gotReq, &req); err != nil {
				t.Fatal(err)
			}
			if req.MaxTokens != tt.want {
				t.Errorf("max_tokens = %d, want %d", req.MaxTokens, tt.want)
			}
		})
	}
}

func TestJudgedOpenAI_OutputCapError(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": `{"pairs":[]}`},
				"finish_reason": "length",
			}},
		})
	})

	_, err := f.openAI(t, f.catalogedCaps(t)).Adjudicate(context.Background(), testPairs())
	if err == nil {
		t.Fatal("expected an output-cap error")
	}
	capErr, ok := errors.AsType[*OutputCapError](err)
	if !ok {
		t.Fatalf("error type = %T, want *OutputCapError", err)
	}
	if capErr.Cap != 640 {
		t.Errorf("Cap = %d, want the 640-token judged budget", capErr.Cap)
	}
	if !strings.HasPrefix(err.Error(), "tag adjudication: ") {
		t.Errorf("error = %q, want the step label prefix", err.Error())
	}
}

func TestJudgedOpenAI_ReplyParseErrorIsBounded(t *testing.T) {
	longRaw := strings.Repeat("x", 3000) + "ENDMARKER"
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": longRaw}}},
		})
	})

	_, err := f.openAI(t, f.catalogedCaps(t)).Adjudicate(context.Background(), testPairs())
	parseErr, ok := errors.AsType[*ReplyParseError](err)
	if !ok {
		t.Fatalf("error type = %T, want *ReplyParseError", err)
	}
	if parseErr.RawLen != len(longRaw) {
		t.Errorf("RawLen = %d, want %d", parseErr.RawLen, len(longRaw))
	}
	if len(parseErr.RawPrefix) != replyParsePrefixBytes {
		t.Errorf("len(RawPrefix) = %d, want %d", len(parseErr.RawPrefix), replyParsePrefixBytes)
	}
	if strings.Contains(err.Error(), "ENDMARKER") {
		t.Error("error carries the tail of a 3000-byte reply")
	}
}

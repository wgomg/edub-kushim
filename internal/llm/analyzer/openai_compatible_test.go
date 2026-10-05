package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/llm"
)

// responsesCapsJSON serves the testprovider model the way the opencode
// gateway serves its Responses-only models: reasoning cannot be disabled, the
// chat-only output-bound field is meaningless, and structured output travels
// under text.format.
const responsesCapsJSON = `"max_input_tokens":100000,"max_output_tokens":4096,"disable_reasoning":false,"reasoning_efforts":["minimal","low","medium","high","xhigh"],"supports_structured_output":true,"supports_temperature":true,"uses_max_completion_tokens":true,"uses_responses_endpoint":true`

// completedResponses wraps text in the shape the Responses dialect returns
// for a finished reply.
func completedResponses(text string) map[string]any {
	return map[string]any{
		"status": "completed",
		"output": []any{
			map[string]any{"type": "message", "role": "assistant", "content": []any{
				map[string]any{"type": "output_text", "text": text},
			}},
		},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 20, "total_tokens": 30},
	}
}

func TestResponses_Envelope(t *testing.T) {
	gotPath := make(chan string, 1)
	gotReq := make(chan []byte, 1)
	f := newFixtureWithCatalog(t, responsesCapsJSON, func(w http.ResponseWriter, r *http.Request) {
		gotPath <- r.URL.Path
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		} else {
			gotReq <- raw
		}
		writeTestJSON(w, http.StatusOK, completedResponses(fencedVerdictResponse))
	})

	if _, err := f.openAI(t, f.catalogedCaps(t)).Adjudicate(context.Background(), testPairs()); err != nil {
		t.Fatalf("Adjudicate() error = %v", err)
	}

	if path := <-gotPath; path != "/responses" {
		t.Errorf("path = %q, want /responses", path)
	}

	var body map[string]any
	if err := json.Unmarshal(<-gotReq, &body); err != nil {
		t.Fatal(err)
	}

	if body["instructions"] != AdjudicationSystemMessage {
		t.Errorf("instructions = %v, want the system message", body["instructions"])
	}
	input, ok := body["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %v, want the conversation turns only", body["input"])
	}
	turn, _ := input[0].(map[string]any)
	if turn["role"] != "user" || turn["content"] != BuildAdjudicationPrompt(testPairs()) {
		t.Errorf("input turn = %v, want the prompt as the sole user turn", turn)
	}

	for _, chatOnly := range []string{"messages", "stream", "response_format", "max_completion_tokens"} {
		if _, present := body[chatOnly]; present {
			t.Errorf("%s is a chat-completions field, sent on the Responses dialect", chatOnly)
		}
	}
	if _, present := body["max_output_tokens"]; !present {
		t.Error("max_output_tokens missing: the Responses dialect names the bound this way")
	}
	if body["store"] != false {
		t.Errorf("store = %v, want false: the pipeline replays its own context", body["store"])
	}

	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "minimal" {
		t.Errorf("reasoning = %v, want the lowest cataloged effort", body["reasoning"])
	}
	format, _ := body["text"].(map[string]any)
	if f, _ := format["format"].(map[string]any); f["type"] != "json_object" {
		t.Errorf("text.format = %v, want json_object", format)
	}
}

// TestResponses_FoldsOutputTextAndUsage pins the normalization contract:
// reasoning items are skipped, every output_text block is joined, and usage
// lands in the analysis stats the callers already read.
func TestResponses_FoldsOutputTextAndUsage(t *testing.T) {
	split := strings.Index(analysisWireResponse, `"type"`)
	part1, part2 := analysisWireResponse[:split], analysisWireResponse[split:]

	f := newFixtureWithCatalog(t, responsesCapsJSON, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"status": "completed",
			"output": []any{
				map[string]any{"type": "reasoning", "summary": []any{
					map[string]any{"type": "summary_text", "text": "no visible content"},
				}},
				map[string]any{"type": "message", "role": "assistant", "content": []any{
					map[string]any{"type": "output_text", "text": part1},
					map[string]any{"type": "output_text", "text": part2},
				}},
			},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 20, "total_tokens": 30},
		})
	})

	analysis, err := f.openAI(t, f.catalogedCaps(t)).Analyze(context.Background(), "text", nil, nil, nil)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.Title != "sample" || analysis.DocType != "article" {
		t.Errorf("analysis = %+v, want the joined output_text blocks to parse", analysis)
	}
	if analysis.Stats == nil {
		t.Fatal("Stats missing, want the Responses usage")
	}
	var usage tokenUsage
	if err := json.Unmarshal(*analysis.Stats, &usage); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 20 || usage.TotalTokens != 30 {
		t.Errorf("usage = %+v, want 10/20/30 from input_tokens/output_tokens/total_tokens", usage)
	}
}

func TestResponses_OutputCapError(t *testing.T) {
	f := newFixtureWithCatalog(t, responsesCapsJSON, func(w http.ResponseWriter, _ *http.Request) {
		payload := completedResponses(fencedVerdictResponse)
		payload["status"] = "incomplete"
		payload["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
		writeTestJSON(w, http.StatusOK, payload)
	})

	_, err := f.openAI(t, f.catalogedCaps(t)).Adjudicate(context.Background(), testPairs())
	capErr, ok := errors.AsType[*OutputCapError](err)
	if !ok {
		t.Fatalf("error type = %T, want *OutputCapError, got %v", err, err)
	}
	if capErr.Cap != 1280 {
		t.Errorf("Cap = %d, want the doubled judged budget", capErr.Cap)
	}
}

// TestOpenAI_ReasoningPolicy pins the catalog's two-state policy on the chat
// dialect for the three states a default configuration can reach.
func TestOpenAI_ReasoningPolicy(t *testing.T) {
	tests := []struct {
		name string
		caps *llm.ModelCapability
		want string // "" asserts the knob is absent
	}{
		{"no effort levels cataloged", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 4096}, ""},
		{"cannot disable reasoning runs at the lowest effort", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 4096, ReasoningEffortLevels: []string{"minimal", "low"}}, "minimal"},
		{"disableable model takes the disable form", &llm.ModelCapability{MaxInputTokens: 100000, MaxOutputTokens: 4096, CanDisableReasoning: true}, "none"},
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
			got, present := body["reasoning_effort"]
			if tt.want == "" {
				if present {
					t.Errorf("reasoning_effort = %v, want no reasoning knob", got)
				}
				return
			}
			if !present || got != tt.want {
				t.Errorf("reasoning_effort = %v, want %q", got, tt.want)
			}
		})
	}
}

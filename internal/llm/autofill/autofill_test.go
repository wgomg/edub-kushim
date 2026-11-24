package autofill

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func devTestServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testClient(devURL string) *Client {
	c := NewClient()
	c.modelsDevURL = devURL
	return c
}

func assertInt(t *testing.T, got *int, want int) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("got %v, want %d", got, want)
	}
}

func fieldSource(fields []FilledField, field string) string {
	for _, f := range fields {
		if f.Field == field {
			return f.Source
		}
	}
	return ""
}

func TestFillMergesEndpointAndModelsDev(t *testing.T) {
	dev := devTestServer(t, `{"openai":{"id":"openai","name":"OpenAI","models":{"gpt-test":{
		"id":"gpt-test","name":"GPT Test",
		"limit":{"context":100000,"input":80000,"output":16000},
		"capabilities":{"reasoning":{"supported":true,"toggle":true,"effort":["high","low","default","high"]},
			"cache":{"implicit":true},"structured_output":false,"temperature":false},
		"api":{"chat-completions":{"base_url":"https://api.openai.com/v1","sdk":{"aisdk":"@ai-sdk/openai"}}},
		"recommended_api":"chat-completions"}}}}`)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-test","display_name":"Endpoint Name",
			"context_window":200000,"max_output_tokens":32000,"effort":{"supported_levels":["low","high"]}}]}`))
	}))
	t.Cleanup(endpoint.Close)

	out, err := testClient(dev.URL).Fill(context.Background(), AutofillRequest{
		Provider: "openai", Adapter: "openai-compatible", OfficialURL: endpoint.URL, ModelID: "gpt-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Endpoint == nil || out.Endpoint.Status != statusOK {
		t.Fatalf("endpoint status = %+v, want ok", out.Endpoint)
	}
	if len(out.Models) != 1 || out.Models[0].DisplayName != "Endpoint Name" {
		t.Errorf("model list = %+v, want the endpoint's display name", out.Models)
	}
	if out.Model == nil {
		t.Fatal("no model suggestion")
	}
	if out.Model.DisplayName != "Endpoint Name" {
		t.Errorf("display_name = %q, want the endpoint value", out.Model.DisplayName)
	}
	assertInt(t, out.Model.MaxInputTokens, 200000)
	assertInt(t, out.Model.MaxOutputTokens, 32000)
	if out.Model.ReasoningEfforts == nil || !reflect.DeepEqual(*out.Model.ReasoningEfforts, []string{"low", "high"}) {
		t.Errorf("reasoning_efforts = %v, want sorted endpoint levels", out.Model.ReasoningEfforts)
	}
	if out.Model.DisableReasoning == nil || !*out.Model.DisableReasoning {
		t.Errorf("disable_reasoning = %v, want models.dev toggle", out.Model.DisableReasoning)
	}
	if out.Model.SupportsPromptCaching == nil || !*out.Model.SupportsPromptCaching {
		t.Errorf("supports_prompt_caching = %v, want models.dev cache", out.Model.SupportsPromptCaching)
	}
	if out.Model.UsesMaxCompletionTokens == nil || !*out.Model.UsesMaxCompletionTokens {
		t.Errorf("uses_max_completion_tokens = %v, want the OpenAI SDK default", out.Model.UsesMaxCompletionTokens)
	}
	if out.Model.UsesResponsesEndpoint == nil || *out.Model.UsesResponsesEndpoint {
		t.Errorf("uses_responses_endpoint = %v, want false for a chat-completions recommendation", out.Model.UsesResponsesEndpoint)
	}
	if got := fieldSource(out.Filled, "display_name"); got != sourceEndpoint {
		t.Errorf("display_name source = %q, want %q", got, sourceEndpoint)
	}
	if got := fieldSource(out.Filled, "supports_prompt_caching"); got != sourceModelsDev {
		t.Errorf("prompt caching source = %q, want %q", got, sourceModelsDev)
	}
}

func TestFillEndpointStatusesAndTokenHandling(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		location string
		want     string
	}{
		{"ok", http.StatusOK, `{"data":[]}`, "", statusOK},
		{"unauthorized", http.StatusUnauthorized, "", "", statusUnauthorized},
		{"not found", http.StatusNotFound, "", "", statusNotFound},
		{"redirect", http.StatusFound, "", "https://elsewhere.test/models", statusRedirected},
		{"invalid response", http.StatusOK, `{"unexpected":true}`, "", statusInvalidResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := ""
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.Header.Get("Authorization") + "|" + r.URL.Path
				if tt.location != "" {
					w.Header().Set("Location", tt.location)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer endpoint.Close()

			dev := devTestServer(t, `{}`)
			out, err := testClient(dev.URL).Fill(context.Background(), AutofillRequest{
				Provider: "unknown", OfficialURL: endpoint.URL, Token: "secret-token-xyz",
			})
			if err != nil {
				t.Fatal(err)
			}
			if out.Endpoint == nil || out.Endpoint.Status != tt.want {
				t.Fatalf("endpoint = %+v, want status %q", out.Endpoint, tt.want)
			}
			if seen != "Bearer secret-token-xyz|/models" {
				t.Errorf("probe auth/path = %q, want a bearer token on /models", seen)
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "secret-token-xyz") {
				t.Errorf("response leaked the probe token: %s", encoded)
			}
		})
	}
}

func TestFillProviderCandidates(t *testing.T) {
	dev := devTestServer(t, `{
		"opencode":{"id":"opencode","name":"OpenCode","models":{"one":{"id":"one",
			"api":{"chat-completions":{"base_url":"https://a.test/v1"}}}}},
		"opencode-go":{"id":"opencode-go","name":"OpenCode Go","models":{"two":{"id":"two",
			"api":{"chat-completions":{"base_url":"https://b.test/v1"}}}}}
	}`)
	client := testClient(dev.URL)

	ambiguous, err := client.Fill(context.Background(), AutofillRequest{Provider: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ambiguous.ProviderCandidates) != 2 || ambiguous.Provider != nil {
		t.Fatalf("ambiguous match = %+v, want two candidates and no provider", ambiguous)
	}

	chosen, err := client.Fill(context.Background(), AutofillRequest{
		Provider: "open", ModelsDevProvider: "opencode-go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if chosen.Provider == nil || chosen.Provider.OfficialURL != "https://b.test/v1" ||
		chosen.Provider.Adapter != adapterOpenAICompatible {
		t.Fatalf("chosen provider = %+v, want the opencode-go endpoint", chosen.Provider)
	}

	exact, err := client.Fill(context.Background(), AutofillRequest{Provider: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if exact.Provider == nil || exact.Provider.OfficialURL != "https://a.test/v1" || len(exact.ProviderCandidates) != 0 {
		t.Fatalf("exact match = %+v, want the opencode endpoint and no candidates", exact)
	}
}

func TestFillLegacyKnobAlias(t *testing.T) {
	dev := devTestServer(t, `{}`)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-test"}]}`))
	}))
	t.Cleanup(endpoint.Close)

	out, err := testClient(dev.URL).Fill(context.Background(), AutofillRequest{
		Provider: "qwen", OfficialURL: endpoint.URL, ModelID: "qwen-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Model == nil || out.Model.ReasoningToggle == nil {
		t.Fatalf("no legacy knob suggestion: %+v", out.Model)
	}
	toggle := out.Model.ReasoningToggle
	if toggle.Field != "enable_thinking" || toggle.On != true || toggle.Off != false {
		t.Errorf("legacy toggle = %+v, want enable_thinking true/false", toggle)
	}
	if got := fieldSource(out.Filled, "reasoning_toggle"); got != sourceDefaults {
		t.Errorf("reasoning_toggle source = %q, want %q", got, sourceDefaults)
	}
}

func TestFillUnknownModelsDevProvider(t *testing.T) {
	dev := devTestServer(t, `{}`)
	_, err := testClient(dev.URL).Fill(context.Background(), AutofillRequest{ModelsDevProvider: "missing"})
	if !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("Fill() error = %v, want ErrUnknownProvider", err)
	}
}

func TestSortEfforts(t *testing.T) {
	got := sortEfforts([]string{"default", "high", "low", "xhigh", "weird", "low"})
	want := []string{"low", "high", "xhigh", "weird"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sortEfforts() = %v, want %v", got, want)
	}
}

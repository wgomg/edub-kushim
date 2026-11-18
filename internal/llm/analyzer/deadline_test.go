package analyzer

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

func newOpenAIWithTimeout(t *testing.T, f *judgedFixture, tool config.ToolConfig, delay float64) *LlmOpenAiCompatible {
	t.Helper()
	a, err := NewLlmOpenAiCompatible(log.NewDiscardLogger(), tool, &config.LlmConfig{
		Provider:     "testprovider",
		Model:        "test-model",
		RequestDelay: delay,
	}, f.catalogedCaps(t), "", 0, f.reg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newAnthropicWithTimeout(t *testing.T, f *judgedFixture, tool config.ToolConfig, delay float64) *LlmAnthropic {
	t.Helper()
	a, err := NewLlmAnthropic(log.NewDiscardLogger(), tool, &config.LlmConfig{
		Provider:     "testprovider",
		Model:        "test-model",
		RequestDelay: delay,
	}, f.catalogedCaps(t), "", 0, f.reg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestDoRequest_DeadlineBoundsHTTPRoundTrip pins the ToolConfig deadline as the bound of the HTTP
// round trip: a handler that answers far later than the deadline must surface DeadlineExceeded, which
// the classifier maps to Abort instead of a retry. The handler sleeps a bounded span because a server
// blocked on the request context only learns of the client's disconnect between requests.
func TestDoRequest_DeadlineBoundsHTTPRoundTrip(t *testing.T) {
	f := newJudgedFixture(t, func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(400 * time.Millisecond)
	})

	assertDeadline := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("Adjudicate() error = nil, want the deadline to fire")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want context.DeadlineExceeded", err)
		}
	}

	t.Run("openai-compatible", func(t *testing.T) {
		a := newOpenAIWithTimeout(t, f, config.ToolConfig{Timeout: 100 * time.Millisecond}, 0)
		_, err := a.Adjudicate(context.Background(), testPairs())
		assertDeadline(t, err)
	})

	t.Run("anthropic", func(t *testing.T) {
		a := newAnthropicWithTimeout(t, f, config.ToolConfig{Timeout: 100 * time.Millisecond}, 0)
		_, err := a.Adjudicate(context.Background(), testPairs())
		assertDeadline(t, err)
	})
}

// TestDoRequest_DeadlineCoversBodyRead pins the body read inside the deadline: headers delivered
// before the deadline but a body that finishes far later must still surface DeadlineExceeded.
func TestDoRequest_DeadlineCoversBodyRead(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(400 * time.Millisecond)
	})
	a := newOpenAIWithTimeout(t, f, config.ToolConfig{Timeout: 100 * time.Millisecond}, 0)

	_, err := a.Adjudicate(context.Background(), testPairs())
	if err == nil {
		t.Fatal("Adjudicate() error = nil, want the deadline to fire during the body read")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
}

// TestDoRequest_PacingRunsOutsideTheDeadline pins the request_delay sleep outside the attempt
// deadline: a delay longer than the timeout must still run in full and the call must succeed.
func TestDoRequest_PacingRunsOutsideTheDeadline(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": fencedVerdictResponse}}},
		})
	})
	a := newOpenAIWithTimeout(t, f, config.ToolConfig{Timeout: 100 * time.Millisecond}, 0.25)

	start := time.Now()
	if _, err := a.Adjudicate(context.Background(), testPairs()); err != nil {
		t.Fatalf("Adjudicate() error = %v, want success with the pacing sleep outside the deadline", err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("elapsed = %v, want the full pacing sleep outside the deadline", elapsed)
	}
}

// TestDoRequest_ZeroTimeoutHasNoDeadline pins the zero value: no artificial deadline is installed, so a
// response slower than any accidental default still succeeds.
func TestDoRequest_ZeroTimeoutHasNoDeadline(t *testing.T) {
	f := newJudgedFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		writeTestJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": fencedVerdictResponse}}},
		})
	})
	a := newOpenAIWithTimeout(t, f, config.ToolConfig{}, 0)

	if _, err := a.Adjudicate(context.Background(), testPairs()); err != nil {
		t.Fatalf("Adjudicate() error = %v, want no artificial deadline", err)
	}
}

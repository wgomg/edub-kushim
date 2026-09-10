package tagmatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
)

func TestMaxMatchBodyBytes(t *testing.T) {
	tests := []struct {
		name              string
		reduceTargetWords int
		want              int
	}{
		{"negative returns ceiling", -1, maxBodyBytes},
		{"zero disables reduction, returns ceiling", 0, maxBodyBytes},
		{"small value clamps to floor", 10, minBodyBytes},
		{"default 4000 words clamps to floor", 4000, minBodyBytes},
		{"just above floor", 10923, 10923 * bytesPerWord},
		{"just below ceiling", 174762, 174762 * bytesPerWord},
		{"large value clamps to ceiling", 200000, maxBodyBytes},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaxMatchBodyBytes(tt.reduceTargetWords)
			if got != tt.want {
				t.Errorf("MaxMatchBodyBytes(%d) = %d, want %d", tt.reduceTargetWords, got, tt.want)
			}
		})
	}
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{"ascii under limit", "hello", 10, "hello"},
		{"ascii exactly at limit", "hello", 5, "hello"},
		{"ascii truncates cleanly", "hello world", 5, "hello"},
		{"cjk backs off to rune boundary", "你好世界", 5, "你"},
		{"cjk backs off one full rune", "你好世界", 6, "你好"},
		{"mixed ascii and cjk", "hi你好world", 7, "hi你"},
		{"mixed ascii and cjk at boundary", "hi你好world", 8, "hi你好"},
		{"empty string", "", 10, ""},
		{"zero max bytes", "hello", 0, ""},
		{"single rune ascii", "a", 1, "a"},
		{"single rune cjk exceeds limit", "好", 1, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateUTF8(tt.input, tt.maxBytes)
			if got != tt.want {
				t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tt.input, tt.maxBytes, got, tt.want)
			}
			if len(got) > tt.maxBytes {
				t.Errorf("result length %d exceeds maxBytes %d", len(got), tt.maxBytes)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result %q is not valid UTF-8", got)
			}
		})
	}
}

func TestTruncateUTF8Idempotent(t *testing.T) {
	input := strings.Repeat("hello ", 1000)
	truncated := truncateUTF8(input, 100)
	if truncated != truncateUTF8(truncated, 100) {
		t.Error("truncateUTF8 is not idempotent — truncating the result again should yield the same string")
	}
}

// newMatcherTestServer serves handler over a unix socket and returns the socket path.
func newMatcherTestServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "matcher.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return sockPath
}

func TestMatcherClient_Match(t *testing.T) {
	var gotDocID, gotInput string
	sockPath := newMatcherTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DocID string `json:"doc_id"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotDocID, gotInput = req.DocID, req.Input
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"matches":["alpha","beta"]}`)
	})

	c := NewMatcherClient(sockPath, MaxMatchBodyBytes(4000))
	defer c.Close()

	tags, err := c.Match(context.Background(), "doc-1", "some text")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !slices.Equal(tags, []string{"alpha", "beta"}) {
		t.Errorf("Match returned %v, want [alpha beta]", tags)
	}
	if gotDocID != "doc-1" || gotInput != "some text" {
		t.Errorf("server received doc_id=%q input=%q, want doc_id=%q input=%q", gotDocID, gotInput, "doc-1", "some text")
	}
}

func TestMatcherClient_ContextDeadlineFires(t *testing.T) {
	// handler holds the connection open without responding, like a wedged matcher
	sockPath := newMatcherTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	c := NewMatcherClient(sockPath, MaxMatchBodyBytes(4000))
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Match(ctx, "doc-1", "some text")
	if err == nil {
		t.Fatal("expected error when matcher never responds")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	// the call must be bounded by the context deadline, not hang
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Match took %v, want bounded by the 200ms context deadline", elapsed)
	}
}

// rankHandler returns an http.HandlerFunc that serves /rpc/v1/rank (200) and
// /rpc/v1/consolidate (the response named in consolidateBody). This lets one
// test exercise both the happy path and the 404-fallback path by configuring
// the rank response or omitting it.
func rankHandler(rankBody, consolidateBody string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rpc/v1/rank":
			if rankBody == "" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			fmt.Fprint(w, rankBody)
		case "/rpc/v1/consolidate":
			fmt.Fprint(w, consolidateBody)
		default:
			http.NotFound(w, r)
		}
	}
}

func TestMatcherClient_Rank_ServesRankEndpoint(t *testing.T) {
	want := []tagmatcher.RankResult{
		{KeptName: "a", Candidates: []tagmatcher.Candidate{{Tag: "x", Similarity: 0.91}}},
		{KeptName: "b"},
	}
	body, err := json.Marshal(struct {
		Results []tagmatcher.RankResult `json:"results"`
	}{Results: want})
	if err != nil {
		t.Fatal(err)
	}
	sockPath := newMatcherTestServer(t, rankHandler(string(body), ""))

	c := NewMatcherClient(sockPath, MaxMatchBodyBytes(4000))
	defer c.Close()

	got, err := c.Rank(context.Background(), "doc-1", []string{"a", "b"})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Rank returned %d results, want %d", len(got), len(want))
	}
	if got[0].KeptName != want[0].KeptName || len(got[0].Candidates) != 1 ||
		got[0].Candidates[0].Tag != "x" || got[0].Candidates[0].Similarity != 0.91 {
		t.Errorf("Rank result[0] = %+v, want kept=a candidates=[{x 0.91}]", got[0])
	}
	if got[1].KeptName != "b" || len(got[1].Candidates) != 0 {
		t.Errorf("Rank result[1] = %+v, want kept=b no candidates", got[1])
	}
}

func TestMatcherClient_Rank_FallsBackToConsolidateOnNotFound(t *testing.T) {
	// /rpc/v1/rank returns 404 (old daemon); /rpc/v1/consolidate returns final names.
	// The client must wrap each name in a RankResult with empty candidates so the
	// enricher's applyConsolidationPolicy keeps the name verbatim.
	consolidateBody := `{"results":["alpha","beta"]}`
	sockPath := newMatcherTestServer(t, rankHandler("", consolidateBody))

	c := NewMatcherClient(sockPath, MaxMatchBodyBytes(4000))
	defer c.Close()

	got, err := c.Rank(context.Background(), "doc-1", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	want := []tagmatcher.RankResult{
		{KeptName: "alpha"},
		{KeptName: "beta"},
	}
	if len(got) != len(want) {
		t.Fatalf("Rank returned %d results, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("Rank result[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

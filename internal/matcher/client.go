package matcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
	"unicode/utf8"
)

// ErrMatcherUnavailable wraps every transport-level failure of the client:
// unreachable socket, non-2xx status, and request or response encoding
// errors. Callers use errors.Is to tell the daemon being down or misbehaving
// apart from domain-level results.
var ErrMatcherUnavailable = fmt.Errorf("matcher unavailable")

// StatusError carries the HTTP status the daemon answered with.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("status %d", e.Code) }

// - bytesPerWord is a generous per-word byte estimate (covers CJK ~3 bytes/char plus
// low-whitespace degenerate text).
// - envelopeMargin reserves space for the JSON envelope (doc_id, quotes, escaping)
// so the client truncation target stays strictly below the server cap.
// - minBodyBytes and maxBodyBytes clamp the derived cap so a misconfigured
// reduce_target_words cannot produce a nonsensical limit.
const (
	bytesPerWord   = 24
	envelopeMargin = 4096
	minBodyBytes   = 256 * 1024
	maxBodyBytes   = 4 * 1024 * 1024
)

// MaxMatchBodyBytes derives the byte cap for a match request body from the
// configured reduce target words: a generous per-word byte estimate minus an
// envelope margin, clamped between 256 KiB and 4 MiB. Non-positive targets
// get the 4 MiB ceiling.
func MaxMatchBodyBytes(reduceTargetWords int) int {
	if reduceTargetWords <= 0 {
		return maxBodyBytes
	}
	n := reduceTargetWords * bytesPerWord
	switch {
	case n < minBodyBytes:
		return minBodyBytes
	case n > maxBodyBytes:
		return maxBodyBytes
	default:
		return n
	}
}

// TargetWordCount resolves the reduction target a caller configured: a
// negative target is a divisor of the content's word count (for example -4
// means one quarter), a positive one is used as-is, and the result never
// goes below 2000 words.
func TargetWordCount(contentWC, targetWC int) int {
	result := targetWC
	if result < 0 {
		result = contentWC / -result
	}
	return max(2000, result)
}

// MatcherClient is the API-process side of semantic matching: it satisfies
// Matcher and Embedder by calling the matcher daemon over its Unix-socket
// HTTP API. All methods are safe for concurrent use; every transport-level
// failure reports ErrMatcherUnavailable.
type MatcherClient struct {
	client            *http.Client
	maxMatchBodyBytes int
}

// NewMatcherClient prepares a client for the daemon listening on socketPath.
// maxMatchBodyBytes caps a match request body: longer inputs are truncated
// UTF-8-safely below it so requests never exceed the daemon's limit.
func NewMatcherClient(socketPath string, maxMatchBodyBytes int) *MatcherClient {
	return &MatcherClient{
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
				MaxIdleConns:    1,
				IdleConnTimeout: 120 * time.Second,
			},
		},
		maxMatchBodyBytes: maxMatchBodyBytes,
	}
}

func (c *MatcherClient) do(ctx context.Context, method, path string, req, resp any) error {
	var body bytes.Buffer
	if req != nil {
		if err := json.NewEncoder(&body).Encode(req); err != nil {
			return fmt.Errorf("%w: encode request: %w", ErrMatcherUnavailable, err)
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, &body)
	if err != nil {
		return fmt.Errorf("%w: create request: %w", ErrMatcherUnavailable, err)
	}
	if req != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMatcherUnavailable, err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %w", ErrMatcherUnavailable, &StatusError{Code: httpResp.StatusCode})
	}

	if resp != nil {
		if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
			return fmt.Errorf("%w: decode response: %w", ErrMatcherUnavailable, err)
		}
	}

	return nil
}

// Match matches a document's reduced text against the daemon's stored tags.
// Input longer than the body cap is truncated before the request.
func (c *MatcherClient) Match(ctx context.Context, docId, input string) ([]Candidate, error) {
	target := c.maxMatchBodyBytes - envelopeMargin
	if len(input) > target {
		input = truncateUTF8(input, target)
	}
	var resp struct {
		Matches []Candidate `json:"matches"`
	}
	req := struct {
		DocID string `json:"doc_id"`
		Input string `json:"input"`
	}{
		DocID: docId,
		Input: input,
	}
	if err := c.do(ctx, "POST", "/rpc/v1/match", req, &resp); err != nil {
		return nil, err
	}
	return resp.Matches, nil
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	b := s[:maxBytes]
	for len(b) > 0 {
		r, size := utf8.DecodeLastRuneInString(b)
		if r != utf8.RuneError || size != 1 {
			break
		}
		b = b[:len(b)-size]
	}
	return b
}

// Close releases the client's idle connections. In-flight requests are not
// cancelled.
func (c *MatcherClient) Close() {
	c.client.CloseIdleConnections()
}

// Name identifies this implementation, distinguishing the socket client from
// the in-process engine behind the Matcher seam.
func (c *MatcherClient) Name() string {
	return "matcher-rpc-client"
}

type encodeResult struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Encode embeds the texts through the daemon, one returned vector per
// input, in input order.
func (c *MatcherClient) Encode(ctx context.Context, _ string, texts []string) ([][]float32, error) {
	var resp encodeResult
	req := struct {
		Texts []string `json:"texts"`
	}{Texts: texts}
	if err := c.do(ctx, "POST", "/rpc/v1/encode", req, &resp); err != nil {
		return nil, err
	}
	return resp.Embeddings, nil
}

// Rank ranks the queries against the daemon's stored tags.
func (c *MatcherClient) Rank(ctx context.Context, docId string, queries []string) ([]RankResult, error) {
	var resp struct {
		Results []RankResult `json:"results"`
	}
	req := struct {
		DocID   string   `json:"doc_id"`
		Queries []string `json:"queries"`
	}{DocID: docId, Queries: queries}
	if err := c.do(ctx, "POST", "/rpc/v1/rank", req, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// Health pings the daemon; ErrMatcherUnavailable when it is not reachable.
func (c *MatcherClient) Health(ctx context.Context) error {
	return c.do(ctx, "GET", "/health", nil, nil)
}

// AddToStore embeds the named tags and stores them on the daemon.
func (c *MatcherClient) AddToStore(ctx context.Context, names []string) error {
	req := struct {
		Names []string `json:"names"`
	}{Names: names}
	return c.do(ctx, "POST", "/rpc/v1/add-to-store", req, nil)
}

// RemoveFromStore drops the named tags from the daemon's store.
func (c *MatcherClient) RemoveFromStore(ctx context.Context, names []string) error {
	req := struct {
		Names []string `json:"names"`
	}{Names: names}
	return c.do(ctx, "POST", "/rpc/v1/remove-from-store", req, nil)
}

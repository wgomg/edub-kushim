package tagmatch

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

var ErrMatcherUnavailable = fmt.Errorf("matcher unavailable")

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

type MatcherClient struct {
	client            *http.Client
	maxMatchBodyBytes int
}

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
		return fmt.Errorf("%w: status %d", ErrMatcherUnavailable, httpResp.StatusCode)
	}

	if resp != nil {
		if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
			return fmt.Errorf("%w: decode response: %w", ErrMatcherUnavailable, err)
		}
	}

	return nil
}

func (c *MatcherClient) Match(ctx context.Context, docId, input string) ([]string, error) {
	target := c.maxMatchBodyBytes - envelopeMargin
	if len(input) > target {
		input = truncateUTF8(input, target)
	}
	var resp struct {
		Matches []string `json:"matches"`
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

func (c *MatcherClient) Close() {
	c.client.CloseIdleConnections()
}

func (c *MatcherClient) Name() string {
	return "matcher-rpc-client"
}

type encodeResult struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (c *MatcherClient) Encode(ctx context.Context, _ *string, texts []string) ([][]float32, error) {
	var resp encodeResult
	req := struct {
		Texts []string `json:"texts"`
	}{Texts: texts}
	if err := c.do(ctx, "POST", "/rpc/v1/encode", req, &resp); err != nil {
		return nil, err
	}
	return resp.Embeddings, nil
}

func (c *MatcherClient) Consolidate(ctx context.Context, docId string, queries []string) ([]string, error) {
	var resp struct {
		Results []string `json:"results"`
	}
	req := struct {
		DocID   string   `json:"doc_id"`
		Queries []string `json:"queries"`
	}{
		DocID:   docId,
		Queries: queries,
	}
	if err := c.do(ctx, "POST", "/rpc/v1/consolidate", req, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

func (c *MatcherClient) Health(ctx context.Context) error {
	return c.do(ctx, "GET", "/health", nil, nil)
}

func (c *MatcherClient) AddToStore(ctx context.Context, names []string) error {
	req := struct {
		Names []string `json:"names"`
	}{Names: names}
	return c.do(ctx, "POST", "/rpc/v1/add-to-store", req, nil)
}

func (c *MatcherClient) RemoveFromStore(ctx context.Context, names []string) error {
	req := struct {
		Names []string `json:"names"`
	}{Names: names}
	return c.do(ctx, "POST", "/rpc/v1/remove-from-store", req, nil)
}

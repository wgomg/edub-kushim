package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

var ErrMatcherUnavailable = fmt.Errorf("matcher unavailable")

type MatcherClient struct {
	client *http.Client
}

func NewMatcherClient(socketPath string) *MatcherClient {
	return &MatcherClient{
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			MaxIdleConns:    1,
			IdleConnTimeout: 120 * time.Second,
		},
		Timeout: 120 * time.Second,
		},
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

func (c *MatcherClient) Match(ctx context.Context, docId, input string, candidateTags []string) ([]string, error) {
	var resp struct {
		Matches []string `json:"matches"`
	}
	req := struct {
		DocID         string   `json:"doc_id"`
		Input         string   `json:"input"`
		CandidateTags []string `json:"candidate_tags"`
	}{
		DocID:         docId,
		Input:         input,
		CandidateTags: candidateTags,
	}
	if err := c.do(ctx, "POST", "/rpc/v1/match", req, &resp); err != nil {
		return nil, err
	}
	return resp.Matches, nil
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

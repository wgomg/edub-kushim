package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestHealthHandler(t *testing.T) {
	ctx := context.WithValue(context.Background(), "reqid", "test-req")
	req := httptest.NewRequest(http.MethodGet, "/health", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	HealthHandler(w, req, utils.NewDiscardLogger())

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "healthy" {
		t.Errorf("Status = %q", body.Status)
	}
	if body.Version != "0.1.0" {
		t.Errorf("Version = %q", body.Version)
	}
	if body.Time == "" {
		t.Error("Time is empty")
	}
}

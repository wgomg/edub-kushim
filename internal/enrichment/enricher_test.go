package enrichment

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestEnrich_ReturnsSkipped(t *testing.T) {
	cfg := &config.Config{}
	logger := utils.NewDiscardLogger()
	e, err := NewEnricher(cfg, logger, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	raw, err := e.Enrich(context.Background(), 42)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	var result struct {
		DocumentID int64  `json:"document_id"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.DocumentID != 42 {
		t.Errorf("document_id = %d, want 42", result.DocumentID)
	}
	if result.Status != "skipped" {
		t.Errorf("status = %q, want %q", result.Status, "skipped")
	}
}

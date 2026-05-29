package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestEnrichTaskHandler_Handle_Success(t *testing.T) {
	cfg := &config.Config{}
	logger := utils.NewDiscardLogger()
	e, err := enrichment.NewEnricher(cfg, logger, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	h := NewEnrichTaskHandler(e)
	ctx := context.Background()

	task := database.Task{
		TaskID:  "enrich-1",
		Payload: json.RawMessage(`{"document_id":42}`),
	}

	result, err := h.Handle(ctx, task)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var parsed struct {
		DocumentID int64  `json:"document_id"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.DocumentID != 42 {
		t.Errorf("document_id = %d, want 42", parsed.DocumentID)
	}
	if parsed.Status != "skipped" {
		t.Errorf("status = %q, want %q", parsed.Status, "skipped")
	}
}

func TestEnrichTaskHandler_Handle_InvalidPayload(t *testing.T) {
	cfg := &config.Config{}
	logger := utils.NewDiscardLogger()
	e, err := enrichment.NewEnricher(cfg, logger, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	h := NewEnrichTaskHandler(e)
	ctx := context.Background()

	task := database.Task{
		TaskID:  "enrich-2",
		Payload: json.RawMessage(`not json`),
	}

	_, err = h.Handle(ctx, task)
	if err == nil {
		t.Fatal("Handle() expected error for invalid JSON, got nil")
	}
}

func TestEnrichTaskHandler_Handle_MissingDocumentID(t *testing.T) {
	cfg := &config.Config{}
	logger := utils.NewDiscardLogger()
	e, err := enrichment.NewEnricher(cfg, logger, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	h := NewEnrichTaskHandler(e)
	ctx := context.Background()

	task := database.Task{
		TaskID:  "enrich-3",
		Payload: json.RawMessage(`{}`),
	}

	_, err = h.Handle(ctx, task)
	if err == nil {
		t.Fatal("Handle() expected error for missing document_id, got nil")
	}
}

func TestEnrichTaskHandler_DedupKey(t *testing.T) {
	cfg := &config.Config{}
	logger := utils.NewDiscardLogger()
	e, err := enrichment.NewEnricher(cfg, logger, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	h := NewEnrichTaskHandler(e)

	key := h.DedupKey(json.RawMessage(`{"document_id":42}`))
	if key != "enrich:doc:42" {
		t.Errorf("DedupKey = %q, want %q", key, "enrich:doc:42")
	}
}

func TestEnrichTaskHandler_DedupKey_Empty(t *testing.T) {
	cfg := &config.Config{}
	logger := utils.NewDiscardLogger()
	e, err := enrichment.NewEnricher(cfg, logger, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	h := NewEnrichTaskHandler(e)

	key := h.DedupKey(json.RawMessage(`{}`))
	if key != "" {
		t.Errorf("DedupKey = %q, want empty string", key)
	}
}

package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/enrichment"
)

type EnrichTaskHandler struct {
	enricher *enrichment.Enricher
}

func NewEnrichTaskHandler(enricher *enrichment.Enricher) *EnrichTaskHandler {
	return &EnrichTaskHandler{enricher: enricher}
}

func (h *EnrichTaskHandler) Handle(ctx context.Context, t database.Task) (json.RawMessage, error) {
	var p struct {
		DocumentID int64 `json:"document_id"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal enrich payload: %w", err)
	}
	if p.DocumentID == 0 {
		return nil, fmt.Errorf("enrich task %s has no document_id in payload", t.TaskID)
	}

	return h.enricher.Enrich(ctx, p.DocumentID)
}

func (h *EnrichTaskHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		DocumentID int64 `json:"document_id"`
	}
	json.Unmarshal(payload, &p)
	if p.DocumentID == 0 {
		return ""
	}
	return fmt.Sprintf("enrich:doc:%d", p.DocumentID)
}

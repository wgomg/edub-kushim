package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type EnrichTaskHandler struct {
	enricher *enrichment.Enricher
	queries  *database.Queries
	logger   *utils.Logger
}

func NewEnrichTaskHandler(enricher *enrichment.Enricher, queries *database.Queries, logger *utils.Logger) *EnrichTaskHandler {
	return &EnrichTaskHandler{enricher: enricher, queries: queries, logger: logger}
}

func (h *EnrichTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	var p struct {
		DocumentID string `json:"document_id"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal enrich payload: %w", err)
	}

	document, err := h.queries.GetDocument(ctx, p.DocumentID)
	if err != nil {
		if p.DocumentID != "" {
			return nil, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("document %s not found", p.DocumentID)}
		}
		return nil, fmt.Errorf("document %s not found", p.DocumentID)
	}

	result, err := h.enricher.Enrich(ctx, document.ToDocument())
	{
		mem := utils.ReadMemFull()
		h.logger.Debug(&p.DocumentID, "post-enrich memory: %s", utils.FormatMemFull(mem))
	}
	if err != nil {
		return nil, err
	}

	return *result, nil
}

func (h *EnrichTaskHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		DocumentID string `json:"document_id"`
	}
	json.Unmarshal(payload, &p)
	if p.DocumentID == "" {
		return ""
	}
	return fmt.Sprintf("enrich:doc:%s", p.DocumentID)
}

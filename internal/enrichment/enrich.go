package enrichment

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
)

// EnrichTaskHandler enriches one stored document as a blocking task: it
// loads the document row, runs the enrichment coordinator, and stores the
// result payload on the task.
type EnrichTaskHandler struct {
	enricher *Enricher
	queries  *database.Queries
	logger   *log.Logger
}

// NewEnrichTaskHandler wires the handler to the enricher, the queries and
// the logger.
func NewEnrichTaskHandler(enricher *Enricher, queries *database.Queries, logger *log.Logger) *EnrichTaskHandler {
	return &EnrichTaskHandler{enricher: enricher, queries: queries, logger: logger}
}

// Handle loads the payload's document and enriches it; a missing document
// fails the task with its id attached.
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

	result, err := h.enricher.Enrich(ctx, document.ToDocument(), task.NewProgressTracker(h.queries, t.TaskID).Set)
	{
		mem := log.ReadMemFull()
		h.logger.Debug(p.DocumentID, "post-enrich memory: %s", log.FormatMemFull(mem))
	}
	if err != nil {
		return nil, err
	}

	return *result, nil
}

// DedupKey is the payload's document id, so one document never enriches
// twice concurrently.
func (h *EnrichTaskHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		DocumentID string `json:"document_id"`
	}
	_ = json.Unmarshal(payload, &p)
	if p.DocumentID == "" {
		return ""
	}
	return fmt.Sprintf("enrich:doc:%s", p.DocumentID)
}

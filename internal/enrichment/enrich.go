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

	result, counts, err := h.enricher.Enrich(ctx, document.ToDocument(), task.NewProgressTracker(h.queries, t.TaskID).Set)
	{
		mem := log.ReadMemFull()
		h.logger.Debug(p.DocumentID, "post-enrich memory: %s", log.FormatMemFull(mem))
	}
	if err != nil {
		return nil, err
	}

	var stats json.RawMessage
	if result != nil {
		stats = *result
	}
	return marshalEnrichResult(stats, counts)
}

// enrichResult is the enrich task's stored result payload: the analyzer
// stats plus the per-document counts the batch bookend aggregates. Counts
// is nil only on payloads written before the envelope existed.
type enrichResult struct {
	Stats  json.RawMessage `json:"stats"`
	Counts *DocumentCounts `json:"counts"`
}

// marshalEnrichResult serializes an enrich task's result envelope; a nil
// stats serializes as JSON null, and stats that are not valid JSON return
// the marshal error.
func marshalEnrichResult(stats json.RawMessage, counts DocumentCounts) (json.RawMessage, error) {
	payload, err := json.Marshal(enrichResult{Stats: stats, Counts: &counts})
	if err != nil {
		return nil, fmt.Errorf("marshal enrich result: %w", err)
	}
	return payload, nil
}

// CountsFromResult extracts the per-document counts from an enrich task's
// stored result payload; ok is false for payloads written before the
// envelope existed and for payloads that do not decode.
func CountsFromResult(result json.RawMessage) (DocumentCounts, bool) {
	var parsed enrichResult
	if len(result) == 0 || json.Unmarshal(result, &parsed) != nil || parsed.Counts == nil {
		return DocumentCounts{}, false
	}
	return *parsed.Counts, true
}

package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// ReEnrich re-enqueues enrichment for stored documents: one document, or every live document up to a
// limit. Tasks deduplicate per document, so a triggered run and a bulk run converge on whichever runs
// first.
type ReEnrich struct {
	queries  *database.Queries
	enqueuer *task.Enqueuer
}

// NewReEnrich wires the enqueue path to the queries and the transactional enqueuer.
func NewReEnrich(queries *database.Queries, enqueuer *task.Enqueuer) *ReEnrich {
	return &ReEnrich{
		queries:  queries,
		enqueuer: enqueuer,
	}
}

// ReEnrich queues one document's enrichment in a fresh batch; the document must exist and an enrich
// task already queued for it is a conflict.
func (s *ReEnrich) ReEnrich(ctx context.Context, spec task.BatchSpec, documentUUID string) (batchID string, err error) {
	_, err = s.queries.GetDocument(ctx, documentUUID)
	if err != nil {
		return "", errs.FromDB(err, "get document")
	}

	payload, _ := json.Marshal(map[string]any{
		"document_id": documentUUID,
	})

	result, err := s.enqueuer.EnqueueTask(ctx, spec, task.TaskSpec{
		Type:    types.Task.Type.Enrich,
		Payload: payload,
	})
	if err != nil {
		return "", fmt.Errorf("create enrich task: %w", err)
	}
	if result.BatchID == "" {
		return "", errs.EConflict("re-enrich", fmt.Errorf("enrich task for document %s is already queued", documentUUID))
	}

	return result.BatchID, nil
}

// ReEnrichMany queues enrichment for every live document, capped by a positive limit. Tasks whose dedup
// key still holds are skipped; when no task is enqueued no batch is created.
func (s *ReEnrich) ReEnrichMany(ctx context.Context, spec task.BatchSpec, limit int64) (batchID string, enqueued, skipped int, err error) {
	documentIDs, err := s.queries.ListLiveDocumentIDs(ctx)
	if err != nil {
		return "", 0, 0, errs.FromDB(err, "list live documents")
	}

	if limit > 0 && int64(len(documentIDs)) > limit {
		documentIDs = documentIDs[:limit]
	}

	tasks := make([]task.TaskSpec, 0, len(documentIDs))
	for _, documentID := range documentIDs {
		payload, _ := json.Marshal(map[string]any{
			"document_id": documentID,
		})
		tasks = append(tasks, task.TaskSpec{Type: types.Task.Type.Enrich, Payload: payload})
	}

	result, err := s.enqueuer.EnqueueBatch(ctx, spec, tasks)
	if err != nil {
		return "", 0, 0, fmt.Errorf("enqueue enrich tasks: %w", err)
	}

	return result.BatchID, len(result.TaskIDs), result.Skipped, nil
}

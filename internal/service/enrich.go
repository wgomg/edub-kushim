package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/types"
)

// ReEnrich re-enqueues enrichment for stored documents: one document, or
// every live document up to a limit. Tasks deduplicate per document, so a
// triggered run and a bulk run converge on whichever runs first.
type ReEnrich struct {
	queries      *database.Queries
	taskCreator  TaskCreator
	batchCreator BatchCreator
}

// NewReEnrich wires the enqueue path to the queries and the task and
// batch creators.
func NewReEnrich(queries *database.Queries, taskCreator TaskCreator, batchCreator BatchCreator) *ReEnrich {
	return &ReEnrich{
		queries:      queries,
		taskCreator:  taskCreator,
		batchCreator: batchCreator,
	}
}

// ReEnrich queues one document's enrichment in a fresh reenrich batch;
// the document must exist.
func (s *ReEnrich) ReEnrich(ctx context.Context, documentUUID string) (batchID string, err error) {
	_, err = s.queries.GetDocument(ctx, documentUUID)
	if err != nil {
		return "", errs.FromDB(err, "get document")
	}

	batchID = uuid.New().String()

	if err := s.batchCreator.Create(ctx, batchID, types.Batch.Source.Reenrich, types.Batch.Status.Queued); err != nil {
		return "", errs.FromDB(err, "create batch")
	}

	taskID := uuid.New().String()

	payload, _ := json.Marshal(map[string]any{
		"document_id": documentUUID,
	})

	_, err = s.taskCreator.CreateTask(ctx, types.Task.Type.Enrich, batchID, payload, taskID, types.Task.Status.Pending, "enrich:doc:"+documentUUID)
	if err != nil {
		return "", errs.FromDB(err, "create enrich task")
	}

	return batchID, nil
}

// ReEnrichMany queues enrichment for every live document, capped by a
// positive limit. Tasks whose dedup key still holds are skipped; when no
// task could be enqueued no batch is created, and a batch-creation
// failure removes the created tasks so their dedup keys do not block
// later runs.
func (s *ReEnrich) ReEnrichMany(ctx context.Context, limit int64) (batchID string, enqueued, skipped int, err error) {
	documentIDs, err := s.queries.ListLiveDocumentIDs(ctx)
	if err != nil {
		return "", 0, 0, errs.FromDB(err, "list live documents")
	}

	if limit > 0 && int64(len(documentIDs)) > limit {
		documentIDs = documentIDs[:limit]
	}

	batchID = uuid.New().String()

	for _, documentID := range documentIDs {
		payload, _ := json.Marshal(map[string]any{
			"document_id": documentID,
		})

		_, err := s.taskCreator.CreateTask(ctx, types.Task.Type.Enrich, batchID, payload, uuid.New().String(), types.Task.Status.Pending, "enrich:doc:"+documentID)
		if err != nil {
			skipped++
			continue
		}
		enqueued++
	}

	if enqueued == 0 {
		return "", 0, skipped, nil
	}

	if err := s.batchCreator.Create(ctx, batchID, types.Batch.Source.Reenrich, types.Batch.Status.Queued); err != nil {
		return "", enqueued, skipped, errs.FromDB(errors.Join(err, s.deleteUnbatchedTasks(batchID)), "create batch")
	}

	return batchID, enqueued, skipped, nil
}

// unbatched tasks would keep their dedup keys and block later runs
func (s *ReEnrich) deleteUnbatchedTasks(batchID string) error {
	if _, err := s.queries.DeleteTasksByBatch(context.Background(), sql.NullString{String: batchID, Valid: true}); err != nil {
		return err
	}
	return nil
}

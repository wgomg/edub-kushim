package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// ThumbnailBackfill enqueues thumbnail tasks for documents that lack a thumbnail: one batch's worth,
// or a single document. The all-documents scan belongs to the thumbbackfill type definition's shared
// builder, which the CLI and the scheduler both use.
type ThumbnailBackfill struct {
	queries  *database.Queries
	enqueuer *task.Enqueuer
}

// NewThumbnailBackfill wires the enqueue path to the queries and the transactional enqueuer.
func NewThumbnailBackfill(queries *database.Queries, enqueuer *task.Enqueuer) *ThumbnailBackfill {
	return &ThumbnailBackfill{
		queries:  queries,
		enqueuer: enqueuer,
	}
}

// BackfillBatch enqueues thumbnails for the documents of one past batch.
func (s *ThumbnailBackfill) BackfillBatch(ctx context.Context, spec task.BatchSpec, batchID string) (newBatchID string, enqueued, skipped int, err error) {
	rows, err := s.queries.ListDocumentsWithoutThumbnailsByBatch(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return "", 0, 0, errs.FromDB(err, "list documents without thumbnails by batch")
	}

	tasks := make([]task.TaskSpec, 0, len(rows))
	for _, row := range rows {
		payload, _ := json.Marshal(map[string]any{
			"document_id":  row.DocumentID,
			"storage_path": row.StoragePath,
		})
		tasks = append(tasks, task.TaskSpec{Type: types.Task.Type.ThumbBackfill, Payload: payload})
	}

	result, err := s.enqueuer.EnqueueBatch(ctx, spec, tasks)
	if err != nil {
		return "", 0, 0, fmt.Errorf("enqueue thumbnail tasks: %w", err)
	}
	return result.BatchID, len(result.TaskIDs), result.Skipped, nil
}

// BackfillDocument enqueues one document's thumbnail task; documents that already have a thumbnail,
// or whose task is already queued, are refused with a conflict.
func (s *ThumbnailBackfill) BackfillDocument(ctx context.Context, spec task.BatchSpec, documentUUID string) (batchID string, err error) {
	_, err = s.queries.GetDocument(ctx, documentUUID)
	if err != nil {
		return "", errs.FromDB(err, "get document")
	}

	row, err := s.queries.GetDocumentWithoutThumbnail(ctx, documentUUID)
	if err != nil {
		if errs.KindOf(errs.FromDB(err, "get document without thumbnail")) == errs.KindNotFound {
			return "", errs.EConflict("backfill thumbnail", fmt.Errorf("document %s already has a thumbnail", documentUUID))
		}
		return "", errs.FromDB(err, "get document without thumbnail")
	}

	payload, _ := json.Marshal(map[string]any{
		"document_id":  row.DocumentID,
		"storage_path": row.StoragePath,
	})
	result, err := s.enqueuer.EnqueueTask(ctx, spec, task.TaskSpec{
		Type:    types.Task.Type.ThumbBackfill,
		Payload: payload,
	})
	if err != nil {
		return "", fmt.Errorf("enqueue thumbnail task: %w", err)
	}
	if result.BatchID == "" {
		return "", errs.EConflict("backfill thumbnail", fmt.Errorf("document %s already has a thumbnail or a task is already queued", documentUUID))
	}
	return result.BatchID, nil
}

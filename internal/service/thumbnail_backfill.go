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

// thumbnailBackfillPageSize bounds each page of the all-documents scan.
const thumbnailBackfillPageSize = 500

// ThumbnailBackfill enqueues thumbnail tasks for the documents of one past batch or for a single document.
// It also owns the thumbnail types' dedup vocabulary.
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

// ThumbnailDedupKey derives the thumbnail and thumbbackfill tasks' dedup key from the payload's document
// id: one live thumbnail task per document.
func ThumbnailDedupKey(payload json.RawMessage) string {
	var p struct {
		DocumentID string `json:"document_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.DocumentID == "" {
		return ""
	}
	return "thumbnail:doc:" + p.DocumentID
}

// ThumbnailBackfillSpecs returns one thumbnail-backfill task per document that lacks a thumbnail, scanning
// the table in id order; the enqueuer settles documents whose task is already live.
func ThumbnailBackfillSpecs(ctx context.Context, queries *database.Queries) ([]task.TaskSpec, error) {
	var specs []task.TaskSpec
	var lastID int64
	for {
		rows, err := queries.ListDocumentsWithoutThumbnails(ctx, database.ListDocumentsWithoutThumbnailsParams{
			ID:    lastID,
			Limit: thumbnailBackfillPageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("list documents without thumbnails: %w", err)
		}
		if len(rows) == 0 {
			return specs, nil
		}
		for _, row := range rows {
			lastID = row.ID
			payload, err := json.Marshal(map[string]string{
				"document_id":  row.DocumentID,
				"storage_path": row.StoragePath,
			})
			if err != nil {
				return nil, fmt.Errorf("marshal thumbnail payload: %w", err)
			}
			specs = append(specs, task.TaskSpec{Type: types.Task.Type.ThumbBackfill, Payload: payload})
		}
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

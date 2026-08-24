package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const backfillPageSize = 500

type ThumbnailBackfill struct {
	queries      *database.Queries
	logger       *utils.Logger
	taskCreator  TaskCreator
	batchCreator BatchCreator
}

func NewThumbnailBackfill(queries *database.Queries, logger *utils.Logger, taskCreator TaskCreator, batchCreator BatchCreator) *ThumbnailBackfill {
	return &ThumbnailBackfill{
		queries:      queries,
		logger:       logger,
		taskCreator:  taskCreator,
		batchCreator: batchCreator,
	}
}

func (s *ThumbnailBackfill) BackfillAll(ctx context.Context) (batchID string, enqueued, skipped int, err error) {
	runBatchID := uuid.New().String()
	batchCreated := false
	defer func() {
		if !batchCreated && enqueued > 0 {
			s.deleteUnbatchedTasks(runBatchID)
		}
	}()
	var lastID int64
	for {
		rows, err := s.queries.ListDocumentsWithoutThumbnails(ctx, database.ListDocumentsWithoutThumbnailsParams{
			ID:    lastID,
			Limit: backfillPageSize,
		})
		if err != nil {
			return "", enqueued, skipped, errs.FromDB(err, "list documents without thumbnails")
		}
		if len(rows) == 0 {
			break
		}

		for _, row := range rows {
			lastID = row.ID
			if s.addDocument(ctx, runBatchID, row.DocumentID, row.StoragePath) {
				enqueued++
			} else {
				skipped++
			}
		}
	}

	if enqueued == 0 {
		return "", 0, skipped, nil
	}
	if err := s.batchCreator.Create(ctx, runBatchID, "thumbbackfill", "queued"); err != nil {
		return "", enqueued, skipped, errs.FromDB(err, "create batch")
	}
	batchCreated = true
	return runBatchID, enqueued, skipped, nil
}

func (s *ThumbnailBackfill) BackfillBatch(ctx context.Context, batchID string) (newBatchID string, enqueued, skipped int, err error) {
	rows, err := s.queries.ListDocumentsWithoutThumbnailsByBatch(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return "", 0, 0, errs.FromDB(err, "list documents without thumbnails by batch")
	}

	runBatchID := uuid.New().String()
	batchCreated := false
	defer func() {
		if !batchCreated && enqueued > 0 {
			s.deleteUnbatchedTasks(runBatchID)
		}
	}()
	for _, row := range rows {
		if s.addDocument(ctx, runBatchID, row.DocumentID, row.StoragePath) {
			enqueued++
		} else {
			skipped++
		}
	}

	if enqueued == 0 {
		return "", 0, skipped, nil
	}
	if err := s.batchCreator.Create(ctx, runBatchID, "thumbbackfill", "queued"); err != nil {
		return "", enqueued, skipped, errs.FromDB(err, "create batch")
	}
	batchCreated = true
	return runBatchID, enqueued, skipped, nil
}

func (s *ThumbnailBackfill) BackfillDocument(ctx context.Context, documentUUID string) (batchID string, err error) {
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

	runBatchID := uuid.New().String()
	taskCreated := s.addDocument(ctx, runBatchID, row.DocumentID, row.StoragePath)
	if !taskCreated {
		return "", errs.EConflict("backfill thumbnail", fmt.Errorf("document %s already has a thumbnail or a task is already queued", documentUUID))
	}
	if err := s.batchCreator.Create(ctx, runBatchID, "thumbbackfill", "queued"); err != nil {
		s.deleteUnbatchedTasks(runBatchID)
		return "", errs.FromDB(err, "create batch")
	}
	return runBatchID, nil
}

func (s *ThumbnailBackfill) deleteUnbatchedTasks(batchID string) {
	if _, err := s.queries.DeleteTasksByBatch(context.Background(), sql.NullString{String: batchID, Valid: true}); err != nil {
		s.logger.Error(nil, "cleanup of unbatched thumbnail tasks: %v", err)
	}
}

func (s *ThumbnailBackfill) addDocument(ctx context.Context, batchID, documentID, storagePath string) bool {
	payload, _ := json.Marshal(map[string]any{
		"document_id":  documentID,
		"storage_path": storagePath,
	})
	_, err := s.taskCreator.CreateTask(ctx, "thumbnail", batchID, payload, uuid.New().String(), "pending", "thumbnail:doc:"+documentID)
	if err != nil {
		err = errs.FromDB(err, "create thumbnail task")
		if errs.KindOf(err) == errs.KindConflict {
			s.logger.Warn(&documentID, "thumbnail task already pending for %s, skipping", documentID)
			return false
		}
		s.logger.Error(&documentID, "thumbnail task creation failed for %s: %v", documentID, err)
		return false
	}
	return true
}

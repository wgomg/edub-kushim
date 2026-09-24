package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/types"
)

type TagIdentity struct {
	queries      *database.Queries
	taskCreator  service.TaskCreator
	batchCreator service.BatchCreator
}

func NewTagIdentity(queries *database.Queries, taskCreator service.TaskCreator, batchCreator service.BatchCreator) *TagIdentity {
	return &TagIdentity{
		queries:      queries,
		taskCreator:  taskCreator,
		batchCreator: batchCreator,
	}
}

func (s *TagIdentity) Enqueue(ctx context.Context) (batchID string, alreadyQueued bool, err error) {
	active, err := s.queries.CountActiveTagIdentityBatches(ctx)
	if err != nil {
		return "", false, errs.FromDB(err, "count active tag identity batches")
	}
	if active > 0 {
		return "", true, nil
	}

	batchID = uuid.New().String()
	taskID := uuid.New().String()

	if _, err := s.taskCreator.CreateTask(ctx, types.Task.Type.TagIdentity, batchID, json.RawMessage("{}"), taskID, types.Task.Status.Pending, "tagidentity"); err != nil {
		return "", true, nil
	}

	if err := s.batchCreator.Create(ctx, batchID, types.Batch.Source.TagIdentity, types.Batch.Status.Queued); err != nil {
		return "", false, errs.FromDB(errors.Join(err, s.deleteUnbatchedTasks(batchID)), "create tag identity batch")
	}

	return batchID, false, nil
}

// unbatched tasks would keep their dedup keys and block later runs
func (s *TagIdentity) deleteUnbatchedTasks(batchID string) error {
	if _, err := s.queries.DeleteTasksByBatch(context.Background(), sql.NullString{String: batchID, Valid: true}); err != nil {
		return err
	}
	return nil
}

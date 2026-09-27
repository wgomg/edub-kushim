package curation

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TagIdentity enqueues identity resolution sweeps: the human trigger for
// the machinery the admission resolver serves.
type TagIdentity struct {
	queries      *database.Queries
	taskCreator  types.TaskCreator
	batchCreator types.BatchCreator
}

// NewTagIdentity wires the enqueue path to the queries and the batch and
// task creators.
func NewTagIdentity(queries *database.Queries, taskCreator types.TaskCreator, batchCreator types.BatchCreator) *TagIdentity {
	return &TagIdentity{
		queries:      queries,
		taskCreator:  taskCreator,
		batchCreator: batchCreator,
	}
}

// Enqueue queues a tag identity sweep batch and its task, or reports
// alreadyQueued when one is still active.
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
		return "", false, errs.FromDB(errors.Join(err, task.DeleteBatchTasks(context.Background(), s.queries, batchID)), "create tag identity batch")
	}

	return batchID, false, nil
}

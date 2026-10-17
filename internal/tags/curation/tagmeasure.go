package curation

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TagMeasure enqueues measurement sweeps: the manual trigger for the machinery the queue daemon otherwise
// runs on its schedule.
type TagMeasure struct {
	queries      *database.Queries
	taskCreator  types.TaskCreator
	batchCreator types.BatchCreator
}

// NewTagMeasure wires the enqueue path to the queries and the batch and task creators.
func NewTagMeasure(queries *database.Queries, taskCreator types.TaskCreator, batchCreator types.BatchCreator) *TagMeasure {
	return &TagMeasure{
		queries:      queries,
		taskCreator:  taskCreator,
		batchCreator: batchCreator,
	}
}

// Enqueue queues a tag measure batch and its task, or reports alreadyQueued when one is still active.
func (s *TagMeasure) Enqueue(ctx context.Context) (batchID string, alreadyQueued bool, err error) {
	active, err := s.queries.CountActiveTagMeasureBatches(ctx)
	if err != nil {
		return "", false, errs.FromDB(err, "count active tag measure batches")
	}
	if active > 0 {
		return "", true, nil
	}

	batchID = uuid.New().String()
	taskID := uuid.New().String()

	// a unique violation on the active-dedup index means another measure task already owns the slot (the
	// count above lost a race); anything else is a real failure and must not be reported as already queued
	if _, err := s.taskCreator.CreateTask(ctx, types.Task.Type.TagMeasure, batchID, json.RawMessage("{}"), taskID, types.Task.Status.Pending, tags.TagMeasureDedupKey); err != nil {
		if errs.IsConstraint(err) {
			return "", true, nil
		}
		return "", false, errs.FromDB(err, "create tag measure task")
	}

	if err := s.batchCreator.Create(ctx, batchID, types.Batch.Source.TagMeasure, types.Batch.Status.Queued); err != nil {
		return "", false, errs.FromDB(errors.Join(err, task.DeleteBatchTasks(context.Background(), s.queries, batchID)), "create tag measure batch")
	}

	return batchID, false, nil
}

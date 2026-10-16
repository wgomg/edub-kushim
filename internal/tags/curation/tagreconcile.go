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

// TagReconcileDedupKey is the active-dedup key shared by the enqueue path and the task handler so they agree.
const TagReconcileDedupKey = "tagreconcile"

// TagReconcile enqueues scheduled reconciliation sweeps: the manual trigger for the machinery the queue
// daemon otherwise runs on its schedule.
type TagReconcile struct {
	queries      *database.Queries
	taskCreator  types.TaskCreator
	batchCreator types.BatchCreator
}

// NewTagReconcile wires the enqueue path to the queries and the batch and task creators.
func NewTagReconcile(queries *database.Queries, taskCreator types.TaskCreator, batchCreator types.BatchCreator) *TagReconcile {
	return &TagReconcile{
		queries:      queries,
		taskCreator:  taskCreator,
		batchCreator: batchCreator,
	}
}

// Enqueue queues a tag reconcile batch and its task, or reports alreadyQueued when one is still active.
func (s *TagReconcile) Enqueue(ctx context.Context) (batchID string, alreadyQueued bool, err error) {
	active, err := s.queries.CountActiveTagReconcileBatches(ctx)
	if err != nil {
		return "", false, errs.FromDB(err, "count active tag reconcile batches")
	}
	if active > 0 {
		return "", true, nil
	}

	batchID = uuid.New().String()
	taskID := uuid.New().String()

	// a unique violation on the active-dedup index means another reconcile task already owns the slot (the
	// count above lost a race); anything else is a real failure and must not be reported as already queued
	if _, err := s.taskCreator.CreateTask(ctx, types.Task.Type.TagReconcile, batchID, json.RawMessage("{}"), taskID, types.Task.Status.Pending, TagReconcileDedupKey); err != nil {
		if errs.IsConstraint(err) {
			return "", true, nil
		}
		return "", false, errs.FromDB(err, "create tag reconcile task")
	}

	if err := s.batchCreator.Create(ctx, batchID, types.Batch.Source.TagReconcile, types.Batch.Status.Queued); err != nil {
		return "", false, errs.FromDB(errors.Join(err, task.DeleteBatchTasks(context.Background(), s.queries, batchID)), "create tag reconcile batch")
	}

	return batchID, false, nil
}

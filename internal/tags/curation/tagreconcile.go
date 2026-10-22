package curation

import (
	"context"
	"encoding/json"

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
	queries  *database.Queries
	enqueuer *task.Enqueuer
}

// NewTagReconcile wires the enqueue path to the queries and the transactional enqueuer.
func NewTagReconcile(queries *database.Queries, enqueuer *task.Enqueuer) *TagReconcile {
	return &TagReconcile{
		queries:  queries,
		enqueuer: enqueuer,
	}
}

// Enqueue queues a tag reconcile batch and its task through spec, or reports alreadyQueued when one is
// still active.
func (s *TagReconcile) Enqueue(ctx context.Context, spec task.BatchSpec) (batchID string, alreadyQueued bool, err error) {
	active, err := s.queries.CountActiveTagReconcileBatches(ctx)
	if err != nil {
		return "", false, errs.FromDB(err, "count active tag reconcile batches")
	}
	if active > 0 {
		return "", true, nil
	}

	result, err := s.enqueuer.EnqueueTask(ctx, spec, task.TaskSpec{
		Type:    types.Task.Type.TagReconcile,
		Payload: json.RawMessage("{}"),
	})
	if err != nil {
		return "", false, errs.FromDB(err, "create tag reconcile task")
	}
	if result.BatchID == "" {
		return "", true, nil
	}

	return result.BatchID, false, nil
}

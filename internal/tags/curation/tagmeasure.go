package curation

import (
	"context"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TagMeasure enqueues measurement sweeps: the manual trigger for the machinery the queue daemon otherwise
// runs on its schedule.
type TagMeasure struct {
	queries  *database.Queries
	enqueuer *task.Enqueuer
}

// NewTagMeasure wires the enqueue path to the queries and the transactional enqueuer.
func NewTagMeasure(queries *database.Queries, enqueuer *task.Enqueuer) *TagMeasure {
	return &TagMeasure{
		queries:  queries,
		enqueuer: enqueuer,
	}
}

// MeasureSpecs returns the task set a tag measure run enqueues.
func MeasureSpecs() []task.TaskSpec {
	return []task.TaskSpec{{Type: types.Task.Type.TagMeasure, Payload: json.RawMessage("{}")}}
}

// Enqueue queues a tag measure batch and its task through spec, or reports alreadyQueued when one is
// still active.
func (s *TagMeasure) Enqueue(ctx context.Context, spec task.BatchSpec) (batchID string, alreadyQueued bool, err error) {
	active, err := s.queries.CountActiveTagMeasureBatches(ctx)
	if err != nil {
		return "", false, errs.FromDB(err, "count active tag measure batches")
	}
	if active > 0 {
		return "", true, nil
	}

	result, err := s.enqueuer.EnqueueBatch(ctx, spec, MeasureSpecs())
	if err != nil {
		return "", false, errs.FromDB(err, "create tag measure task")
	}
	if result.BatchID == "" {
		return "", true, nil
	}

	return result.BatchID, false, nil
}

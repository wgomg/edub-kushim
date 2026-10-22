package curation

import (
	"context"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TagIdentityDedupKey is the active-dedup key shared by the enqueue path and the task handler so they agree.
const TagIdentityDedupKey = "tagidentity"

// TagIdentity enqueues identity resolution sweeps: the human trigger for the machinery the admission
// resolver serves.
type TagIdentity struct {
	queries  *database.Queries
	enqueuer *task.Enqueuer
}

// NewTagIdentity wires the enqueue path to the queries and the transactional enqueuer.
func NewTagIdentity(queries *database.Queries, enqueuer *task.Enqueuer) *TagIdentity {
	return &TagIdentity{
		queries:  queries,
		enqueuer: enqueuer,
	}
}

// Enqueue queues a tag identity sweep batch and its task through spec, or reports alreadyQueued when
// one is still active.
func (s *TagIdentity) Enqueue(ctx context.Context, spec task.BatchSpec) (batchID string, alreadyQueued bool, err error) {
	active, err := s.queries.CountActiveTagIdentityBatches(ctx)
	if err != nil {
		return "", false, errs.FromDB(err, "count active tag identity batches")
	}
	if active > 0 {
		return "", true, nil
	}

	result, err := s.enqueuer.EnqueueTask(ctx, spec, task.TaskSpec{
		Type:    types.Task.Type.TagIdentity,
		Payload: json.RawMessage("{}"),
	})
	if err != nil {
		return "", false, errs.FromDB(err, "create tag identity task")
	}
	if result.BatchID == "" {
		return "", true, nil
	}

	return result.BatchID, false, nil
}

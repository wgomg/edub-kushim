package dispatch

import (
	"context"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
)

// InProcessRun adapts an in-process host's dependencies to an in-process registration: it builds the task
// store over the client the dispatch passes, marks the run as hosted so no pid is stamped on the lease, and
// runs the leased batch. A failure before the batch was adopted — it was cancelled, paused or is no longer
// a claimed processing batch — releases the claim and hands the batch back to the queue, mirroring the
// launcher's launch-failure recovery; the same call is harmless after a teardown that already requeued.
// deps.Client and deps.Store are supplied per call, so a reconnect needs no cache invalidation.
func InProcessRun(deps task.BatchRunDeps) func(ctx context.Context, client *database.Client, batchID string, token uuid.UUID) error {
	return func(ctx context.Context, client *database.Client, batchID string, token uuid.UUID) error {
		deps.Client = client
		deps.Store = task.NewStore(client)
		deps.InProcess = true
		if _, err := task.RunBatch(ctx, deps, batchID, token); err != nil {
			requeueAfterLaunchFailure(ctx, client, deps.Logger, batchID, token)
			return err
		}
		return nil
	}
}

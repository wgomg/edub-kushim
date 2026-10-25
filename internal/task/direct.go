package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// RunDirect runs body under the manual holder path: it acquires the policy's holds all-or-nothing with a
// NULL token, keeps their time window fresh, drains the in-flight tasks whose waits include a held lock,
// runs the body and releases. wait false fails instead of waiting when a lock is held or the drain set is
// non-empty, the --no-wait contract of the direct commands.
func RunDirect(
	ctx context.Context,
	client *database.Client,
	logger *log.Logger,
	what string,
	policy LockPolicy,
	wait bool,
	onWait func(count int64),
	body func() error,
) error {
	token := uuid.NullUUID{}

	for {
		err := AcquireLocks(ctx, client, policy.Holds, policy.WaitsFor, token, "")
		if err == nil {
			break
		}
		if !errors.Is(err, ErrLockBusy) {
			return err
		}
		if !wait {
			return fmt.Errorf("%s: a required lock is held by another process", what)
		}
		logger.Info("", "%s: a required lock is held by another process — waiting...", what)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := releaseLocksForToken(releaseCtx, client.Queries, token, lockNameStrings(policy.Holds)); err != nil {
			logger.Error("", "%s: release locks: %v", what, err)
		}
	}()

	stop := database.StartLockHeartbeat(ctx, client.Queries, logger, LockHeartbeatInterval, policy.Holds, token)
	defer stop()

	drainTypes := types.TaskTypesWaitingOn(policy.Holds...)
	if !wait {
		count, err := client.Queries.CountProcessingTasks(ctx, drainTypes)
		if err != nil {
			return fmt.Errorf("%s: count in-flight tasks: %w", what, err)
		}
		if count > 0 {
			return fmt.Errorf("%s: %d in-flight task(s) conflict with the run", what, count)
		}
	}
	if err := database.WaitForTaskDrain(ctx, client.Queries, logger, what, drainTypes, onWait); err != nil {
		return err
	}

	return body()
}

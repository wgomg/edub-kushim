package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/ops/consume"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// tagPassTimeout bounds one tag pass in both modes; the passes are long but finite sweeps.
const tagPassTimeout = 6 * time.Hour

// resolveTagPassSelection validates the batch selector and the execution token's shape before anything
// touches the database. A batch-mode invocation must carry the token; a tokenless selector refuses and
// points at batch run.
func resolveTagPassSelection(batchID, execToken string) (string, string, error) {
	if batchID == "" && execToken == "" {
		return "", "", nil
	}
	if batchID == "" {
		return "", "", fmt.Errorf("--exec-token requires --from-batch <id>")
	}
	if execToken == "" {
		return "", "", fmt.Errorf("--from-batch requires --exec-token: run 'kushim batch run <id>' to execute a queued batch")
	}
	if _, err := uuid.Parse(execToken); err != nil {
		return "", "", fmt.Errorf("invalid exec token %q: %w", execToken, err)
	}
	return batchID, execToken, nil
}

// runTagPassBatch executes one claimed tag batch in this child: the dispatcher claim acquired the maintenance
// lock, the handler runs in the registered single-type registry, and its report lands on the task row.
func runTagPassBatch(c *container.Container, batchID, execTokenParam string, taskType types.TaskType, handler task.Handler) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	store := task.NewStore(client)
	registry := task.NewRegistry()
	registry.Register(taskType, handler)
	_, err = consume.RunClaimedBatch(c, batchID, execTokenParam, store, registry, nil)
	return err
}

// runTagPassDirect runs one pass immediately under the type's manual lock declarations: no batch or task
// rows, the lock and drain the queued run would use, and the report printed for the operator.
func runTagPassDirect(c *container.Container, taskType types.TaskType, noWait bool, handler task.Handler) error {
	ctx, cancel := context.WithTimeout(context.Background(), tagPassTimeout)
	defer cancel()

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	policy := task.DirectLockPolicyFor(taskType)
	return task.RunDirect(ctx, client, c.GetLogger(), string(taskType), policy, !noWait, nil, func() error {
		raw, err := handler.Handle(ctx, task.Task{})
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", raw)
		return nil
	})
}

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/ops/consume"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// tagPassTimeout bounds one tag pass in both modes; the passes are long but finite sweeps.
const tagPassTimeout = 6 * time.Hour

// tagPass is the action body of one tag verb: it runs the pass and returns its serialized report, which
// the direct mode prints and the batch mode stores as the task result.
type tagPass func(ctx context.Context) (json.RawMessage, error)

// resolveTagPassSelection folds the deprecated --run alias onto the batch selector and validates the
// execution token's shape before anything touches the database. A batch-mode invocation must carry the
// token; a tokenless --run refuses and points at batch run, because the nested-fork contract that ran a
// pass without a lease and lock no longer exists.
func resolveTagPassSelection(batchID, execToken, runID string) (string, string, error) {
	if runID != "" {
		if batchID != "" {
			return "", "", fmt.Errorf("--run is a deprecated alias of --from-batch; pass only one")
		}
		if execToken == "" {
			return "", "", fmt.Errorf("--run is deprecated and now requires --exec-token: run 'kushim batch run <id>' to execute a queued batch")
		}
		fmt.Fprintln(os.Stderr, "warning: --run is deprecated; use --from-batch with --exec-token")
		batchID = runID
	}
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
// lock, the pass runs in the registered handler, and its report lands on the task row.
func runTagPassBatch(c *container.Container, batchID, execTokenParam string, taskType types.TaskType, pass tagPass) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	store := task.NewStore(client)
	registry := task.NewRegistry()
	registry.Register(taskType, tagPassHandler{pass: pass})
	_, err = consume.RunClaimedBatch(c, batchID, execTokenParam, store, registry, nil)
	return err
}

// runTagPassDirect runs one pass immediately under the type's manual lock declarations: no batch or task
// rows, the lock and drain the queued run would use, and the report printed for the operator.
func runTagPassDirect(c *container.Container, taskType types.TaskType, noWait bool, pass tagPass) error {
	ctx, cancel := context.WithTimeout(context.Background(), tagPassTimeout)
	defer cancel()

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	var raw json.RawMessage
	policy := task.DirectLockPolicyFor(taskType)
	err = task.RunDirect(ctx, client, c.GetLogger(), string(taskType), policy, !noWait, nil, func() error {
		var runErr error
		raw, runErr = pass(ctx)
		return runErr
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", raw)
	return nil
}

// tagPassHandler adapts a tag pass to the task handler the batch-mode registry carries; the returned
// value is the report the task row keeps.
type tagPassHandler struct {
	pass tagPass
}

// Handle runs the pass and returns its report as the task result.
func (h tagPassHandler) Handle(ctx context.Context, _ task.Task) (json.RawMessage, error) {
	return h.pass(ctx)
}

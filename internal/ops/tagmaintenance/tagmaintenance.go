// Package tagmaintenance is the kushim tagmaintenance verb: the direct mode that runs the tag maintenance
// routine — reconciliation, identity resolution, coverage audit and measurement, in that order — right
// under the maintenance lock, and the batch mode that executes one claimed maintenance batch whose four
// tag tasks wait in a chain, each on its predecessor.
package tagmaintenance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tags/reconcile"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const helpText = "Usage: kushim tagmaintenance [--from-batch <id> --exec-token <id>] [--no-wait]\n\n" +
	"  Run the tag maintenance routine: reconcile, identity, coverage audit and\n" +
	"  measurement, in that order.\n\n" +
	"  Runs the routine immediately under the maintenance lock, creating no queue rows.\n" +
	"  --from-batch <id>  execute one claimed batch (dispatcher and 'kushim batch run' contract)\n" +
	"  --exec-token <id>  execution token passed with --from-batch\n" +
	"  --no-wait          exit non-zero instead of waiting for a lock or in-flight work\n\n" +
	"  To enqueue a run: kushim batch create tagmaintenance"

// routineTimeout bounds one direct routine: four long but finite sweeps under a single lock hold.
const routineTimeout = 24 * time.Hour

// Run is the kushim tagmaintenance verb. With --from-batch it executes one claimed maintenance batch under
// the execution token the dispatcher or batch run passed; otherwise it runs the routine immediately,
// creating no batch or task rows. Use "kushim batch create tagmaintenance" to enqueue a run for the queue
// daemon.
func Run(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(helpText) {
		return nil
	}

	var batchID, execTokenParam string
	if err := fp.String("--from-batch", &batchID); err != nil {
		return err
	}
	if err := fp.String("--exec-token", &execTokenParam); err != nil {
		return err
	}
	noWait := false
	fp.Bool("--no-wait", &noWait)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	if batchID != "" || execTokenParam != "" {
		if batchID == "" {
			return fmt.Errorf("--exec-token requires --from-batch <id>")
		}
		if noWait {
			return fmt.Errorf("--no-wait applies to the direct run only")
		}
		return runBatchMode(c, batchID, execTokenParam)
	}
	return runDirect(c, noWait)
}

// runDirect runs the four passes in order under the reconcile type's manual lock policy, printing each
// report with its pass label. The first failed pass stops the routine: the passes after it would read the
// state it was meant to repair.
func runDirect(c *container.Container, noWait bool) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	reconcileHandler, err := newReconcileHandler(c, client)
	if err != nil {
		return err
	}
	identityHandler, err := newIdentityHandler(c, client)
	if err != nil {
		return err
	}
	auditHandler := newAuditHandler(c, client)
	measureHandler := newMeasureHandler(c, client)

	ctx, cancel := context.WithTimeout(context.Background(), routineTimeout)
	defer cancel()

	policy := task.DirectLockPolicyFor(types.Task.Type.TagReconcile)
	return task.RunDirect(ctx, client, c.GetLogger(), "tagmaintenance", policy, !noWait, nil, func() error {
		raw, err := reconcileHandler.Handle(ctx, task.Task{})
		if err != nil {
			return err
		}
		fmt.Printf("reconcile: %s\n", raw)

		if raw, err = identityHandler.Handle(ctx, task.Task{}); err != nil {
			return err
		}
		fmt.Printf("identity: %s\n", raw)

		if raw, err = auditHandler.Handle(ctx, task.Task{}); err != nil {
			return err
		}
		fmt.Printf("audit: %s\n", raw)

		if raw, err = measureHandler.Handle(ctx, task.Task{}); err != nil {
			return err
		}
		fmt.Printf("measure: %s\n", raw)
		return nil
	})
}

// runBatchMode executes one claimed maintenance batch: it builds the routine's handlers, the store and the
// registry, then hands the run to RunClaimedBatch.
func runBatchMode(c *container.Container, batchID, execTokenParam string) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}

	reconcileHandler, err := newReconcileHandler(c, client)
	if err != nil {
		return err
	}
	identityHandler, err := newIdentityHandler(c, client)
	if err != nil {
		return err
	}

	registry := task.NewRegistry()
	registry.Register(types.Task.Type.TagReconcile, reconcileHandler)
	registry.Register(types.Task.Type.TagIdentity, identityHandler)
	registry.Register(types.Task.Type.TagAudit, newAuditHandler(c, client))
	registry.Register(types.Task.Type.TagMeasure, newMeasureHandler(c, client))

	_, err = RunClaimedBatch(c, batchID, execTokenParam, task.NewStore(client), registry)
	return err
}

// RunClaimedBatch executes one claimed maintenance batch through the shared execution library. The dispatcher
// and batch run own the claim and pass the execution token, so this child adopts the lease, runs the batch's
// chained tag tasks through the registry its command assembled and settles. The caller supplies the store
// and the registry because the handlers act on the same store the run does. The catalog is this package's
// own: it names the four tag types with their constant dedup keys, which waiting-successor activation
// derives through this process's catalog — a type without a hook here would be discarded instead of
// activated. An error means the run was interrupted or could not start, and the process must exit non-zero.
func RunClaimedBatch(c *container.Container, batchID, execTokenParam string, store *task.Store, registry *task.Registry) (task.BatchRunResult, error) {
	token, err := parseExecToken(execTokenParam)
	if err != nil {
		return task.BatchRunResult{}, err
	}

	client, err := c.GetClient()
	if err != nil {
		return task.BatchRunResult{}, fmt.Errorf("failed to get database: %w", err)
	}

	catalog, err := task.NewCatalog(map[types.TaskType]task.TypeWiring{
		types.Task.Type.TagReconcile: {DedupKey: task.ConstantDedupKey(curation.TagReconcileDedupKey)},
		types.Task.Type.TagIdentity:  {DedupKey: task.ConstantDedupKey(curation.TagIdentityDedupKey)},
		types.Task.Type.TagAudit:     {DedupKey: task.ConstantDedupKey(curation.TagAuditDedupKey)},
		types.Task.Type.TagMeasure:   {DedupKey: task.ConstantDedupKey(tags.TagMeasureDedupKey)},
	})
	if err != nil {
		return task.BatchRunResult{}, fmt.Errorf("task catalog: %w", err)
	}

	deps := task.BatchRunDeps{
		Client:     client,
		Logger:     c.GetLogger(),
		Catalog:    catalog,
		Store:      store,
		Registry:   registry,
		MaxRetries: int32(c.GetConfig().Consumer.Reclaim.MaxRetries),
	}

	c.GetLogger().SetLevel(log.LevelSilent)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchSignals(ctx, cancel)

	if batch, readErr := client.Queries.GetBatch(ctx, batchID); readErr == nil && batch.Status == types.Batch.Status.Paused {
		fmt.Printf("Batch %s is paused due to an LLM provider credit/balance error.\n", batchID)
		fmt.Printf("Resolve the billing issue, then re-queue the batch and run this command again.\n")
		return task.BatchRunResult{Status: batch.Status, Paused: true}, nil
	}

	result, err := task.RunBatch(ctx, deps, batchID, token)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Println("\nBatch cancelled")
		}
		return result, err
	}
	if result.Paused {
		fmt.Printf("\nBatch paused: LLM provider credit/balance error detected. Resolve billing and re-queue the batch.\n")
	}
	if result.Status == types.Batch.Status.Processing {
		fmt.Printf("\nWarning: batch %s did not settle; tasks remain active and the batch is left to recovery.\n", batchID)
	}
	return result, nil
}

// watchSignals cancels the run on SIGTERM or SIGINT, so a stop reaches the batch through the execution
// library's teardown.
func watchSignals(ctx context.Context, cancel context.CancelFunc) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()
}

// parseExecToken parses the execution token the dispatcher or batch run passes with --from-batch. The
// token is required: the bare selector is no longer a self-claim path.
func parseExecToken(value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, fmt.Errorf("--from-batch requires --exec-token: run 'kushim batch run <id>' to execute a queued batch")
	}
	token, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid exec token %q: %w", value, err)
	}
	return token, nil
}

// newReconcileHandler constructs the reconcile handler over the command's reconciler.
func newReconcileHandler(c *container.Container, client *database.Client) (task.Handler, error) {
	tagService, err := c.GetTagService()
	if err != nil {
		return nil, fmt.Errorf("tag service: %w", err)
	}
	reconciler := reconcile.NewReconciler(
		func() *config.Config { return c.GetConfig() },
		client,
		tagService,
		c.GetTagToolsRunner(),
		c.GetLogger(),
	)
	return reconcile.NewReconcileTaskHandler(reconciler), nil
}

// newIdentityHandler constructs the identity handler over the command's resolver.
func newIdentityHandler(c *container.Container, client *database.Client) (task.Handler, error) {
	tagService, err := c.GetTagService()
	if err != nil {
		return nil, fmt.Errorf("tag service: %w", err)
	}
	resolver := admission.NewIdentityResolver(
		func() *config.Config { return c.GetConfig() },
		c.GetLogger(),
		client,
		tagService,
		c.GetTagToolsRunner(),
	)
	return admission.NewIdentityTaskHandler(resolver), nil
}

// newAuditHandler constructs the audit handler over the command's audit runner.
func newAuditHandler(c *container.Container, client *database.Client) task.Handler {
	runner := admission.NewAuditRunner(
		func() *config.Config { return c.GetConfig() },
		c.GetLogger(),
		client,
		c.GetTagToolsRunner(),
	)
	return admission.NewAuditTaskHandler(runner)
}

// newMeasureHandler constructs the measurement handler over the command's measurement runner.
func newMeasureHandler(c *container.Container, client *database.Client) task.Handler {
	return tags.NewMeasureTaskHandler(tags.NewRunner(client, nil), c.GetLogger())
}

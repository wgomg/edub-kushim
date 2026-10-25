package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tags/reconcile"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const tagReconcileHelpText = "Usage: kushim tagreconcile [--run <batch-id>]\n\n" +
	"  Re-adjudicate the stored tag evidence: re-check stale, unjudged, drifted\n" +
	"  and aged pairs, split stored compounds, canonicalize twins, sweep dead and\n" +
	"  provisional tags, and revoke influence where the decision says so.\n\n" +
	"  Without flags: enqueue a blocking task and print its batch id; the queue\n" +
	"  daemon claims it under the maintenance lock.\n" +
	"  --run <batch-id>: the queued run's execution mode (no lock, no drain)."

func tagReconcileHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagReconcileHelpText) {
		return nil
	}

	var batchID string
	fp.String("--run", &batchID)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	if batchID != "" {
		if _, err := uuid.Parse(batchID); err != nil {
			return fmt.Errorf("--run: invalid batch id %q", batchID)
		}
		return runTagReconcilePass(c, batchID)
	}
	return enqueueTagReconcile(c)
}

// enqueueTagReconcile creates the queued batch and its blocking task, or reports that a run is already
// active; it writes no lock and never waits.
func enqueueTagReconcile(c *container.Container) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	enqueuer, err := newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
		types.Task.Type.TagReconcile: task.ConstantDedupKey(curation.TagReconcileDedupKey),
	})
	if err != nil {
		return err
	}
	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}
	batchID, alreadyQueued, err := curation.NewTagReconcile(client.Queries, enqueuer).Enqueue(context.Background(), spec)
	if err != nil {
		return fmt.Errorf("enqueue tag reconcile: %w", err)
	}
	if alreadyQueued {
		fmt.Println("A tag reconcile run is already queued or processing.")
		return nil
	}

	fmt.Printf("Batch %s queued — run 'kushim queue' to process\n", batchID)
	return nil
}

// runTagReconcilePass runs one reconcile pass as the queued task's forked child; the claiming parent
// holds the maintenance lock and already drained.
func runTagReconcilePass(c *container.Container, batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	tagService, err := c.GetTagService()
	if err != nil {
		return fmt.Errorf("dispatcher: %w", err)
	}

	reconciler := reconcile.NewReconciler(
		func() *config.Config { return c.GetConfig() },
		client,
		tagService,
		c.GetTagToolsRunner(),
		c.GetLogger(),
	)

	report, err := reconciler.Run(ctx, nil)
	if err != nil {
		return fmt.Errorf("tag reconcile: %w", err)
	}

	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal tag reconcile report: %w", err)
	}
	c.GetLogger().Info("", "tag reconcile run %s: report_only=%t", batchID, report.ReportOnly)
	fmt.Printf("%s%s\n", reconcile.ReportPrefix, raw)
	return nil
}

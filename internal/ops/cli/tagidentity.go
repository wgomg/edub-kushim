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
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const tagIdentityHelpText = "Usage: kushim tagidentity [--run <batch-id>]\n\n" +
	"  Adjudicate the pending identity evidence: resolve the flagged candidates'\n" +
	"  base-form criteria and the same-intent pairs, then apply the dispositions.\n\n" +
	"  Without flags: enqueue a blocking task and print its batch id; the queue\n" +
	"  daemon claims it under the maintenance lock.\n" +
	"  --run <batch-id>: the queued run's execution mode (no lock, no drain).\n" +
	"  The run also fires on its own daily schedule while the tag adjudicator is on."

func tagIdentityHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagIdentityHelpText) {
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
		return runTagIdentityPass(c, batchID)
	}
	return enqueueTagIdentity(c)
}

// enqueueTagIdentity creates the queued batch and its blocking task, or reports that a run is already
// active; it writes no lock and never waits.
func enqueueTagIdentity(c *container.Container) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	catalog, err := task.NewCatalog(map[types.TaskType]task.DedupKeyFunc{
		types.Task.Type.TagIdentity: task.ConstantDedupKey(curation.TagIdentityDedupKey),
	}, nil)
	if err != nil {
		return fmt.Errorf("task catalog: %w", err)
	}
	enqueuer := task.NewEnqueuer(client, catalog)
	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}
	batchID, alreadyQueued, err := curation.NewTagIdentity(client.Queries, enqueuer).Enqueue(context.Background(), spec)
	if err != nil {
		return fmt.Errorf("enqueue tag identity: %w", err)
	}
	if alreadyQueued {
		fmt.Println("A tag identity run is already queued or processing.")
		return nil
	}

	fmt.Printf("Batch %s queued — run 'kushim queue' to process\n", batchID)
	return nil
}

func runTagIdentityPass(c *container.Container, batchID string) error {
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

	resolver := admission.NewIdentityResolver(
		func() *config.Config { return c.GetConfig() },
		c.GetLogger(),
		client,
		tagService,
		c.GetTagToolsRunner(),
	)

	report, err := resolver.ResolvePending(ctx, c.GetConfig().Enricher.TagReconcile.MaxRejudge)
	if err != nil {
		return fmt.Errorf("tag identity: %w", err)
	}

	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal tag identity report: %w", err)
	}
	c.GetLogger().Info("", "tag identity run %s: pending=%d resolved=%d folded=%d aliased=%d deferred=%d failed=%d",
		batchID, report.Pending, report.Resolved, report.Folded, report.Aliased, report.Deferred, report.Failed)
	fmt.Printf("%s%s\n", admission.IdentityReportPrefix, raw)
	return nil
}

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
)

const tagMeasureHelpText = "Usage: kushim tagmeasure [--run <batch-id>]\n\n" +
	"  Measure the tag store's extension algebra: per-value mass, pairwise\n" +
	"  overlap, compound reconstructibility, and part coverage, written as\n" +
	"  versioned evidence records.\n\n" +
	"  Without flags: enqueue a blocking task and print its batch id; the queue\n" +
	"  daemon claims it under the maintenance lock.\n" +
	"  --run <batch-id>: the queued run's execution mode (no lock, no drain)."

func tagMeasureHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagMeasureHelpText) {
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
		return runTagMeasurePass(c, batchID)
	}
	return enqueueTagMeasure(c)
}

// enqueueTagMeasure creates the queued batch and its blocking task, or reports that a run is already
// active; it writes no lock and never waits.
func enqueueTagMeasure(c *container.Container) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	enqueuer, err := c.GetEnqueuer()
	if err != nil {
		return fmt.Errorf("enqueuer: %w", err)
	}
	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}
	batchID, alreadyQueued, err := curation.NewTagMeasure(client.Queries, enqueuer).Enqueue(context.Background(), spec)
	if err != nil {
		return fmt.Errorf("enqueue tag measure: %w", err)
	}
	if alreadyQueued {
		fmt.Println("A tag measure run is already queued or processing.")
		return nil
	}

	fmt.Printf("Batch %s queued — run 'kushim queue' to process\n", batchID)
	return nil
}

// runTagMeasurePass runs one measurement pass as the queued task's forked child; the claiming parent
// holds the maintenance lock and already drained.
func runTagMeasurePass(c *container.Container, batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	if err := logTagMeasureStart(ctx, c, client); err != nil {
		return err
	}
	report, err := tags.NewRunner(client, nil).Run(ctx, uuid.New())
	if err != nil {
		return fmt.Errorf("tag measure: %w", err)
	}
	return printTagMeasureReport(c, report, batchID)
}

// logTagMeasureStart records the sweep's opening line: how many tags the
// store holds before the snapshot is read.
func logTagMeasureStart(ctx context.Context, c *container.Container, client *database.Client) error {
	count, err := client.Queries.CountTags(ctx)
	if err != nil {
		return fmt.Errorf("count tags: %w", err)
	}
	c.GetLogger().Info("", "tag measure run starting: %d tags in store", count)
	return nil
}

func printTagMeasureReport(c *container.Container, report *tags.Report, batchID string) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal tag measure report: %w", err)
	}

	c.GetLogger().Info("", "tag measure run %s (batch %s): snapshot=%s rule_set=%s tags=%d operators=%v states=%v skipped=%v worklists=%+v",
		report.RunID, batchID, report.SnapshotAt.Format(time.RFC3339), report.RuleSetVersion, report.Tags,
		report.Operators, report.States, report.Skipped, report.Worklists)

	fmt.Printf("%s%s\n", tags.ReportPrefix, raw)
	return nil
}

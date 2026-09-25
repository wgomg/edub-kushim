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
)

const tagMeasureHelpText = "Usage: kushim tagmeasure [--run <batch-id>]\n\n" +
	"  Measure the tag store's extension algebra: per-value mass, pairwise\n" +
	"  overlap, compound reconstructibility, and part coverage, written as\n" +
	"  versioned evidence records.\n\n" +
	"  Without flags: run in the foreground under the maintenance lock.\n" +
	"  --run <batch-id>: the scheduled fork's execution mode (no lock).\n\n" +
	"  Exits non-zero on failure; the scheduled path is enqueued by the queue daemon."

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

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	if batchID != "" {
		if _, err := uuid.Parse(batchID); err != nil {
			return fmt.Errorf("--run: invalid batch id %q", batchID)
		}
		return runTagMeasurePass(c, client, batchID)
	}
	return runTagMeasureManual(c, client)
}

// The scheduled path's child: the claiming parent holds the tokenized lock and
// drained in-flight work before forking.
func runTagMeasurePass(c *container.Container, client *database.Client, batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()

	report, err := tags.NewRunner(client, nil).Run(ctx, uuid.New())
	if err != nil {
		return fmt.Errorf("tag measure: %w", err)
	}
	return printTagMeasureReport(c, report, batchID)
}

func runTagMeasureManual(c *container.Container, client *database.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()

	release, err := database.HoldMaintenanceLock(ctx, client.Queries, c.GetLogger(), "tag measure", uuid.NullUUID{}, nil)
	if err != nil {
		return err
	}
	defer release()

	report, err := tags.NewRunner(client, nil).Run(ctx, uuid.New())
	if err != nil {
		return fmt.Errorf("tag measure: %w", err)
	}
	return printTagMeasureReport(c, report, "")
}

func printTagMeasureReport(c *container.Container, report *tags.Report, batchID string) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal tag measure report: %w", err)
	}

	batchSuffix := ""
	if batchID != "" {
		batchSuffix = fmt.Sprintf(" (batch %s)", batchID)
	}
	c.GetLogger().Info("", "tag measure run %s%s: snapshot=%s rule_set=%s tags=%d operators=%v states=%v skipped=%v worklists=%+v",
		report.RunID, batchSuffix, report.SnapshotAt.Format(time.RFC3339), report.RuleSetVersion, report.Tags,
		report.Operators, report.States, report.Skipped, report.Worklists)

	fmt.Printf("%s%s\n", tags.ReportPrefix, raw)
	return nil
}

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
	"github.com/wgomg/edub-kushim/internal/types"
)

const tagMeasureHelpText = "Usage: kushim tagmeasure [--from-batch <id> --exec-token <id>] [--no-wait]\n\n" +
	"  Measure the tag store's extension algebra: per-value mass, pairwise\n" +
	"  overlap, compound reconstructibility, and part coverage, written as\n" +
	"  versioned evidence records.\n\n" +
	"  Runs the pass immediately under the maintenance lock, creating no queue rows.\n" +
	"  --from-batch <id>  execute one claimed batch (dispatcher and 'kushim batch run' contract)\n" +
	"  --exec-token <id>  execution token passed with --from-batch\n" +
	"  --no-wait          exit non-zero instead of waiting for a lock or in-flight work\n\n" +
	"  To enqueue a measurement: kushim batch create tagmeasure"

func tagMeasureHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagMeasureHelpText) {
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

	batchID, execTokenParam, err := resolveTagPassSelection(batchID, execTokenParam)
	if err != nil {
		return err
	}
	if batchID != "" || execTokenParam != "" {
		if noWait {
			return fmt.Errorf("--no-wait applies to the direct run only")
		}
		return runTagPassBatch(c, batchID, execTokenParam, types.Task.Type.TagMeasure, func(ctx context.Context) (json.RawMessage, error) {
			return tagMeasurePass(ctx, c)
		})
	}
	return runTagPassDirect(c, types.Task.Type.TagMeasure, noWait, func(ctx context.Context) (json.RawMessage, error) {
		return tagMeasurePass(ctx, c)
	})
}

// tagMeasurePass runs one measurement pass and returns its serialized report, the shared body of the
// direct and batch modes. The batch id never enters the measurement's run keys: the run id stays fresh.
func tagMeasurePass(ctx context.Context, c *container.Container) (json.RawMessage, error) {
	client, err := c.GetClient()
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := logTagMeasureStart(ctx, c, client); err != nil {
		return nil, err
	}
	report, err := tags.NewRunner(client, nil).Run(ctx, uuid.New())
	if err != nil {
		return nil, fmt.Errorf("tag measure: %w", err)
	}
	return marshalTagMeasureReport(c, report)
}

// logTagMeasureStart records the sweep's opening line: how many tags the store holds before the snapshot
// is read.
func logTagMeasureStart(ctx context.Context, c *container.Container, client *database.Client) error {
	count, err := client.Queries.CountTags(ctx)
	if err != nil {
		return fmt.Errorf("count tags: %w", err)
	}
	c.GetLogger().Info("", "tag measure run starting: %d tags in store", count)
	return nil
}

func marshalTagMeasureReport(c *container.Container, report *tags.Report) (json.RawMessage, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal tag measure report: %w", err)
	}

	c.GetLogger().Info("", "tag measure run %s: snapshot=%s rule_set=%s tags=%d operators=%v states=%v skipped=%v worklists=%+v",
		report.RunID, report.SnapshotAt.Format(time.RFC3339), report.RuleSetVersion, report.Tags,
		report.Operators, report.States, report.Skipped, report.Worklists)
	return raw, nil
}

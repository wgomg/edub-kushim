package cli

import (
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/task"
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

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	handler := newMeasureHandler(c, client)

	if batchID != "" || execTokenParam != "" {
		if noWait {
			return fmt.Errorf("--no-wait applies to the direct run only")
		}
		return runTagPassBatch(c, batchID, execTokenParam, types.Task.Type.TagMeasure, handler)
	}
	return runTagPassDirect(c, types.Task.Type.TagMeasure, noWait, handler)
}

// newMeasureHandler constructs the measurement handler over the command's measurement runner.
func newMeasureHandler(c *container.Container, client *database.Client) task.Handler {
	return tags.NewMeasureTaskHandler(tags.NewRunner(client, nil), c.GetLogger())
}

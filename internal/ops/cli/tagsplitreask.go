package cli

import (
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tags/reconcile"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const tagSplitReaskHelpText = "Usage: kushim tagsplitreask [--from-batch <id> --exec-token <id>] [--no-wait]\n\n" +
	"  Re-ask the split gate's cached abstentions: stale part and compound deferrals\n" +
	"  are judged again so a noisy deferral cannot withhold a compound forever.\n\n" +
	"  Runs the pass immediately under the maintenance lock, creating no queue rows.\n" +
	"  --from-batch <id>  execute one claimed batch (dispatcher and 'kushim batch run' contract)\n" +
	"  --exec-token <id>  execution token passed with --from-batch\n" +
	"  --no-wait          exit non-zero instead of waiting for a lock or in-flight work\n\n" +
	"  To enqueue a run: kushim batch create tagsplitreask\n" +
	"  The run also fires on its own daily schedule while the tag adjudicator is on."

func tagSplitReaskHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagSplitReaskHelpText) {
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
	handler := newSplitReaskHandler(c, client)

	if batchID != "" || execTokenParam != "" {
		if noWait {
			return fmt.Errorf("--no-wait applies to the direct run only")
		}
		return runTagPassBatch(c, batchID, execTokenParam, types.Task.Type.TagSplitReask, handler)
	}
	return runTagPassDirect(c, types.Task.Type.TagSplitReask, noWait, handler)
}

// newSplitReaskHandler constructs the split re-ask handler over the command's judge runner.
func newSplitReaskHandler(c *container.Container, client *database.Client) task.Handler {
	runner := reconcile.NewReaskRunner(
		func() *config.Config { return c.GetConfig() },
		c.GetLogger(),
		client,
		c.GetTagToolsRunner(),
	)
	return reconcile.NewReaskTaskHandler(runner)
}

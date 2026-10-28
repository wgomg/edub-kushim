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

const tagReconcileHelpText = "Usage: kushim tagreconcile [--from-batch <id> --exec-token <id>] [--no-wait]\n\n" +
	"  Re-adjudicate the stored tag evidence: re-check stale, unjudged, drifted\n" +
	"  and aged pairs, split stored compounds, canonicalize twins, sweep dead and\n" +
	"  provisional tags, and revoke influence where the decision says so.\n\n" +
	"  Runs the pass immediately under the maintenance lock, creating no queue rows.\n" +
	"  --from-batch <id>  execute one claimed batch (dispatcher and 'kushim batch run' contract)\n" +
	"  --exec-token <id>  execution token passed with --from-batch\n" +
	"  --no-wait          exit non-zero instead of waiting for a lock or in-flight work\n\n" +
	"  To enqueue a reconcile pass: kushim batch create tagreconcile"

func tagReconcileHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagReconcileHelpText) {
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
	handler, err := newReconcileHandler(c, client)
	if err != nil {
		return err
	}

	if batchID != "" || execTokenParam != "" {
		if noWait {
			return fmt.Errorf("--no-wait applies to the direct run only")
		}
		return runTagPassBatch(c, batchID, execTokenParam, types.Task.Type.TagReconcile, handler)
	}
	return runTagPassDirect(c, types.Task.Type.TagReconcile, noWait, handler)
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

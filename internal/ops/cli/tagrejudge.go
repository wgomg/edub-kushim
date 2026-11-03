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

const tagRejudgeHelpText = "Usage: kushim tagrejudge --from-batch <id> --exec-token <id>\n\n" +
	"  Re-run one judge over one stored tag, refreshing the evidence the tag review\n" +
	"  queue reads: the quality judge over the value's first live document, or the\n" +
	"  composition judge over the compound.\n\n" +
	"  Runs one queued re-judge task; the API enqueues it for a review action.\n" +
	"  --from-batch <id>  execute one claimed batch (dispatcher and 'kushim batch run' contract)\n" +
	"  --exec-token <id>  execution token passed with --from-batch"

// tagRejudgeHandler executes one queued re-judge batch. The task payload names the tag and the scope,
// so there is no direct mode: an operator action in the review queue is the only producer.
func tagRejudgeHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagRejudgeHelpText) {
		return nil
	}

	var batchID, execTokenParam string
	if err := fp.String("--from-batch", &batchID); err != nil {
		return err
	}
	if err := fp.String("--exec-token", &execTokenParam); err != nil {
		return err
	}
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	batchID, execTokenParam, err := resolveTagPassSelection(batchID, execTokenParam)
	if err != nil {
		return err
	}
	if batchID == "" {
		return fmt.Errorf("tagrejudge runs one queued task: the tag review queue enqueues it")
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	handler, err := newRejudgeHandler(c, client)
	if err != nil {
		return err
	}
	return runTagPassBatch(c, batchID, execTokenParam, types.Task.Type.TagRejudge, handler)
}

// newRejudgeHandler constructs the re-judge handler over the command's reconciler.
func newRejudgeHandler(c *container.Container, client *database.Client) (task.Handler, error) {
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
	return reconcile.NewRejudgeTaskHandler(reconciler), nil
}

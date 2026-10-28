package cli

import (
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const tagIdentityHelpText = "Usage: kushim tagidentity [--from-batch <id> --exec-token <id>] [--no-wait]\n\n" +
	"  Adjudicate the pending identity evidence: resolve the flagged candidates'\n" +
	"  base-form criteria and the same-intent pairs, then apply the dispositions.\n\n" +
	"  Runs the pass immediately under the maintenance lock, creating no queue rows.\n" +
	"  --from-batch <id>  execute one claimed batch (dispatcher and 'kushim batch run' contract)\n" +
	"  --exec-token <id>  execution token passed with --from-batch\n" +
	"  --no-wait          exit non-zero instead of waiting for a lock or in-flight work\n\n" +
	"  To enqueue a run: kushim batch create tagidentity\n" +
	"  The run also fires on its own daily schedule while the tag adjudicator is on."

func tagIdentityHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagIdentityHelpText) {
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
	handler, err := newIdentityHandler(c, client)
	if err != nil {
		return err
	}

	if batchID != "" || execTokenParam != "" {
		if noWait {
			return fmt.Errorf("--no-wait applies to the direct run only")
		}
		return runTagPassBatch(c, batchID, execTokenParam, types.Task.Type.TagIdentity, handler)
	}
	return runTagPassDirect(c, types.Task.Type.TagIdentity, noWait, handler)
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

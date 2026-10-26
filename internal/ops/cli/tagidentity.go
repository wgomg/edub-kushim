package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/types"
)

const tagIdentityHelpText = "Usage: kushim tagidentity [--from-batch <id> --exec-token <id>] [--no-wait]\n\n" +
	"  Adjudicate the pending identity evidence: resolve the flagged candidates'\n" +
	"  base-form criteria and the same-intent pairs, then apply the dispositions.\n\n" +
	"  Runs the pass immediately under the maintenance lock, creating no queue rows.\n" +
	"  --from-batch <id>  execute one claimed batch (dispatcher and 'kushim batch run' contract)\n" +
	"  --exec-token <id>  execution token passed with --from-batch\n" +
	"  --no-wait          exit non-zero instead of waiting for a lock or in-flight work\n" +
	"  --run <id>         deprecated alias of --from-batch\n\n" +
	"  To enqueue a run: kushim batch create tagidentity\n" +
	"  The run also fires on its own daily schedule while the tag adjudicator is on."

func tagIdentityHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagIdentityHelpText) {
		return nil
	}

	var batchID, execTokenParam, runID string
	if err := fp.String("--from-batch", &batchID); err != nil {
		return err
	}
	if err := fp.String("--exec-token", &execTokenParam); err != nil {
		return err
	}
	if err := fp.String("--run", &runID); err != nil {
		return err
	}
	noWait := false
	fp.Bool("--no-wait", &noWait)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	batchID, execTokenParam, err := resolveTagPassSelection(batchID, execTokenParam, runID)
	if err != nil {
		return err
	}
	if batchID != "" || execTokenParam != "" {
		if noWait {
			return fmt.Errorf("--no-wait applies to the direct run only")
		}
		return runTagPassBatch(c, batchID, execTokenParam, types.Task.Type.TagIdentity, func(ctx context.Context) (json.RawMessage, error) {
			return tagIdentityPass(ctx, c)
		})
	}
	return runTagPassDirect(c, types.Task.Type.TagIdentity, noWait, func(ctx context.Context) (json.RawMessage, error) {
		return tagIdentityPass(ctx, c)
	})
}

// tagIdentityPass runs one identity resolution pass and returns its serialized report, the shared body of
// the direct and batch modes. The pass owns its no-op gate.
func tagIdentityPass(ctx context.Context, c *container.Container) (json.RawMessage, error) {
	client, err := c.GetClient()
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	tagService, err := c.GetTagService()
	if err != nil {
		return nil, fmt.Errorf("dispatcher: %w", err)
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
		return nil, fmt.Errorf("tag identity: %w", err)
	}

	raw, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal tag identity report: %w", err)
	}
	c.GetLogger().Info("", "tag identity run: pending=%d resolved=%d folded=%d aliased=%d deferred=%d failed=%d",
		report.Pending, report.Resolved, report.Folded, report.Aliased, report.Deferred, report.Failed)
	return raw, nil
}

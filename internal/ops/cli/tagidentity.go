package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tools"
)

const tagIdentityHelpText = "Usage: kushim tagidentity --run <batch-id>\n\n" +
	"  Adjudicate the pending identity evidence: resolve the flagged candidates'\n" +
	"  base-form criteria and the same-intent pairs, then apply the dispositions.\n\n" +
	"  --run <batch-id>: the queued trigger's execution mode (no lock, no drain).\n" +
	"  The trigger is the review surface's reevaluate action; the provisional\n" +
	"  sweep is the standing backstop."

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
	if batchID == "" {
		return fmt.Errorf("--run <batch-id> is required")
	}
	if _, err := uuid.Parse(batchID); err != nil {
		return fmt.Errorf("--run: invalid batch id %q", batchID)
	}

	return runTagIdentityPass(c, batchID)
}

func runTagIdentityPass(c *container.Container, batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	services, err := c.GetServices()
	if err != nil {
		return fmt.Errorf("dispatcher: %w", err)
	}

	cfg := c.GetConfig()
	matcherClient := matcher.NewMatcherClient(c.SocketPath(), matcher.MaxMatchBodyBytes(cfg.Enricher.TagMatcher.ReduceTargetWords))
	runner := tools.NewRunnerWithMatcher(c.GetLogger(), cfg, []string{"textreducer", "contentanalyzer", "tagmatcher"}, matcherClient)
	resolver := enrichment.NewIdentityResolver(
		func() *config.Config { return c.GetConfig() },
		c.GetLogger(),
		client,
		services,
		runner,
	)

	report, err := resolver.ResolvePending(ctx, cfg.Enricher.TagReconcile.MaxRejudge)
	if err != nil {
		return fmt.Errorf("tag identity: %w", err)
	}

	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal tag identity report: %w", err)
	}
	c.GetLogger().Info(nil, "tag identity run %s: pending=%d resolved=%d folded=%d aliased=%d deferred=%d failed=%d",
		batchID, report.Pending, report.Resolved, report.Folded, report.Aliased, report.Deferred, report.Failed)
	fmt.Printf("%s%s\n", enrichment.IdentityReportPrefix, raw)
	return nil
}

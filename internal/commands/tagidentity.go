package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tools"
)

const tagIdentityHelpText = "Usage: kushim tagidentity --run <batch-id>\n\n" +
	"  Adjudicate the pending identity evidence: resolve the flagged candidates'\n" +
	"  base-form criteria and the same-intent pairs, then apply the dispositions.\n\n" +
	"  --run <batch-id>: the queued trigger's execution mode (no lock, no drain).\n" +
	"  The trigger is the review surface's reevaluate action; the provisional\n" +
	"  sweep is the standing backstop."

func tagIdentityHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
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

func runTagIdentityPass(c *Container, batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	if _, err := c.GetDispatcher(); err != nil {
		return fmt.Errorf("dispatcher: %w", err)
	}

	cfg := c.cfg.Load()
	matcherClient := matcher.NewMatcherClient(c.socketPath(), matcher.MaxMatchBodyBytes(cfg.Enricher.TagMatcher.ReduceTargetWords))
	runner := tools.NewRunnerWithMatcher(c.logger, cfg, []string{"textreducer", "contentanalyzer", "tagmatcher"}, matcherClient)
	resolver := enrichment.NewIdentityResolver(
		func() *config.Config { return c.cfg.Load() },
		c.logger,
		client,
		c.services,
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
	c.logger.Info(nil, "tag identity run %s: pending=%d resolved=%d folded=%d aliased=%d deferred=%d failed=%d",
		batchID, report.Pending, report.Resolved, report.Folded, report.Aliased, report.Deferred, report.Failed)
	fmt.Printf("%s%s\n", enrichment.IdentityReportPrefix, raw)
	return nil
}

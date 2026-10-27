package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/types"
)

const tagAuditHelpText = "Usage: kushim tagaudit [--from-batch <id> --exec-token <id>] [--no-wait]\n\n" +
	"  Audit the documents' coverage against the tag vocabulary: sample and\n" +
	"  flag the documents, verify the applicable dimensions with the audit\n" +
	"  judge, fill the confirmed gaps, and record the coverage classes.\n\n" +
	"  Runs the pass immediately under the maintenance lock, creating no queue rows.\n" +
	"  --from-batch <id>  execute one claimed batch (dispatcher and 'kushim batch run' contract)\n" +
	"  --exec-token <id>  execution token passed with --from-batch\n" +
	"  --no-wait          exit non-zero instead of waiting for a lock or in-flight work\n\n" +
	"  To enqueue an audit: kushim batch create tagaudit"

func tagAuditHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagAuditHelpText) {
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
		return runTagPassBatch(c, batchID, execTokenParam, types.Task.Type.TagAudit, func(ctx context.Context) (json.RawMessage, error) {
			return tagAuditPass(ctx, c)
		})
	}
	return runTagPassDirect(c, types.Task.Type.TagAudit, noWait, func(ctx context.Context) (json.RawMessage, error) {
		return tagAuditPass(ctx, c)
	})
}

// tagAuditPass runs one coverage audit and returns its serialized report, the shared body of the direct
// and batch modes. The run id stays fresh, matching the tagmeasure convention so batch ids never enter the
// measurement substrate's run keys.
func tagAuditPass(ctx context.Context, c *container.Container) (json.RawMessage, error) {
	client, err := c.GetClient()
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}

	runner := admission.NewAuditRunner(
		func() *config.Config { return c.GetConfig() },
		c.GetLogger(),
		client,
		c.GetTagToolsRunner(),
	)

	report, err := runner.Run(ctx, uuid.New())
	if err != nil {
		return nil, fmt.Errorf("tag audit: %w", err)
	}

	c.GetLogger().Info("", "tag audit run %s: sweep=%d sampled=%d flagged=%d fills=%d flags=%d pending_coverage=%d",
		report.RunID, report.SweepCount, report.Sampled, report.Flagged, report.Fills, report.Flags, report.PendingCoverage)

	raw, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal tag audit report: %w", err)
	}
	return raw, nil
}

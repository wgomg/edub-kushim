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

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	handler := newAuditHandler(c, client)

	if batchID != "" || execTokenParam != "" {
		if noWait {
			return fmt.Errorf("--no-wait applies to the direct run only")
		}
		return runTagPassBatch(c, batchID, execTokenParam, types.Task.Type.TagAudit, handler)
	}
	return runTagPassDirect(c, types.Task.Type.TagAudit, noWait, handler)
}

// newAuditHandler constructs the audit handler over the command's audit runner.
func newAuditHandler(c *container.Container, client *database.Client) task.Handler {
	runner := admission.NewAuditRunner(
		func() *config.Config { return c.GetConfig() },
		c.GetLogger(),
		client,
		c.GetTagToolsRunner(),
	)
	return admission.NewAuditTaskHandler(runner)
}

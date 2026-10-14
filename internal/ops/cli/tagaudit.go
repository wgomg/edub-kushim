package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
)

const tagAuditHelpText = "Usage: kushim tagaudit [--run <batch-id>]\n\n" +
	"  Audit the documents' coverage against the tag vocabulary: sample and\n" +
	"  flag the documents, verify the applicable dimensions with the audit\n" +
	"  judge, fill the confirmed gaps, and record the coverage classes.\n\n" +
	"  Without flags: run in the foreground under the maintenance lock.\n" +
	"  --run <batch-id>: the scheduled fork's execution mode (no lock)."

func tagAuditHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(tagAuditHelpText) {
		return nil
	}

	var batchID string
	fp.String("--run", &batchID)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	return runTagAuditPass(c, batchID)
}

// runTagAuditPass runs the audit; a manual invocation (no batch id) holds the maintenance lock. The
// scheduled fork's batch id only labels the run: the run id stays fresh, matching the tagmeasure convention
// so batch ids never enter the measurement substrate's run keys.
func runTagAuditPass(c *container.Container, batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	if batchID != "" {
		if _, err := uuid.Parse(batchID); err != nil {
			return fmt.Errorf("--run: invalid batch id %q", batchID)
		}
	}

	if batchID == "" {
		release, err := database.HoldMaintenanceLock(ctx, client.Queries, c.GetLogger(), "tag audit", uuid.NullUUID{}, nil)
		if err != nil {
			return err
		}
		defer release()
	}

	runner := admission.NewAuditRunner(
		func() *config.Config { return c.GetConfig() },
		c.GetLogger(),
		client,
		c.GetTagToolsRunner(),
	)

	report, err := runner.Run(ctx, uuid.New())
	if err != nil {
		return fmt.Errorf("tag audit: %w", err)
	}

	if batchID != "" {
		c.GetLogger().Info("", "tag audit batch %s: run %s", batchID, report.RunID)
	}

	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal tag audit report: %w", err)
	}
	fmt.Printf("%s%s\n", admission.AuditReportPrefix, raw)
	return nil
}

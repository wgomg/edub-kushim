package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const tagAuditHelpText = "Usage: kushim tagaudit [--run <batch-id>]\n\n" +
	"  Audit the documents' coverage against the tag vocabulary: sample and\n" +
	"  flag the documents, verify the applicable dimensions with the audit\n" +
	"  judge, fill the confirmed gaps, and record the coverage classes.\n\n" +
	"  Without flags: enqueue a blocking task and print its batch id; the queue\n" +
	"  daemon claims it under the maintenance lock.\n" +
	"  --run <batch-id>: the queued run's execution mode (no lock, no drain)."

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

	if batchID != "" {
		if _, err := uuid.Parse(batchID); err != nil {
			return fmt.Errorf("--run: invalid batch id %q", batchID)
		}
		return runTagAuditPass(c, batchID)
	}
	return enqueueTagAudit(c)
}

// enqueueTagAudit creates the queued audit batch and its blocking task, or reports that a run is already
// active; it writes no lock and never waits.
func enqueueTagAudit(c *container.Container) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	enqueuer, err := newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
		types.Task.Type.TagAudit: task.ConstantDedupKey(curation.TagAuditDedupKey),
	})
	if err != nil {
		return err
	}
	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}
	batchID, alreadyQueued, err := curation.NewTagAudit(client.Queries, enqueuer).Enqueue(context.Background(), spec)
	if err != nil {
		return fmt.Errorf("enqueue tag audit: %w", err)
	}
	if alreadyQueued {
		fmt.Println("A tag audit run is already queued or processing.")
		return nil
	}

	fmt.Printf("Batch %s queued — run 'kushim queue' to process\n", batchID)
	return nil
}

// runTagAuditPass runs one coverage audit as the queued task's forked child; the claiming parent holds
// the maintenance lock and already drained. The batch id only labels the run: the run id stays fresh,
// matching the tagmeasure convention so batch ids never enter the measurement substrate's run keys.
func runTagAuditPass(c *container.Container, batchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
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

	c.GetLogger().Info("", "tag audit batch %s: run %s", batchID, report.RunID)

	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal tag audit report: %w", err)
	}
	fmt.Printf("%s%s\n", admission.AuditReportPrefix, raw)
	return nil
}

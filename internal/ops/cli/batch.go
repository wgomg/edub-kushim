package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/dispatch"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const batchHelpText = "Usage: kushim batch <subcommand> [args]\n\n" +
	"  Queue surface: create, run and manage batches.\n\n" +
	"Subcommands:\n" +
	"  create <task> [args]            Enqueue a task set for the queue daemon\n" +
	"  run <batch-id>                  Claim a queued batch and run its task CLI now\n" +
	"  stop <batch-id>                 Cancel a batch (alias: cancel)\n" +
	"  resume <batch-id>               Requeue a paused, failed or cancelled batch\n" +
	"  restart <batch-id>              Resume a batch and retry its failed tasks\n" +
	"  list [--limit N] [--offset N]   List recent batches\n" +
	"  status <batch-id>               Show one batch's state and task counts\n\n" +
	"Tasks for create:\n" +
	"  consume                                       Scan the inbox and enqueue an ingest batch\n" +
	"  backup --mode <full|database|documents> [--path <dir>] [--keep N]\n" +
	"  mirror [--path <dir>]\n" +
	"  enrich <document-id>... | --all | --limit N\n" +
	"  thumbnails [--from-batch <id> | --document <id>]\n" +
	"  tagreconcile | tagmeasure | tagidentity | tagaudit\n\n" +
	"  Direct commands (for example 'kushim backup') run immediately and create\n" +
	"  no queue rows; this command is the queue surface."

const batchCreateHelpText = "Usage: kushim batch create <task> [args]\n\n" +
	"  consume\n" +
	"  backup --mode <full|database|documents> [--path <dir>] [--keep N]\n" +
	"  mirror [--path <dir>]\n" +
	"  enrich <document-id>... | --all | --limit N\n" +
	"  thumbnails [--from-batch <id> | --document <id>]\n" +
	"  tagreconcile | tagmeasure | tagidentity | tagaudit"

// cliBatchSpec returns the spec of a CLI-triggered enqueue; every CLI boundary that creates work uses
// this same origin.
func cliBatchSpec() (task.BatchSpec, error) {
	return task.NewBatchSpec(types.Batch.Origin.CLI, "")
}

// newEnqueuer builds an enqueuer whose catalog carries only the dedup hooks of the types this call site
// enqueues; enqueueing any other pending type fails loudly instead of creating keyless work.
func newEnqueuer(client *database.Client, hooks map[types.TaskType]task.DedupKeyFunc) (*task.Enqueuer, error) {
	catalog, err := task.NewCatalog(hooks, nil)
	if err != nil {
		return nil, fmt.Errorf("task catalog: %w", err)
	}
	return task.NewEnqueuer(client, catalog), nil
}

func batchHandler(c *container.Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println(batchHelpText)
		return nil
	}

	switch args[0] {
	case "create":
		return batchCreateHandler(c, args[1:])
	case "run":
		return batchRunHandler(c, args[1:])
	case "stop", "cancel":
		return batchStopHandler(c, args[1:])
	case "resume":
		return batchResumeHandler(c, args[1:], false)
	case "restart":
		return batchResumeHandler(c, args[1:], true)
	case "list":
		return batchListHandler(c, args[1:])
	case "status":
		return batchStatusHandler(c, args[1:])
	default:
		return fmt.Errorf("unknown batch subcommand %q", args[0])
	}
}

func batchCreateHandler(c *container.Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println(batchCreateHelpText)
		return nil
	}

	taskName := args[0]
	for _, arg := range args[1:] {
		if arg == "--help" || arg == "-h" {
			fmt.Println(batchCreateHelpText)
			return nil
		}
	}
	if strings.HasPrefix(taskName, "-") {
		return fmt.Errorf("batch create requires a task name")
	}

	if handled, err := batchCreateSelection(c, taskName, args[1:]); handled {
		return err
	}

	switch taskName {
	case "consume", "backup", "mirror", "enrich", "thumbnails",
		"tagreconcile", "tagmeasure", "tagidentity", "tagaudit":
	default:
		return fmt.Errorf("unknown task %q; supported: consume, backup, mirror, enrich, thumbnails, "+
			"tagreconcile, tagmeasure, tagidentity, tagaudit", taskName)
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	ctx := context.Background()
	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}

	var enqueuer *task.Enqueuer
	var tasks []task.TaskSpec

	switch taskName {
	case "consume":
		if rest := flags.NewFlagParser(args[1:]).Rest(); len(rest) > 0 {
			return fmt.Errorf("consume takes no arguments, got %s", rest[0])
		}
		// the paused-batch guard keeps the CLI and polling creation paths aligned: while the provider credits
		// are broken each new batch would process a few files and pause itself; the CLI refuses while the polling
		// scan skips its tick
		pausedIDs, err := client.Queries.ListPausedBatches(ctx)
		if err != nil {
			return fmt.Errorf("check paused batches: %w", err)
		}
		if len(pausedIDs) > 0 {
			return fmt.Errorf("%d paused batch(es) exist due to LLM credit/balance errors: %s\n"+
				"Resolve the billing issue, then resume or cancel each paused batch before creating new ones.",
				len(pausedIDs), strings.Join(pausedIDs, ", "))
		}
		enqueuer, err = newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
			types.Task.Type.Consume: consumption.DedupKey,
		})
		if err != nil {
			return err
		}
		tasks, err = consumption.IngestSpecsFromInbox(c.GetConfig(), c.GetLogger())
		if err != nil {
			return fmt.Errorf("scan inbox: %w", err)
		}

	case "backup":
		fp := flags.NewFlagParser(args[1:])
		var mode, path string
		keep := 0
		if err := fp.String("--mode", &mode); err != nil {
			return err
		}
		if err := fp.String("--path", &path); err != nil {
			return err
		}
		if err := fp.Int("--keep", &keep, 0, 1_000_000); err != nil {
			return err
		}
		if rest := fp.Rest(); len(rest) > 0 {
			return fmt.Errorf("unknown argument: %s", rest[0])
		}
		enqueuer, err = newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
			types.Task.Type.Backup: backup.BackupDedupKey,
		})
		if err != nil {
			return err
		}
		tasks, err = backup.BackupSpecs(mode, path, keep, backup.ScheduleTiming{})
		if err != nil {
			return err
		}

	case "mirror":
		fp := flags.NewFlagParser(args[1:])
		var path string
		if err := fp.String("--path", &path); err != nil {
			return err
		}
		if rest := fp.Rest(); len(rest) > 0 {
			return fmt.Errorf("unknown argument: %s", rest[0])
		}
		enqueuer, err = newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
			types.Task.Type.Mirror: backup.MirrorDedupKey,
		})
		if err != nil {
			return err
		}
		tasks, err = backup.MirrorSpecs(path, 0, backup.ScheduleTiming{})
		if err != nil {
			return err
		}

	case "enrich":
		documentIDs := flags.NewFlagParser(args[1:]).Rest()
		if len(documentIDs) == 0 {
			return fmt.Errorf("enrich requires at least one document id, --all or --limit N")
		}
		for _, documentID := range documentIDs {
			if strings.HasPrefix(documentID, "-") {
				return fmt.Errorf("unknown flag: %s", documentID)
			}
		}
		enqueuer, err = newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
			types.Task.Type.Enrich: enrichment.DedupKey,
		})
		if err != nil {
			return err
		}
		tasks, err = enrichment.EnrichSpecs(documentIDs)
		if err != nil {
			return err
		}

	case "thumbnails":
		if rest := flags.NewFlagParser(args[1:]).Rest(); len(rest) > 0 {
			return fmt.Errorf("thumbnails takes no arguments, got %s", rest[0])
		}
		enqueuer, err = newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
			types.Task.Type.ThumbBackfill: service.ThumbnailDedupKey,
		})
		if err != nil {
			return err
		}
		tasks, err = service.ThumbnailBackfillSpecs(ctx, client.Queries)
		if err != nil {
			return fmt.Errorf("build thumbnail backfill tasks: %w", err)
		}

	case "tagreconcile", "tagmeasure", "tagidentity", "tagaudit":
		if rest := flags.NewFlagParser(args[1:]).Rest(); len(rest) > 0 {
			return fmt.Errorf("%s takes no arguments, got %s", taskName, rest[0])
		}
		switch taskName {
		case "tagreconcile":
			tasks = curation.ReconcileSpecs()
			enqueuer, err = newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
				types.Task.Type.TagReconcile: task.ConstantDedupKey(curation.TagReconcileDedupKey),
			})
		case "tagmeasure":
			tasks = curation.MeasureSpecs()
			enqueuer, err = newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
				types.Task.Type.TagMeasure: task.ConstantDedupKey(tags.TagMeasureDedupKey),
			})
		case "tagidentity":
			tasks = curation.IdentitySpecs()
			enqueuer, err = newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
				types.Task.Type.TagIdentity: task.ConstantDedupKey(curation.TagIdentityDedupKey),
			})
		case "tagaudit":
			tasks = curation.AuditSpecs()
			enqueuer, err = newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
				types.Task.Type.TagAudit: task.ConstantDedupKey(curation.TagAuditDedupKey),
			})
		}
		if err != nil {
			return err
		}
	}

	result, err := enqueuer.EnqueueBatch(ctx, spec, tasks)
	if err != nil {
		return fmt.Errorf("enqueue %s batch: %w", taskName, err)
	}

	if result.BatchID == "" {
		fmt.Printf("Nothing enqueued: %d task(s) already covered by pending work\n", result.Skipped)
		return nil
	}
	fmt.Printf("Batch %s queued with %d task(s) — run 'kushim queue' to process\n", result.BatchID, len(result.TaskIDs))
	return nil
}

func batchRunHandler(c *container.Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim batch run <batch-id>\n\n" +
			"  Claim a queued batch and run its task CLI in batch mode, waiting for it.\n" +
			"  Only batches whose task types have a batch-mode CLI can run.")
		if len(args) == 0 {
			return fmt.Errorf("run requires a batch id")
		}
		return nil
	}
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	catalog, err := task.NewCatalog(nil, nil)
	if err != nil {
		return fmt.Errorf("task catalog: %w", err)
	}
	return dispatch.LaunchQueuedBatch(context.Background(), client, c.GetLogger(), catalog, args[0])
}

func batchStopHandler(c *container.Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim batch stop <batch-id>\n\n" +
			"  Cancel a queued, paused or running batch (alias: cancel). Pending tasks\n" +
			"  are cancelled and a SIGTERM is sent to the process running the batch.")
		if len(args) == 0 {
			return fmt.Errorf("stop requires a batch id")
		}
		return nil
	}

	batchID := args[0]
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := task.CancelBatch(ctx, client, c.GetLogger(), batchID)
	if err != nil {
		if errors.Is(err, task.ErrBatchNotFound) {
			return fmt.Errorf("batch %q not found", batchID)
		}
		return err
	}

	switch {
	case result.OwnerPID == 0:
		fmt.Printf("No running process found for batch %s\n", batchID)
		fmt.Printf("%d pending tasks cancelled\n", result.PendingCancelled)
	case !result.OwnerAlive:
		fmt.Printf("Process %d is no longer running\n", result.OwnerPID)
		fmt.Printf("%d pending tasks cancelled\n", result.PendingCancelled)
	default:
		fmt.Printf("Batch %s: %d pending + %d processing cancelled, signal sent to PID %d\n",
			batchID, result.PendingCancelled, result.ProcessingCancelled, result.OwnerPID)
	}
	return nil
}

func batchResumeHandler(c *container.Container, args []string, includeFailed bool) error {
	name := "resume"
	if includeFailed {
		name = "restart"
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Printf("Usage: kushim batch %s <batch-id>\n\n", name)
		if includeFailed {
			fmt.Println("  Requeue a paused, failed or cancelled batch and retry its failed tasks.")
		} else {
			fmt.Println("  Requeue a paused, failed or cancelled batch. Its failed tasks stay failed;\n  use 'batch restart' to retry them.")
		}
		if len(args) == 0 {
			return fmt.Errorf("%s requires a batch id", name)
		}
		return nil
	}

	batchID := args[0]
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	ctx := context.Background()
	maxRetries := int32(c.GetConfig().Consumer.Reclaim.MaxRetries)
	result, err := task.ResumeBatch(ctx, client, c.GetLogger(), batchID, maxRetries, includeFailed)
	if err != nil {
		if errors.Is(err, task.ErrBatchNotFound) {
			return fmt.Errorf("batch %q not found", batchID)
		}
		return err
	}
	if !result.Resumed {
		fmt.Printf("Batch %s has no active work to resume\n", batchID)
		return nil
	}
	fmt.Printf("Batch %s requeued: %d processing tasks reset, %d failed tasks retried\n",
		batchID, result.Reset, result.FailedRetried)
	return nil
}

func batchListHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)

	if fp.Help("Usage: kushim batch list [--limit N] [--offset N]\n" +
		"  List recent batches, newest first.\n\n" +
		"  --limit N   max results (default 20, max 100)\n" +
		"  --offset N  result offset (default 0)") {
		return nil
	}

	var limit, offset int
	if err := fp.Int("--limit", &limit, 1, 100); err != nil {
		return err
	}
	if err := fp.Int("--offset", &offset, 0, 1_000_000); err != nil {
		return err
	}
	if limit == 0 {
		limit = 20
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	svc := service.NewBatch(client, c.GetConfig().Consumer.Reclaim.MaxRetries)
	batches, err := svc.ListOverviews(context.Background(), int32(limit), int32(offset))
	if err != nil {
		return fmt.Errorf("list batches: %w", err)
	}

	if len(batches) == 0 {
		fmt.Println("No batches found")
		return nil
	}

	fmt.Printf("%-36s %-11s %-8s %-18s %-11s %-7s %-8s %s\n",
		"BATCH ID", "STATUS", "ORIGIN", "SCHEDULE", "PROGRESS", "FAILED", "OWNER", "CREATED")
	fmt.Println(strings.Repeat("-", 110))

	for _, b := range batches {
		schedule := b.Schedule
		if schedule == "" {
			schedule = "-"
		}
		if len(schedule) > 18 {
			schedule = schedule[:17] + "…"
		}
		owner := b.OwnerState
		if b.Orphaned {
			owner = "orphaned"
		}
		fmt.Printf("%-36s %-11s %-8s %-18s %-11s %-7d %-8s %s\n",
			b.BatchID, b.Status, b.Origin, schedule,
			fmt.Sprintf("%d/%d", b.Completed, b.Total), b.Failed, owner,
			b.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

func batchStatusHandler(c *container.Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim batch status <batch-id>\n\n" +
			"  Show one batch's status, origin, task counts and owner state.")
		if len(args) == 0 {
			return fmt.Errorf("status requires a batch id")
		}
		return nil
	}

	batchID := args[0]
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	svc := service.NewBatch(client, c.GetConfig().Consumer.Reclaim.MaxRetries)
	summary, err := svc.GetSummary(context.Background(), batchID)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return fmt.Errorf("batch %q not found", batchID)
		}
		return err
	}

	total := summary.Waiting + summary.Pending + summary.Processing + summary.Completed +
		summary.Failed + summary.Cancelled + summary.Discarded

	fmt.Printf("Batch ID:   %s\n", summary.BatchID)
	fmt.Printf("Status:     %s\n", summary.Status)
	fmt.Printf("Origin:     %s\n", summary.Origin)
	if summary.Schedule != "" {
		fmt.Printf("Schedule:   %s\n", summary.Schedule)
	}
	fmt.Printf("Tasks:      %d total — %d waiting, %d pending, %d processing, %d completed, %d failed, %d cancelled, %d discarded\n",
		total, summary.Waiting, summary.Pending, summary.Processing, summary.Completed,
		summary.Failed, summary.Cancelled, summary.Discarded)
	if summary.OwnerPID > 0 {
		fmt.Printf("Owner:      %s (PID %d)\n", summary.OwnerState, summary.OwnerPID)
	} else {
		fmt.Printf("Owner:      %s\n", summary.OwnerState)
	}
	fmt.Printf("Orphaned:   %t\n", summary.Orphaned)
	fmt.Printf("\nTask details: kushim task list --batch %s\n", batchID)
	return nil
}

// batchCreateSelection routes the create verbs whose task set is selected from live data rather than an
// explicit id list; handled is false when the verb reads its targets from the arguments.
func batchCreateSelection(c *container.Container, taskName string, args []string) (handled bool, err error) {
	switch taskName {
	case "enrich":
		fp := flags.NewFlagParser(args)
		var all bool
		if err := fp.Bool("--all", &all); err != nil {
			return true, err
		}
		limit := 0
		if err := fp.Int("--limit", &limit, 1, 1_000_000); err != nil {
			return true, err
		}
		if !all && limit == 0 {
			return false, nil
		}
		if rest := fp.Rest(); len(rest) > 0 {
			return true, fmt.Errorf("a document ID cannot be combined with --limit or --all")
		}
		if all && limit > 0 {
			return true, fmt.Errorf("--limit and --all are mutually exclusive")
		}
		effectiveLimit := int64(limit)
		if all {
			effectiveLimit = 0
		}
		return true, enqueueEnrichSelection(c, effectiveLimit)

	case "thumbnails":
		fp := flags.NewFlagParser(args)
		var fromBatch, documentID string
		if err := fp.String("--from-batch", &fromBatch); err != nil {
			return true, err
		}
		if err := fp.String("--document", &documentID); err != nil {
			return true, err
		}
		if fromBatch == "" && documentID == "" {
			return false, nil
		}
		if fromBatch != "" && documentID != "" {
			return true, fmt.Errorf("--from-batch and --document are mutually exclusive")
		}
		if rest := fp.Rest(); len(rest) > 0 {
			return true, fmt.Errorf("unknown argument: %s", rest[0])
		}
		if fromBatch != "" {
			return true, enqueueThumbnailBackfillBatch(c, fromBatch)
		}
		return true, enqueueThumbnailBackfillDocument(c, documentID)
	}
	return false, nil
}

// enqueueEnrichSelection enqueues enrichment for live documents, oldest first, capped by limit (0 means
// no cap).
func enqueueEnrichSelection(c *container.Container, limit int64) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	enqueuer, err := newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
		types.Task.Type.Enrich: enrichment.DedupKey,
	})
	if err != nil {
		return err
	}
	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}

	svc := service.NewReEnrich(client.Queries, enqueuer)
	batchID, enqueued, skipped, err := svc.ReEnrichMany(context.Background(), spec, limit)
	if err != nil {
		return fmt.Errorf("re-enrich: %w", err)
	}

	if batchID == "" {
		if skipped == 0 {
			fmt.Println("No live documents to enrich")
			return nil
		}
		fmt.Printf("No documents enqueued: %d document(s) already have a pending enrich task\n", skipped)
		return nil
	}

	fmt.Printf("Batch %s queued with %d enrich task(s)", batchID, enqueued)
	if skipped > 0 {
		fmt.Printf(", %d skipped (an enrich task is already pending)", skipped)
	}
	fmt.Println(" — run 'kushim queue' to process")
	return nil
}

// enqueueThumbnailBackfillBatch enqueues thumbnails for the documents of one past batch.
func enqueueThumbnailBackfillBatch(c *container.Container, sourceBatchID string) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	enqueuer, err := newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
		types.Task.Type.ThumbBackfill: service.ThumbnailDedupKey,
	})
	if err != nil {
		return err
	}
	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}

	svc := service.NewThumbnailBackfill(client.Queries, enqueuer)
	batchID, enqueued, skipped, err := svc.BackfillBatch(context.Background(), spec, sourceBatchID)
	if err != nil {
		return fmt.Errorf("backfill thumbnails for batch: %w", err)
	}
	if enqueued == 0 && skipped == 0 {
		fmt.Println("No documents missing thumbnails in that batch.")
		return nil
	}
	if enqueued == 0 {
		fmt.Printf("%d document(s) in that batch already have a pending thumbnail task.\n", skipped)
		return nil
	}
	fmt.Printf("Batch %s queued with %d thumbnail task(s) — run 'kushim queue' to process\n", batchID, enqueued)
	return nil
}

// enqueueThumbnailBackfillDocument enqueues one document's thumbnail task.
func enqueueThumbnailBackfillDocument(c *container.Container, documentID string) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	enqueuer, err := newEnqueuer(client, map[types.TaskType]task.DedupKeyFunc{
		types.Task.Type.ThumbBackfill: service.ThumbnailDedupKey,
	})
	if err != nil {
		return err
	}
	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}

	svc := service.NewThumbnailBackfill(client.Queries, enqueuer)
	batchID, err := svc.BackfillDocument(context.Background(), spec, documentID)
	if err != nil {
		switch errs.KindOf(err) {
		case errs.KindNotFound:
			return fmt.Errorf("document %s not found", documentID)
		case errs.KindConflict:
			return fmt.Errorf("document %s already has a thumbnail or a task is already queued", documentID)
		default:
			return fmt.Errorf("backfill thumbnail: %w", err)
		}
	}
	fmt.Printf("Batch %s queued with 1 thumbnail task — run 'kushim queue' to process\n", batchID)
	return nil
}

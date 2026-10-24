package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/ops/queue"
	"github.com/wgomg/edub-kushim/internal/service"
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
	"  backup --mode <full|database|documents> [--path <dir>] [--keep N]\n" +
	"  mirror [--path <dir>]\n" +
	"  enrich <document-id> [<document-id> ...]\n" +
	"  thumbnails              Backfill every document missing a thumbnail\n" +
	"  tagreconcile | tagmeasure | tagidentity | tagaudit\n\n" +
	"  Direct commands (for example 'kushim backup') run immediately and create\n" +
	"  no queue rows; this command is the queue surface."

const batchCreateHelpText = "Usage: kushim batch create <task> [args]\n\n" +
	"  backup --mode <full|database|documents> [--path <dir>] [--keep N]\n" +
	"  mirror [--path <dir>]\n" +
	"  enrich <document-id> [<document-id> ...]\n" +
	"  thumbnails\n" +
	"  tagreconcile | tagmeasure | tagidentity | tagaudit"

// cliBatchSpec returns the spec of a CLI-triggered enqueue; every CLI boundary that creates work uses
// this same origin.
func cliBatchSpec() (task.BatchSpec, error) {
	return task.NewBatchSpec(types.Batch.Origin.CLI, "")
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

	taskType, input, err := batchBuildInput(taskName, args[1:])
	if err != nil {
		return err
	}

	catalog, err := c.GetCatalog()
	if err != nil {
		return fmt.Errorf("task catalog: %w", err)
	}
	def, err := catalog.Definition(taskType)
	if err != nil {
		return fmt.Errorf("task type: %w", err)
	}
	if def.BuildTasks == nil {
		return fmt.Errorf("task type %q cannot be enqueued with batch create", taskType)
	}

	ctx := context.Background()
	tasks, err := def.BuildTasks(ctx, input)
	if err != nil {
		return fmt.Errorf("build %s tasks: %w", taskType, err)
	}

	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}
	enqueuer, err := c.GetEnqueuer()
	if err != nil {
		return fmt.Errorf("enqueuer: %w", err)
	}
	result, err := enqueuer.EnqueueBatch(ctx, spec, tasks)
	if err != nil {
		return fmt.Errorf("enqueue %s batch: %w", taskType, err)
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
	return queue.LaunchQueuedBatch(context.Background(), c, args[0])
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

// batchBuildInput turns the task name and its remaining arguments into the task type and the typed build
// input its definition's builder expects.
func batchBuildInput(taskName string, args []string) (types.TaskType, task.BuildInput, error) {
	fp := flags.NewFlagParser(args)

	switch taskName {
	case "backup":
		var mode, path string
		keep := 0
		if err := fp.String("--mode", &mode); err != nil {
			return "", nil, err
		}
		if err := fp.String("--path", &path); err != nil {
			return "", nil, err
		}
		if err := fp.Int("--keep", &keep, 0, 1_000_000); err != nil {
			return "", nil, err
		}
		if rest := fp.Rest(); len(rest) > 0 {
			return "", nil, fmt.Errorf("unknown argument: %s", rest[0])
		}
		return types.Task.Type.Backup, container.BackupBuildInput{
			Mode: mode,
			Path: path,
			Keep: keep,
		}, nil

	case "mirror":
		var path string
		if err := fp.String("--path", &path); err != nil {
			return "", nil, err
		}
		if rest := fp.Rest(); len(rest) > 0 {
			return "", nil, fmt.Errorf("unknown argument: %s", rest[0])
		}
		return types.Task.Type.Mirror, container.MirrorBuildInput{Path: path}, nil

	case "enrich":
		documentIDs := fp.Rest()
		if len(documentIDs) == 0 {
			return "", nil, fmt.Errorf("enrich requires at least one document id")
		}
		return types.Task.Type.Enrich, container.EnrichBuildInput{DocumentIDs: documentIDs}, nil

	case "thumbnails":
		if rest := fp.Rest(); len(rest) > 0 {
			return "", nil, fmt.Errorf("thumbnails takes no arguments, got %s", rest[0])
		}
		return types.Task.Type.ThumbBackfill, container.NewEmptyBuildInput(types.Task.Type.ThumbBackfill), nil

	case "tagreconcile":
		if rest := fp.Rest(); len(rest) > 0 {
			return "", nil, fmt.Errorf("%s takes no arguments, got %s", taskName, rest[0])
		}
		return types.Task.Type.TagReconcile, container.NewEmptyBuildInput(types.Task.Type.TagReconcile), nil

	case "tagmeasure":
		if rest := fp.Rest(); len(rest) > 0 {
			return "", nil, fmt.Errorf("%s takes no arguments, got %s", taskName, rest[0])
		}
		return types.Task.Type.TagMeasure, container.NewEmptyBuildInput(types.Task.Type.TagMeasure), nil

	case "tagidentity":
		if rest := fp.Rest(); len(rest) > 0 {
			return "", nil, fmt.Errorf("%s takes no arguments, got %s", taskName, rest[0])
		}
		return types.Task.Type.TagIdentity, container.NewEmptyBuildInput(types.Task.Type.TagIdentity), nil

	case "tagaudit":
		if rest := fp.Rest(); len(rest) > 0 {
			return "", nil, fmt.Errorf("%s takes no arguments, got %s", taskName, rest[0])
		}
		return types.Task.Type.TagAudit, container.NewEmptyBuildInput(types.Task.Type.TagAudit), nil

	default:
		return "", nil, fmt.Errorf("unknown task %q; supported: backup, mirror, enrich, thumbnails, tagreconcile, tagmeasure, tagidentity, tagaudit", taskName)
	}
}

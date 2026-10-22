package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const batchHelpText = "Usage: kushim batch create <task> [args]\n\n" +
	"  Enqueue a task set for the queue daemon to process.\n\n" +
	"Tasks:\n" +
	"  backup --mode <full|database|documents> [--path <dir>] [--keep N]\n" +
	"  mirror [--path <dir>]\n" +
	"  enrich <document-id> [<document-id> ...]\n" +
	"  thumbnails              Backfill every document missing a thumbnail\n" +
	"  tagreconcile | tagmeasure | tagidentity | tagaudit\n\n" +
	"  Direct commands (for example 'kushim backup') run immediately and create\n" +
	"  no queue rows; this command only enqueues."

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
	if args[0] != "create" {
		return fmt.Errorf("unknown batch subcommand %q (only create exists)", args[0])
	}
	return batchCreateHandler(c, args[1:])
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

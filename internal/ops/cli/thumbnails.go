package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

func backfillThumbnailsHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help("Usage: kushim thumbnails <mode> [flags]\n\n" +
		"Modes:\n" +
		"  --all                Enqueue thumbnail tasks for every document missing one\n" +
		"  --batch <id>         Enqueue for documents in the given batch\n" +
		"  --document <id>      Enqueue for a single document\n\n" +
		"Flags:\n" +
		"  --force              Proceed even if thumbnails are disabled in config") {
		return nil
	}

	var all bool
	fp.Bool("--all", &all)
	var batchID string
	fp.String("--batch", &batchID)
	var documentID string
	fp.String("--document", &documentID)
	var force bool
	fp.Bool("--force", &force)

	modes := 0
	if all {
		modes++
	}
	if batchID != "" {
		modes++
	}
	if documentID != "" {
		modes++
	}
	if modes != 1 {
		return fmt.Errorf("exactly one of --all, --batch <id>, --document <id> is required")
	}

	cfg := c.GetConfig()
	if !cfg.Consumer.Thumbnail.Enabled && !force {
		return fmt.Errorf("thumbnails are disabled in config (consumer.thumbnail.enabled); use --force to override")
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	enqueuer, err := c.GetEnqueuer()
	if err != nil {
		return fmt.Errorf("enqueuer: %w", err)
	}
	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}
	svc := service.NewThumbnailBackfill(client.Queries, enqueuer)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	switch {
	case all:
		result, err := enqueueThumbnailBackfillAll(ctx, c, enqueuer, spec)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				if len(result.TaskIDs) == 0 {
					return fmt.Errorf("backfill thumbnails: %w — timed out before any task was queued; re-run to continue", err)
				}
				return fmt.Errorf("backfill thumbnails: %w — timed out after enqueueing %d task(s); re-run to continue", err, len(result.TaskIDs))
			}
			return fmt.Errorf("backfill thumbnails: %w", err)
		}
		if result.BatchID == "" {
			if result.Skipped == 0 {
				fmt.Println("No documents missing thumbnails.")
			} else {
				fmt.Printf("%d document(s) already have a pending thumbnail task.\n", result.Skipped)
			}
			return nil
		}
		fmt.Printf("Batch %s queued with %d thumbnail task(s) — run 'kushim queue' to process\n", result.BatchID, len(result.TaskIDs))
	case batchID != "":
		newBatchID, enqueued, skipped, err := svc.BackfillBatch(ctx, spec, batchID)
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
		fmt.Printf("Batch %s queued with %d thumbnail task(s) — run 'kushim queue' to process\n", newBatchID, enqueued)
	case documentID != "":
		batchID, err := svc.BackfillDocument(ctx, spec, documentID)
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
	}

	return nil
}

// enqueueThumbnailBackfillAll runs the thumbbackfill definition's builder over every document missing
// a thumbnail and enqueues the resulting batch.
func enqueueThumbnailBackfillAll(ctx context.Context, c *container.Container, enqueuer *task.Enqueuer, spec task.BatchSpec) (task.EnqueueResult, error) {
	catalog, err := c.GetCatalog()
	if err != nil {
		return task.EnqueueResult{}, fmt.Errorf("task catalog: %w", err)
	}
	def, err := catalog.Definition(types.Task.Type.ThumbBackfill)
	if err != nil {
		return task.EnqueueResult{}, fmt.Errorf("thumbbackfill definition: %w", err)
	}
	tasks, err := def.BuildTasks(ctx, container.NewEmptyBuildInput(types.Task.Type.ThumbBackfill))
	if err != nil {
		return task.EnqueueResult{}, err
	}
	result, err := enqueuer.EnqueueBatch(ctx, spec, tasks)
	if err != nil {
		return result, err
	}
	return result, nil
}

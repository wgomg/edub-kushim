package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
)

func backfillThumbnailsHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
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

	cfg := c.cfg.Load()
	if !cfg.Consumer.Thumbnail.Enabled && !force {
		return fmt.Errorf("thumbnails are disabled in config (consumer.thumbnail.enabled); use --force to override")
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	store := task.NewStore(client.Queries)
	batchSvc := service.NewBatch(client, cfg.Consumer.Reclaim.MaxRetries)
	svc := service.NewThumbnailBackfill(client.Queries, c.logger, store, batchSvc)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	switch {
	case all:
		batchID, enqueued, skipped, err := svc.BackfillAll(ctx)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				if enqueued == 0 {
					return fmt.Errorf("backfill thumbnails: %w — timed out before any task was queued; re-run to continue", err)
				}
				return fmt.Errorf("backfill thumbnails: %w — timed out after enqueueing %d task(s); re-run to continue", err, enqueued)
			}
			return fmt.Errorf("backfill thumbnails: %w", err)
		}
		if enqueued == 0 && skipped == 0 {
			fmt.Println("No documents missing thumbnails.")
			return nil
		}
		if enqueued == 0 {
			fmt.Printf("%d document(s) already have a pending thumbnail task.\n", skipped)
			return nil
		}
		fmt.Printf("Batch %s queued with %d thumbnail task(s) — run 'kushim queue' to process\n", batchID, enqueued)
	case batchID != "":
		newBatchID, enqueued, skipped, err := svc.BackfillBatch(ctx, batchID)
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
		batchID, err := svc.BackfillDocument(ctx, documentID)
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

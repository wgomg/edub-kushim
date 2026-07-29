package commands

import (
	"context"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
)

func enrichHandler(c *Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim enrich <document-id>\n\n" +
			"  Re-run enrichment (LLM classification) for a single document.\n" +
			"  Creates a queued batch with a pending enrich task.\n" +
			"  Start 'kushim queue' to process the batch.")
		if len(args) == 0 {
			return fmt.Errorf("enrich requires a document ID")
		}
		return nil
	}

	documentID := args[0]

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	store := task.NewStore(client.Queries)
	batchSvc := service.NewBatch(client, c.cfg.Load().Consumer.Reclaim.MaxRetries)
	svc := service.NewReEnrich(client.Queries, store, batchSvc)

	batchID, err := svc.ReEnrich(context.Background(), documentID)
	if err != nil {
		kind := errs.KindOf(err)
		switch kind {
		case errs.KindNotFound:
			return fmt.Errorf("document %s not found", documentID)
		case errs.KindConflict:
			return fmt.Errorf("re-enrich already queued for document %s", documentID)
		default:
			return fmt.Errorf("re-enrich: %w", err)
		}
	}

	fmt.Printf("Batch %s queued — run 'kushim queue' to process\n", batchID)
	return nil
}

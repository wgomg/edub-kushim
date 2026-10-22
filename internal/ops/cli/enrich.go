package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/service"
)

const enrichHelpText = "Usage: kushim enrich [<document-id>] [--limit N | --all]\n\n" +
	"  Re-run enrichment (LLM classification).\n\n" +
	"  With a document id: enqueue that single document.\n" +
	"  Without a document id: enqueue one batch of live documents, oldest first.\n\n" +
	"  --limit N         cap the run at N documents (default: consumer.max_files_per_batch)\n" +
	"  --all             enqueue every live document\n\n" +
	"  Creates a queued batch with one pending enrich task per document.\n" +
	"  Start 'kushim queue' to process the batch."

func enrichHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)

	if fp.Help(enrichHelpText) {
		return nil
	}

	var all bool
	fp.Bool("--all", &all)

	var limitRaw string
	if err := fp.String("--limit", &limitRaw); err != nil {
		return err
	}

	limit := 0
	if limitRaw != "" {
		v, err := strconv.Atoi(limitRaw)
		if err != nil || v <= 0 {
			return fmt.Errorf("--limit: must be a positive integer")
		}
		limit = v
	}

	rest := fp.Rest()
	if len(rest) > 1 {
		return fmt.Errorf("enrich accepts at most one document ID")
	}

	if len(rest) == 1 && strings.HasPrefix(rest[0], "-") {
		return fmt.Errorf("unknown flag: %s", rest[0])
	}

	if len(rest) == 1 {
		if all || limit > 0 {
			return fmt.Errorf("a document ID cannot be combined with --limit or --all")
		}
		return enrichDocument(c, rest[0])
	}

	if all && limit > 0 {
		return fmt.Errorf("--limit and --all are mutually exclusive")
	}

	effectiveLimit := int64(limit)
	switch {
	case all:
		effectiveLimit = 0
	case limit == 0:
		effectiveLimit = int64(c.GetConfig().Consumer.MaxFilesPerBatch)
	}

	return enrichMany(c, effectiveLimit)
}

func enrichDocument(c *container.Container, documentID string) error {
	svc, err := newReEnrichService(c)
	if err != nil {
		return err
	}

	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}

	batchID, err := svc.ReEnrich(context.Background(), spec, documentID)
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

func enrichMany(c *container.Container, limit int64) error {
	svc, err := newReEnrichService(c)
	if err != nil {
		return err
	}

	spec, err := cliBatchSpec()
	if err != nil {
		return err
	}

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

func newReEnrichService(c *container.Container) (*service.ReEnrich, error) {
	client, err := c.GetClient()
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}

	enqueuer, err := c.GetEnqueuer()
	if err != nil {
		return nil, fmt.Errorf("enqueuer: %w", err)
	}
	return service.NewReEnrich(client.Queries, enqueuer), nil
}

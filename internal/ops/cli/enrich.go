package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/ops/consume"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const enrichHelpText = "Usage: kushim enrich [<document-id>] [--limit N | --all] [--no-wait]\n\n" +
	"  Re-run enrichment (LLM classification) for stored documents.\n\n" +
	"  With a document id: enrich that single document now.\n" +
	"  Without a document id: enrich live documents, oldest first.\n\n" +
	"  --limit N          cap the run at N documents (default: consumer.max_files_per_batch)\n" +
	"  --all              select every live document\n" +
	"  --no-wait          exit non-zero instead of waiting for a lock or in-flight work\n\n" +
	"  Runs immediately under the maintenance lock, creating no queue rows.\n" +
	"  To enqueue enrichment: kushim batch create enrich <document-id>... | --all | --limit N"

func enrichHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)

	if fp.Help(enrichHelpText) {
		return nil
	}

	if slices.Contains(args, "--batch") {
		return fmt.Errorf("--batch is now --from-batch: run 'kushim batch run <id>' to execute a queued batch, " +
			"or 'kushim batch create enrich' to enqueue one")
	}

	var all bool
	fp.Bool("--all", &all)

	var limitRaw string
	if err := fp.String("--limit", &limitRaw); err != nil {
		return err
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

	if batchID != "" || execTokenParam != "" {
		if len(rest) > 0 || all || limit > 0 {
			return fmt.Errorf("--from-batch cannot be combined with a document selection")
		}
		if batchID == "" {
			return fmt.Errorf("--exec-token requires --from-batch <id>")
		}
		return runEnrichBatch(c, batchID, execTokenParam)
	}

	if len(rest) == 1 {
		if all || limit > 0 {
			return fmt.Errorf("a document ID cannot be combined with --limit or --all")
		}
		return enrichDocumentsDirect(c, []string{rest[0]}, noWait)
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

	return enrichLiveDocumentsDirect(c, effectiveLimit, noWait)
}

// newEnrichHandler constructs the enrich handler over the command's own enricher.
func newEnrichHandler(c *container.Container, client *database.Client) (task.Handler, error) {
	tag, err := c.GetTagService()
	if err != nil {
		return nil, fmt.Errorf("tag service: %w", err)
	}
	enricher := enrichment.NewEnricher(c.GetConfig(), c.GetLogger(), client, tag, c.GetMatcherClient())
	return enrichment.NewEnrichTaskHandler(enricher, client.Queries, c.GetLogger()), nil
}

// runEnrichBatch executes one claimed enrich batch and adds the batch's enrich bookend on completion.
func runEnrichBatch(c *container.Container, batchID, execTokenParam string) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	handler, err := newEnrichHandler(c, client)
	if err != nil {
		return err
	}
	store := task.NewStore(client)
	registry := task.NewRegistry()
	registry.Register(types.Task.Type.Enrich, handler)

	result, err := consume.RunClaimedBatch(c, batchID, execTokenParam, store, registry, nil)
	if err != nil {
		return err
	}
	if result.Paused {
		return nil
	}

	enrichment.LogBatchBookend(context.Background(), client.Queries, c.GetLogger(), batchID, result.Status)
	return nil
}

// enrichLiveDocumentsDirect selects live documents, oldest first, capped by limit (0 means no cap).
func enrichLiveDocumentsDirect(c *container.Container, limit int64, noWait bool) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	documentIDs, err := client.Queries.ListLiveDocumentIDs(context.Background())
	if err != nil {
		return errs.FromDB(err, "list live documents")
	}
	if limit > 0 && int64(len(documentIDs)) > limit {
		documentIDs = documentIDs[:limit]
	}
	if len(documentIDs) == 0 {
		fmt.Println("No live documents to enrich")
		return nil
	}

	return enrichDocumentsDirect(c, documentIDs, noWait)
}

// enrichDocumentsDirect enriches the selected documents immediately under the manual lock path. A failed
// document is reported and the run continues; a provider credit error aborts it. The command exits non-zero
// when any document failed.
func enrichDocumentsDirect(c *container.Container, documentIDs []string, noWait bool) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	ctx := context.Background()
	if len(documentIDs) == 1 {
		if _, err := client.Queries.GetDocument(ctx, documentIDs[0]); err != nil {
			if errs.KindOf(errs.FromDB(err, "get document")) == errs.KindNotFound {
				return fmt.Errorf("document %s not found", documentIDs[0])
			}
			return fmt.Errorf("get document %s: %w", documentIDs[0], err)
		}
	}

	handler, err := newEnrichHandler(c, client)
	if err != nil {
		return err
	}

	failed := 0
	fmt.Printf("Enriching %d document(s)...\n", len(documentIDs))
	policy := task.DirectLockPolicyFor(types.Task.Type.Enrich)
	err = task.RunDirect(ctx, client, c.GetLogger(), "enrich", policy, !noWait, nil, func() error {
		for i, documentID := range documentIDs {
			payload, err := json.Marshal(map[string]string{"document_id": documentID})
			if err != nil {
				failed++
				fmt.Printf("  [%d/%d] %s ... failed: %v\n", i+1, len(documentIDs), documentID, err)
				continue
			}
			_, err = handler.Handle(ctx, task.Task{
				TaskID:   uuid.New().String(),
				TaskType: types.Task.Type.Enrich,
				Payload:  payload,
			})
			if err != nil {
				failed++
				fmt.Printf("  [%d/%d] %s ... failed: %v\n", i+1, len(documentIDs), documentID, err)
				if tErr, ok := errors.AsType[*task.Error](err); ok && tErr.PauseBatch {
					return fmt.Errorf("enrich aborted: %w", err)
				}
				continue
			}
			fmt.Printf("  [%d/%d] %s ... done\n", i+1, len(documentIDs), documentID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if failed > 0 {
		fmt.Printf("\nSummary: %d document(s) — %d successful, %d failed\n", len(documentIDs), len(documentIDs)-failed, failed)
		return fmt.Errorf("%d document(s) failed", failed)
	}
	fmt.Printf("\nSummary: %d document(s) — all successful\n", len(documentIDs))
	return nil
}

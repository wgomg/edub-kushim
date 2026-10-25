package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/ops/consume"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

func backfillThumbnailsHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help("Usage: kushim thumbnails <mode> [flags]\n\n" +
		"Modes:\n" +
		"  --all                Generate every missing thumbnail now\n" +
		"  --document <id>      Generate one document's thumbnail now\n\n" +
		"Flags:\n" +
		"  --force              Proceed even if thumbnails are disabled in config\n" +
		"  --no-wait            Exit non-zero instead of waiting for a lock or in-flight work\n\n" +
		"  Runs immediately under the storage hold, creating no queue rows.\n" +
		"  To enqueue a backfill: kushim batch create thumbnails [--from-batch <id> | --document <id>]") {
		return nil
	}

	if slices.Contains(args, "--batch") {
		return fmt.Errorf("--batch is now --from-batch: use 'kushim batch create thumbnails --from-batch <id>' " +
			"to enqueue a backfill for a batch's documents, or 'kushim batch run <id>' to execute a queued batch")
	}

	var all bool
	fp.Bool("--all", &all)
	var documentID string
	if err := fp.String("--document", &documentID); err != nil {
		return err
	}
	var batchID, execTokenParam string
	if err := fp.String("--from-batch", &batchID); err != nil {
		return err
	}
	if err := fp.String("--exec-token", &execTokenParam); err != nil {
		return err
	}
	var force bool
	fp.Bool("--force", &force)
	noWait := false
	fp.Bool("--no-wait", &noWait)

	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown argument: %s", rest[0])
	}

	modes := 0
	if all {
		modes++
	}
	if documentID != "" {
		modes++
	}
	if batchID != "" || execTokenParam != "" {
		modes++
	}
	if modes != 1 {
		return fmt.Errorf("exactly one of --all, --document <id>, --from-batch <id> is required")
	}

	if batchID != "" || execTokenParam != "" {
		if batchID == "" {
			return fmt.Errorf("--exec-token requires --from-batch <id>")
		}
		_, err := consume.RunClaimedBatch(c, batchID, execTokenParam, nil)
		return err
	}

	cfg := c.GetConfig()
	if !cfg.Consumer.Thumbnail.Enabled && !force {
		return fmt.Errorf("thumbnails are disabled in config (consumer.thumbnail.enabled); use --force to override")
	}

	var specs []task.TaskSpec
	var err error
	switch {
	case all:
		specs, err = thumbnailSpecsForAll(c)
	case documentID != "":
		specs, err = thumbnailSpecsForDocument(c, documentID)
	}
	if err != nil {
		return err
	}

	return runThumbnailsDirect(c, specs, noWait)
}

// thumbnailSpecsForAll runs the thumbbackfill definition's builder over every document missing a thumbnail,
// the same selection the schedule and `batch create thumbnails` use.
func thumbnailSpecsForAll(c *container.Container) ([]task.TaskSpec, error) {
	catalog, err := c.GetCatalog()
	if err != nil {
		return nil, fmt.Errorf("task catalog: %w", err)
	}
	def, err := catalog.Definition(types.Task.Type.ThumbBackfill)
	if err != nil {
		return nil, fmt.Errorf("thumbbackfill definition: %w", err)
	}
	specs, err := def.BuildTasks(context.Background(), container.NewEmptyBuildInput(types.Task.Type.ThumbBackfill))
	if err != nil {
		return nil, err
	}
	return specs, nil
}

// thumbnailSpecsForDocument selects one document's missing thumbnail.
func thumbnailSpecsForDocument(c *container.Container, documentID string) ([]task.TaskSpec, error) {
	client, err := c.GetClient()
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	ctx := context.Background()

	if _, err := client.Queries.GetDocument(ctx, documentID); err != nil {
		if errs.KindOf(errs.FromDB(err, "get document")) == errs.KindNotFound {
			return nil, fmt.Errorf("document %s not found", documentID)
		}
		return nil, errs.FromDB(err, "get document")
	}

	row, err := client.Queries.GetDocumentWithoutThumbnail(ctx, documentID)
	if err != nil {
		if errs.KindOf(errs.FromDB(err, "get document without thumbnail")) == errs.KindNotFound {
			return nil, fmt.Errorf("document %s already has a thumbnail", documentID)
		}
		return nil, errs.FromDB(err, "get document without thumbnail")
	}

	payload, err := json.Marshal(map[string]any{
		"document_id":  row.DocumentID,
		"storage_path": row.StoragePath,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal thumbnail payload: %w", err)
	}
	return []task.TaskSpec{{Type: types.Task.Type.ThumbBackfill, Payload: payload}}, nil
}

// runThumbnailsDirect generates the selected thumbnails immediately under the manual storage hold. A
// failed document is reported and the run continues; the command exits non-zero when any failed.
func runThumbnailsDirect(c *container.Container, specs []task.TaskSpec, noWait bool) error {
	if len(specs) == 0 {
		fmt.Println("No documents missing thumbnails.")
		return nil
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	catalog, err := c.GetCatalog()
	if err != nil {
		return fmt.Errorf("task catalog: %w", err)
	}
	def, err := catalog.Definition(types.Task.Type.Thumbnail)
	if err != nil {
		return fmt.Errorf("thumbnail definition: %w", err)
	}
	handler, err := c.GetHandler(types.Task.Type.Thumbnail)
	if err != nil {
		return fmt.Errorf("thumbnail handler: %w", err)
	}

	ctx := context.Background()
	failed := 0
	fmt.Printf("Generating %d thumbnail(s)...\n", len(specs))
	err = task.RunDirect(ctx, client, c.GetLogger(), "thumbnails", def.LocksForDirectMode(), !noWait, nil, func() error {
		for i, spec := range specs {
			_, err := handler.Handle(ctx, task.Task{
				TaskID:   uuid.New().String(),
				TaskType: spec.Type,
				Payload:  spec.Payload,
			})
			if err != nil {
				failed++
				fmt.Printf("  [%d/%d] ... failed: %v\n", i+1, len(specs), err)
				continue
			}
			fmt.Printf("  [%d/%d] ... done\n", i+1, len(specs))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if failed > 0 {
		fmt.Printf("\nSummary: %d thumbnail(s) — %d successful, %d failed\n", len(specs), len(specs)-failed, failed)
		return fmt.Errorf("%d thumbnail(s) failed", failed)
	}
	fmt.Printf("\nSummary: %d thumbnail(s) — all successful\n", len(specs))
	return nil
}

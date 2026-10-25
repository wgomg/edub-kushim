// Package consume is the kushim consume verb: the direct mode that consumes the inbox immediately under
// the manual maintenance lock, the batch mode that executes one claimed consume-family batch through the
// shared execution library, and the cancel subcommand.
package consume

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const progressInterval = 500 * time.Millisecond

func watchSignals(ctx context.Context, cancel context.CancelFunc) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()
}

// Run is the kushim consume verb. With "cancel" it cancels a running batch; with --from-batch it executes
// one claimed batch under the execution token the dispatcher or batch run passed; otherwise it consumes
// the inbox immediately, creating no batch or task rows. Use "kushim batch create consume" to enqueue
// ingest work for the queue.
func Run(c *container.Container, args []string) error {
	if len(args) > 0 && args[0] == "cancel" {
		return consumeCancelHandler(c, args[1:])
	}

	fp := flags.NewFlagParser(args)

	if fp.Help("Usage: kushim consume [--from-batch <id> --exec-token <id>] [--no-wait]\n" +
		"  Scan the inbox and consume its files immediately, without queue rows.\n\n" +
		"  --from-batch <id>  execute one claimed batch (dispatcher and 'kushim batch run' contract)\n" +
		"  --exec-token <id>  execution token passed with --from-batch\n" +
		"  --no-wait          exit non-zero instead of waiting for a lock or in-flight work\n\n" +
		"  To enqueue an ingest batch: kushim batch create consume\n\n" +
		"Subcommands:\n" +
		"  cancel <id>       cancel a running batch") {
		return nil
	}

	if slices.Contains(args, "--batch") {
		return fmt.Errorf("--batch is now --from-batch: run 'kushim batch run <id>' to execute a queued batch, " +
			"or 'kushim batch create consume' to enqueue one")
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

	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag: %s", rest[0])
	}

	if batchID != "" || execTokenParam != "" {
		if batchID == "" {
			return fmt.Errorf("--exec-token requires --from-batch <id>")
		}
		return runBatchMode(c, batchID, execTokenParam)
	}

	return runDirect(c, noWait)
}

// runBatchMode executes one claimed consume-family batch: it gates on the external tools a consume task
// needs, then hands the run to `RunClaimedBatch` with the consume file-quarantine hook.
func runBatchMode(c *container.Container, batchID, execTokenParam string) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	ctx := context.Background()

	missingTools := config.FilterToolErrors(config.MissingExternalTools(c.GetConfig()))
	if len(missingTools) > 0 {
		hasConsumeTasks, listErr := batchHasConsumeTasks(ctx, client, batchID)
		if listErr != nil || hasConsumeTasks {
			printToolBlock(missingTools)
			return fmt.Errorf("consume blocked: missing required external tools")
		}
	}

	cfg := c.GetConfig()
	maxRetries := int32(cfg.Consumer.Reclaim.MaxRetries)
	defer triggerOrphanScan(c)

	result, err := RunClaimedBatch(c, batchID, execTokenParam, func(qctx context.Context, taskIDs []string) error {
		return consumption.QuarantineFailedFiles(qctx, client.Queries, cfg.Storage.StorageDir,
			c.GetLogger(), batchID, maxRetries, taskIDs)
	})
	if err != nil {
		return err
	}
	if result.Paused {
		return nil
	}

	enrichment.LogBatchBookend(ctx, client.Queries, c.GetLogger(), batchID, result.Status)
	return nil
}

// RunClaimedBatch executes one claimed batch through the shared execution library. The dispatcher and
// batch run own the claim and pass the execution token, so this child adopts the lease, runs the batch's
// tasks through their registered handlers and settles. quarantine is the host's consume-file cleanup
// hook; nil is correct for commands whose batches never carry consume tasks. The paused notice is printed
// here; an error means the run was interrupted or could not start, and the process must exit non-zero.
func RunClaimedBatch(c *container.Container, batchID, execTokenParam string, quarantine func(ctx context.Context, taskIDs []string) error) (task.BatchRunResult, error) {
	token, err := parseExecToken(execTokenParam)
	if err != nil {
		return task.BatchRunResult{}, err
	}

	client, err := c.GetClient()
	if err != nil {
		return task.BatchRunResult{}, fmt.Errorf("failed to get database: %w", err)
	}
	catalog, err := c.GetCatalog()
	if err != nil {
		return task.BatchRunResult{}, fmt.Errorf("task catalog: %w", err)
	}
	store, err := c.GetStore()
	if err != nil {
		return task.BatchRunResult{}, fmt.Errorf("task store: %w", err)
	}
	registry, err := c.GetRegistry()
	if err != nil {
		return task.BatchRunResult{}, fmt.Errorf("task registry: %w", err)
	}

	deps := task.BatchRunDeps{
		Client:     client,
		Logger:     c.GetLogger(),
		Catalog:    catalog,
		Store:      store,
		Registry:   registry,
		MaxRetries: int32(c.GetConfig().Consumer.Reclaim.MaxRetries),
		Quarantine: quarantine,
	}

	c.GetLogger().SetLevel(log.LevelSilent)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchSignals(ctx, cancel)

	if batch, readErr := client.Queries.GetBatch(ctx, batchID); readErr == nil && batch.Status == types.Batch.Status.Paused {
		fmt.Printf("Batch %s is paused due to an LLM provider credit/balance error.\n", batchID)
		fmt.Printf("Resolve the billing issue, then re-queue the batch and run this command again.\n")
		return task.BatchRunResult{Status: batch.Status, Paused: true}, nil
	}

	progressCtx, stopProgress := context.WithCancel(ctx)
	defer stopProgress()
	go streamBatchProgress(progressCtx, client.Queries, batchID)

	result, err := task.RunBatch(ctx, deps, batchID, token)
	stopProgress()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Println("\nBatch cancelled")
		}
		return result, err
	}
	if result.Paused {
		fmt.Printf("\nBatch paused: LLM provider credit/balance error detected. Resolve billing and re-queue the batch.\n")
	}
	if result.Status == types.Batch.Status.Processing {
		fmt.Printf("\nWarning: batch %s did not settle; tasks remain active and the batch is left to recovery.\n", batchID)
	}
	return result, nil
}

// runDirect consumes the inbox immediately under the manual maintenance lock: no batch and no task rows
// are created, the tasks of a failed file are reported and skipped, and the command exits non-zero when
// anything failed.
func runDirect(c *container.Container, noWait bool) error {
	cfg := c.GetConfig()
	allTools := config.MissingExternalTools(cfg)
	missingTools := config.FilterToolErrors(allTools)
	if len(missingTools) > 0 {
		printToolBlock(missingTools)
		return fmt.Errorf("consume blocked: missing required external tools")
	}
	printOcrmypdfAdvisory(cfg, allTools)

	files, err := consumption.GetFiles(cfg.Storage.ConsumptionDir, cfg.Consumer.SupportedFiles, cfg.Consumer.MaxFilesPerBatch)
	if err != nil {
		return fmt.Errorf("failed to scan inbox: %w", err)
	}
	if len(files) == 0 {
		fmt.Println("No files found in consumption directory")
		return nil
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	catalog, err := c.GetCatalog()
	if err != nil {
		return fmt.Errorf("task catalog: %w", err)
	}
	def, err := catalog.Definition(types.Task.Type.Consume)
	if err != nil {
		return fmt.Errorf("consume definition: %w", err)
	}

	run, err := newDirectRun(c, cfg.Consumer.Thumbnail.Enabled)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchSignals(ctx, cancel)

	fmt.Printf("Consuming %d file(s)...\n", len(files))
	err = task.RunDirect(ctx, client, c.GetLogger(), "consume", def.LocksForDirectMode(), !noWait, nil, func() error {
		return run.process(ctx, files)
	})
	if err != nil {
		return err
	}
	if run.failed > 0 {
		return fmt.Errorf("%d task(s) failed", run.failed)
	}

	triggerOrphanScan(c)
	return nil
}

// directRun holds the handlers and counters of one immediate inbox run: tasks counts every attempted step
// and every failure lands inside one, so the derived successful count cannot go negative.
type directRun struct {
	consume    task.Handler
	enrich     task.Handler
	thumbnail  task.Handler
	files      int
	tasks      int
	failed     int
	showThumbs bool
}

func newDirectRun(c *container.Container, thumbnailsEnabled bool) (*directRun, error) {
	consumeHandler, err := c.GetHandler(types.Task.Type.Consume)
	if err != nil {
		return nil, fmt.Errorf("consume handler: %w", err)
	}
	enrichHandler, err := c.GetHandler(types.Task.Type.Enrich)
	if err != nil {
		return nil, fmt.Errorf("enrich handler: %w", err)
	}
	run := &directRun{consume: consumeHandler, enrich: enrichHandler, showThumbs: thumbnailsEnabled}
	if thumbnailsEnabled {
		thumbnailHandler, err := c.GetHandler(types.Task.Type.Thumbnail)
		if err != nil {
			return nil, fmt.Errorf("thumbnail handler: %w", err)
		}
		run.thumbnail = thumbnailHandler
	}
	return run, nil
}

// process consumes the scanned files sequentially: the consume action, then the document's enrichment
// and, when enabled, its thumbnail. A failed step is reported and the run continues with the next file;
// a provider credit error aborts, because every remaining document would fail the same way.
func (r *directRun) process(ctx context.Context, files []consumption.File) error {
	r.files = len(files)
	for i, file := range files {
		index := i + 1

		r.tasks++
		md5hash, err := storage.CalculateMD5(file.OriginalPath)
		if err != nil {
			r.fail(index, types.Task.Type.Consume, file.Name, fmt.Errorf("md5: %w", err))
			continue
		}

		documentID := uuid.New().String()
		consumePayload, err := json.Marshal(map[string]any{
			"file_path":   file.OriginalPath,
			"file_index":  index,
			"document_id": documentID,
			"md5":         md5hash,
		})
		if err != nil {
			r.fail(index, types.Task.Type.Consume, file.Name, fmt.Errorf("marshal consume payload: %w", err))
			continue
		}

		result, err := r.consume.Handle(ctx, directTask(types.Task.Type.Consume, consumePayload))
		if err != nil {
			r.fail(index, types.Task.Type.Consume, file.Name, err)
			if pauseRequested(err) {
				return fmt.Errorf("consume aborted: %w", err)
			}
			continue
		}
		r.done(index, types.Task.Type.Consume, file.Name)

		var consumed struct {
			StoragePath string `json:"storage_path"`
		}
		if err := json.Unmarshal(result, &consumed); err != nil {
			r.fail(index, types.Task.Type.Consume, file.Name, fmt.Errorf("parse consume result: %w", err))
			continue
		}

		r.tasks++
		enrichPayload, err := json.Marshal(map[string]any{
			"document_id": documentID,
			"file_name":   file.Name,
			"file_index":  index,
		})
		if err != nil {
			r.fail(index, types.Task.Type.Enrich, file.Name, fmt.Errorf("marshal enrich payload: %w", err))
			continue
		}
		if _, err := r.enrich.Handle(ctx, directTask(types.Task.Type.Enrich, enrichPayload)); err != nil {
			r.fail(index, types.Task.Type.Enrich, file.Name, err)
			if pauseRequested(err) {
				return fmt.Errorf("enrich aborted: %w", err)
			}
			continue
		}
		r.done(index, types.Task.Type.Enrich, file.Name)

		if !r.showThumbs {
			continue
		}
		r.tasks++
		if consumed.StoragePath == "" {
			r.fail(index, types.Task.Type.Thumbnail, file.Name, fmt.Errorf("consume returned no storage path"))
			continue
		}
		thumbnailPayload, err := json.Marshal(map[string]any{
			"document_id":  documentID,
			"storage_path": consumed.StoragePath,
			"file_name":    file.Name,
			"file_index":   index,
		})
		if err != nil {
			r.fail(index, types.Task.Type.Thumbnail, file.Name, fmt.Errorf("marshal thumbnail payload: %w", err))
			continue
		}
		if _, err := r.thumbnail.Handle(ctx, directTask(types.Task.Type.Thumbnail, thumbnailPayload)); err != nil {
			r.fail(index, types.Task.Type.Thumbnail, file.Name, err)
			if pauseRequested(err) {
				return fmt.Errorf("thumbnail aborted: %w", err)
			}
			continue
		}
		r.done(index, types.Task.Type.Thumbnail, file.Name)
	}

	r.printSummary()
	return nil
}

func (r *directRun) done(index int, taskType types.TaskType, fileName string) {
	fmt.Printf("  [%d/%d] %-8s %s ... done\n", index, r.files, taskType, fileName)
}

func (r *directRun) fail(index int, taskType types.TaskType, fileName string, err error) {
	r.failed++
	fmt.Printf("  [%d/%d] %-8s %s ... failed: %v\n", index, r.files, taskType, fileName, err)
}

func (r *directRun) printSummary() {
	if r.failed > 0 {
		fmt.Printf("\nSummary: %d files, %d tasks — %d successful, %d failed\n", r.files, r.tasks, r.tasks-r.failed, r.failed)
		return
	}
	fmt.Printf("\nSummary: %d files, %d tasks — all successful\n", r.files, r.tasks)
}

func directTask(taskType types.TaskType, payload json.RawMessage) task.Task {
	return task.Task{TaskID: uuid.New().String(), TaskType: taskType, Payload: payload}
}

// pauseRequested reports whether a handler error asks the execution to pause for a provider credit or
// balance problem; a direct run has no batch to pause, so it stops instead.
func pauseRequested(err error) bool {
	tErr, ok := errors.AsType[*task.Error](err)
	return ok && tErr.PauseBatch
}

// parseExecToken parses the execution token the dispatcher or batch run passes with --from-batch. The
// token is required: the bare selector is no longer a self-claim path.
func parseExecToken(value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, fmt.Errorf("--from-batch requires --exec-token: run 'kushim batch run <id>' to execute a queued batch")
	}
	token, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid exec token %q: %w", value, err)
	}
	return token, nil
}

func batchHasConsumeTasks(ctx context.Context, client *database.Client, batchID string) (bool, error) {
	tasks, err := task.ListFiltered(ctx, client.Queries, task.TaskFilter{
		BatchID:  batchID,
		TaskType: string(types.Task.Type.Consume),
		Limit:    1,
	})
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

func consumeCancelHandler(c *container.Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim consume cancel <batch-id>\n\n" +
			"  Cancel a running batch. Pending tasks are marked as cancelled\n" +
			"  and a SIGTERM is sent to the process currently processing the batch.")
		if len(args) == 0 {
			return fmt.Errorf("cancel requires a batch ID")
		}
		return nil
	}

	batchID := args[0]

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := task.CancelBatch(cancelCtx, client, c.GetLogger(), batchID)
	if err != nil {
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

type taskDisplay struct {
	index    int
	fileName string
	taskType string
}

func taskDisplayInfo(t database.ListBatchTaskProgressRow) taskDisplay {
	payload := task.ReadPayload(t.Payload)
	return taskDisplay{
		index:    payload.FileIndex,
		fileName: payload.DisplayName(),
		taskType: string(t.TaskType),
	}
}

func totalFiles(tasks []database.ListBatchTaskProgressRow) int {
	maxIdx := 0
	for _, t := range tasks {
		if t.TaskType == types.Task.Type.Consume {
			info := taskDisplayInfo(t)
			if info.index > maxIdx {
				maxIdx = info.index
			}
		}
	}
	if maxIdx > 0 {
		return maxIdx
	}

	n := 0
	for _, t := range tasks {
		if t.TaskType == types.Task.Type.Consume {
			n++
		}
	}
	return n
}

// streamBatchProgress prints a batch's task transitions to stdout and one closing summary, then returns
// once no pending or processing task is left (or the context ends). It only observes: the execution
// library owns pause, completion and settlement.
func streamBatchProgress(ctx context.Context, queries *database.Queries, batchID string) {
	previous := make(map[string]string)
	previousProgress := make(map[string]string)

	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		tasks, err := queries.ListBatchTaskProgress(ctx, sql.NullString{String: batchID, Valid: true})
		if err != nil {
			continue
		}

		total := totalFiles(tasks)
		remain := 0
		for _, t := range tasks {
			switch t.Status {
			case types.Task.Status.Processing:
				prog := ""
				if t.Progress != nil {
					var p task.ProgressSnapshot
					if json.Unmarshal(*t.Progress, &p) == nil && p.Step != "" {
						prog = p.Step
						if p.Detail != "" {
							prog += " " + p.Detail
						}
					}
				}
				if previous[t.TaskID] == "pending" {
					info := taskDisplayInfo(t)
					status := "processing"
					if prog != "" {
						status = prog
					}
					fmt.Printf("  [%d/%d] %-8s %s ... %s\n", info.index, total, info.taskType, info.fileName, status)
				} else if prog != "" && previousProgress[t.TaskID] != prog {
					info := taskDisplayInfo(t)
					fmt.Printf("  [%d/%d] %-8s %s ... %s\n", info.index, total, info.taskType, info.fileName, prog)
				}
				previousProgress[t.TaskID] = prog
			case types.Task.Status.Completed:
				if previous[t.TaskID] != "completed" && previous[t.TaskID] != "" {
					info := taskDisplayInfo(t)
					fmt.Printf("  [%d/%d] %-8s %s ... done\n", info.index, total, info.taskType, info.fileName)
				}
			case types.Task.Status.Failed:
				if previous[t.TaskID] != "failed" && previous[t.TaskID] != "" {
					info := taskDisplayInfo(t)
					errMsg := ""
					if t.Error.Valid {
						errMsg = fmt.Sprintf(": %s", t.Error.String)
					}
					fmt.Printf("  [%d/%d] %-8s %s ... failed%s\n", info.index, total, info.taskType, info.fileName, errMsg)
				}
			}
			if t.Status == types.Task.Status.Pending || t.Status == types.Task.Status.Processing {
				remain++
			}
			previous[t.TaskID] = string(t.Status)
		}

		if remain == 0 {
			printBatchSummary(tasks)
			return
		}
	}
}

func printBatchSummary(tasks []database.ListBatchTaskProgressRow) {
	var files, taskCount, failed int64
	for _, t := range tasks {
		taskCount++
		if t.TaskType == types.Task.Type.Consume {
			files++
		}
		if t.Status == types.Task.Status.Failed {
			failed++
		}
	}
	if failed > 0 {
		fmt.Printf("\nSummary: %d files, %d tasks — %d successful, %d failed\n", files, taskCount, taskCount-failed, failed)
		return
	}
	fmt.Printf("\nSummary: %d files, %d tasks — all successful\n", files, taskCount)
}

func triggerOrphanScan(c *container.Container) {
	client, err := c.GetClient()
	if err != nil {
		c.GetLogger().Error("", "orphan scan: get client: %v", err)
		return
	}
	enqueuer, err := c.GetEnqueuer()
	if err != nil {
		c.GetLogger().Error("", "orphan scan: get enqueuer: %v", err)
		return
	}
	svc := service.NewOrphaned(client.Queries, c.GetConfig(), c.GetLogger(), enqueuer)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	count, err := svc.ScanAndQuarantine(ctx)
	if err != nil {
		c.GetLogger().Error("", "orphan scan after batch: %v", err)
		return
	}
	if count > 0 {
		c.GetLogger().Info("", "post-batch orphan scan: %d files quarantined", count)
	}
}

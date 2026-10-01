// Package consume is the per-batch consume driver: one child process per
// batch, processing its consume, enrich and thumbnail tasks through task
// pools, with batch ownership, heartbeat, the two-phase cancel protocol,
// and the credit-pause handling. It also exposes IsAlive, the process
// liveness check the queue daemon's stale reclamation shares.
package consume

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const (
	heartbeatInterval = 5 * time.Second
)

// ErrBatchPaused tells the queue the batch hit a credit or balance error
// and should pause instead of fail.
var ErrBatchPaused = errors.New("batch paused due to LLM credit/balance error")

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

type poolSet struct {
	consume       *task.Pool
	enrich        *task.Pool
	thumbnail     *task.Pool
	thumbbackfill *task.Pool
}

func (p poolSet) forSource(source types.BatchSource) []*task.Pool {
	if source == types.Batch.Source.Thumbbackfill {
		return []*task.Pool{p.thumbbackfill}
	}
	return []*task.Pool{p.consume, p.enrich, p.thumbnail}
}

func fetchPools(c *container.Container) (poolSet, error) {
	consumePool, err := c.GetPool(types.Task.Type.Consume)
	if err != nil {
		return poolSet{}, fmt.Errorf("failed to get pool: %w", err)
	}
	enrichPool, err := c.GetPool(types.Task.Type.Enrich)
	if err != nil {
		return poolSet{}, fmt.Errorf("failed to get enrich pool: %w", err)
	}
	thumbnailPool, err := c.GetPool(types.Task.Type.Thumbnail)
	if err != nil {
		return poolSet{}, fmt.Errorf("failed to get thumbnail pool: %w", err)
	}
	backfillPool, err := c.GetPool(types.Task.Type.ThumbBackfill)
	if err != nil {
		return poolSet{}, fmt.Errorf("failed to get thumbbackfill pool: %w", err)
	}
	return poolSet{
		consume:       consumePool,
		enrich:        enrichPool,
		thumbnail:     thumbnailPool,
		thumbbackfill: backfillPool,
	}, nil
}

type batchRun struct {
	client  *database.Client
	owner   *task.Owner
	ownerID string
	batchID string
	source  types.BatchSource
	pools   poolSet
}

// Run is the kushim consume verb: with "cancel" it drives the batch
// cancel protocol; otherwise it claims the given (or next queued) batch,
// takes ownership, and runs its tasks through the pools until done or
// interrupted. The parent process stays clean and shells the heavy
// lifting to per-task children where the adapter does so.
func Run(c *container.Container, args []string) error {
	if len(args) > 0 && args[0] == "cancel" {
		return consumeCancelHandler(c, args[1:])
	}

	fp := flags.NewFlagParser(args)

	if fp.Help("Usage: kushim consume [--batch <id>]\n" +
		"  Scan inbox, create one task per file, and process them.\n" +
		"  Streams per-file progress to stdout.\n\n" +
		"  Prerequisite: kushim hugot must be running (sibling process).\n" +
		"  Start it in a separate terminal or via systemd before running consume.\n\n" +
		"  --batch <id>      resume processing of an existing batch\n" +
		"  --force           override stale PID file lock\n\n" +
		"Subcommands:\n" +
		"  cancel <id>       cancel a running batch") {
		return nil
	}

	var batchIDParam string
	fp.String("--batch", &batchIDParam)

	force := false
	fp.Bool("--force", &force)

	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag: %s", rest[0])
	}

	pools, err := fetchPools(c)
	if err != nil {
		return err
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	allTools := config.MissingExternalTools(c.GetConfig())
	missingTools := config.FilterToolErrors(allTools)
	if len(missingTools) > 0 {
		printToolBlock(missingTools)
	}

	if batchIDParam != "" {
		if len(missingTools) > 0 {
			tasks, listErr := task.ListFiltered(ctx, client.Queries, task.TaskFilter{
				BatchID: batchIDParam,
			})
			hasConsumeTasks := false
			if listErr == nil {
				for _, t := range tasks {
					if t.TaskType == types.Task.Type.Consume {
						hasConsumeTasks = true
						break
					}
				}
			}
			if hasConsumeTasks || listErr != nil {
				return fmt.Errorf("consume blocked: missing required external tools")
			}
		}

		batch, batchErr := client.Queries.GetBatch(ctx, batchIDParam)
		if batchErr == nil && batch.Status == types.Batch.Status.Paused {
			fmt.Printf("Batch %s is paused due to an LLM provider credit/balance error.\n", batchIDParam)
			fmt.Printf("Resolve the billing issue, then re-queue the batch and run this command again.\n")
			return nil
		}

		batchSource := types.Batch.Source.CLI
		if batchErr == nil {
			batchSource = batch.Source
		}

		ownerID := uuid.New().String()
		pid := os.Getpid()
		owner := task.NewOwner(client, ownerID, pid, c.GetLogger(), c.GetConfig().Consumer.Reclaim.MaxRetries)
		c.GetLogger().Info("", "consume: acquiring batch %s (PID=%d, ownerID=%s)", batchIDParam, pid, ownerID)

		if force {
			// the queue daemon pre-creates a placeholder owner row and
			// forks with --force: take it directly instead of failing
			// the plain Acquire against the live placeholder first
			if err := owner.AcquireForce(ctx, batchIDParam); err != nil {
				return fmt.Errorf("force acquire: %w", err)
			}
		} else if err := owner.Acquire(ctx, batchIDParam); err != nil {
			if err == task.ErrBatchLocked {
				bo, boErr := client.GetBatchOwner(ctx, batchIDParam)
				if boErr == nil {
					return fmt.Errorf("batch %s is being processed by PID %d (use --force to override)", batchIDParam, bo.Pid)
				}
				return fmt.Errorf("batch %s is being processed by another process (use --force to override)", batchIDParam)
			}
			return fmt.Errorf("acquire batch %s: %w", batchIDParam, err)
		}

		c.GetLogger().Debug("", "consume: GOMAXPROCS=%d", runtime.GOMAXPROCS(0))

		reset, _ := owner.ResetProcessingByBatch(ctx, batchIDParam)
		if reset > 0 {
			fmt.Printf("  reset %d orphaned processing tasks\n", reset)
		}
		if err := consumption.QuarantineFailedFiles(ctx, client.Queries, c.GetConfig().Storage.StorageDir, c.GetLogger(), batchIDParam); err != nil {
			c.GetLogger().Error("", "quarantine files for batch %s: %v", batchIDParam, err)
		}

		return runBatchThroughPools(ctx, cancel, c, batchRun{
			client:  client,
			owner:   owner,
			ownerID: ownerID,
			batchID: batchIDParam,
			source:  batchSource,
			pools:   pools,
		})
	}

	printOcrmypdfAdvisory(c.GetConfig(), allTools)

	if len(missingTools) > 0 {
		return fmt.Errorf("consume blocked: missing required external tools")
	}

	pausedIDs, err := client.Queries.ListPausedBatches(ctx)
	if err != nil {
		return fmt.Errorf("check paused batches: %w", err)
	}
	if len(pausedIDs) > 0 {
		return fmt.Errorf(
			"%d paused batch(es) exist due to LLM credit/balance errors: %s\n"+
				"Resolve the billing issue, then resume or cancel each paused batch before creating new ones.",
			len(pausedIDs), strings.Join(pausedIDs, ", "),
		)
	}

	files, err := consumption.GetFiles(
		c.GetConfig().Storage.ConsumptionDir,
		c.GetConfig().Consumer.SupportedFiles,
		c.GetConfig().Consumer.MaxFilesPerBatch,
	)
	if err != nil {
		return fmt.Errorf("failed to scan inbox: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No files found in consumption directory")
		return nil
	}

	dispatcher, err := c.GetDispatcher()
	if err != nil {
		return fmt.Errorf("failed to get dispatcher: %w", err)
	}

	batchID := uuid.New().String()
	enqueued := 0
	thumbnailsEnabled := c.GetConfig().Consumer.Thumbnail.Enabled
	for i, f := range files {
		consumeTaskID := uuid.New().String()
		enrichTaskID := uuid.New().String()
		thumbnailTaskID := uuid.New().String()
		documentID := uuid.New().String()

		consumePayloadMap := map[string]any{
			"file_path":    f.OriginalPath,
			"file_index":   i + 1,
			"on_completed": enrichTaskID,
			"document_id":  documentID,
		}
		if thumbnailsEnabled {
			consumePayloadMap["on_completed_thumbnail"] = thumbnailTaskID
		}
		consumePayload, _ := json.Marshal(consumePayloadMap)
		_, err := dispatcher.Enqueue(ctx, types.Task.Type.Consume, batchID, consumePayload, consumeTaskID)
		if err != nil {
			c.GetLogger().Error(documentID, "enqueue %s: %v", f.OriginalPath, err)
			continue
		}
		enqueued++

		enrichPayload, _ := json.Marshal(map[string]any{
			"waiting_for": consumeTaskID,
			"file_name":   filepath.Base(f.OriginalPath),
			"file_index":  i + 1,
		})
		if _, err := dispatcher.Enqueue(ctx, types.Task.Type.Enrich, batchID, enrichPayload, enrichTaskID, types.Task.Status.Waiting); err != nil {
			c.GetLogger().Error(documentID, "create enrich task for %s: %v", f.OriginalPath, err)
		}

		if thumbnailsEnabled {
			thumbnailPayload, _ := json.Marshal(map[string]any{
				"waiting_for": consumeTaskID,
				"file_name":   filepath.Base(f.OriginalPath),
				"file_index":  i + 1,
			})
			if _, err := dispatcher.Enqueue(ctx, types.Task.Type.Thumbnail, batchID, thumbnailPayload, thumbnailTaskID, types.Task.Status.Waiting); err != nil {
				c.GetLogger().Error(documentID, "create thumbnail task for %s: %v", f.OriginalPath, err)
			}
		}
	}

	if enqueued == 0 {
		fmt.Printf("No new files to consume\n")
		return nil
	}

	batchSvc := service.NewBatch(client, c.GetConfig().Consumer.Reclaim.MaxRetries)

	countBefore, err := batchSvc.CountQueuedConsumeBatches(ctx)
	if err != nil {
		return fmt.Errorf("failed to check queue: %w", err)
	}

	if err := client.CreateBatch(ctx, database.CreateBatchParams{
		ID:     batchID,
		Source: types.Batch.Source.CLI,
		Status: types.Batch.Status.Queued,
	}); err != nil {
		return fmt.Errorf("create batch: %w", err)
	}

	fmt.Printf("Batch: %s\n", batchID)

	if countBefore > 0 {
		fmt.Println("Batch queued — kushim queue will pick it up. Run 'kushim queue' if you haven't started it yet.")
		return nil
	}

	for i, file := range files {
		fmt.Printf("[%d/%d] %s → queued\n", i+1, len(files), file.Name)
	}

	fmt.Println("Waiting for completion...")

	ownerID := uuid.New().String()
	owner := task.NewOwner(client, ownerID, os.Getpid(), c.GetLogger(), c.GetConfig().Consumer.Reclaim.MaxRetries)

	if err := owner.Acquire(ctx, batchID); err != nil {
		return fmt.Errorf("acquire batch %s: %w", batchID, err)
	}

	return runBatchThroughPools(ctx, cancel, c, batchRun{
		client:  client,
		owner:   owner,
		ownerID: ownerID,
		batchID: batchID,
		source:  types.Batch.Source.CLI,
		pools:   pools,
	})
}

// The post-acquire sequence both halves of Run share.
func runBatchThroughPools(ctx context.Context, cancel context.CancelFunc, c *container.Container, run batchRun) error {
	if err := c.SetRunnerOwnerID(run.ownerID); err != nil {
		return fmt.Errorf("set runner owner: %w", err)
	}

	watchSignals(ctx, cancel)

	hb := task.NewHeartbeat(run.owner, heartbeatInterval, c.GetLogger())
	hb.Start(ctx)

	err := pollBatch(ctx, run, c.GetLogger())
	if err == nil {
		batchSvc := service.NewBatch(run.client, c.GetConfig().Consumer.Reclaim.MaxRetries)
		if setErr := setBatchTerminalStatus(ctx, run.client.Queries, batchSvc, c.GetLogger(), run.batchID); setErr != nil {
			c.GetLogger().Error("", "set batch terminal status %s: %v", run.batchID, setErr)
		}
	} else if errors.Is(err, ErrBatchPaused) {
		err = nil
	}

	triggerOrphanScan(c)

	hb.Stop()
	relCtx, relCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if relErr := run.owner.Release(relCtx, run.batchID); relErr != nil {
		c.GetLogger().Error("", "release batch %s: %v", run.batchID, relErr)
	}
	relCancel()

	return err
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

	releaseOwner := func(ownerID string) {
		if _, err := client.ReleaseBatchOwner(cancelCtx, database.ReleaseBatchOwnerParams{
			BatchID: batchID,
			OwnerID: ownerID,
		}); err != nil {
			c.GetLogger().Error("", "release batch owner for %s: %v", batchID, err)
		}
	}

	count, err := client.CancelPendingTasksByBatch(cancelCtx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return fmt.Errorf("cancel pending tasks: %w", err)
	}

	bo, err := client.GetBatchOwner(cancelCtx, batchID)
	if err != nil {
		if err == sql.ErrNoRows {
			fmt.Printf("No running process found for batch %s\n", batchID)
			fmt.Printf("%d pending tasks cancelled\n", count)
			return nil
		}
		return fmt.Errorf("get batch owner: %w", err)
	}

	if !IsAlive(bo.Pid) {
		fmt.Printf("Process %d is no longer running\n", bo.Pid)
		fmt.Printf("%d pending tasks cancelled\n", count)
		releaseOwner(bo.OwnerID)
		return nil
	}

	if err := syscall.Kill(int(bo.Pid), syscall.SIGTERM); err != nil {
		c.GetLogger().Error("", "signal SIGTERM to PID %d for batch %s: %v", bo.Pid, batchID, err)
	}

	tx, txErr := client.BeginTx(cancelCtx, nil)
	if txErr != nil {
		return fmt.Errorf("begin cancel transaction: %w", txErr)
	}
	bid := sql.NullString{String: batchID, Valid: true}
	txQ := client.Queries.WithTx(tx)
	if _, relErr := txQ.ReleaseMaintenanceLockForBatch(cancelCtx, database.ReleaseMaintenanceLockForBatchParams{
		BatchID: bid,
		Types:   types.BlockingTaskTypes(),
	}); relErr != nil {
		tx.Rollback()
		return fmt.Errorf("release maintenance lock: %w", relErr)
	}
	procCount, procErr := txQ.CancelProcessingTasksByBatch(cancelCtx, bid)
	if procErr != nil {
		tx.Rollback()
		return fmt.Errorf("cancel processing tasks: %w", procErr)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cancel transaction: %w", err)
	}

	releaseOwner(bo.OwnerID)

	fmt.Printf("Batch %s: %d pending + %d processing cancelled, signal sent to PID %d\n",
		batchID, count, procCount, bo.Pid)
	return nil
}

// IsAlive reports whether a PID belongs to a live process; a negative
// pid is always false.
func IsAlive(pid int64) bool {
	return syscall.Kill(int(pid), 0) == nil
}

type taskDisplay struct {
	index    int
	fileName string
	taskType string
}

func taskDisplayInfo(t database.Task) taskDisplay {
	payload := task.ReadPayload(t.Payload)
	return taskDisplay{
		index:    payload.FileIndex,
		fileName: payload.DisplayName(),
		taskType: string(t.TaskType),
	}
}

func totalFiles(tasks []database.Task) int {
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

func pollBatch(ctx context.Context, run batchRun, logger *log.Logger) error {
	logger.SetLevel(log.LevelSilent)

	queries := run.client.Queries
	pools := run.pools.forSource(run.source)
	for _, p := range pools {
		p.Start(ctx)
	}

	stopPools := func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, p := range pools {
			p.Stop(stopCtx)
		}
	}

	previous := make(map[string]string)
	previousProgress := make(map[string]string)

	tickInterval := 500 * time.Millisecond
	if run.source == types.Batch.Source.Thumbbackfill {
		tickInterval = 2 * time.Second
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			fmt.Println("\nBatch cancelled")
			stopPools()
			return ctx.Err()
		}

		batch, err := queries.GetBatch(ctx, run.batchID)
		if err == nil && batch.Status == types.Batch.Status.Paused {
			fmt.Printf("\nBatch paused: LLM provider credit/balance error detected. Resolve billing and re-queue the batch.\n")
			stopPools()
			return ErrBatchPaused
		}

		tasks, err := task.ListFiltered(ctx, queries, task.TaskFilter{
			BatchID: run.batchID,
		})
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
				if previous[t.TaskID] != "processing" && previous[t.TaskID] == "pending" {
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
			} else {
				fmt.Printf("\nSummary: %d files, %d tasks — all successful\n", files, taskCount)
			}

			stopPools()
			return nil
		}
	}
}

// setBatchTerminalStatus settles a batch as completed or failed by its
// tasks' statuses and logs one INFO bookend built from them — the task
// status counts and, when completed enrich results decoded, the aggregated
// per-document enrich counts. An already-terminal batch skips only the
// settle write, never the bookend. A failed settle write is returned after
// the bookend; a failed listing returns before it, because the bookend's
// counts come from that listing.
func setBatchTerminalStatus(ctx context.Context, queries *database.Queries, batchSvc *service.Batch, logger *log.Logger, batchID string) error {
	batch, err := queries.GetBatch(ctx, batchID)
	if err != nil {
		return fmt.Errorf("get batch: %w", err)
	}

	tasks, err := task.ListFiltered(ctx, queries, task.TaskFilter{BatchID: batchID})
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}

	status := batch.Status
	var settleErr error
	switch status {
	case types.Batch.Status.Completed, types.Batch.Status.Failed, types.Batch.Status.Cancelled, types.Batch.Status.Paused:
	default:
		hasFailed := false
		for _, t := range tasks {
			if t.Status == types.Task.Status.Failed {
				hasFailed = true
				break
			}
		}
		if hasFailed {
			status = types.Batch.Status.Failed
			settleErr = batchSvc.SetBatchFailed(ctx, batchID)
		} else {
			status = types.Batch.Status.Completed
			settleErr = batchSvc.SetBatchCompleted(ctx, batchID)
		}
	}

	logBatchBookend(logger, batchID, status, tasks)
	return settleErr
}

// logBatchBookend logs one INFO line with the batch's settle status, the
// completed/failed/cancelled task counts, and — over completed enrich
// tasks whose stored result decoded — the aggregated per-document enrich
// counts.
func logBatchBookend(logger *log.Logger, batchID string, status types.BatchStatus, tasks []database.Task) {
	completed, failed, cancelled := 0, 0, 0
	var totals enrichment.DocumentCounts
	documents := 0
	for _, t := range tasks {
		switch t.Status {
		case types.Task.Status.Completed:
			completed++
		case types.Task.Status.Failed:
			failed++
		case types.Task.Status.Cancelled:
			cancelled++
		}
		if t.TaskType != types.Task.Type.Enrich || t.Status != types.Task.Status.Completed || t.Result == nil {
			continue
		}
		counts, ok := enrichment.CountsFromResult(*t.Result)
		if !ok {
			continue
		}
		documents++
		totals.Add(counts)
	}

	line := fmt.Sprintf("batch %s settled as %s: %d completed, %d failed, %d cancelled",
		batchID, status, completed, failed, cancelled)
	if documents > 0 {
		line += fmt.Sprintf("; enrich: %d documents, %d assignments, %d births, %d promotions, %d rejects, %d defers, %d splits, %d keeps, %d drops",
			documents, totals.Assignments, totals.Births, totals.Promotions, totals.Rejects, totals.Defers, totals.Splits, totals.Keeps, totals.Drops)
	}
	logger.Info("", "%s", line)
}

func triggerOrphanScan(c *container.Container) {
	client, err := c.GetClient()
	if err != nil {
		c.GetLogger().Error("", "orphan scan: get client: %v", err)
		return
	}
	store := task.NewStore(client)
	batchSvc := service.NewBatch(client, c.GetConfig().Consumer.Reclaim.MaxRetries)
	svc := service.NewOrphaned(client.Queries, c.GetConfig(), c.GetLogger(), store, batchSvc)
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

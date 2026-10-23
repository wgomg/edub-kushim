// Package consume is the per-batch consume driver: one child process per batch, processing its consume,
// enrich and thumbnail tasks through task pools, with batch ownership, heartbeat, the cancel protocol and
// the credit-pause handling.
package consume

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
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

func (p poolSet) forThumbbackfill(thumbbackfill bool) []*task.Pool {
	if thumbbackfill {
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
	client        *database.Client
	owner         *task.Owner
	ownerID       string
	batchID       string
	thumbbackfill bool
	pools         poolSet
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

		thumbbackfill, tbfErr := batchRunsThumbbackfill(ctx, client, batchIDParam)
		if tbfErr != nil {
			return fmt.Errorf("inspect batch tasks: %w", tbfErr)
		}

		ownerID := uuid.New().String()
		pid := os.Getpid()
		owner := task.NewOwner(client, ownerID, pid, c.GetLogger())
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
				bo, boErr := client.GetBatchOwner(ctx, database.GetBatchOwnerParams{
					BatchID:           batchIDParam,
					StaleAfterSeconds: task.LeaseStaleSeconds,
				})
				if boErr == nil {
					return fmt.Errorf("batch %s is being processed by PID %d (use --force to override)", batchIDParam, bo.Pid)
				}
				return fmt.Errorf("batch %s is being processed by another process (use --force to override)", batchIDParam)
			}
			return fmt.Errorf("acquire batch %s: %w", batchIDParam, err)
		}

		c.GetLogger().Debug("", "consume: GOMAXPROCS=%d", runtime.GOMAXPROCS(0))

		maxRetries := int32(c.GetConfig().Consumer.Reclaim.MaxRetries)
		reset, resetErr := task.ResetProcessingTasksByBatch(ctx, client, batchIDParam, maxRetries)
		if resetErr != nil {
			c.GetLogger().Error("", "reset processing tasks for batch %s: %v", batchIDParam, resetErr)
		}
		if reset.Reset > 0 {
			fmt.Printf("  reset %d orphaned processing tasks\n", reset.Reset)
		}
		if err := consumption.QuarantineFailedFiles(ctx, client.Queries, c.GetConfig().Storage.StorageDir, c.GetLogger(), batchIDParam, maxRetries, reset.ConsumeTaskIDs()); err != nil {
			c.GetLogger().Error("", "quarantine files for batch %s: %v", batchIDParam, err)
		}

		release, err := acquireBatchHolds(ctx, c, client, batchIDParam, ownerID)
		if err != nil {
			return err
		}
		defer release()

		return runBatchThroughPools(ctx, cancel, c, batchRun{
			client:        client,
			owner:         owner,
			ownerID:       ownerID,
			batchID:       batchIDParam,
			thumbbackfill: thumbbackfill,
			pools:         pools,
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

	enqueuer, err := c.GetEnqueuer()
	if err != nil {
		return fmt.Errorf("failed to get enqueuer: %w", err)
	}

	batchSvc := service.NewBatch(client, c.GetConfig().Consumer.Reclaim.MaxRetries)

	countBefore, err := batchSvc.CountQueuedConsumeBatches(ctx)
	if err != nil {
		return fmt.Errorf("failed to check queue: %w", err)
	}

	thumbnailsEnabled := c.GetConfig().Consumer.Thumbnail.Enabled
	var tasks []task.TaskSpec
	consumeTaskIDs := make(map[string]struct{}, len(files))
	for i, f := range files {
		md5hash, md5Err := storage.CalculateMD5(f.OriginalPath)
		if md5Err != nil {
			c.GetLogger().Error("", "md5 %s: %v", f.OriginalPath, md5Err)
			continue
		}

		fileTasks := consumption.IngestTaskSpecs(consumption.IngestFile{
			Path:       f.OriginalPath,
			Index:      i + 1,
			MD5:        md5hash,
			DocumentID: uuid.New().String(),
		}, thumbnailsEnabled)
		consumeTaskIDs[fileTasks[0].TaskID] = struct{}{}
		tasks = append(tasks, fileTasks...)
	}

	if len(tasks) == 0 {
		fmt.Printf("No new files to consume\n")
		return nil
	}

	spec, err := task.NewBatchSpec(types.Batch.Origin.CLI, "")
	if err != nil {
		return fmt.Errorf("build batch spec: %w", err)
	}

	result, err := enqueuer.EnqueueBatch(ctx, spec, tasks)
	if err != nil {
		return fmt.Errorf("enqueue batch: %w", err)
	}

	enqueued := 0
	for _, taskID := range result.TaskIDs {
		if _, ok := consumeTaskIDs[taskID]; ok {
			enqueued++
		}
	}
	if enqueued == 0 || result.BatchID == "" {
		fmt.Printf("No new files to consume\n")
		return nil
	}

	batchID := result.BatchID
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
	owner := task.NewOwner(client, ownerID, os.Getpid(), c.GetLogger())

	if err := owner.Acquire(ctx, batchID); err != nil {
		return fmt.Errorf("acquire batch %s: %w", batchID, err)
	}

	release, err := acquireBatchHolds(ctx, c, client, batchID, ownerID)
	if err != nil {
		return err
	}
	defer release()

	return runBatchThroughPools(ctx, cancel, c, batchRun{
		client:        client,
		owner:         owner,
		ownerID:       ownerID,
		batchID:       batchID,
		thumbbackfill: false,
		pools:         pools,
	})
}

// batchRunsThumbbackfill reports whether the batch carries thumbbackfill tasks, which select the backfill
// pool instead of the consume family.
func batchRunsThumbbackfill(ctx context.Context, client *database.Client, batchID string) (bool, error) {
	tasks, err := task.ListFiltered(ctx, client.Queries, task.TaskFilter{
		BatchID:  batchID,
		TaskType: string(types.Task.Type.ThumbBackfill),
		Limit:    1,
	})
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

// acquireBatchHolds takes the locks the batch's task types declare for the whole run, waiting until
// they are free, and returns the release function the run defers. It also tells the claim path that the
// batch locks are already held, so per-task claims only verify their waits. A batch whose task types
// declare no holds needs nothing and releases nothing.
func acquireBatchHolds(ctx context.Context, c *container.Container, client *database.Client, batchID, ownerID string) (func(), error) {
	taskTypes, err := client.Queries.ListBatchTaskTypes(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list batch task types: %w", err)
	}

	var holds, waits []types.LockName
	for _, taskType := range taskTypes {
		declaration := types.LockDeclarationFor(taskType)
		holds = task.UnionLockNames(holds, declaration.Holds)
		waits = task.UnionLockNames(waits, declaration.WaitsFor)
	}
	if len(holds) == 0 {
		return func() {}, nil
	}

	token, err := uuid.Parse(ownerID)
	if err != nil {
		return nil, fmt.Errorf("batch owner token: %w", err)
	}
	lockToken := uuid.NullUUID{UUID: token, Valid: true}

	for {
		err := task.AcquireLocks(ctx, client, holds, waits, lockToken, batchID)
		if err == nil {
			break
		}
		if !errors.Is(err, task.ErrLockBusy) {
			return nil, fmt.Errorf("acquire batch locks: %w", err)
		}
		c.GetLogger().Info("", "consume: batch %s is waiting for its locks", batchID)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	stopHeartbeat := database.StartLockHeartbeat(ctx, client.Queries, c.GetLogger(), task.LockHeartbeatInterval, holds, lockToken)
	release := func() {
		stopHeartbeat()
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := task.ReleaseLocksForToken(releaseCtx, client.Queries, lockToken); err != nil {
			c.GetLogger().Error("", "consume: release batch %s locks: %v", batchID, err)
		}
	}

	if err := database.WaitForTaskDrain(ctx, client.Queries, c.GetLogger(), "batch "+batchID, types.TaskTypesWaitingOn(holds...), nil); err != nil {
		release()
		return nil, err
	}

	if err := c.SetRunnerBatchHoldsAcquired(true); err != nil {
		release()
		return nil, err
	}

	return release, nil
}

// The post-acquire sequence both halves of Run share.
func runBatchThroughPools(ctx context.Context, cancel context.CancelFunc, c *container.Container, run batchRun) error {
	if err := c.SetRunnerOwnerID(run.ownerID); err != nil {
		return fmt.Errorf("set runner owner: %w", err)
	}

	exec, err := leasedExecution(run.ownerID, run.batchID)
	if err != nil {
		return err
	}

	watchSignals(ctx, cancel)

	hb := task.NewHeartbeat(run.owner, heartbeatInterval, c.GetLogger())
	hb.Start(ctx)

	err = pollBatch(ctx, run, c.GetLogger())
	if err == nil {
		// waiting children of failed consumes block the settle, and a run that died before its quarantine
		// must still move the failed files out; the sweep covers both before the terminal write
		cfg := c.GetConfig()
		if qErr := consumption.QuarantineFailedFiles(ctx, run.client.Queries, cfg.Storage.StorageDir, c.GetLogger(),
			run.batchID, int32(cfg.Consumer.Reclaim.MaxRetries), nil); qErr != nil {
			c.GetLogger().Error("", "quarantine files for batch %s: %v", run.batchID, qErr)
		}
		status, settleErr := task.SettleBatch(ctx, run.client, c.GetLogger(), exec)
		if settleErr != nil {
			c.GetLogger().Error("", "settle batch %s: %v", run.batchID, settleErr)
		} else {
			logEnrichBookend(ctx, run.client.Queries, c.GetLogger(), run.batchID, status)
		}
	} else if errors.Is(err, ErrBatchPaused) {
		err = nil
	}

	triggerOrphanScan(c)

	hb.Stop()

	// the lease is the execution's own teardown: a settle writes the terminal status and releases the locks,
	// while the owner row goes away here (and in pause, cancel and the reaper's recovery) so a terminal batch
	// never keeps one behind
	relCtx, relCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if relErr := run.owner.Release(relCtx, run.batchID); relErr != nil {
		c.GetLogger().Error("", "release batch %s: %v", run.batchID, relErr)
	}
	relCancel()

	return err
}

// leasedExecution builds the guard the child's lifecycle writes carry: the lease owner, scoped to one
// batch.
func leasedExecution(ownerID, batchID string) (task.Execution, error) {
	token, err := uuid.Parse(ownerID)
	if err != nil {
		return task.Execution{}, fmt.Errorf("batch owner token: %w", err)
	}
	return task.Execution{BatchID: batchID, Guard: token, Leased: true}, nil
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
	pools := run.pools.forThumbbackfill(run.thumbbackfill)
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
	if run.thumbbackfill {
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

// logEnrichBookend adds one INFO line with the aggregated per-document enrich counts over the batch's
// completed enrich tasks, complementing the settle line SettleBatch already logged. A failed listing is
// logged, not returned: the bookend is observability, not lifecycle.
func logEnrichBookend(ctx context.Context, queries *database.Queries, logger *log.Logger, batchID string, status types.BatchStatus) {
	tasks, err := task.ListFiltered(ctx, queries, task.TaskFilter{BatchID: batchID})
	if err != nil {
		logger.Error("", "batch %s enrich bookend: list tasks: %v", batchID, err)
		return
	}

	var totals enrichment.DocumentCounts
	documents := 0
	for _, t := range tasks {
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
	if documents == 0 {
		return
	}

	logger.Info("", "batch %s (%s) enrich: %d documents, %d assignments, %d births, %d promotions, %d rejects, %d defers, %d splits, %d keeps, %d drops",
		batchID, status, documents, totals.Assignments, totals.Births, totals.Promotions, totals.Rejects, totals.Defers, totals.Splits, totals.Keeps, totals.Drops)
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

package commands

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	heartbeatInterval = 5 * time.Second
)

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

func consumeHandler(c *Container, args []string) error {
	if len(args) > 0 && args[0] == "cancel" {
		return consumeCancelHandler(c, args[1:])
	}

	fp := NewFlagParser(args)

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

	p, err := c.GetPool("consume")
	if err != nil {
		return fmt.Errorf("failed to get pool: %w", err)
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	allTools := config.MissingExternalTools(c.config)
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
					if t.TaskType == "consume" {
						hasConsumeTasks = true
						break
					}
				}
			}
			if hasConsumeTasks || listErr != nil {
				return fmt.Errorf("consume blocked: missing required external tools")
			}
		}

		ownerID := uuid.New().String()
		pid := os.Getpid()
		owner := task.NewOwner(client, ownerID, pid, c.logger, c.config.Consumer.Reclaim.MaxRetries)
		c.logger.Info(nil, "consume: acquiring batch %s (PID=%d, ownerID=%s)", batchIDParam, pid, ownerID)

		if err := owner.Acquire(ctx, batchIDParam, task.StaleAfter); err == task.ErrBatchLocked {
			if !force {
				bo, boErr := client.GetBatchOwner(ctx, batchIDParam)
				if boErr == nil {
					return fmt.Errorf("batch %s is being processed by PID %d (use --force to override)", batchIDParam, bo.Pid)
				}
				return fmt.Errorf("batch %s is being processed by another process (use --force to override)", batchIDParam)
			}
			if err := owner.AcquireForce(ctx, batchIDParam); err != nil {
				return fmt.Errorf("force acquire: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("acquire batch %s: %w", batchIDParam, err)
		}

		c.logger.Debug(nil, "consume: GOMAXPROCS=%d", runtime.GOMAXPROCS(0))

		reset, _ := owner.ResetProcessingByBatch(ctx, batchIDParam)
		if reset > 0 {
			fmt.Printf("  reset %d orphaned processing tasks\n", reset)
		}
		if err := consumption.QuarantineFailedFiles(ctx, client.Queries, c.config.Storage.StorageDir, c.logger, batchIDParam); err != nil {
			c.logger.Error(nil, "quarantine files for batch %s: %v", batchIDParam, err)
		}

		ep, err := c.GetPool("enrich")
		if err != nil {
			return fmt.Errorf("failed to get enrich pool: %w", err)
		}

		if err := c.SetRunnerOwnerID(ownerID); err != nil {
			return fmt.Errorf("set runner owner: %w", err)
		}

		watchSignals(ctx, cancel)

		hb := task.NewHeartbeat(owner, heartbeatInterval, c.logger)
		hb.Start(ctx)

		err = pollBatch(ctx, client.Queries, p, ep, c.logger, batchIDParam)
		batchSvc := service.NewBatch(client, c.config.Consumer.Reclaim.MaxRetries)
		if err == nil {
			if setErr := setBatchTerminalStatus(ctx, client.Queries, batchSvc, batchIDParam); setErr != nil {
				c.logger.Error(nil, "set batch terminal status %s: %v", batchIDParam, setErr)
			}
		}

		triggerOrphanScan(c)

		hb.Stop()
		relCtx, relCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if relErr := owner.Release(relCtx, batchIDParam); relErr != nil {
			c.logger.Error(nil, "release batch %s: %v", batchIDParam, relErr)
		}
		relCancel()

		return err
	}

	printOcrmypdfAdvisory(c.config, allTools)

	if len(missingTools) > 0 {
		return fmt.Errorf("consume blocked: missing required external tools")
	}

	files, err := consumption.GetFiles(
		c.config.Storage.ConsumptionDir,
		c.config.Consumer.SupportedFiles,
		c.config.Consumer.MaxFilesPerBatch,
	)
	if err != nil {
		return fmt.Errorf("failed to scan inbox: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No files found in consumption directory")
		return nil
	}

	batchID := uuid.New().String()
	enqueued := 0
	for i, f := range files {
		consumeTaskID := uuid.New().String()
		enrichTaskID := uuid.New().String()
		documentID := uuid.New().String()

		consumePayload, _ := json.Marshal(map[string]any{
			"file_path":    f.OriginalPath,
			"file_index":   i + 1,
			"on_completed": enrichTaskID,
			"document_id":  documentID,
		})
		_, err := c.dispatcher.Enqueue(ctx, "consume", batchID, consumePayload, consumeTaskID)
		if err != nil {
			c.logger.Error(&documentID, "enqueue %s: %v", f.OriginalPath, err)
			continue
		}
		enqueued++

		enrichPayload, _ := json.Marshal(map[string]any{
			"waiting_for": consumeTaskID,
			"file_name":   filepath.Base(f.OriginalPath),
			"file_index":  i + 1,
		})
		if _, err := c.dispatcher.Enqueue(ctx, "enrich", batchID, enrichPayload, enrichTaskID, "waiting"); err != nil {
			c.logger.Error(&documentID, "create enrich task for %s: %v", f.OriginalPath, err)
		}
	}

	if enqueued == 0 {
		fmt.Printf("No new files to consume\n")
		return nil
	}

	batchSvc := service.NewBatch(client, c.config.Consumer.Reclaim.MaxRetries)

	countBefore, err := batchSvc.CountQueuedBatches(ctx)
	if err != nil {
		return fmt.Errorf("failed to check queue: %w", err)
	}

	if err := client.CreateBatch(ctx, database.CreateBatchParams{
		ID:     batchID,
		Source: "cli",
		Status: "queued",
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

	ep, err := c.GetPool("enrich")
	if err != nil {
		return fmt.Errorf("failed to get enrich pool: %w", err)
	}

	ownerID := uuid.New().String()
	owner := task.NewOwner(client, ownerID, os.Getpid(), c.logger, c.config.Consumer.Reclaim.MaxRetries)

	if err := owner.Acquire(ctx, batchID, task.StaleAfter); err != nil {
		return fmt.Errorf("acquire batch %s: %w", batchID, err)
	}

	if err := c.SetRunnerOwnerID(ownerID); err != nil {
		return fmt.Errorf("set runner owner: %w", err)
	}

	watchSignals(ctx, cancel)

	hb := task.NewHeartbeat(owner, heartbeatInterval, c.logger)
	hb.Start(ctx)

	err = pollBatch(ctx, client.Queries, p, ep, c.logger, batchID)
	if err == nil {
		if setErr := setBatchTerminalStatus(ctx, client.Queries, batchSvc, batchID); setErr != nil {
			c.logger.Error(nil, "set batch terminal status %s: %v", batchID, setErr)
		}
	}

	triggerOrphanScan(c)

	hb.Stop()
	relCtx, relCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if relErr := owner.Release(relCtx, batchID); relErr != nil {
		c.logger.Error(nil, "release batch %s: %v", batchID, relErr)
	}
	relCancel()

	return err
}

func consumeCancelHandler(c *Container, args []string) error {
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

	if !isAlive(bo.Pid) {
		fmt.Printf("Process %d is no longer running\n", bo.Pid)
		fmt.Printf("%d pending tasks cancelled\n", count)
		client.ReleaseBatchOwner(cancelCtx, database.ReleaseBatchOwnerParams{
			BatchID: batchID,
			OwnerID: bo.OwnerID,
		})
		return nil
	}

	syscall.Kill(int(bo.Pid), syscall.SIGTERM)

	procCount, procErr := client.CancelProcessingTasksByBatch(cancelCtx, sql.NullString{String: batchID, Valid: true})
	if procErr != nil {
		return fmt.Errorf("cancel processing tasks: %w", procErr)
	}

	client.ReleaseBatchOwner(cancelCtx, database.ReleaseBatchOwnerParams{
		BatchID: batchID,
		OwnerID: bo.OwnerID,
	})

	fmt.Printf("Batch %s: %d pending + %d processing cancelled, signal sent to PID %d\n",
		batchID, count, procCount, bo.Pid)
	return nil
}

func isAlive(pid int64) bool {
	return syscall.Kill(int(pid), 0) == nil
}

func printToolBlock(missing []config.ExternalTool) {
	fmt.Println("Cannot consume — the following required tools are not installed:")
	for _, t := range missing {
		if t.Engine == "curl" {
			fmt.Printf("  Prerequisite \"curl\" — %s\n", t.Purpose)
		} else if len(t.Companions) > 0 {
			fmt.Printf("  OCR engine \"%s\" (binary not found in PATH)\n", t.Engine)
		} else {
			fmt.Printf("  %s engine \"%s\" (binary not found in PATH)\n", t.Category, t.Engine)
		}
		for _, system := range config.InstallHintOrder {
			if cmd, ok := t.InstallHints[system]; ok {
				fmt.Printf("    %-16s %s\n", system+":", cmd)
			}
		}
		fmt.Println()
		for _, c := range t.Companions {
			if c.Required && !c.Available {
				fmt.Printf("  Companion \"%s\" — %s\n", c.Command, c.Purpose)
				for _, system := range config.InstallHintOrder {
					if cmd, ok := c.InstallHints[system]; ok {
						fmt.Printf("    %-16s %s\n", system+":", cmd)
					}
				}
				fmt.Println()
			}
		}
	}
	fmt.Println("Install the missing tools, or switch to a built-in engine via `kushim setup` or the Settings page.")
}

func printOcrmypdfAdvisory(cfg *config.Config, allTools []config.ExternalTool) {
	if cfg.Consumer.OCR.Engine != config.OCR.OcrMyPdf {
		return
	}
	if len(cfg.Consumer.OCR.Languages) == 0 {
		return
	}

	var ocrTool *config.ExternalTool
	for i, t := range allTools {
		if t.Engine == config.OCR.OcrMyPdf {
			ocrTool = &allTools[i]
			break
		}
	}
	if ocrTool == nil {
		return
	}

	fmt.Printf("\nNote: ocrmypdf uses the system tesseract, which reads its own tessdata directory.\n")
	fmt.Printf("Make sure the tesseract language packs for your configured languages are installed:\n\n")
	fmt.Printf("  Configured languages: %s\n", strings.Join(cfg.Consumer.OCR.Languages, ", "))
	if len(ocrTool.LangHints) > 0 {
		combined := make(map[string][]string)
		for _, lh := range ocrTool.LangHints {
			for _, system := range config.InstallHintOrder {
				if cmd, ok := lh.InstallHints[system]; ok {
					combined[system] = append(combined[system], cmd)
				}
			}
		}
		for _, system := range config.InstallHintOrder {
			cmds, ok := combined[system]
			if !ok {
				continue
			}
			seen := map[string]bool{}
			var unique []string
			for _, c := range cmds {
				if !seen[c] {
					seen[c] = true
					unique = append(unique, c)
				}
			}
			fmt.Printf("    %-16s %s\n", system+":", strings.Join(unique, "  "))
		}
	}
	fmt.Println()

	for _, c := range ocrTool.Companions {
		if !c.Required && !c.Available {
			fmt.Printf("Optional companion \"%s\" — %s\n", c.Command, c.Purpose)
			fmt.Printf("  ocrmypdf will skip this feature. Install it for best results:\n")
			for _, system := range config.InstallHintOrder {
				if cmd, ok := c.InstallHints[system]; ok {
					fmt.Printf("    %-16s %s\n", system+":", cmd)
				}
			}
			fmt.Println()
		}
	}
}

type taskDisplay struct {
	index    int
	fileName string
	taskType string
}

func taskDisplayInfo(t database.Task) taskDisplay {
	info := taskDisplay{taskType: t.TaskType}
	if t.Payload == nil {
		return info
	}
	var p struct {
		FilePath  string `json:"file_path"`
		FileName  string `json:"file_name"`
		FileIndex int    `json:"file_index"`
	}
	json.Unmarshal(t.Payload, &p)
	info.index = p.FileIndex
	if p.FilePath != "" {
		info.fileName = filepath.Base(p.FilePath)
	} else {
		info.fileName = p.FileName
	}
	return info
}

func totalFiles(tasks []database.Task) int {
	maxIdx := 0
	for _, t := range tasks {
		if t.TaskType == "consume" {
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
		if t.TaskType == "consume" {
			n++
		}
	}
	return n
}

func pollBatch(ctx context.Context, queries *database.Queries, cp, ep *pool.Pool, logger *utils.Logger, batchID string) error {
	logger.SetLevel(utils.LevelSilent)
	cp.Start(ctx)
	ep.Start(ctx)

	previous := make(map[string]string)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			fmt.Println("\nBatch cancelled")
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			cp.Stop(stopCtx)
			ep.Stop(stopCtx)
			return ctx.Err()
		}
		tasks, err := task.ListFiltered(ctx, queries, task.TaskFilter{
			BatchID: batchID,
		})
		if err != nil {
			continue
		}

		total := totalFiles(tasks)

		remain := 0
		for _, t := range tasks {
			switch t.Status {
			case "processing":
				if previous[t.TaskID] != "processing" && previous[t.TaskID] == "pending" {
					info := taskDisplayInfo(t)
					fmt.Printf("  [%d/%d] %-8s %s ... processing\n", info.index, total, info.taskType, info.fileName)
				}
			case "completed":
				if previous[t.TaskID] != "completed" && previous[t.TaskID] != "" {
					info := taskDisplayInfo(t)
					fmt.Printf("  [%d/%d] %-8s %s ... done\n", info.index, total, info.taskType, info.fileName)
				}
			case "failed":
				if previous[t.TaskID] != "failed" && previous[t.TaskID] != "" {
					info := taskDisplayInfo(t)
					errMsg := ""
					if t.Error.Valid {
						errMsg = fmt.Sprintf(": %s", t.Error.String)
					}
					fmt.Printf("  [%d/%d] %-8s %s ... failed%s\n", info.index, total, info.taskType, info.fileName, errMsg)
				}
			}
			if t.Status == "pending" || t.Status == "processing" {
				remain++
			}
			previous[t.TaskID] = t.Status
		}

		if remain == 0 {
			var files, taskCount, failed int64
			for _, t := range tasks {
				taskCount++
				if t.TaskType == "consume" {
					files++
				}
				if t.Status == "failed" {
					failed++
				}
			}
			if failed > 0 {
				fmt.Printf("\nSummary: %d files, %d tasks — %d successful, %d failed\n", files, taskCount, taskCount-failed, failed)
			} else {
				fmt.Printf("\nSummary: %d files, %d tasks — all successful\n", files, taskCount)
			}

			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cp.Stop(stopCtx)
			ep.Stop(stopCtx)
			return nil
		}
	}
}

func setBatchTerminalStatus(ctx context.Context, queries *database.Queries, batchSvc *service.Batch, batchID string) error {
	tasks, err := task.ListFiltered(ctx, queries, task.TaskFilter{BatchID: batchID})
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}

	hasFailed := false
	for _, t := range tasks {
		if t.Status == "failed" {
			hasFailed = true
			break
		}
	}

	if hasFailed {
		return batchSvc.SetBatchFailed(ctx, batchID)
	}
	return batchSvc.SetBatchCompleted(ctx, batchID)
}

func triggerOrphanScan(c *Container) {
	client, err := c.GetClient()
	if err != nil {
		c.logger.Error(nil, "orphan scan: get client: %v", err)
		return
	}
	store := task.NewStore(client.Queries)
	batchSvc := service.NewBatch(client, c.config.Consumer.Reclaim.MaxRetries)
	svc := service.NewOrphaned(client.Queries, c.config, c.logger, store, batchSvc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	count, err := svc.ScanAndQuarantine(ctx)
	if err != nil {
		c.logger.Error(nil, "orphan scan after batch: %v", err)
		return
	}
	if count > 0 {
		c.logger.Info(nil, "post-batch orphan scan: %d files quarantined", count)
	}
}

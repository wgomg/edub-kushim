package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pidfile"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func consumeHandler(c *Container, args []string) error {
	if len(args) > 0 && args[0] == "cancel" {
		return consumeCancelHandler(c, args[1:])
	}

	fp := NewFlagParser(args)

	if fp.Help("Usage: kushim consume [--bg | --batch <id>]\n" +
		"  Scan inbox, create one task per file, and process them.\n" +
		"  Streams per-file progress to stdout.\n\n" +
		"  --bg              enqueue and process in background (releases console)\n" +
		"  --batch <id>      resume processing of an existing batch\n" +
		"  --force           override stale PID file lock\n\n" +
		"Subcommands:\n" +
		"  cancel <id>       cancel a running batch") {
		return nil
	}

	bgFlag := false
	fp.Bool("--bg", &bgFlag)

	var batchIDParam string
	fp.String("--batch", &batchIDParam)

	force := false
	fp.Bool("--force", &force)

	if bgFlag && batchIDParam != "" {
		return fmt.Errorf("--bg and --batch are mutually exclusive")
	}

	p, err := c.GetPool("consume")
	if err != nil {
		return fmt.Errorf("failed to get pool: %w", err)
	}

	db, err := c.GetDB()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	queries := database.NewQueries(db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if batchIDParam != "" {
		tasks, err := task.ListFiltered(ctx, queries, task.TaskFilter{
			BatchID: batchIDParam,
		})
		if err != nil {
			return fmt.Errorf("query batch %s: %w", batchIDParam, err)
		}
		if len(tasks) == 0 {
			fmt.Println("batch not found")
			return fmt.Errorf("batch %s not found", batchIDParam)
		}

		counts := task.CountBatchStatuses(ctx, queries, batchIDParam)
		if counts.Pending == 0 && counts.Processing == 0 {
			fmt.Println("batch already finished")
			return nil
		}

		fmt.Printf("Resuming batch %s (%d pending)...\n", batchIDParam, counts.Pending)

		ep, err := c.GetPool("enrich")
		if err != nil {
			return fmt.Errorf("failed to get enrich pool: %w", err)
		}

		lock, err := pidfile.Acquire(batchIDParam, force, cancel)
		if err != nil {
			return err
		}
		defer lock.Release()

		return pollBatch(ctx, queries, p, ep, c.logger, batchIDParam)
	}

	files, err := consumption.GetFiles(
		c.config.Storage.ConsumptionDir,
		c.config.Consumer.SupportedFiles,
	)
	if err != nil {
		return fmt.Errorf("failed to scan inbox: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No files found in consumption directory")
		return nil
	}

	dispatcher, err := task.NewDispatcher(c.config, c.logger, db)
	if err != nil {
		return fmt.Errorf("dispatcher: %w", err)
	}

	batchID := uuid.New().String()
	enqueued := 0
	for _, f := range files {
		payload, _ := json.Marshal(map[string]string{"file_path": f.OriginalPath})
		_, err := dispatcher.Enqueue(ctx, "consume", batchID, payload)
		if err != nil {
			c.logger.Error(nil, "enqueue %s: %v", f.OriginalPath, err)
			continue
		}
		enqueued++
	}

	if enqueued == 0 {
		fmt.Printf("No new files to consume\n")
		return nil
	}

	fmt.Printf("Batch: %s\n", batchID)

	if bgFlag {
		fmt.Printf("Files: %d\n", enqueued)
		fmt.Printf("Use 'kushim task list --batch %s' to track progress.\n", batchID)

		c.logger.SetLevel(utils.LevelSilent)

		cmd := exec.Command(os.Args[0], "consume", "--batch", batchID)
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start background process: %w", err)
		}

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

	lock, err := pidfile.Acquire(batchID, false, cancel)
	if err != nil {
		return err
	}
	defer lock.Release()

	return pollBatch(ctx, queries, p, ep, c.logger, batchID)
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

	db, err := c.GetDB()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	queries := database.NewQueries(db)

	cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	count, err := queries.CancelPendingTasksByBatch(cancelCtx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return fmt.Errorf("cancel pending tasks: %w", err)
	}

	pid, err := pidfile.Read(batchID)
	if err != nil {
		fmt.Printf("No running process found for batch %s\n", batchID)
		fmt.Printf("%d pending tasks cancelled\n", count)
		return nil
	}

	if !pidfile.IsAlive(batchID) {
		fmt.Printf("Process %d is no longer running\n", pid)
		fmt.Printf("%d pending tasks cancelled\n", count)
		return nil
	}

	syscall.Kill(pid, syscall.SIGTERM)

	procCount, procErr := queries.CancelProcessingTasksByBatch(cancelCtx, sql.NullString{String: batchID, Valid: true})
	if procErr != nil {
		return fmt.Errorf("cancel processing tasks: %w", procErr)
	}

	fmt.Printf("Batch %s: %d pending + %d processing cancelled, signal sent to PID %d\n",
		batchID, count, procCount, pid)
	return nil
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
			return nil
		}
		tasks, err := task.ListFiltered(ctx, queries, task.TaskFilter{
			BatchID: batchID,
		})
		if err != nil {
			continue
		}

		remain := 0
		for _, t := range tasks {
			switch t.Status {
			case "completed":
				if previous[t.TaskID] != "completed" && previous[t.TaskID] != "" {
					fileName := ""
					if t.Payload != nil {
						var p struct {
							FilePath string `json:"file_path"`
						}
						json.Unmarshal(t.Payload, &p)
						fileName = p.FilePath
					}
					fmt.Printf("%s → completed\n", fileName)
				}
			case "failed":
				if previous[t.TaskID] != "failed" && previous[t.TaskID] != "" {
					fileName := ""
					if t.Payload != nil {
						var p struct {
							FilePath string `json:"file_path"`
						}
						json.Unmarshal(t.Payload, &p)
						fileName = p.FilePath
					}
					errMsg := ""
					if t.Error.Valid {
						errMsg = fmt.Sprintf(": %s", t.Error.String)
					}
					fmt.Printf("%s → failed%s\n", fileName, errMsg)
				}
			}
			if t.Status == "pending" || t.Status == "processing" {
				remain++
			}
			previous[t.TaskID] = t.Status
		}

		if remain == 0 {
			var completed, failed int64
			for _, t := range tasks {
				switch t.Status {
				case "completed":
					completed++
				case "failed":
					failed++
				}
			}
			if failed > 0 {
				fmt.Printf("\nBatch finished: %d completed, %d failed\n", completed, failed)
			} else {
				fmt.Printf("\nBatch finished: all %d files processed successfully\n", completed)
			}

			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cp.Stop(stopCtx)
			ep.Stop(stopCtx)
			return nil
		}
	}
}

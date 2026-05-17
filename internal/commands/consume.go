package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/queue"
)

func consumeHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)

	if fp.Help("Usage: kushim consume [--wait]\n" +
		"  Scan inbox, create one task per file, and return.\n" +
		"  Tasks are processed asynchronously by the server.\n\n" +
		"  --wait          process inline and stream per-file progress to stdout") {
		return nil
	}

	waitFlag := false
	fp.Bool("--wait", &waitFlag)

	q, err := c.GetQueue()
	if err != nil {
		return fmt.Errorf("failed to get queue: %w", err)
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

	batchID := uuid.New().String()
	q.EnqueueFilePaths(context.Background(), "consume", batchID, consumption.FilePaths(files))

	fmt.Printf("Batch: %s\n", batchID)

	if !waitFlag {
		fmt.Printf("Files: %d\n", len(files))
		fmt.Printf("Use 'kushim task list --batch %s' to track progress.\n", batchID)
		return nil
	}

	for i, file := range files {
		fmt.Printf("[%d/%d] %s → queued\n", i+1, len(files), file.Name)
	}

	fmt.Println("Waiting for completion...")

	q.Start()

	db, err := c.GetDB()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	queries := database.NewQueries(db)
	ctx := context.Background()

	previous := make(map[string]string)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		tasks, err := queue.ListTasksFiltered(ctx, queries, queue.TaskFilter{
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
					if t.FilePath.Valid {
						fileName = t.FilePath.String
					}
					fmt.Printf("%s → completed\n", fileName)
				}
			case "failed":
				if previous[t.TaskID] != "failed" && previous[t.TaskID] != "" {
					fileName := ""
					if t.FilePath.Valid {
						fileName = t.FilePath.String
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
			q.Stop(stopCtx)
			return nil
		}
	}

	return nil
}

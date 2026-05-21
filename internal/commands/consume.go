package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
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

	p, err := c.GetPool()
	if err != nil {
		return fmt.Errorf("failed to get pool: %w", err)
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

	db, err := c.GetDB()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	queries := database.NewQueries(db)
	ctx := context.Background()

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

	if !waitFlag {
		fmt.Printf("Files: %d\n", enqueued)
		fmt.Printf("Use 'kushim task list --batch %s' to track progress.\n", batchID)
		return nil
	}

	for i, file := range files {
		fmt.Printf("[%d/%d] %s → queued\n", i+1, len(files), file.Name)
	}

	fmt.Println("Waiting for completion...")

	c.logger.SetLevel(utils.LevelError)
	p.Start()

	previous := make(map[string]string)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
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
			p.Stop(stopCtx)
			return nil
		}
	}

	return nil
}

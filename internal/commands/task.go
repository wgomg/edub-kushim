package commands

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/queue"
)

func taskHandler(c *Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim task <subcommand> [options]\n\n" +
			"Subcommands:\n" +
			"  list              list tasks (--batch, --status, --limit, --offset)\n" +
			"  status <task-id>  show task details and error if failed\n" +
			"  retry  <task-id>  re-enqueue a failed task as pending")
		if len(args) == 0 {
			return fmt.Errorf("task requires a subcommand")
		}
		return nil
	}

	switch args[0] {
	case "list":
		return taskListHandler(c, args[1:])
	case "status":
		return taskStatusHandler(c, args[1:])
	case "retry":
		return taskRetryHandler(c, args[1:])
	default:
		return fmt.Errorf("unknown task subcommand: %s", args[0])
	}
}

func taskListHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)

	if fp.Help("Usage: kushim task list [--batch <id>] [--status <status>] [--limit N] [--offset N]\n" +
		"  List tasks with optional filters.\n\n" +
		"  --batch <id>      filter by batch UUID\n" +
		"  --status <s>      filter by status (pending|processing|completed|failed)\n" +
		"  --limit N         max results (default 20, max 100)\n" +
		"  --offset N        result offset (default 0)") {
		return nil
	}

	var batchID, statusFilter string
	var limit, offset int

	fp.String("--batch", &batchID)
	fp.String("--status", &statusFilter)
	fp.Int("--limit", &limit, 1, 100)
	fp.Int("--offset", &offset, 0, 0)

	if limit == 0 {
		limit = 20
	}

	db, err := c.GetDB()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	tasks, err := queue.ListTasksFiltered(context.Background(), database.NewQueries(db), queue.TaskFilter{
		BatchID: batchID,
		Status:  statusFilter,
		Limit:   int64(limit),
		Offset:  int64(offset),
	})
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}

	if len(tasks) == 0 {
		fmt.Println("No tasks found")
		return nil
	}

	fmt.Printf("%-36s %-12s %-12s %s\n", "TASK ID", "STATUS", "BATCH", "FILE")
	fmt.Println(strings.Repeat("-", 80))

	for _, t := range tasks {
		fileName := ""
		if t.FilePath.Valid {
			fileName = filepath.Base(t.FilePath.String)
		}
		batchShort := t.BatchID.String
		if len(batchShort) > 12 {
			batchShort = batchShort[:12] + "…"
		}
		fmt.Printf("%-36s %-12s %-12s %s\n", t.TaskID, t.Status, batchShort, fileName)
	}

	return nil
}

func taskStatusHandler(c *Container, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: task status <task-id>")
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim task status <task-id>\n\n" +
			"  Show task details including status, timestamps, file name,\n" +
			"  linked document ID, and error message if failed.")
		return nil
	}

	db, err := c.GetDB()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	task, err := queue.GetTask(context.Background(), database.NewQueries(db), args[0])
	if err != nil {
		if errors.Is(err, queue.ErrTaskNotFound) {
			return fmt.Errorf("task %q not found", args[0])
		}
		return fmt.Errorf("get task: %w", err)
	}

	fileName := ""
	if task.FilePath.Valid {
		fileName = filepath.Base(task.FilePath.String)
	}

	fmt.Printf("Task ID:    %s\n", task.TaskID)
	fmt.Printf("Batch ID:   %s\n", task.BatchID.String)
	fmt.Printf("Status:     %s\n", task.Status)
	fmt.Printf("File:       %s\n", fileName)
	fmt.Printf("Created:    %s\n", task.CreatedAt.Time.Format(time.RFC3339))
	if task.StartedAt.Valid {
		fmt.Printf("Started:    %s\n", task.StartedAt.Time.Format(time.RFC3339))
	}
	if task.CompletedAt.Valid {
		fmt.Printf("Completed:  %s\n", task.CompletedAt.Time.Format(time.RFC3339))
	}
	if task.DocumentID.Valid {
		fmt.Printf("Document:   %d\n", task.DocumentID.Int64)
	}
	if task.Error.Valid {
		fmt.Printf("Error:      %s\n", task.Error.String)
	}

	return nil
}

func taskRetryHandler(c *Container, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: task retry <task-id>")
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim task retry <task-id>\n\n" +
			"  Re-enqueue a failed task. Its status is reset to 'pending'\n" +
			"  so a worker can pick it up on the next poll cycle.\n" +
			"  Only failed tasks can be retried.")
		return nil
	}

	db, err := c.GetDB()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	if err := queue.RetryTaskByTaskID(context.Background(), database.NewQueries(db), args[0]); err != nil {
		return err
	}

	fmt.Printf("Task %q retried — status reset to pending\n", args[0])
	return nil
}

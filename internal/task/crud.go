package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
)

var ErrTaskNotFound = errors.New("task not found")

type TaskFilter struct {
	BatchID string
	Status  string
	Limit   int64
	Offset  int64
}

type BatchCounts struct {
	Pending    int64
	Processing int64
	Completed  int64
	Failed     int64
}

func (bc BatchCounts) Total() int64 {
	return bc.Pending + bc.Processing + bc.Completed + bc.Failed
}

func Get(ctx context.Context, queries *database.Queries, taskID string) (database.Task, error) {
	task, err := queries.GetTaskByTaskID(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return database.Task{}, ErrTaskNotFound
		}
		return database.Task{}, err
	}
	return task, nil
}

func ListFiltered(ctx context.Context, queries *database.Queries, f TaskFilter) ([]database.Task, error) {
	switch {
	case f.BatchID != "" && f.Status != "":
		return queries.ListTasksByBatchAndStatus(ctx, database.ListTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: f.BatchID, Valid: true},
			Status:  f.Status,
		})
	case f.BatchID != "":
		return queries.ListTasksByBatch(ctx, sql.NullString{String: f.BatchID, Valid: true})
	case f.Status != "":
		return queries.ListTasksByStatus(ctx, database.ListTasksByStatusParams{
			Status: f.Status,
			Limit:  f.Limit,
			Offset: f.Offset,
		})
	default:
		return queries.ListTasks(ctx, database.ListTasksParams{
			Limit:  f.Limit,
			Offset: f.Offset,
		})
	}
}

func Retry(ctx context.Context, queries *database.Queries, taskID string) error {
	task, err := queries.GetTaskByTaskID(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTaskNotFound
		}
		return err
	}
	if task.Status != "failed" {
		return fmt.Errorf("task %q is %s, not failed", taskID, task.Status)
	}
	return queries.RetryTask(ctx, task.ID)
}

func CountBatchStatuses(ctx context.Context, queries *database.Queries, batchID string) BatchCounts {
	statuses := []string{"pending", "processing", "completed", "failed"}
	counts := BatchCounts{}

	for _, status := range statuses {
		count, err := queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: batchID, Valid: true},
			Status:  status,
		})
		if err != nil {
			continue
		}
		switch status {
		case "pending":
			counts.Pending = count
		case "processing":
			counts.Processing = count
		case "completed":
			counts.Completed = count
		case "failed":
			counts.Failed = count
		}
	}
	return counts
}

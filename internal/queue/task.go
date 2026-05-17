package queue

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

func ListTasksFiltered(ctx context.Context, queries *database.Queries, f TaskFilter) ([]database.Task, error) {
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

func RetryTaskByTaskID(ctx context.Context, queries *database.Queries, taskID string) error {
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

func GetTask(ctx context.Context, queries *database.Queries, taskID string) (database.Task, error) {
	task, err := queries.GetTaskByTaskID(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return database.Task{}, ErrTaskNotFound
		}
		return database.Task{}, err
	}
	return task, nil
}

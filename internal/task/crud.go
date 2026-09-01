package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type BatchFilter struct {
	Status string
	Limit  int32
	Offset int32
}

var ErrTaskNotFound = errors.New("task not found")

type TaskFilter struct {
	BatchID  string
	Status   string
	TaskType string
	Limit    int32
	Offset   int32
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
	case f.Status == "active" && f.BatchID == "" && f.TaskType == "":
		return queries.ListActiveTasks(ctx, database.ListActiveTasksParams{
			Limit: f.Limit, Offset: f.Offset,
		})
	case f.BatchID != "" && f.Status != "" && f.TaskType != "" && f.Limit > 0:
		return queries.ListTasksByBatchAndStatusAndType(ctx, database.ListTasksByBatchAndStatusAndTypeParams{
			BatchID:  sql.NullString{String: f.BatchID, Valid: true},
			Status:   f.Status,
			TaskType: f.TaskType,
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.BatchID != "" && f.Status != "" && f.TaskType != "":
		return queries.ListAllTasksByBatchAndStatusAndType(ctx, database.ListAllTasksByBatchAndStatusAndTypeParams{
			BatchID:  sql.NullString{String: f.BatchID, Valid: true},
			Status:   f.Status,
			TaskType: f.TaskType,
		})
	case f.BatchID != "" && f.TaskType != "" && f.Limit > 0:
		return queries.ListTasksByBatchAndType(ctx, database.ListTasksByBatchAndTypeParams{
			BatchID:  sql.NullString{String: f.BatchID, Valid: true},
			TaskType: f.TaskType,
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.BatchID != "" && f.TaskType != "":
		return queries.ListAllTasksByBatchAndType(ctx, database.ListAllTasksByBatchAndTypeParams{
			BatchID:  sql.NullString{String: f.BatchID, Valid: true},
			TaskType: f.TaskType,
		})
	case f.Status != "" && f.TaskType != "" && f.Limit > 0:
		return queries.ListTasksByStatusAndType(ctx, database.ListTasksByStatusAndTypeParams{
			Status:   f.Status,
			TaskType: f.TaskType,
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.Status != "" && f.TaskType != "":
		return queries.ListAllTasksByStatusAndType(ctx, database.ListAllTasksByStatusAndTypeParams{
			Status:   f.Status,
			TaskType: f.TaskType,
		})
	case f.TaskType != "" && f.Limit > 0:
		return queries.ListTasksByType(ctx, database.ListTasksByTypeParams{
			TaskType: f.TaskType,
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.TaskType != "":
		return queries.ListAllTasksByType(ctx, f.TaskType)
	case f.BatchID != "" && f.Status != "" && f.Limit > 0:
		return queries.ListTasksByBatchAndStatus(ctx, database.ListTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: f.BatchID, Valid: true},
			Status:  f.Status,
			Limit:   f.Limit,
			Offset:  f.Offset,
		})
	case f.BatchID != "" && f.Status != "":
		return queries.ListAllTasksByBatchAndStatus(ctx, database.ListAllTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: f.BatchID, Valid: true},
			Status:  f.Status,
		})
	case f.BatchID != "" && f.Limit > 0:
		return queries.ListTasksByBatch(ctx, database.ListTasksByBatchParams{
			BatchID: sql.NullString{String: f.BatchID, Valid: true},
			Limit:   f.Limit,
			Offset:  f.Offset,
		})
	case f.BatchID != "":
		return queries.ListAllTasksByBatch(ctx, sql.NullString{String: f.BatchID, Valid: true})
	case f.Status != "" && f.Limit > 0:
		return queries.ListTasksByStatus(ctx, database.ListTasksByStatusParams{
			Status: f.Status,
			Limit:  f.Limit,
			Offset: f.Offset,
		})
	case f.Status != "":
		return queries.ListAllTasksByStatus(ctx, f.Status)
	case f.Limit > 0:
		return queries.ListTasks(ctx, database.ListTasksParams{
			Limit:  f.Limit,
			Offset: f.Offset,
		})
	default:
		return queries.ListAllTasks(ctx)
	}
}

func Retry(ctx context.Context, queries *database.Queries, logger *utils.Logger, taskID string) error {
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
	if err := queries.RetryTask(ctx, task.ID); err != nil {
		return err
	}

	if task.TaskType == "consume" && task.Payload != nil {
		if onCompleted := consumeOnCompleted(*task.Payload); onCompleted != "" {
			if _, err := queries.SetEnrichTaskWaiting(ctx, onCompleted); err != nil {
				logger.Error(nil, "restore enrich task %s after retry of consume %s failed: %v (will be recovered by activation or sweep)", onCompleted, taskID, err)
			}
		}
		if onCompletedThumbnail := consumeOnCompletedThumbnail(*task.Payload); onCompletedThumbnail != "" {
			if _, err := queries.SetEnrichTaskWaiting(ctx, onCompletedThumbnail); err != nil {
				logger.Error(nil, "restore thumbnail task %s after retry of consume %s failed: %v (will be recovered by activation or sweep)", onCompletedThumbnail, taskID, err)
			}
		}
	}
	return nil
}

func consumeOnCompleted(payload json.RawMessage) string {
	var p struct {
		OnCompleted string `json:"on_completed"`
	}
	json.Unmarshal(payload, &p)
	return p.OnCompleted
}

func consumeOnCompletedThumbnail(payload json.RawMessage) string {
	var p struct {
		OnCompletedThumbnail string `json:"on_completed_thumbnail"`
	}
	json.Unmarshal(payload, &p)
	return p.OnCompletedThumbnail
}

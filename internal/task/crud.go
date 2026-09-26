package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// BatchFilter scopes batch queries for the list and admin surfaces.
type BatchFilter struct {
	Status string
	Limit  int32
	Offset int32
}

// ErrTaskNotFound reports a task id that resolves to no row.
var ErrTaskNotFound = errors.New("task not found")

// TaskFilter scopes task list queries. Empty fields are ignored; the
// status value "active" is a pseudo-status that selects the active view
// and combines with no other filter.
type TaskFilter struct {
	BatchID  string
	Status   string
	TaskType string
	Limit    int32
	Offset   int32
}

// Get returns one task row, ErrTaskNotFound when the id is unknown.
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

// ListFiltered lists tasks matching the filter, choosing the matching
// query shape (empty filters list everything).
func ListFiltered(ctx context.Context, queries *database.Queries, f TaskFilter) ([]database.Task, error) {
	// "active" is a filter pseudo-status, never a column value; combined
	// with other filters it matches nothing by definition.
	if f.Status == "active" {
		if f.BatchID != "" || f.TaskType != "" {
			return []database.Task{}, nil
		}
		return queries.ListActiveTasks(ctx, database.ListActiveTasksParams{
			Limit: f.Limit, Offset: f.Offset,
		})
	}
	switch {
	case f.BatchID != "" && f.Status != "" && f.TaskType != "" && f.Limit > 0:
		return queries.ListTasksByBatchAndStatusAndType(ctx, database.ListTasksByBatchAndStatusAndTypeParams{
			BatchID:  sql.NullString{String: f.BatchID, Valid: true},
			Status:   types.TaskStatus(f.Status),
			TaskType: types.TaskType(f.TaskType),
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.BatchID != "" && f.Status != "" && f.TaskType != "":
		return queries.ListAllTasksByBatchAndStatusAndType(ctx, database.ListAllTasksByBatchAndStatusAndTypeParams{
			BatchID:  sql.NullString{String: f.BatchID, Valid: true},
			Status:   types.TaskStatus(f.Status),
			TaskType: types.TaskType(f.TaskType),
		})
	case f.BatchID != "" && f.TaskType != "" && f.Limit > 0:
		return queries.ListTasksByBatchAndType(ctx, database.ListTasksByBatchAndTypeParams{
			BatchID:  sql.NullString{String: f.BatchID, Valid: true},
			TaskType: types.TaskType(f.TaskType),
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.BatchID != "" && f.TaskType != "":
		return queries.ListAllTasksByBatchAndType(ctx, database.ListAllTasksByBatchAndTypeParams{
			BatchID:  sql.NullString{String: f.BatchID, Valid: true},
			TaskType: types.TaskType(f.TaskType),
		})
	case f.Status != "" && f.TaskType != "" && f.Limit > 0:
		return queries.ListTasksByStatusAndType(ctx, database.ListTasksByStatusAndTypeParams{
			Status:   types.TaskStatus(f.Status),
			TaskType: types.TaskType(f.TaskType),
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.Status != "" && f.TaskType != "":
		return queries.ListAllTasksByStatusAndType(ctx, database.ListAllTasksByStatusAndTypeParams{
			Status:   types.TaskStatus(f.Status),
			TaskType: types.TaskType(f.TaskType),
		})
	case f.TaskType != "" && f.Limit > 0:
		return queries.ListTasksByType(ctx, database.ListTasksByTypeParams{
			TaskType: types.TaskType(f.TaskType),
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.TaskType != "":
		return queries.ListAllTasksByType(ctx, types.TaskType(f.TaskType))
	case f.BatchID != "" && f.Status != "" && f.Limit > 0:
		return queries.ListTasksByBatchAndStatus(ctx, database.ListTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: f.BatchID, Valid: true},
			Status:  types.TaskStatus(f.Status),
			Limit:   f.Limit,
			Offset:  f.Offset,
		})
	case f.BatchID != "" && f.Status != "":
		return queries.ListAllTasksByBatchAndStatus(ctx, database.ListAllTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: f.BatchID, Valid: true},
			Status:  types.TaskStatus(f.Status),
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
			Status: types.TaskStatus(f.Status),
			Limit:  f.Limit,
			Offset: f.Offset,
		})
	case f.Status != "":
		return queries.ListAllTasksByStatus(ctx, types.TaskStatus(f.Status))
	case f.Limit > 0:
		return queries.ListTasks(ctx, database.ListTasksParams{
			Limit:  f.Limit,
			Offset: f.Offset,
		})
	default:
		return queries.ListAllTasks(ctx)
	}
}

// Retry resets a task for reprocessing: it re-queues the task and returns
// an error for unknown ids or states that cannot be retried.
func Retry(ctx context.Context, client *database.Client, logger *log.Logger, taskID string) error {
	task, err := client.Queries.GetTaskByTaskID(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTaskNotFound
		}
		return err
	}
	if task.Status != types.Task.Status.Failed {
		return fmt.Errorf("task %q is %s, not failed", taskID, task.Status)
	}

	if task.TaskType.IsBlocking() {
		// the lock must not outlive the processing row it was claimed for
		tx, err := client.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin transaction for retry %s: %w", taskID, err)
		}
		defer tx.Rollback()

		txQ := client.Queries.WithTx(tx)
		if _, err := txQ.ReleaseMaintenanceLockForBatch(ctx, database.ReleaseMaintenanceLockForBatchParams{
			BatchID: task.BatchID,
			Types:   types.BlockingTaskTypes(),
		}); err != nil {
			return fmt.Errorf("release maintenance lock for retry %s: %w", taskID, err)
		}
		if err := txQ.RetryTask(ctx, task.ID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit transaction for retry %s: %w", taskID, err)
		}
	} else {
		if err := client.Queries.RetryTask(ctx, task.ID); err != nil {
			return err
		}
	}

	if task.TaskType == types.Task.Type.Consume && task.Payload != nil {
		onCompleted, onCompletedThumbnail := consumeChildTaskIDs(*task.Payload)
		if onCompleted != "" {
			if _, err := client.Queries.SetEnrichTaskWaiting(ctx, onCompleted); err != nil {
				logger.Error("", "restore enrich task %s after retry of consume %s failed: %v (will be recovered by activation or sweep)", onCompleted, taskID, err)
			}
		}
		if onCompletedThumbnail != "" {
			if _, err := client.Queries.SetEnrichTaskWaiting(ctx, onCompletedThumbnail); err != nil {
				logger.Error("", "restore thumbnail task %s after retry of consume %s failed: %v (will be recovered by activation or sweep)", onCompletedThumbnail, taskID, err)
			}
		}
	}
	return nil
}

// consumeChildTaskIDs decodes the waiting child tasks of a consume task
// payload: the enrich task and the optional thumbnail task queued behind
// it. Decode failures read as no children, the same contract
// recoverOnCompleted documents in the consumption package.
func consumeChildTaskIDs(payload json.RawMessage) (onCompleted, onCompletedThumbnail string) {
	var p struct {
		OnCompleted          string `json:"on_completed"`
		OnCompletedThumbnail string `json:"on_completed_thumbnail"`
	}
	_ = json.Unmarshal(payload, &p)
	return p.OnCompleted, p.OnCompletedThumbnail
}

package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/types"
)

// ErrTaskNotFound reports a task id that resolves to no row.
var ErrTaskNotFound = errors.New("task not found")

// TaskFilter scopes task list queries. Empty fields are ignored; the status value "active" is a pseudo-status
// that selects the active view and combines with no other filter. Limit must be positive; internal whole-set
// reads (the enrich bookend) call the queries directly instead of passing a zero limit here.
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

// ListFiltered lists one page of tasks matching the filter, choosing the query shape for the exposed
// filters: batch, status and task type. "active" is a filter pseudo-status, never a column value; combined
// with other filters it matches nothing by definition. A non-positive limit is a caller bug and is rejected
// instead of being silently replaced, so a listing can never truncate behind the caller's back.
func ListFiltered(ctx context.Context, queries *database.Queries, f TaskFilter) ([]database.Task, error) {
	if f.Limit <= 0 {
		return nil, fmt.Errorf("task list requires a positive limit, got %d", f.Limit)
	}
	if f.Status == "active" {
		if f.BatchID != "" || f.TaskType != "" {
			return []database.Task{}, nil
		}
		return queries.ListActiveTasks(ctx, database.ListActiveTasksParams{
			Limit: f.Limit, Offset: f.Offset,
		})
	}
	switch {
	case f.BatchID != "" && f.Status != "" && f.TaskType != "":
		return queries.ListTasksByBatchAndStatusAndType(ctx, database.ListTasksByBatchAndStatusAndTypeParams{
			BatchID:  sql.NullString{String: f.BatchID, Valid: true},
			Status:   types.TaskStatus(f.Status),
			TaskType: types.TaskType(f.TaskType),
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.BatchID != "" && f.TaskType != "":
		return queries.ListTasksByBatchAndType(ctx, database.ListTasksByBatchAndTypeParams{
			BatchID:  sql.NullString{String: f.BatchID, Valid: true},
			TaskType: types.TaskType(f.TaskType),
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.Status != "" && f.TaskType != "":
		return queries.ListTasksByStatusAndType(ctx, database.ListTasksByStatusAndTypeParams{
			Status:   types.TaskStatus(f.Status),
			TaskType: types.TaskType(f.TaskType),
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.TaskType != "":
		return queries.ListTasksByType(ctx, database.ListTasksByTypeParams{
			TaskType: types.TaskType(f.TaskType),
			Limit:    f.Limit,
			Offset:   f.Offset,
		})
	case f.BatchID != "" && f.Status != "":
		return queries.ListTasksByBatchAndStatus(ctx, database.ListTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: f.BatchID, Valid: true},
			Status:  types.TaskStatus(f.Status),
			Limit:   f.Limit,
			Offset:  f.Offset,
		})
	case f.BatchID != "":
		return queries.ListTasksByBatch(ctx, database.ListTasksByBatchParams{
			BatchID: sql.NullString{String: f.BatchID, Valid: true},
			Limit:   f.Limit,
			Offset:  f.Offset,
		})
	case f.Status != "":
		return queries.ListTasksByStatus(ctx, database.ListTasksByStatusParams{
			Status: types.TaskStatus(f.Status),
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

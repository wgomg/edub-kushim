package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
)

type BatchFilter struct {
	Status string
	Limit  int64
	Offset int64
}

var ErrTaskNotFound = errors.New("task not found")

type TaskFilter struct {
	BatchID  string
	Status   string
	TaskType string
	Limit    int64
	Offset   int64
}

type BatchCounts struct {
	BatchID    string
	Waiting    int64
	Pending    int64
	Processing int64
	Completed  int64
	Failed     int64
	Cancelled  int64
	Discarded  int64
}

func (bc BatchCounts) Total() int64 {
	return bc.Waiting + bc.Pending + bc.Processing + bc.Completed + bc.Failed + bc.Cancelled + bc.Discarded
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
	statuses := []string{"waiting", "pending", "processing", "completed", "failed", "cancelled", "discarded"}
	counts := BatchCounts{BatchID: batchID}

	for _, status := range statuses {
		count, err := queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: batchID, Valid: true},
			Status:  status,
		})
		if err != nil {
			continue
		}
		switch status {
		case "waiting":
			counts.Waiting = count
		case "pending":
			counts.Pending = count
		case "processing":
			counts.Processing = count
		case "completed":
			counts.Completed = count
		case "failed":
			counts.Failed = count
		case "cancelled":
			counts.Cancelled = count
		case "discarded":
			counts.Discarded = count
		}
	}
	return counts
}

func ListBatchSummaries(ctx context.Context, queries *database.Queries, f BatchFilter) ([]BatchCounts, error) {
	var rows []sql.NullString
	var err error

	if f.Status != "" {
		rows, err = queries.ListDistinctBatchIDsByStatus(ctx, database.ListDistinctBatchIDsByStatusParams{
			Status: f.Status,
			Limit:  f.Limit,
			Offset: f.Offset,
		})
	} else {
		rows, err = queries.ListDistinctBatchIDs(ctx, database.ListDistinctBatchIDsParams{
			Limit:  f.Limit,
			Offset: f.Offset,
		})
	}
	if err != nil {
		return nil, err
	}

	summaries := make([]BatchCounts, 0, len(rows))
	for _, row := range rows {
		if !row.Valid {
			continue
		}
		counts := CountBatchStatuses(ctx, queries, row.String)
		summaries = append(summaries, counts)
	}
	return summaries, nil
}

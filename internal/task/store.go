package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
)

type Store struct {
	queries *database.Queries
	ownerID string
}

func NewStore(queries *database.Queries) *Store {
	return &Store{queries: queries}
}

func (s *Store) SetOwnerID(id string) {
	s.ownerID = id
}

func (s *Store) CreateTask(
	ctx context.Context,
	taskType, batchID string,
	payload json.RawMessage,
	taskID, status, dedupKey string,
) (string, error) {
	if taskID == "" {
		taskID = uuid.New().String()
	}
	if status == "" {
		status = "pending"
	}

	var dkey sql.NullString
	if dedupKey != "" {
		dkey = sql.NullString{String: dedupKey, Valid: true}
	}

	_, err := s.queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   taskID,
		TaskType: taskType,
		Status:   status,
		BatchID:  sql.NullString{String: batchID, Valid: batchID != ""},
		Payload:  payload,
		DedupKey: dkey,
	})
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}
	return taskID, nil
}

func (s *Store) ClaimNextPending(ctx context.Context, taskType string) (database.Task, error) {
	var id int64
	var err error
	if s.ownerID != "" {
		id, err = s.queries.GetNextPendingTaskOfTypeForOwner(ctx, database.GetNextPendingTaskOfTypeForOwnerParams{
			TaskType: taskType,
			OwnerID:  s.ownerID,
		})
	} else {
		id, err = s.queries.GetNextPendingTaskOfType(ctx, taskType)
	}
	if err != nil {
		return database.Task{}, err
	}

	rows, err := s.queries.ClaimTask(ctx, id)
	if err != nil {
		return database.Task{}, fmt.Errorf("claim task %d: %w", id, err)
	}
	if rows == 0 {
		return database.Task{}, sql.ErrNoRows
	}

	return s.queries.GetTask(ctx, id)
}

func (s *Store) GetTask(ctx context.Context, id int64) (database.Task, error) {
	return s.queries.GetTask(ctx, id)
}

func (s *Store) GetTaskByTaskID(ctx context.Context, taskID string) (database.Task, error) {
	return s.queries.GetTaskByTaskID(ctx, taskID)
}

func (s *Store) CompleteTask(ctx context.Context, id int64, result json.RawMessage) error {
	return s.queries.CompleteTask(ctx, database.CompleteTaskParams{
		ID:     id,
		Result: &result,
	})
}

func (s *Store) FailTask(ctx context.Context, id int64, errMsg string) error {
	return s.queries.FailTask(ctx, database.FailTaskParams{
		ID:    id,
		Error: sql.NullString{String: errMsg, Valid: true},
	})
}

func (s *Store) SetPending(ctx context.Context, id int64, payload json.RawMessage) error {
	return s.queries.SetEnrichTaskPending(ctx, database.SetEnrichTaskPendingParams{
		ID:      id,
		Payload: payload,
	})
}

func (s *Store) Discard(ctx context.Context, id int64, errMsg string) error {
	return s.queries.DiscardEnrichTask(ctx, database.DiscardEnrichTaskParams{
		ID:    id,
		Error: sql.NullString{String: errMsg, Valid: true},
	})
}

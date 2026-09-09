package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/types"
)

var ErrLockBusy = errors.New("maintenance lock busy")

type Store struct {
	client  *database.Client
	queries *database.Queries
	ownerID string
}

func NewStore(client *database.Client) *Store {
	return &Store{client: client, queries: client.Queries}
}

func (s *Store) SetOwnerID(id string) {
	s.ownerID = id
}

func (s *Store) CreateTask(
	ctx context.Context,
	taskType types.TaskType, batchID string,
	payload json.RawMessage,
	taskID string, status types.TaskStatus, dedupKey string,
) (string, error) {
	if taskID == "" {
		taskID = uuid.New().String()
	}
	if status == "" {
		status = types.Task.Status.Pending
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
		Payload:  &payload,
		DedupKey: dkey,
	})
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}
	return taskID, nil
}

func (s *Store) ClaimNextPending(ctx context.Context, taskType types.TaskType) (database.Task, uuid.UUID, error) {
	if taskType.IsBlocking() {
		return s.claimBlockingNextPending(ctx, taskType)
	}
	return s.claimNextPending(ctx, taskType)
}

func (s *Store) claimNextPending(ctx context.Context, taskType types.TaskType) (database.Task, uuid.UUID, error) {
	var id int64
	var err error

	gated := taskType.IsLockGated()

	if s.ownerID != "" {
		id, err = s.queries.GetNextPendingTaskOfTypeForOwner(ctx, database.GetNextPendingTaskOfTypeForOwnerParams{
			TaskType: taskType,
			OwnerID:  s.ownerID,
			Gated:    gated,
		})
	} else {
		id, err = s.queries.GetNextPendingTaskOfType(ctx, database.GetNextPendingTaskOfTypeParams{
			TaskType: taskType,
			Gated:    gated,
		})
	}
	if err != nil {
		return database.Task{}, uuid.Nil, err
	}

	rows, err := s.queries.ClaimTask(ctx, database.ClaimTaskParams{ID: id})
	if err != nil {
		return database.Task{}, uuid.Nil, fmt.Errorf("claim task %d: %w", id, err)
	}
	if rows == 0 {
		return database.Task{}, uuid.Nil, sql.ErrNoRows
	}

	task, err := s.queries.GetTask(ctx, id)
	if err != nil {
		return database.Task{}, uuid.Nil, err
	}
	return task, uuid.Nil, nil
}

func (s *Store) claimBlockingNextPending(ctx context.Context, taskType types.TaskType) (database.Task, uuid.UUID, error) {
	token := uuid.New()

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return database.Task{}, uuid.Nil, fmt.Errorf("begin claim transaction: %w", err)
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)

	var id int64
	if s.ownerID != "" {
		id, err = txQ.GetNextPendingTaskOfTypeForOwner(ctx, database.GetNextPendingTaskOfTypeForOwnerParams{
			TaskType: taskType,
			OwnerID:  s.ownerID,
			Gated:    false,
		})
	} else {
		id, err = txQ.GetNextPendingTaskOfType(ctx, database.GetNextPendingTaskOfTypeParams{
			TaskType: taskType,
			Gated:    false,
		})
	}
	if err != nil {
		return database.Task{}, uuid.Nil, err
	}

	rows, err := txQ.ClaimTask(ctx, database.ClaimTaskParams{
		ID:         id,
		ClaimToken: uuid.NullUUID{UUID: token, Valid: true},
	})
	if err != nil {
		return database.Task{}, uuid.Nil, fmt.Errorf("claim task %d: %w", id, err)
	}
	if rows == 0 {
		return database.Task{}, uuid.Nil, sql.ErrNoRows
	}

	rows, err = txQ.AcquireMaintenanceLockForTask(ctx, uuid.NullUUID{UUID: token, Valid: true})
	if err != nil {
		return database.Task{}, uuid.Nil, fmt.Errorf("acquire maintenance lock: %w", err)
	}
	if rows == 0 {
		return database.Task{}, uuid.Nil, ErrLockBusy
	}

	task, err := txQ.GetTask(ctx, id)
	if err != nil {
		return database.Task{}, uuid.Nil, err
	}

	if err := tx.Commit(); err != nil {
		return database.Task{}, uuid.Nil, fmt.Errorf("commit claim transaction: %w", err)
	}
	return task, token, nil
}

func (s *Store) GetTask(ctx context.Context, id int64) (database.Task, error) {
	return s.queries.GetTask(ctx, id)
}

func (s *Store) GetTaskByTaskID(ctx context.Context, taskID string) (database.Task, error) {
	return s.queries.GetTaskByTaskID(ctx, taskID)
}

func (s *Store) CompleteTask(ctx context.Context, id int64, result json.RawMessage) (int64, error) {
	return s.queries.CompleteTask(ctx, database.CompleteTaskParams{
		ID:     id,
		Result: &result,
	})
}

// the transition and the release must be atomic so a crash between them
// cannot leave the lock held with no task to clear it
func (s *Store) withBlockingTransition(ctx context.Context, op string, id int64, token uuid.UUID, transition func(*database.Queries) (int64, error)) (int64, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin %s transaction: %w", op, err)
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)

	rows, err := transition(txQ)
	if err != nil {
		return 0, fmt.Errorf("%s blocking task %d: %w", op, id, err)
	}
	if rows > 0 {
		if _, err := txQ.ReleaseMaintenanceLockForTask(ctx, uuid.NullUUID{UUID: token, Valid: true}); err != nil {
			return 0, fmt.Errorf("release maintenance lock for task %d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit %s transaction: %w", op, err)
	}
	return rows, nil
}

func (s *Store) CompleteBlockingTask(ctx context.Context, id int64, result json.RawMessage, token uuid.UUID) (int64, error) {
	return s.withBlockingTransition(ctx, "complete", id, token, func(txQ *database.Queries) (int64, error) {
		return txQ.CompleteBlockingTask(ctx, database.CompleteBlockingTaskParams{
			Result:     &result,
			ID:         id,
			ClaimToken: uuid.NullUUID{UUID: token, Valid: true},
		})
	})
}

func (s *Store) FailTask(ctx context.Context, id int64, errMsg string) error {
	return s.queries.FailTask(ctx, database.FailTaskParams{
		ID:    id,
		Error: sql.NullString{String: errMsg, Valid: true},
	})
}

func (s *Store) FailBlockingTask(ctx context.Context, id int64, errMsg string, token uuid.UUID) error {
	_, err := s.withBlockingTransition(ctx, "fail", id, token, func(txQ *database.Queries) (int64, error) {
		return txQ.FailBlockingTask(ctx, database.FailBlockingTaskParams{
			Error:      sql.NullString{String: errMsg, Valid: true},
			ID:         id,
			ClaimToken: uuid.NullUUID{UUID: token, Valid: true},
		})
	})
	return err
}

func (s *Store) SetPending(ctx context.Context, id int64, payload json.RawMessage) error {
	return s.queries.SetEnrichTaskPending(ctx, database.SetEnrichTaskPendingParams{
		ID:      id,
		Payload: &payload,
	})
}

func (s *Store) PauseBatch(ctx context.Context, batchID string) error {
	return s.queries.SetBatchPaused(ctx, batchID)
}

func (s *Store) Discard(ctx context.Context, id int64, errMsg string) (int64, error) {
	return s.queries.DiscardEnrichTask(ctx, database.DiscardEnrichTaskParams{
		ID:    id,
		Error: sql.NullString{String: errMsg, Valid: true},
	})
}

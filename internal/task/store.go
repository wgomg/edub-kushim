package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Store is the persistence of the task lifecycle: creating tasks with their dedup keys, claiming pending
// tasks with claim tokens, completing, failing and retrying them, plus the lock-holding variants that
// coordinate through the declared resource locks and claim tokens. All methods are safe for concurrent use.
type Store struct {
	client  *database.Client
	queries *database.Queries
	ownerID string

	// batchHoldsAcquired tells claims that the execution already holds the batch locks, so a claim verifies
	// its waits and takes the token without acquiring. The consume child sets it after its batch-level
	// acquisition.
	batchHoldsAcquired bool
}

// NewStore builds the task store over the shared database client.
func NewStore(client *database.Client) *Store {
	return &Store{client: client, queries: client.Queries}
}

// SetOwnerID records the owner identity written on claims and batch
// ownership rows.
func (s *Store) SetOwnerID(id string) {
	s.ownerID = id
}

// SetBatchHoldsAcquired must run after the batch-level acquisition and before the claims it affects.
func (s *Store) SetBatchHoldsAcquired(acquired bool) {
	s.batchHoldsAcquired = acquired
}

// ClaimNextPending claims one pending task of the type. Types that hold locks claim through their own
// token-guarded protocol; the others only verify their declared waits. ErrLockBusy surfaces whenever a lock
// the claim needs is held.
func (s *Store) ClaimNextPending(ctx context.Context, taskType types.TaskType) (database.Task, uuid.UUID, error) {
	if taskType.HoldsLocks() {
		return s.claimBlockingNextPending(ctx, taskType)
	}
	return s.claimNextPending(ctx, taskType)
}

func (s *Store) claimNextPending(ctx context.Context, taskType types.TaskType) (database.Task, uuid.UUID, error) {
	var id int64
	var err error

	waitLocks := lockNameStrings(types.LockDeclarationFor(taskType).WaitsFor)

	if s.ownerID != "" {
		id, err = s.queries.GetNextPendingTaskOfTypeForOwner(ctx, database.GetNextPendingTaskOfTypeForOwnerParams{
			TaskType:  taskType,
			OwnerID:   s.ownerID,
			WaitLocks: waitLocks,
		})
	} else {
		id, err = s.queries.GetNextPendingTaskOfType(ctx, database.GetNextPendingTaskOfTypeParams{
			TaskType:  taskType,
			WaitLocks: waitLocks,
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
	declaration := types.LockDeclarationFor(taskType)
	waitLocks := lockNameStrings(declaration.WaitsFor)

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return database.Task{}, uuid.Nil, fmt.Errorf("begin claim transaction: %w", err)
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)

	var id int64
	if s.ownerID != "" {
		id, err = txQ.GetNextPendingTaskOfTypeForOwner(ctx, database.GetNextPendingTaskOfTypeForOwnerParams{
			TaskType:  taskType,
			OwnerID:   s.ownerID,
			WaitLocks: waitLocks,
		})
	} else {
		id, err = txQ.GetNextPendingTaskOfType(ctx, database.GetNextPendingTaskOfTypeParams{
			TaskType:  taskType,
			WaitLocks: waitLocks,
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

	task, err := txQ.GetTask(ctx, id)
	if err != nil {
		return database.Task{}, uuid.Nil, err
	}

	if !s.batchHoldsAcquired {
		if err := acquireLocks(ctx, txQ, declaration.Holds, declaration.WaitsFor,
			uuid.NullUUID{UUID: token, Valid: true}, task.BatchID.String); err != nil {
			return database.Task{}, uuid.Nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return database.Task{}, uuid.Nil, fmt.Errorf("commit claim transaction: %w", err)
	}
	return task, token, nil
}

// GetTask returns one task row by row id.
func (s *Store) GetTask(ctx context.Context, id int64) (database.Task, error) {
	return s.queries.GetTask(ctx, id)
}

// GetTaskByTaskID returns one task row by its task UUID.
func (s *Store) GetTaskByTaskID(ctx context.Context, taskID string) (database.Task, error) {
	return s.queries.GetTaskByTaskID(ctx, taskID)
}

// CompleteTask marks a task completed with its result payload, ignoring
// rows that are no longer processing; the return is the rows updated.
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
		if err := ReleaseLocksForToken(ctx, txQ, uuid.NullUUID{UUID: token, Valid: true}); err != nil {
			return 0, fmt.Errorf("release locks for task %d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit %s transaction: %w", op, err)
	}
	return rows, nil
}

// CompleteBlockingTask is the claim-token-checked form of CompleteTask:
// the write lands only when the task is still processing under the given
// claim.
func (s *Store) CompleteBlockingTask(ctx context.Context, id int64, result json.RawMessage, token uuid.UUID) (int64, error) {
	return s.withBlockingTransition(ctx, "complete", id, token, func(txQ *database.Queries) (int64, error) {
		return txQ.CompleteBlockingTask(ctx, database.CompleteBlockingTaskParams{
			Result:     &result,
			ID:         id,
			ClaimToken: uuid.NullUUID{UUID: token, Valid: true},
		})
	})
}

// FailTask marks a task failed with an error message.
func (s *Store) FailTask(ctx context.Context, id int64, errMsg string) error {
	return s.queries.FailTask(ctx, database.FailTaskParams{
		ID:    id,
		Error: sql.NullString{String: errMsg, Valid: true},
	})
}

// FailBlockingTask is the claim-token-checked form of FailTask.
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

// SetPending re-queues a task with fresh payload after a retry-worthy
// failure.
func (s *Store) SetPending(ctx context.Context, id int64, payload json.RawMessage) error {
	return s.queries.SetEnrichTaskPending(ctx, database.SetEnrichTaskPendingParams{
		ID:      id,
		Payload: &payload,
	})
}

// PauseBatch flips the batch into paused, the credit-error state that
// blocks scanning until resumed.
func (s *Store) PauseBatch(ctx context.Context, batchID string) error {
	return s.queries.SetBatchPaused(ctx, batchID)
}

// Discard marks a task (and its waiting children) discarded, removing it
// from the batch counts; the return is the children discarded.
func (s *Store) Discard(ctx context.Context, id int64, errMsg string) (int64, error) {
	return s.queries.DiscardEnrichTask(ctx, database.DiscardEnrichTaskParams{
		ID:    id,
		Error: sql.NullString{String: errMsg, Valid: true},
	})
}

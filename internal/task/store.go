package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Store is the persistence of the task lifecycle: claiming pending tasks under their execution guard,
// completing, failing and activating them, plus the lock-holding variants that coordinate through the
// declared resource locks. All methods are safe for concurrent use.
type Store struct {
	client  *database.Client
	queries *database.Queries
	ownerID string

	// batchHoldsAcquired tells claims that the execution already holds the batch locks, so a claim verifies
	// its waits and takes the guard without acquiring. The shared batch execution sets it after its batch-level
	// acquisition.
	batchHoldsAcquired bool
}

// NewStore builds the task store over the shared database client.
func NewStore(client *database.Client) *Store {
	return &Store{client: client, queries: client.Queries}
}

// SetOwnerID records the owner identity written on claims and batch ownership rows.
func (s *Store) SetOwnerID(id string) {
	s.ownerID = id
}

// SetBatchHoldsAcquired must run after the batch-level acquisition and before the claims it affects.
func (s *Store) SetBatchHoldsAcquired(acquired bool) {
	s.batchHoldsAcquired = acquired
}

// Activation reports what activating a waiting child did: it became pending, or it was discarded because
// live work already covers its dedup key.
type Activation int

// Outcomes of ActivateChild: the child went pending, or it was discarded because live work already covers
// its dedup key and the surviving task does the work.
const (
	Activated Activation = iota
	DiscardedDuplicate
)

// ClaimNextPending claims one pending task of the type under the execution guard and returns the task and
// its guard. A leased execution claims under its owner id; a pool execution claims under a fresh token. The
// selection only considers tasks whose batch is queued or processing, so a task left behind in any other
// batch can neither be claimed nor block the type queue. The claim is one transaction: the task row, the
// batch row lock and the queued-to-processing flip and, for a type that holds locks, the all-or-nothing
// acquisition. ErrLockBusy surfaces whenever a lock the claim needs is held, and sql.ErrNoRows whenever there
// is no claimable task.
func (s *Store) ClaimNextPending(ctx context.Context, taskType types.TaskType) (database.Task, uuid.UUID, error) {
	token, err := s.claimToken()
	if err != nil {
		return database.Task{}, uuid.Nil, err
	}

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

	// the claim is also the batch claim: the batch row is locked for the transaction and flips queued to
	// processing on the first claim, so the remaining tasks of a multi-task batch stay claimable while a
	// paused, cancelled, terminal or missing batch refuses the claim with the task update rolled back
	if task.BatchID.Valid {
		status, err := txQ.LockBatchForClaim(ctx, task.BatchID.String)
		if err != nil {
			return database.Task{}, uuid.Nil, err
		}
		switch status {
		case types.Batch.Status.Queued:
			claimed, err := txQ.MarkBatchClaimed(ctx, task.BatchID.String)
			if err != nil {
				return database.Task{}, uuid.Nil, fmt.Errorf("mark batch %s claimed: %w", task.BatchID.String, err)
			}
			if claimed == 0 {
				return database.Task{}, uuid.Nil, sql.ErrNoRows
			}
		case types.Batch.Status.Processing:
		default:
			return database.Task{}, uuid.Nil, sql.ErrNoRows
		}
	}

	if taskType.HoldsLocks() && !s.batchHoldsAcquired {
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

// CompleteTask marks a claimed task completed, writing only while the task is still processing under the
// guard; the return is the rows updated. A task whose claim holds its own task-scoped locks releases them
// in the same transaction.
func (s *Store) CompleteTask(ctx context.Context, taskType types.TaskType, id int64, result json.RawMessage, token uuid.UUID) (int64, error) {
	return s.withTransition(ctx, "complete", taskType, id, token, func(txQ *database.Queries) (int64, error) {
		return txQ.CompleteBlockingTask(ctx, database.CompleteBlockingTaskParams{
			Result:     &result,
			ID:         id,
			ClaimToken: uuid.NullUUID{UUID: token, Valid: true},
		})
	})
}

// FailTask marks a claimed task failed under the guard, with the same lock-release rule as CompleteTask.
func (s *Store) FailTask(ctx context.Context, taskType types.TaskType, id int64, errMsg string, token uuid.UUID) (int64, error) {
	return s.withTransition(ctx, "fail", taskType, id, token, func(txQ *database.Queries) (int64, error) {
		return txQ.FailBlockingTask(ctx, database.FailBlockingTaskParams{
			Error:      sql.NullString{String: errMsg, Valid: true},
			ID:         id,
			ClaimToken: uuid.NullUUID{UUID: token, Valid: true},
		})
	})
}

// ActivateChild makes a waiting child pending under the dedup key its type definition derives, in one
// transaction. The key must be non-empty: a child that cannot carry its own dedup key would bypass the
// duplicate protection, so an empty key fails the activation instead of writing a keyless row. A key that
// collides with live work discards the child instead of failing its parent: the duplicate carries no work
// the surviving task does not already cover. A child that is no longer waiting or discarded is left as is
// and reports Activated.
func (s *Store) ActivateChild(ctx context.Context, id int64, payload json.RawMessage, dedupKey string) (Activation, error) {
	if dedupKey == "" {
		return Activated, fmt.Errorf("activate child %d: empty dedup key", id)
	}

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return Activated, fmt.Errorf("begin activation transaction: %w", err)
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)
	key := sql.NullString{String: dedupKey, Valid: true}

	if _, err := tx.ExecContext(ctx, "SAVEPOINT child_activation"); err != nil {
		return Activated, fmt.Errorf("set activation savepoint: %w", err)
	}
	if err := txQ.SetEnrichTaskPending(ctx, database.SetEnrichTaskPendingParams{
		Payload:  &payload,
		DedupKey: key,
		ID:       id,
	}); err != nil {
		if errs.IsConstraintOn(err, dedupIndexName) {
			if _, rbErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT child_activation"); rbErr != nil {
				return Activated, fmt.Errorf("roll back activation savepoint: %w", rbErr)
			}
			if _, discardErr := txQ.DiscardEnrichTask(ctx, database.DiscardEnrichTaskParams{
				Error: sql.NullString{String: "duplicate of live work: " + dedupKey, Valid: true},
				ID:    id,
			}); discardErr != nil {
				return Activated, fmt.Errorf("discard duplicate child %d: %w", id, discardErr)
			}
			if err := tx.Commit(); err != nil {
				return Activated, fmt.Errorf("commit duplicate discard: %w", err)
			}
			return DiscardedDuplicate, nil
		}
		return Activated, fmt.Errorf("activate child %d: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT child_activation"); err != nil {
		return Activated, fmt.Errorf("release activation savepoint: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Activated, fmt.Errorf("commit activation transaction: %w", err)
	}
	return Activated, nil
}

// Discard marks a task (and its waiting children) discarded, removing it from the batch counts; the
// return is the children discarded.
func (s *Store) Discard(ctx context.Context, id int64, errMsg string) (int64, error) {
	return s.queries.DiscardEnrichTask(ctx, database.DiscardEnrichTaskParams{
		ID:    id,
		Error: sql.NullString{String: errMsg, Valid: true},
	})
}

// claimToken returns the guard a claim records: the lease owner for a leased execution, a fresh token
// for every task of a pool execution. An owner id that is not a UUID is a programming error and fails
// loudly instead of writing an unusable guard.
func (s *Store) claimToken() (uuid.UUID, error) {
	if s.ownerID == "" {
		return uuid.New(), nil
	}
	token, err := uuid.Parse(s.ownerID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("owner id %q is not a uuid: %w", s.ownerID, err)
	}
	return token, nil
}

// withTransition runs one guarded task transition and releases the task-scoped locks the claim acquired,
// in the same transaction so a crash between the transition and the release cannot leave the lock held
// with no task to clear it. A batch-level hold belongs to the run and is released by the settle instead,
// which is why batch-holds mode and non-holding types skip the release.
func (s *Store) withTransition(ctx context.Context, op string, taskType types.TaskType, id int64, token uuid.UUID, transition func(*database.Queries) (int64, error)) (int64, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin %s transaction: %w", op, err)
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)

	rows, err := transition(txQ)
	if err != nil {
		return 0, fmt.Errorf("%s task %d: %w", op, id, err)
	}
	if rows > 0 && taskType.HoldsLocks() && !s.batchHoldsAcquired {
		if err := ReleaseLocksForToken(ctx, txQ, uuid.NullUUID{UUID: token, Valid: true}); err != nil {
			return 0, fmt.Errorf("release locks for task %d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit %s transaction: %w", op, err)
	}
	return rows, nil
}

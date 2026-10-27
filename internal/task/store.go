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
// completing, failing and activating them. The guard is always the owning batch execution's lease owner;
// the batch claim acquired the batch's locks, so task claims only verify their waits. All methods are safe
// for concurrent use.
type Store struct {
	client  *database.Client
	queries *database.Queries
	ownerID string
}

// NewStore builds the task store over the shared database client.
func NewStore(client *database.Client) *Store {
	return &Store{client: client, queries: client.Queries}
}

// SetOwnerID records the owner identity written on claims and batch ownership rows.
func (s *Store) SetOwnerID(id string) {
	s.ownerID = id
}

// Activation reports what activating one waiting successor did: it became pending, it was discarded because
// live work already covers its dedup key, it was discarded because its plan could not be completed, or it
// was no longer waiting because another path transitioned it first.
type Activation int

// Outcomes of successor activation; the zero value is Activated, so an ignored outcome reads as success.
const (
	Activated Activation = iota
	DiscardedDuplicate
	DiscardedInvalid
	AlreadyTransitioned
)

// ClaimNextPending claims one pending task of the type under the batch's execution guard and returns the
// task and its guard, which is the lease owner. The selection only considers tasks whose batch this
// execution owns and that is queued or processing, so a task left behind in any other batch can neither be
// claimed nor block the type queue. A type that waits for a held lock is skipped (its waits are verified by
// the selection; the batch claim owns the holds). sql.ErrNoRows reports that no task is claimable, and a
// store without an execution owner cannot claim: every claim is owner-scoped.
func (s *Store) ClaimNextPending(ctx context.Context, taskType types.TaskType) (database.Task, uuid.UUID, error) {
	if s.ownerID == "" {
		return database.Task{}, uuid.Nil, fmt.Errorf("claim %s: the store carries no execution owner", taskType)
	}
	token, err := uuid.Parse(s.ownerID)
	if err != nil {
		return database.Task{}, uuid.Nil, fmt.Errorf("owner id %q is not a uuid: %w", s.ownerID, err)
	}

	declaration := types.LockDeclarationFor(taskType)
	waitLocks := lockNameStrings(declaration.WaitsFor)

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return database.Task{}, uuid.Nil, fmt.Errorf("begin claim transaction: %w", err)
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)

	id, err := txQ.GetNextPendingTaskOfTypeForOwner(ctx, database.GetNextPendingTaskOfTypeForOwnerParams{
		TaskType:  taskType,
		OwnerID:   s.ownerID,
		WaitLocks: waitLocks,
	})
	if err != nil {
		return database.Task{}, uuid.Nil, err
	}

	rows, err := txQ.ClaimTask(ctx, id)
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

// CompleteTask marks a claimed task completed and activates its planned successors in one transaction: the
// guarded terminal write under the owning execution's lease, then per successor a savepoint flip under the
// key its type definition derived. A completion that writes no row (the claim was taken elsewhere) activates
// nothing, so a stale executor cannot arm successors of work it no longer owns. The transaction commits only
// when the completion and every activation write succeeded; the returned rows are the completion's and the
// outcomes describe each planned successor for the caller's log.
func (s *Store) CompleteTask(ctx context.Context, id int64, result json.RawMessage, token uuid.UUID, successors []SuccessorPlan) (int64, []SuccessorOutcome, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("begin complete transaction: %w", err)
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)
	rows, err := txQ.CompleteBlockingTask(ctx, database.CompleteBlockingTaskParams{
		Result:  &result,
		ID:      id,
		OwnerID: token.String(),
	})
	if err != nil {
		return 0, nil, fmt.Errorf("complete task %d: %w", id, err)
	}
	if rows == 0 {
		return 0, nil, nil
	}

	outcomes, err := activateSuccessors(ctx, tx, txQ, successors)
	if err != nil {
		return 0, nil, err
	}

	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("commit complete transaction: %w", err)
	}
	return rows, outcomes, nil
}

// FailTask marks a claimed task failed and discards its awaiting successor branch in one transaction: a task
// that will not complete can never activate its successors, so the whole waiting branch below it shares its
// terminal outcome and error text. A failure that writes no row (the claim was taken elsewhere) touches
// nothing. The returned counts are the failed rows and the discarded successors.
func (s *Store) FailTask(ctx context.Context, id int64, taskID, errMsg string, token uuid.UUID) (int64, int64, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin fail transaction: %w", err)
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)
	rows, err := txQ.FailBlockingTask(ctx, database.FailBlockingTaskParams{
		Error:   sql.NullString{String: errMsg, Valid: true},
		ID:      id,
		OwnerID: token.String(),
	})
	if err != nil {
		return 0, 0, fmt.Errorf("fail task %d: %w", id, err)
	}

	discarded := int64(0)
	if rows > 0 {
		discarded, err = txQ.DiscardWaitingSuccessorBranch(ctx, database.DiscardWaitingSuccessorBranchParams{
			Error:        errMsg,
			ParentTaskID: taskID,
		})
		if err != nil {
			return 0, 0, fmt.Errorf("discard successors of failed task %s: %w", taskID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit fail transaction: %w", err)
	}
	return rows, discarded, nil
}

// WaitingSuccessors returns one parent's waiting successors in enqueue order, the order activation applies
// them in so a pair of same-key successors resolves deterministically.
func (s *Store) WaitingSuccessors(ctx context.Context, parentTaskID string) ([]database.ListWaitingSuccessorsByParentRow, error) {
	return s.queries.ListWaitingSuccessorsByParent(ctx, parentTaskID)
}

// activateSuccessors executes one plan per waiting successor inside the completion transaction. Each
// successor flips under its own savepoint, so a dedup-index collision rolls back only that successor; any
// other error aborts the whole completion, letting the caller's failure path discard the branch instead of
// leaving half-activated work behind.
func activateSuccessors(ctx context.Context, tx *sql.Tx, txQ *database.Queries, plans []SuccessorPlan) ([]SuccessorOutcome, error) {
	outcomes := make([]SuccessorOutcome, 0, len(plans))
	for _, plan := range plans {
		outcome, err := activateSuccessor(ctx, tx, txQ, plan)
		if err != nil {
			return nil, err
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

// activateSuccessor executes one plan: an unplannable successor is discarded with its error, and a plannable
// one flips to pending under its dedup key, or is discarded as the duplicate of live work together with the
// waiting branch below it when the key collides.
func activateSuccessor(ctx context.Context, tx *sql.Tx, txQ *database.Queries, plan SuccessorPlan) (SuccessorOutcome, error) {
	outcome := SuccessorOutcome{TaskID: plan.TaskID}

	if plan.Err != nil {
		rows, err := txQ.DiscardWaitingSuccessor(ctx, database.DiscardWaitingSuccessorParams{
			Error: plan.Err.Error(),
			ID:    plan.ID,
		})
		if err != nil {
			return outcome, fmt.Errorf("discard unplannable successor %s: %w", plan.TaskID, err)
		}
		if rows == 0 {
			outcome.Activation = AlreadyTransitioned
			return outcome, nil
		}
		outcome.Activation = DiscardedInvalid
		outcome.Err = plan.Err
		return outcome, nil
	}

	if _, err := tx.ExecContext(ctx, "SAVEPOINT child_activation"); err != nil {
		return outcome, fmt.Errorf("set activation savepoint: %w", err)
	}
	rows, err := txQ.SetWaitingSuccessorPending(ctx, database.SetWaitingSuccessorPendingParams{
		Payload:  &plan.Payload,
		DedupKey: plan.DedupKey,
		ID:       plan.ID,
	})
	if err != nil {
		if !errs.IsConstraintOn(err, dedupIndexName) {
			return outcome, fmt.Errorf("activate successor %s: %w", plan.TaskID, err)
		}
		if _, rbErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT child_activation"); rbErr != nil {
			return outcome, fmt.Errorf("roll back activation savepoint: %w", rbErr)
		}
		outcome.Activation, err = discardDuplicateSuccessor(ctx, txQ, plan)
		if err != nil {
			return outcome, err
		}
		return outcome, nil
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT child_activation"); err != nil {
		return outcome, fmt.Errorf("release activation savepoint: %w", err)
	}
	if rows == 0 {
		outcome.Activation = AlreadyTransitioned
		return outcome, nil
	}
	outcome.Activation = Activated
	return outcome, nil
}

// discardDuplicateSuccessor ends the duplicate's row and its whole awaiting branch with the duplicate
// reason: the surviving live task covers the step, and a step that will not run cannot activate its own
// successors.
func discardDuplicateSuccessor(ctx context.Context, txQ *database.Queries, plan SuccessorPlan) (Activation, error) {
	reason := "duplicate of live work: " + plan.DedupKey
	if _, err := txQ.DiscardWaitingSuccessor(ctx, database.DiscardWaitingSuccessorParams{
		Error: reason,
		ID:    plan.ID,
	}); err != nil {
		return DiscardedDuplicate, fmt.Errorf("discard duplicate successor %s: %w", plan.TaskID, err)
	}
	if _, err := txQ.DiscardWaitingSuccessorBranch(ctx, database.DiscardWaitingSuccessorBranchParams{
		Error:        reason,
		ParentTaskID: plan.TaskID,
	}); err != nil {
		return DiscardedDuplicate, fmt.Errorf("discard branch of duplicate successor %s: %w", plan.TaskID, err)
	}
	return DiscardedDuplicate, nil
}

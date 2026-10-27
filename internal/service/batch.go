// Package service is the API-facing use-case layer: the batch read model with owner liveness, the people
// type, document type and user CRUD, the trash lifecycle with its background purge, the errors-directory
// admin, orphaned files, thumbnail backfill, and re-enrichment enqueueing. It sits between the handlers
// and the persistence layer and owns no domain invariants beyond its own. The batch lifecycle writes
// (settle, pause, cancel, resume) live in internal/task; this layer only reads batches and runs the
// reclamation the queue driver calls.
package service

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// BatchOverview is one batch row for every read surface: identity, origin, schedule, the per-status task
// counts, the owner's liveness and the total run duration once terminal. The list, the dashboard, the
// single-batch summary and the CLI all read this one aggregate.
type BatchOverview struct {
	BatchID    string
	Status     string
	Origin     string
	Schedule   string
	CreatedAt  time.Time
	Total      int64
	Waiting    int64
	Pending    int64
	Processing int64
	Completed  int64
	Failed     int64
	Cancelled  int64
	Discarded  int64
	OwnerState string
	OwnerPID   int64
	Orphaned   bool
	DurationMs *int64
}

// Batch is the batch read model and reclamation service: overviews, orphan and active counts, and the
// stale-task resets the queue driver uses.
type Batch struct {
	client     *database.Client
	queries    *database.Queries
	maxRetries int32
}

// lockNameValues is the closed lock set in SQL parameter form, owned by the vocabulary in internal/types.
var lockNameValues = types.LockNameValues()

// NewBatch wires the batch service; maxRetries bounds the task-store
// claim-contention retries.
func NewBatch(client *database.Client, maxRetries int) *Batch {
	return &Batch{client: client, queries: client.Queries, maxRetries: int32(maxRetries)}
}

// GetOverview returns one batch's aggregate row from the same query the list uses, so the single-batch
// view cannot drift from the list. A missing batch surfaces as a not-found error; an owner join failure
// fails the read instead of being reported as a missing owner.
func (s *Batch) GetOverview(ctx context.Context, batchID string) (*BatchOverview, error) {
	rows, err := s.queries.ListBatchOverviews(ctx, database.ListBatchOverviewsParams{
		BatchID:           sql.NullString{String: batchID, Valid: true},
		StaleAfterSeconds: task.LeaseStaleSeconds,
		RowLimit:          1,
	})
	if err != nil {
		return nil, errs.FromDB(err, "get batch "+batchID)
	}
	if len(rows) == 0 {
		return nil, errs.ENotFound("get batch "+batchID, sql.ErrNoRows)
	}
	return overviewFromRow(rows[0]), nil
}

// ListOverviews returns one page of batch aggregate rows, newest first; an empty status means every
// batch, otherwise the batch status must match.
func (s *Batch) ListOverviews(ctx context.Context, status string, limit, offset int32) ([]BatchOverview, error) {
	statusParam := sql.NullString{}
	if status != "" {
		statusParam = sql.NullString{String: status, Valid: true}
	}

	rows, err := s.queries.ListBatchOverviews(ctx, database.ListBatchOverviewsParams{
		StaleAfterSeconds: task.LeaseStaleSeconds,
		Status:            statusParam,
		RowLimit:          limit,
		RowOffset:         offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list batch overviews")
	}

	items := make([]BatchOverview, 0, len(rows))
	for _, row := range rows {
		items = append(items, *overviewFromRow(row))
	}
	return items, nil
}

// CountBatches counts the batch table directly; the total is batch-derived, not task-derived.
func (s *Batch) CountBatches(ctx context.Context) (int64, error) {
	count, err := s.queries.CountBatches(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count batches")
	}
	return count, nil
}

// CountActiveBatches counts the non-terminal batches: queued, processing and paused.
func (s *Batch) CountActiveBatches(ctx context.Context) (int64, error) {
	count, err := s.queries.CountActiveBatches(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count active batches")
	}
	return count, nil
}

// CountOrphaned counts the batches whose processing lease is missing or stale; the rule lives in the
// database's batch_orphaned function, so the list and the count cannot disagree.
func (s *Batch) CountOrphaned(ctx context.Context) (int64, error) {
	count, err := s.queries.CountOrphanedBatches(ctx, task.LeaseStaleSeconds)
	if err != nil {
		return 0, errs.FromDB(err, "count orphaned batches")
	}
	return count, nil
}

// HasPendingWork reports whether the batch still has waiting or pending
// tasks.
func (s *Batch) HasPendingWork(ctx context.Context, batchID string) (bool, error) {
	statuses := []types.TaskStatus{types.Task.Status.Pending, types.Task.Status.Processing, types.Task.Status.Waiting}
	for _, status := range statuses {
		count, err := s.queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: batchID, Valid: true},
			Status:  status,
		})
		if err != nil {
			return false, errs.FromDB(err, "count "+string(status)+" for batch "+batchID)
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

// CountPausedBatches counts the credit-paused batches, which block
// scanning.
func (s *Batch) CountPausedBatches(ctx context.Context) (int64, error) {
	count, err := s.queries.CountPausedBatches(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count paused batches")
	}
	return count, nil
}

// ListPausedBatches lists the credit-paused batch ids.
func (s *Batch) ListPausedBatches(ctx context.Context) ([]string, error) {
	ids, err := s.queries.ListPausedBatches(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list paused batches")
	}
	return ids, nil
}

// ListQueuedBatchesByAge returns the queued batches oldest first, with the task types each one contains.
// The dispatcher filters eligibility, backoff and claimability over these rows.
func (s *Batch) ListQueuedBatchesByAge(ctx context.Context) ([]database.ListQueuedBatchesByAgeRow, error) {
	rows, err := s.queries.ListQueuedBatchesByAge(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list queued batches by age")
	}
	return rows, nil
}

// CountLiveNoHoldsBatches counts one dispatcher's live leases on batches that hold no resource lock. It is
// that dispatcher's slot budget: lock-holding batches are serialized by their locks, and other dispatchers'
// or manual runs carry a different identity (or none) and are never charged.
func (s *Batch) CountLiveNoHoldsBatches(ctx context.Context, dispatcherID uuid.UUID) (int64, error) {
	count, err := s.queries.CountLiveNoHoldsBatches(ctx, database.CountLiveNoHoldsBatchesParams{
		DispatcherID:      dispatcherID,
		StaleAfterSeconds: task.LeaseStaleSeconds,
	})
	if err != nil {
		return 0, errs.FromDB(err, "count live no-holds batches")
	}
	return count, nil
}

// ListStaleBatchOwners returns the ownership rows whose heartbeat is
// older than the stale threshold.
func (s *Batch) ListStaleBatchOwners(ctx context.Context) ([]database.ListStaleBatchOwnersRow, error) {
	owners, err := s.queries.ListStaleBatchOwners(ctx, task.LeaseStaleSeconds)
	if err != nil {
		return nil, errs.FromDB(err, "list stale batch owners")
	}
	return owners, nil
}

// DeleteBatchOwnerByBatchID removes a batch's ownership row.
func (s *Batch) DeleteBatchOwnerByBatchID(ctx context.Context, batchID string) error {
	if _, err := s.queries.DeleteBatchOwnerByBatchID(ctx, batchID); err != nil {
		return errs.FromDB(err, "delete batch owner "+batchID)
	}
	return nil
}

// ResetProcessingTasksByBatch re-queues a batch's processing tasks and reports what the reset quarantined.
// It is the service's reclamation entry for the queue driver, so the batch reset keeps the service error
// convention instead of exposing the task package's raw database errors.
func (s *Batch) ResetProcessingTasksByBatch(ctx context.Context, batchID string) (task.ResetResult, error) {
	result, err := task.ResetProcessingTasksByBatch(ctx, s.client, batchID, s.maxRetries)
	if err != nil {
		return task.ResetResult{}, errs.FromDB(err, "reset processing tasks "+batchID)
	}
	return result, nil
}

// ResetStaleProcessingTasks re-queues the processing tasks whose owners went stale, bounded by the stale
// window in seconds, and returns the identities the sweeps quarantined. Every comparison uses the database
// clock, so the sweep shares the lease staleness clock instead of the application one.
func (s *Batch) ResetStaleProcessingTasks(ctx context.Context, staleAfterSeconds int64) (task.ResetResult, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return task.ResetResult{}, errs.FromDB(err, "begin transaction for reset stale processing tasks")
	}
	defer tx.Rollback()

	staleAfter := int32(staleAfterSeconds)
	txQ := s.client.Queries.WithTx(tx)

	blockingQuarantined, err := txQ.QuarantineStaleBlockingTasks(ctx, database.QuarantineStaleBlockingTasksParams{
		Attempts:          s.maxRetries,
		StaleAfterSeconds: staleAfter,
		Types:             types.LockHoldingTaskTypes(),
		LockNames:         lockNameValues,
	})
	if err != nil {
		return task.ResetResult{}, errs.FromDB(err, "quarantine stale blocking tasks")
	}

	blockingReset, err := txQ.ResetStaleBlockingTasks(ctx, database.ResetStaleBlockingTasksParams{
		Attempts:          s.maxRetries,
		StaleAfterSeconds: staleAfter,
		Types:             types.LockHoldingTaskTypes(),
		LockNames:         lockNameValues,
	})
	if err != nil {
		return task.ResetResult{}, errs.FromDB(err, "reset stale blocking tasks")
	}

	quarantined, err := txQ.QuarantineStaleProcessingTasks(ctx, database.QuarantineStaleProcessingTasksParams{
		Attempts:          s.maxRetries,
		StaleAfterSeconds: staleAfter,
		Types:             types.LockHoldingTaskTypes(),
	})
	if err != nil {
		return task.ResetResult{}, errs.FromDB(err, "quarantine stale processing tasks")
	}

	reset, err := txQ.ResetStaleProcessingTasks(ctx, database.ResetStaleProcessingTasksParams{
		Attempts:          s.maxRetries,
		StaleAfterSeconds: staleAfter,
		Types:             types.LockHoldingTaskTypes(),
	})
	if err != nil {
		return task.ResetResult{}, errs.FromDB(err, "reset stale processing tasks")
	}

	if _, err := txQ.RestoreDiscardedEnrichTasks(ctx); err != nil {
		return task.ResetResult{}, errs.FromDB(err, "restore discarded enrich tasks")
	}

	// quarantined stale consumes have no batch-level recovery point (the batch owner may still be live), so
	// sweep their waiting enriches here
	if _, err := txQ.DiscardOrphanedWaitingEnrichesGlobal(ctx); err != nil {
		return task.ResetResult{}, errs.FromDB(err, "discard orphaned waiting enriches")
	}

	if err := tx.Commit(); err != nil {
		return task.ResetResult{}, errs.FromDB(err, "commit transaction for reset stale processing tasks")
	}

	result := task.ResetResult{Reset: blockingReset + reset}
	for _, row := range blockingQuarantined {
		result.Quarantined = append(result.Quarantined, task.TaskIdentity{TaskID: row.TaskID, TaskType: row.TaskType})
	}
	for _, row := range quarantined {
		result.Quarantined = append(result.Quarantined, task.TaskIdentity{TaskID: row.TaskID, TaskType: row.TaskType})
	}
	return result, nil
}

// overviewFromRow maps the aggregate row every batch surface reports.
func overviewFromRow(row database.ListBatchOverviewsRow) *BatchOverview {
	var createdAt time.Time
	if row.BatchCreatedAt.Valid {
		createdAt = row.BatchCreatedAt.Time
	}

	var schedule string
	if row.Schedule.Valid {
		schedule = row.Schedule.String
	}

	var durationMs *int64
	if row.Waiting == 0 && row.Pending == 0 && row.Processing == 0 {
		firstStarted := toNullTime(row.FirstStartedAt)
		lastCompleted := toNullTime(row.LastCompletedAt)
		if firstStarted.Valid && lastCompleted.Valid {
			v := lastCompleted.Time.Sub(firstStarted.Time).Milliseconds()
			durationMs = &v
		}
	}

	return &BatchOverview{
		BatchID:    row.BatchID,
		Status:     string(row.BatchStatus),
		Origin:     string(row.Origin),
		Schedule:   schedule,
		CreatedAt:  createdAt,
		Total:      row.Total,
		Waiting:    row.Waiting,
		Pending:    row.Pending,
		Processing: row.Processing,
		Completed:  row.Completed,
		Failed:     row.Failed,
		Cancelled:  row.Cancelled,
		Discarded:  row.Discarded,
		OwnerState: deriveOwnerState(row.OwnerLastHeartbeat, row.OwnerStale).String(),
		OwnerPID:   row.OwnerPid.Int64,
		Orphaned:   row.Orphaned,
		DurationMs: durationMs,
	}
}

func deriveOwnerState(hb sql.NullTime, stale sql.NullBool) task.OwnerState {
	if !hb.Valid {
		return task.OwnerNone
	}
	if stale.Valid && stale.Bool {
		return task.OwnerStale
	}
	return task.OwnerLive
}

// toNullTime reads the aggregate timestamps the database driver returns as interface values for the
// lateral subquery's MIN/MAX columns; sqlc cannot infer their type through the join.
func toNullTime(v any) sql.NullTime {
	switch t := v.(type) {
	case string:
		parsed, err := time.Parse("2006-01-02T15:04:05Z", t)
		if err != nil {
			parsed, err = time.Parse("2006-01-02 15:04:05", t)
			if err != nil {
				return sql.NullTime{}
			}
		}
		return sql.NullTime{Time: parsed, Valid: true}
	case time.Time:
		return sql.NullTime{Time: t, Valid: true}
	default:
		return sql.NullTime{}
	}
}

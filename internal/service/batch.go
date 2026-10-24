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

// BatchSummary is one batch's task counts by status plus its owner's liveness; Orphaned is never true
// for a batch with a non-consume task, which has no owner row by design.
type BatchSummary struct {
	BatchID    string
	Status     string
	Origin     string
	Schedule   string
	Waiting    int64
	Pending    int64
	Processing int64
	Completed  int64
	Failed     int64
	Cancelled  int64
	Discarded  int64
	OwnerState string
	Orphaned   bool
	OwnerPID   int64
}

// BatchOverview is one batch row for the list view: identity, counts, owner state, and the total run
// duration once completed.
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
	Orphaned   bool
	DurationMs *int64
}

// Batch is the batch read model and reclamation service: summaries, orphan detection and the stale-task
// resets the queue driver uses.
type Batch struct {
	client     *database.Client
	queries    *database.Queries
	maxRetries int32
}

// Batches with a non-consume task have no batch_owner row by design; never report them as orphaned.
// The registry owns the task-type set, so the display rule cannot drift from the dispatch exclusion.
var nonConsumeTaskTypeValues = types.NonConsumeTaskTypeValues()

// lockNameValues is the closed lock set in SQL parameter form, owned by the vocabulary in internal/types.
var lockNameValues = types.LockNameValues()

// NewBatch wires the batch service; maxRetries bounds the task-store
// claim-contention retries.
func NewBatch(client *database.Client, maxRetries int) *Batch {
	return &Batch{client: client, queries: client.Queries, maxRetries: int32(maxRetries)}
}

// GetSummary returns one batch's per-status task counts and owner state.
func (s *Batch) GetSummary(ctx context.Context, batchID string) (*BatchSummary, error) {
	statuses := types.AllTaskStatuses()
	summary := &BatchSummary{BatchID: batchID}

	batch, err := s.queries.GetBatch(ctx, batchID)
	if err != nil {
		return nil, errs.FromDB(err, "get batch "+batchID)
	}
	summary.Status = string(batch.Status)
	summary.Origin = string(batch.Origin)
	summary.Schedule = batch.Schedule.String

	for _, status := range statuses {
		count, err := s.queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: batchID, Valid: true},
			Status:  status,
		})
		if err != nil {
			return nil, errs.FromDB(err, "count "+string(status)+" for batch "+batchID)
		}
		switch status {
		case types.Task.Status.Waiting:
			summary.Waiting = count
		case types.Task.Status.Pending:
			summary.Pending = count
		case types.Task.Status.Processing:
			summary.Processing = count
		case types.Task.Status.Completed:
			summary.Completed = count
		case types.Task.Status.Failed:
			summary.Failed = count
		case types.Task.Status.Cancelled:
			summary.Cancelled = count
		case types.Task.Status.Discarded:
			summary.Discarded = count
		}
	}

	state, pid, err := s.batchOwnerState(ctx, batchID)
	if err != nil {
		summary.OwnerState = "none"
		summary.Orphaned = false
	} else {
		summary.OwnerState = state.String()
		summary.OwnerPID = pid
		nonConsume, err := s.queries.BatchHasNonConsumeTask(ctx, database.BatchHasNonConsumeTaskParams{
			BatchID:         sql.NullString{String: batchID, Valid: true},
			NonConsumeTypes: nonConsumeTaskTypeValues,
		})
		if err != nil {
			return nil, errs.FromDB(err, "classify batch "+batchID)
		}
		summary.Orphaned = task.IsOrphaned(state, summary.Pending, summary.Processing) && !nonConsume
	}

	return summary, nil
}

// ListSummaries returns the summaries matching the batch filter.
func (s *Batch) ListSummaries(ctx context.Context, f task.BatchFilter) ([]BatchSummary, error) {
	var rows []sql.NullString
	var err error

	if f.Status != "" {
		rows, err = s.queries.ListDistinctBatchIDsByStatus(ctx, database.ListDistinctBatchIDsByStatusParams{
			Status: types.TaskStatus(f.Status),
			Limit:  f.Limit,
			Offset: f.Offset,
		})
	} else {
		rows, err = s.queries.ListDistinctBatchIDs(ctx, database.ListDistinctBatchIDsParams{
			Limit:  f.Limit,
			Offset: f.Offset,
		})
	}
	if err != nil {
		return nil, errs.FromDB(err, "list batch summaries")
	}

	summaries := make([]BatchSummary, 0, len(rows))
	for _, row := range rows {
		if !row.Valid {
			continue
		}
		s, err := s.GetSummary(ctx, row.String)
		if err != nil {
			continue
		}
		summaries = append(summaries, *s)
	}
	return summaries, nil
}

// ListOverviews pages the batch rows for the list view.
func (s *Batch) ListOverviews(ctx context.Context, limit, offset int32) ([]BatchOverview, error) {
	rows, err := s.queries.ListBatchOverviews(ctx, database.ListBatchOverviewsParams{
		Limit:             limit,
		Offset:            offset,
		StaleAfterSeconds: task.LeaseStaleSeconds,
		NonConsumeTypes:   nonConsumeTaskTypeValues,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list batch overviews")
	}

	items := make([]BatchOverview, 0, len(rows))
	for _, row := range rows {
		pending := toInt64(row.Pending)
		processing := toInt64(row.Processing)
		waiting := toInt64(row.Waiting)

		state := deriveOwnerState(row.OwnerLastHeartbeat, row.OwnerStale)

		var createdAt time.Time
		if row.BatchCreatedAt.Valid {
			createdAt = row.BatchCreatedAt.Time
		}

		var durationMs *int64
		if pending == 0 && processing == 0 && waiting == 0 {
			firstStarted := toNullTime(row.FirstStartedAt)
			lastCompleted := toNullTime(row.LastCompletedAt)
			if firstStarted.Valid && lastCompleted.Valid {
				v := lastCompleted.Time.Sub(firstStarted.Time).Milliseconds()
				durationMs = &v
			}
		}

		var schedule string
		if row.Schedule.Valid {
			schedule = row.Schedule.String
		}

		items = append(items, BatchOverview{
			BatchID:    row.BatchID,
			Status:     string(row.BatchStatus),
			Origin:     string(row.Origin),
			Schedule:   schedule,
			CreatedAt:  createdAt,
			Total:      row.Total,
			Waiting:    waiting,
			Pending:    pending,
			Processing: processing,
			Completed:  toInt64(row.Completed),
			Failed:     toInt64(row.Failed),
			Cancelled:  toInt64(row.Cancelled),
			Discarded:  toInt64(row.Discarded),
			OwnerState: state.String(),
			Orphaned:   task.IsOrphaned(state, pending, processing) && !row.NonConsume,
			DurationMs: durationMs,
		})
	}
	return items, nil
}

// CountOrphaned counts live batches whose owner is gone while work
// remains.
func (s *Batch) CountOrphaned(ctx context.Context) (int64, error) {
	activeIDs, err := s.queries.ActiveBatchIDs(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "active batch ids")
	}

	orphanedCount := int64(0)
	for _, batchID := range activeIDs {
		state, _, err := s.batchOwnerState(ctx, batchID)
		if err != nil {
			continue
		}
		if state != task.OwnerLive {
			orphanedCount++
		}
	}
	return orphanedCount, nil
}

// CountDistinct counts the batch ids in the task table.
func (s *Batch) CountDistinct(ctx context.Context) (int64, error) {
	count, err := s.queries.CountDistinctBatches(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count distinct batches")
	}
	return count, nil
}

// ActiveIDs lists the batch ids that are not terminal.
func (s *Batch) ActiveIDs(ctx context.Context) ([]string, error) {
	ids, err := s.queries.ActiveBatchIDs(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "active batch ids")
	}
	return ids, nil
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

// CountQueuedConsumeBatches counts queued consume batches.
func (s *Batch) CountQueuedConsumeBatches(ctx context.Context) (int64, error) {
	count, err := s.queries.CountQueuedConsumeBatches(ctx, nonConsumeTaskTypeValues)
	if err != nil {
		return 0, errs.FromDB(err, "count queued consume batches")
	}
	return count, nil
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

// CountLiveBatches counts batches in a non-terminal state.
func (s *Batch) CountLiveBatches(ctx context.Context) (int64, error) {
	count, err := s.queries.CountLiveBatches(ctx, task.LeaseStaleSeconds)
	if err != nil {
		return 0, errs.FromDB(err, "count live batches")
	}
	return count, nil
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

	// the lock release must precede the resets because its subselect reads status = 'processing'
	if err := task.ReleaseLocksForStaleHolders(ctx, txQ, types.LockHoldingTaskTypes()); err != nil {
		return task.ResetResult{}, errs.FromDB(err, "release locks for stale holders")
	}

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

func (s *Batch) batchOwnerState(ctx context.Context, batchID string) (task.OwnerState, int64, error) {
	bo, err := s.queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
		BatchID:           batchID,
		StaleAfterSeconds: task.LeaseStaleSeconds,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return task.OwnerNone, 0, nil
		}
		return task.OwnerNone, 0, err
	}

	if bo.Stale {
		return task.OwnerStale, bo.Pid, nil
	}
	return task.OwnerLive, bo.Pid, nil
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

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

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

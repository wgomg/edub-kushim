// Package service is the API-facing use-case layer: the batch lifecycle
// with owner liveness and reclaim, the people, people type, document type
// and user CRUD, the trash lifecycle with its background purge, the
// errors-directory admin, orphaned files, thumbnail backfill, and
// re-enrichment enqueueing. It sits between the handlers and the
// persistence layer and owns no domain invariants beyond its own.
package service

import (
	"context"
	"database/sql"
	"time"

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

// Batch is the batch lifecycle service: summaries, orphan detection,
// retry, cancel protocol, requeueing, and the stale-task reclaim the
// queue driver uses.
type Batch struct {
	client     *database.Client
	queries    *database.Queries
	maxRetries int32
}

// Batches with a non-consume task have no batch_owner row by design; never report them as orphaned.
// The registry owns the task-type set, so the display rule cannot drift from the dispatch exclusion.
var nonConsumeTaskTypeValues = types.NonConsumeTaskTypeValues()

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
		Limit:           limit,
		Offset:          offset,
		NonConsumeTypes: nonConsumeTaskTypeValues,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list batch overviews")
	}

	items := make([]BatchOverview, 0, len(rows))
	for _, row := range rows {
		pending := toInt64(row.Pending)
		processing := toInt64(row.Processing)
		waiting := toInt64(row.Waiting)

		state := deriveOwnerState(row.OwnerLastHeartbeat)

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

// RetryFailed resets a batch's failed tasks back to pending and returns
// how many were reset.
func (s *Batch) RetryFailed(ctx context.Context, batchID string) (int64, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, errs.FromDB(err, "begin transaction for retry failed batch "+batchID)
	}
	defer tx.Rollback()

	bid := sql.NullString{String: batchID, Valid: true}
	txQ := s.client.Queries.WithTx(tx)

	count, err := txQ.RetryFailedTasksByBatch(ctx, bid)
	if err != nil {
		return 0, errs.FromDB(err, "retry failed batch "+batchID)
	}

	if _, err := txQ.RestoreDiscardedEnrichTasksByBatch(ctx, bid); err != nil {
		return 0, errs.FromDB(err, "restore discarded enrich tasks for batch "+batchID)
	}

	if err := tx.Commit(); err != nil {
		return 0, errs.FromDB(err, "commit transaction for retry failed batch "+batchID)
	}

	return count, nil
}

// BeginCancel starts the cancel protocol: the batch flips to cancelling,
// its pending tasks are cancelled, and the live owner's identity is
// returned so the caller can signal it to stop.
func (s *Batch) BeginCancel(ctx context.Context, batchID string) (pendingCancelled int64, ownerPID int64, ownerID string, err error) {
	pendingCancelled, err = s.queries.CancelPendingTasksByBatch(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return 0, 0, "", errs.FromDB(err, "cancel pending tasks for batch "+batchID)
	}

	bo, err := s.queries.GetBatchOwner(ctx, batchID)
	if err != nil {
		if err == sql.ErrNoRows {
			return pendingCancelled, 0, "", nil
		}
		return 0, 0, "", errs.FromDB(err, "get batch owner for "+batchID)
	}

	return pendingCancelled, bo.Pid, bo.OwnerID, nil
}

// CompleteCancel finishes the cancel protocol: the batch flips to
// cancelled and its still-processing tasks are cancelled, only after the
// owner named at BeginCancel has released it.
func (s *Batch) CompleteCancel(ctx context.Context, batchID, ownerID string) (processingCancelled int64, err error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, errs.FromDB(err, "begin transaction for cancel "+batchID)
	}
	defer tx.Rollback()

	bid := sql.NullString{String: batchID, Valid: true}
	txQ := s.client.Queries.WithTx(tx)

	if _, err := txQ.ReleaseMaintenanceLockForBatch(ctx, database.ReleaseMaintenanceLockForBatchParams{
		BatchID: bid,
		Types:   types.BlockingTaskTypes(),
	}); err != nil {
		return 0, errs.FromDB(err, "release maintenance lock for batch "+batchID)
	}

	processingCancelled, err = txQ.CancelProcessingTasksByBatch(ctx, bid)
	if err != nil {
		return 0, errs.FromDB(err, "cancel processing tasks for batch "+batchID)
	}

	if err := tx.Commit(); err != nil {
		return 0, errs.FromDB(err, "commit transaction for cancel "+batchID)
	}

	s.queries.ReleaseBatchOwner(ctx, database.ReleaseBatchOwnerParams{
		BatchID: batchID,
		OwnerID: ownerID,
	})

	if err := s.queries.SetBatchCancelled(ctx, batchID); err != nil {
		return 0, errs.FromDB(err, "set batch cancelled "+batchID)
	}

	return processingCancelled, nil
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

// IsLockedByLiveOwner reports whether the batch's owner heartbeat is
// fresh.
func (s *Batch) IsLockedByLiveOwner(ctx context.Context, batchID string) (bool, error) {
	state, _, err := s.batchOwnerState(ctx, batchID)
	if err != nil {
		return false, nil
	}
	return state == task.OwnerLive, nil
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

// GetNextQueuedBatch claims the next queued consume batch in creation
// order; sql.ErrNoRows when the queue is empty.
func (s *Batch) GetNextQueuedBatch(ctx context.Context) (database.Batch, error) {
	b, err := s.queries.GetNextQueuedBatch(ctx, nonConsumeTaskTypeValues)
	if err != nil {
		return database.Batch{}, errs.FromDB(err, "get next queued batch")
	}
	return b, nil
}

// RequeueBatch flips a completed or failed batch back to queued for
// reprocessing.
func (s *Batch) RequeueBatch(ctx context.Context, batchID string) error {
	if err := s.queries.RequeueBatch(ctx, batchID); err != nil {
		return errs.FromDB(err, "requeue batch")
	}
	return nil
}

// SetBatchProcessing flips the batch into its processing state, ignoring
// absent rows; SetBatchCompleted, SetBatchFailed and SetBatchCancelled
// are the other lifecycle setters with the same semantics.
func (s *Batch) SetBatchProcessing(ctx context.Context, batchID string) error {
	if err := s.queries.SetBatchProcessing(ctx, batchID); err != nil {
		return errs.FromDB(err, "set batch processing")
	}
	return nil
}

// SetBatchCompleted settles the batch as completed.
func (s *Batch) SetBatchCompleted(ctx context.Context, batchID string) error {
	if err := s.queries.SetBatchCompleted(ctx, batchID); err != nil {
		return errs.FromDB(err, "set batch completed")
	}
	return nil
}

// SetBatchFailed settles the batch as failed.
func (s *Batch) SetBatchFailed(ctx context.Context, batchID string) error {
	if err := s.queries.SetBatchFailed(ctx, batchID); err != nil {
		return errs.FromDB(err, "set batch failed")
	}
	return nil
}

// SetBatchCancelled settles the batch as cancelled.
func (s *Batch) SetBatchCancelled(ctx context.Context, batchID string) error {
	if err := s.queries.SetBatchCancelled(ctx, batchID); err != nil {
		return errs.FromDB(err, "set batch cancelled")
	}
	return nil
}

// CountLiveBatches counts batches in a non-terminal state.
func (s *Batch) CountLiveBatches(ctx context.Context) (int64, error) {
	count, err := s.queries.CountLiveBatches(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count live batches")
	}
	return count, nil
}

// ListStaleBatchOwners returns the ownership rows whose heartbeat is
// older than the stale threshold.
func (s *Batch) ListStaleBatchOwners(ctx context.Context) ([]database.ListStaleBatchOwnersRow, error) {
	owners, err := s.queries.ListStaleBatchOwners(ctx)
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

// ResetProcessingTasksByBatch re-queues a batch's processing tasks and
// returns how many were reset.
func (s *Batch) ResetProcessingTasksByBatch(ctx context.Context, batchID string) (int64, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, errs.FromDB(err, "begin transaction for reset "+batchID)
	}
	defer tx.Rollback()

	bid := sql.NullString{String: batchID, Valid: true}
	txQ := s.client.Queries.WithTx(tx)

	if _, err := txQ.ReleaseMaintenanceLockForBatch(ctx, database.ReleaseMaintenanceLockForBatchParams{
		BatchID: bid,
		Types:   types.BlockingTaskTypes(),
	}); err != nil {
		return 0, errs.FromDB(err, "release maintenance lock for batch "+batchID)
	}

	quarantined, err := txQ.QuarantineProcessingTasksByBatch(ctx, database.QuarantineProcessingTasksByBatchParams{
		BatchID:  bid,
		Attempts: s.maxRetries,
	})
	if err != nil {
		return 0, errs.FromDB(err, "quarantine processing tasks "+batchID)
	}

	reset, err := txQ.ResetProcessingTasksByBatch(ctx, database.ResetProcessingTasksByBatchParams{
		BatchID:  bid,
		Attempts: s.maxRetries,
	})
	if err != nil {
		return 0, errs.FromDB(err, "reset processing tasks "+batchID)
	}

	if _, err := txQ.RestoreDiscardedEnrichTasksByBatch(ctx, bid); err != nil {
		return 0, errs.FromDB(err, "restore discarded enrich tasks "+batchID)
	}

	if err := tx.Commit(); err != nil {
		return 0, errs.FromDB(err, "commit transaction for reset "+batchID)
	}

	return quarantined + reset, nil
}

// ResetStaleProcessingTasks re-queues the processing tasks whose owners
// went stale, bounded by the staleness threshold in seconds.
func (s *Batch) ResetStaleProcessingTasks(ctx context.Context, staleAfterSeconds int64) (int64, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, errs.FromDB(err, "begin transaction for reset stale processing tasks")
	}
	defer tx.Rollback()

	cutoff := time.Now().Add(-time.Duration(staleAfterSeconds) * time.Second)
	txQ := s.client.Queries.WithTx(tx)

	// the maintenance lock release must precede the resets because its subselect reads status = 'processing'.
	if _, err := txQ.ReleaseMaintenanceLockForStaleHolders(ctx, types.BlockingTaskTypes()); err != nil {
		return 0, errs.FromDB(err, "release maintenance lock for stale holders")
	}

	blockingQuarantined, err := txQ.QuarantineStaleBlockingTasks(ctx, database.QuarantineStaleBlockingTasksParams{
		Attempts:  s.maxRetries,
		StartedAt: sql.NullTime{Time: cutoff, Valid: true},
		Types:     types.BlockingTaskTypes(),
	})
	if err != nil {
		return 0, errs.FromDB(err, "quarantine stale blocking tasks")
	}

	blockingReset, err := txQ.ResetStaleBlockingTasks(ctx, database.ResetStaleBlockingTasksParams{
		Attempts:  s.maxRetries,
		StartedAt: sql.NullTime{Time: cutoff, Valid: true},
		Types:     types.BlockingTaskTypes(),
	})
	if err != nil {
		return 0, errs.FromDB(err, "reset stale blocking tasks")
	}

	quarantined, err := txQ.QuarantineStaleProcessingTasks(ctx, database.QuarantineStaleProcessingTasksParams{
		Attempts:  s.maxRetries,
		StartedAt: sql.NullTime{Time: cutoff, Valid: true},
		Types:     types.BlockingTaskTypes(),
	})
	if err != nil {
		return 0, errs.FromDB(err, "quarantine stale processing tasks")
	}

	reset, err := txQ.ResetStaleProcessingTasks(ctx, database.ResetStaleProcessingTasksParams{
		Attempts:  s.maxRetries,
		StartedAt: sql.NullTime{Time: cutoff, Valid: true},
		Types:     types.BlockingTaskTypes(),
	})
	if err != nil {
		return 0, errs.FromDB(err, "reset stale processing tasks")
	}

	if _, err := txQ.RestoreDiscardedEnrichTasks(ctx); err != nil {
		return 0, errs.FromDB(err, "restore discarded enrich tasks")
	}

	// quarantined stale consumes have no batch-level recovery point (the
	// batch owner may still be live), so sweep their waiting enriches here.
	if _, err := txQ.DiscardWaitingEnrichesOfFailedConsumesGlobal(ctx); err != nil {
		return 0, errs.FromDB(err, "discard waiting enriches of failed consumes")
	}

	if err := tx.Commit(); err != nil {
		return 0, errs.FromDB(err, "commit transaction for reset stale processing tasks")
	}

	return blockingQuarantined + blockingReset + quarantined + reset, nil
}

func (s *Batch) batchOwnerState(ctx context.Context, batchID string) (task.OwnerState, int64, error) {
	bo, err := s.queries.GetBatchOwner(ctx, batchID)
	if err != nil {
		if err == sql.ErrNoRows {
			return task.OwnerNone, 0, nil
		}
		return task.OwnerNone, 0, err
	}

	if isOwnerStale(bo.LastHeartbeat) {
		return task.OwnerStale, bo.Pid, nil
	}
	return task.OwnerLive, bo.Pid, nil
}

func isOwnerStale(heartbeat time.Time) bool {
	return time.Since(heartbeat) > task.StaleAfter
}

func deriveOwnerState(hb sql.NullTime) task.OwnerState {
	if !hb.Valid {
		return task.OwnerNone
	}
	if isOwnerStale(hb.Time) {
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

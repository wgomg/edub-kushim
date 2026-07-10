package service

import (
	"context"
	"database/sql"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/task"
)

type BatchSummary struct {
	BatchID    string
	Status     string
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

type BatchOverview struct {
	BatchID    string
	Status     string
	Source     string
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

type Batch struct {
	client     *database.Client
	queries    *database.Queries
	maxRetries int64
}

func NewBatch(client *database.Client, maxRetries int) *Batch {
	return &Batch{client: client, queries: client.Queries, maxRetries: int64(maxRetries)}
}

func (s *Batch) GetSummary(ctx context.Context, batchID string) (*BatchSummary, error) {
	statuses := []string{"waiting", "pending", "processing", "completed", "failed", "cancelled", "discarded"}
	summary := &BatchSummary{BatchID: batchID}

	batch, err := s.queries.GetBatch(ctx, batchID)
	if err != nil {
		return nil, errs.FromDB(err, "get batch "+batchID)
	}
	summary.Status = batch.Status

	for _, status := range statuses {
		count, err := s.queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: batchID, Valid: true},
			Status:  status,
		})
		if err != nil {
			return nil, errs.FromDB(err, "count "+status+" for batch "+batchID)
		}
		switch status {
		case "waiting":
			summary.Waiting = count
		case "pending":
			summary.Pending = count
		case "processing":
			summary.Processing = count
		case "completed":
			summary.Completed = count
		case "failed":
			summary.Failed = count
		case "cancelled":
			summary.Cancelled = count
		case "discarded":
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
		summary.Orphaned = task.IsOrphaned(state, summary.Pending, summary.Processing)
	}

	return summary, nil
}

func (s *Batch) ListSummaries(ctx context.Context, f task.BatchFilter) ([]BatchSummary, error) {
	var rows []sql.NullString
	var err error

	if f.Status != "" {
		rows, err = s.queries.ListDistinctBatchIDsByStatus(ctx, database.ListDistinctBatchIDsByStatusParams{
			Status: f.Status,
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

func (s *Batch) ListOverviews(ctx context.Context, limit, offset int64) ([]BatchOverview, error) {
	rows, err := s.queries.ListBatchOverviews(ctx, database.ListBatchOverviewsParams{
		Limit:  limit,
		Offset: offset,
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

		items = append(items, BatchOverview{
			BatchID:    row.BatchID,
			Status:     row.BatchStatus,
			Source:     row.Source,
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
			Orphaned:   task.IsOrphaned(state, pending, processing),
			DurationMs: durationMs,
		})
	}
	return items, nil
}

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

func (s *Batch) RetryFailed(ctx context.Context, batchID string) (int64, error) {
	count, err := s.queries.RetryFailedTasksByBatch(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return 0, errs.FromDB(err, "retry failed batch "+batchID)
	}
	return count, nil
}

func (s *Batch) Create(ctx context.Context, id, source, status string) error {
	if id == "" {
		return errs.EInvalid("create batch", sql.ErrNoRows)
	}
	_, err := s.queries.CreateBatch(ctx, database.CreateBatchParams{
		ID:     id,
		Source: source,
		Status: status,
	})
	if err != nil {
		return errs.FromDB(err, "create batch")
	}
	return nil
}

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

func (s *Batch) CompleteCancel(ctx context.Context, batchID, ownerID string) (processingCancelled int64, err error) {
	processingCancelled, err = s.queries.CancelProcessingTasksByBatch(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return 0, errs.FromDB(err, "cancel processing tasks for batch "+batchID)
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

func (s *Batch) CountDistinct(ctx context.Context) (int64, error) {
	count, err := s.queries.CountDistinctBatches(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count distinct batches")
	}
	return count, nil
}

func (s *Batch) ActiveIDs(ctx context.Context) ([]string, error) {
	ids, err := s.queries.ActiveBatchIDs(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "active batch ids")
	}
	return ids, nil
}

func (s *Batch) HasPendingWork(ctx context.Context, batchID string) (bool, error) {
	statuses := []string{"pending", "processing", "waiting"}
	for _, status := range statuses {
		count, err := s.queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
			BatchID: sql.NullString{String: batchID, Valid: true},
			Status:  status,
		})
		if err != nil {
			return false, errs.FromDB(err, "count "+status+" for batch "+batchID)
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Batch) IsLockedByLiveOwner(ctx context.Context, batchID string) (bool, error) {
	state, _, err := s.batchOwnerState(ctx, batchID)
	if err != nil {
		return false, nil
	}
	return state == task.OwnerLive, nil
}

func (s *Batch) CountQueuedBatches(ctx context.Context) (int64, error) {
	count, err := s.queries.CountQueuedBatches(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count queued batches")
	}
	return count, nil
}

func (s *Batch) GetNextQueuedBatch(ctx context.Context) (database.Batch, error) {
	b, err := s.queries.GetNextQueuedBatch(ctx)
	if err != nil {
		return database.Batch{}, errs.FromDB(err, "get next queued batch")
	}
	return b, nil
}

func (s *Batch) RequeueBatch(ctx context.Context, batchID string) error {
	if err := s.queries.RequeueBatch(ctx, batchID); err != nil {
		return errs.FromDB(err, "requeue batch")
	}
	return nil
}

func (s *Batch) SetBatchProcessing(ctx context.Context, batchID string) error {
	if err := s.queries.SetBatchProcessing(ctx, batchID); err != nil {
		return errs.FromDB(err, "set batch processing")
	}
	return nil
}

func (s *Batch) SetBatchCompleted(ctx context.Context, batchID string) error {
	if err := s.queries.SetBatchCompleted(ctx, batchID); err != nil {
		return errs.FromDB(err, "set batch completed")
	}
	return nil
}

func (s *Batch) SetBatchFailed(ctx context.Context, batchID string) error {
	if err := s.queries.SetBatchFailed(ctx, batchID); err != nil {
		return errs.FromDB(err, "set batch failed")
	}
	return nil
}

func (s *Batch) SetBatchCancelled(ctx context.Context, batchID string) error {
	if err := s.queries.SetBatchCancelled(ctx, batchID); err != nil {
		return errs.FromDB(err, "set batch cancelled")
	}
	return nil
}

func (s *Batch) CountLiveBatches(ctx context.Context) (int64, error) {
	count, err := s.queries.CountLiveBatches(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count live batches")
	}
	return count, nil
}

func (s *Batch) ListStaleBatchOwners(ctx context.Context) ([]database.ListStaleBatchOwnersRow, error) {
	owners, err := s.queries.ListStaleBatchOwners(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list stale batch owners")
	}
	return owners, nil
}

func (s *Batch) DeleteBatchOwnerByBatchID(ctx context.Context, batchID string) error {
	if _, err := s.queries.DeleteBatchOwnerByBatchID(ctx, batchID); err != nil {
		return errs.FromDB(err, "delete batch owner "+batchID)
	}
	return nil
}

func (s *Batch) ResetProcessingTasksByBatch(ctx context.Context, batchID string) (int64, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, errs.FromDB(err, "begin transaction for reset "+batchID)
	}
	defer tx.Rollback()

	bid := sql.NullString{String: batchID, Valid: true}
	txQ := s.client.Queries.WithTx(tx)

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

	if err := tx.Commit(); err != nil {
		return 0, errs.FromDB(err, "commit transaction for reset "+batchID)
	}

	return quarantined + reset, nil
}

func (s *Batch) ResetStaleProcessingTasks(ctx context.Context, staleAfterSeconds int64) (int64, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, errs.FromDB(err, "begin transaction for reset stale processing tasks")
	}
	defer tx.Rollback()

	cutoff := time.Now().Add(-time.Duration(staleAfterSeconds) * time.Second)
	txQ := s.client.Queries.WithTx(tx)

	quarantined, err := txQ.QuarantineStaleProcessingTasks(ctx, database.QuarantineStaleProcessingTasksParams{
		Attempts:  s.maxRetries,
		StartedAt: sql.NullTime{Time: cutoff, Valid: true},
	})
	if err != nil {
		return 0, errs.FromDB(err, "quarantine stale processing tasks")
	}

	reset, err := txQ.ResetStaleProcessingTasks(ctx, database.ResetStaleProcessingTasksParams{
		Attempts:  s.maxRetries,
		StartedAt: sql.NullTime{Time: cutoff, Valid: true},
	})
	if err != nil {
		return 0, errs.FromDB(err, "reset stale processing tasks")
	}

	if err := tx.Commit(); err != nil {
		return 0, errs.FromDB(err, "commit transaction for reset stale processing tasks")
	}

	return quarantined + reset, nil
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

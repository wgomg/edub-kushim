-- +goose Up

-- tessdata and hugot downloads are direct config updates now, so no dispatcher can ever claim a config
-- batch left over from an earlier version: cancel its tasks and the batches that carry only them. Stale
-- leases and locks on those batches are released by the reaper.

UPDATE task
SET status = 'cancelled', completed_at = CURRENT_TIMESTAMP
WHERE task_type = 'config' AND status IN ('pending', 'processing', 'waiting');

UPDATE batch
SET status = 'cancelled'
WHERE status IN ('queued', 'processing', 'paused')
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = batch.id AND t.task_type = 'config')
  AND NOT EXISTS (SELECT 1 FROM task t WHERE t.batch_id = batch.id AND t.task_type <> 'config');

-- A pre-upgrade execution that was killed left its batch processing with claimed task rows but no lease.
-- Nothing can reclaim that state now: the dispatcher lists only queued batches, the reaper watches leases,
-- and resume refuses processing batches. Reset the in-flight tasks so the next claim runs them again, then
-- hand the unleased batches back to the queue; a batch whose work is already settled is claimed and settled
-- by the dispatcher's next execution.

UPDATE task
SET status = 'pending', started_at = NULL, progress = NULL, attempts = attempts + 1
WHERE status = 'processing'
  AND batch_id IN (
    SELECT b.id FROM batch b
    WHERE b.status = 'processing'
      AND NOT EXISTS (SELECT 1 FROM batch_owner bo WHERE bo.batch_id = b.id)
  );

UPDATE batch
SET status = 'queued'
WHERE status = 'processing'
  AND NOT EXISTS (SELECT 1 FROM batch_owner bo WHERE bo.batch_id = batch.id);

-- +goose Down

-- the cleanup is not invertible: the work was cancelled or requeued together with its task type

SELECT 1;

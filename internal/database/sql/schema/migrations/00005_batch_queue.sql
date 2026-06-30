-- +goose Up
ALTER TABLE batch ADD COLUMN status TEXT NOT NULL DEFAULT 'queued';
CREATE INDEX IF NOT EXISTS idx_batch_status ON batch(status);

-- Derive correct status for existing batches based on task data.
-- Priority: queued > failed > cancelled > completed.
--
-- Batches with non‑terminal tasks → queued (stale, re‑queueable).
UPDATE batch SET status = 'queued'
WHERE id IN (
  SELECT DISTINCT batch_id FROM task
  WHERE status IN ('pending', 'processing', 'waiting')
);

-- Batches with only terminal tasks, at least one failed → failed.
UPDATE batch SET status = 'failed'
WHERE id IN (
  SELECT batch_id FROM task
  GROUP BY batch_id
  HAVING SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) > 0
    AND SUM(CASE WHEN status IN ('pending','processing','waiting')
                 THEN 1 ELSE 0 END) = 0
);

-- Batches with only terminal tasks, no failed, at least one cancelled → cancelled.
UPDATE batch SET status = 'cancelled'
WHERE id IN (
  SELECT batch_id FROM task
  GROUP BY batch_id
  HAVING SUM(CASE WHEN status = 'cancelled' THEN 1 ELSE 0 END) > 0
    AND SUM(CASE WHEN status IN ('pending','processing','waiting','failed')
                 THEN 1 ELSE 0 END) = 0
);

-- Everything still 'queued': all tasks completed/discarded, or no tasks → completed.
UPDATE batch SET status = 'completed'
WHERE status = 'queued'
  AND (
    id IN (
      SELECT batch_id FROM task
      GROUP BY batch_id
      HAVING SUM(CASE WHEN status IN ('pending','processing','waiting',
                                       'failed','cancelled')
                       THEN 1 ELSE 0 END) = 0
    )
    OR id NOT IN (SELECT DISTINCT batch_id FROM task WHERE batch_id IS NOT NULL)
  );

-- +goose Down
DROP INDEX IF EXISTS idx_batch_status;
-- SQLite cannot easily DROP COLUMN — column stays but is harmless

-- +goose Up

-- the scheduler reads a schedule's last terminal batch on every housekeeping tick; the filter and ordering
-- only touch this schedule's rows, and without the index each call walks the whole unpruned batch history

CREATE INDEX idx_batch_schedule_terminal ON batch (schedule, created_at DESC)
  WHERE status IN ('completed', 'failed', 'cancelled');

-- +goose Down

DROP INDEX idx_batch_schedule_terminal;

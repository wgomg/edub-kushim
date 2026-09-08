-- +goose Up
UPDATE batch SET source = 'upload' WHERE source = 'api-upload';

CREATE TYPE batch_source AS ENUM ('cli', 'api', 'upload', 'polling', 'reenrich', 'orphaned-restore', 'thumbbackfill', 'backup', 'mirror', 'config');
CREATE TYPE batch_status AS ENUM ('queued', 'processing', 'completed', 'failed', 'paused', 'cancelled');
CREATE TYPE task_type AS ENUM ('consume', 'enrich', 'thumbnail', 'backup', 'mirror', 'config');
CREATE TYPE task_status AS ENUM ('pending', 'processing', 'completed', 'failed', 'cancelled', 'discarded', 'waiting');

-- +goose StatementBegin
DO $$
DECLARE
    offenders text;
BEGIN
    SELECT string_agg(DISTINCT col || ': ' || value, ', ' ORDER BY col || ': ' || value)
    INTO offenders
    FROM (
        SELECT 'batch.source' AS col, source::text AS value FROM batch
        WHERE source::text NOT IN ('cli','api','upload','polling','reenrich','orphaned-restore','thumbbackfill','backup','mirror','config')
        UNION ALL
        SELECT 'batch.status', status::text FROM batch
        WHERE status::text NOT IN ('queued','processing','completed','failed','paused','cancelled')
        UNION ALL
        SELECT 'task.task_type', task_type::text FROM task
        WHERE task_type::text NOT IN ('consume','enrich','thumbnail','backup','mirror','config')
        UNION ALL
        SELECT 'task.status', status::text FROM task
        WHERE status::text NOT IN ('pending','processing','completed','failed','cancelled','discarded','waiting')
    ) bad;
    IF offenders IS NOT NULL THEN
        RAISE EXCEPTION 'unregistered vocabulary values: %', offenders;
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE batch ALTER COLUMN source DROP DEFAULT;
ALTER TABLE batch ALTER COLUMN status DROP DEFAULT;
ALTER TABLE task ALTER COLUMN status DROP DEFAULT;
DROP INDEX idx_task_pending;
DROP INDEX idx_task_pending_type;
DROP INDEX idx_task_dedup;
ALTER TABLE batch ALTER COLUMN source TYPE batch_source USING source::batch_source;
ALTER TABLE batch ALTER COLUMN status TYPE batch_status USING status::batch_status;
ALTER TABLE task ALTER COLUMN task_type TYPE task_type USING task_type::task_type;
ALTER TABLE task ALTER COLUMN status TYPE task_status USING status::task_status;
ALTER TABLE batch ALTER COLUMN status SET DEFAULT 'queued';
ALTER TABLE task ALTER COLUMN status SET DEFAULT 'pending';
CREATE INDEX idx_task_pending ON task(created_at) WHERE status = 'pending';
CREATE INDEX idx_task_pending_type ON task(task_type, created_at) WHERE status = 'pending';
CREATE UNIQUE INDEX idx_task_dedup ON task(task_type, dedup_key)
    WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_batch_queued()
RETURNS trigger AS $$
BEGIN
  IF NEW.status = 'queued' THEN
    PERFORM pg_notify('batch_queued', NEW.id);
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE batch ALTER COLUMN status DROP DEFAULT;
ALTER TABLE task ALTER COLUMN status DROP DEFAULT;
DROP INDEX idx_task_pending;
DROP INDEX idx_task_pending_type;
DROP INDEX idx_task_dedup;
ALTER TABLE batch ALTER COLUMN source TYPE TEXT USING source::text;
ALTER TABLE batch ALTER COLUMN status TYPE TEXT USING status::text;
ALTER TABLE task ALTER COLUMN task_type TYPE TEXT USING task_type::text;
ALTER TABLE task ALTER COLUMN status TYPE TEXT USING status::text;
ALTER TABLE batch ALTER COLUMN source SET DEFAULT 'unknown';
CREATE INDEX idx_task_pending ON task(created_at) WHERE status = 'pending';
CREATE INDEX idx_task_pending_type ON task(task_type, created_at) WHERE status = 'pending';
CREATE UNIQUE INDEX idx_task_dedup ON task(task_type, dedup_key)
    WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_batch_queued()
RETURNS trigger AS $$
BEGIN
  IF NEW.status = 'queued' THEN
    PERFORM pg_notify('batch_queued', NEW.id);
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TYPE batch_source;
DROP TYPE batch_status;
DROP TYPE task_type;
DROP TYPE task_status;
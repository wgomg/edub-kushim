-- +goose Up
CREATE TYPE batch_origin AS ENUM ('cli', 'api', 'polling', 'schedule', 'setup', 'wizard');

ALTER TYPE task_type ADD VALUE 'migrate-db';
ALTER TYPE task_type ADD VALUE 'migrate-storage';

ALTER TABLE batch ADD COLUMN origin batch_origin;
ALTER TABLE batch ADD COLUMN schedule text;

UPDATE batch
SET origin = (CASE source
    WHEN 'cli' THEN 'cli'
    WHEN 'api' THEN 'api'
    WHEN 'polling' THEN 'polling'
    WHEN 'upload' THEN 'api'
    WHEN 'reenrich' THEN 'api'
    WHEN 'orphaned-restore' THEN 'api'
    WHEN 'backup' THEN 'schedule'
    WHEN 'mirror' THEN 'schedule'
    WHEN 'thumbbackfill' THEN 'schedule'
    WHEN 'tagreconcile' THEN 'schedule'
    WHEN 'tagmeasure' THEN 'schedule'
    WHEN 'tagidentity' THEN 'schedule'
    WHEN 'tagaudit' THEN 'schedule'
    WHEN 'config' THEN 'setup'
END)::batch_origin;

ALTER TABLE batch ALTER COLUMN origin SET NOT NULL;
ALTER TABLE batch DROP COLUMN source;
DROP TYPE batch_source;

-- +goose Down
ALTER TABLE batch ADD COLUMN source text;

-- The inverse mapping cannot recover which legacy source an origin came from: api collapses upload,
-- reenrich and orphaned-restore, and schedule is approximated from the batch's first task type. The
-- historical trigger is unrecoverable, so this rollback is deliberately lossy.
UPDATE batch
SET source = CASE
    WHEN origin = 'cli' THEN 'cli'
    WHEN origin = 'api' THEN 'api'
    WHEN origin = 'polling' THEN 'polling'
    WHEN origin IN ('setup', 'wizard') THEN 'config'
    WHEN origin = 'schedule' THEN COALESCE((
        SELECT CASE t.task_type
            WHEN 'backup' THEN 'backup'
            WHEN 'mirror' THEN 'mirror'
            WHEN 'thumbbackfill' THEN 'thumbbackfill'
            WHEN 'tagreconcile' THEN 'tagreconcile'
            WHEN 'tagmeasure' THEN 'tagmeasure'
            WHEN 'tagidentity' THEN 'tagidentity'
            WHEN 'tagaudit' THEN 'tagaudit'
        END
        FROM task t
        WHERE t.batch_id = batch.id
        ORDER BY t.created_at
        LIMIT 1
    ), 'config')
END;

ALTER TABLE batch ALTER COLUMN source SET NOT NULL;

ALTER TABLE batch DROP COLUMN origin;
ALTER TABLE batch DROP COLUMN schedule;

DELETE FROM batch
WHERE id IN (
    SELECT DISTINCT batch_id FROM task
    WHERE task_type IN ('migrate-db', 'migrate-storage') AND batch_id IS NOT NULL
);
DELETE FROM task WHERE task_type IN ('migrate-db', 'migrate-storage');

ALTER TABLE task ALTER COLUMN task_type TYPE TEXT USING task_type::text;
DROP TYPE task_type;
CREATE TYPE task_type AS ENUM (
    'consume', 'enrich', 'thumbnail', 'backup', 'mirror', 'config',
    'tagreconcile', 'thumbbackfill', 'tagmeasure', 'tagidentity', 'tagaudit'
);
ALTER TABLE task ALTER COLUMN task_type TYPE task_type USING task_type::task_type;

CREATE TYPE batch_source AS ENUM (
    'cli', 'api', 'upload', 'polling', 'reenrich', 'orphaned-restore',
    'thumbbackfill', 'backup', 'mirror', 'config', 'tagreconcile',
    'tagmeasure', 'tagidentity', 'tagaudit'
);
ALTER TABLE batch ALTER COLUMN source TYPE batch_source USING source::batch_source;

DROP TYPE batch_origin;

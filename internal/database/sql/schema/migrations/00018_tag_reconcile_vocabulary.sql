-- +goose Up
ALTER TYPE task_type ADD VALUE 'tagreconcile';
ALTER TYPE batch_source ADD VALUE 'tagreconcile';

-- +goose Down
ALTER TABLE task ALTER COLUMN task_type TYPE TEXT USING task_type::text;
ALTER TABLE batch ALTER COLUMN source TYPE TEXT USING source::text;

DROP TYPE task_type;
DROP TYPE batch_source;

CREATE TYPE task_type AS ENUM ('consume', 'enrich', 'thumbnail', 'backup', 'mirror', 'config');
CREATE TYPE batch_source AS ENUM ('cli', 'api', 'upload', 'polling', 'reenrich', 'orphaned-restore', 'thumbbackfill', 'backup', 'mirror', 'config');

ALTER TABLE task ALTER COLUMN task_type TYPE task_type USING task_type::task_type;
ALTER TABLE batch ALTER COLUMN source TYPE batch_source USING source::batch_source;
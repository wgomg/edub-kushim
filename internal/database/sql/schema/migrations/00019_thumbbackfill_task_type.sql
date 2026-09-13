-- +goose Up
ALTER TYPE task_type ADD VALUE 'thumbbackfill';

-- +goose Down
DELETE FROM task WHERE task_type = 'thumbbackfill';

ALTER TABLE task ALTER COLUMN task_type TYPE TEXT USING task_type::text;

DROP TYPE task_type;

CREATE TYPE task_type AS ENUM ('consume', 'enrich', 'thumbnail', 'backup', 'mirror', 'config', 'tagreconcile');

ALTER TABLE task ALTER COLUMN task_type TYPE task_type USING task_type::task_type;

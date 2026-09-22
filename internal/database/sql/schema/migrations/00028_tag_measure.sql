-- +goose Up
ALTER TYPE task_type ADD VALUE 'tagmeasure';
ALTER TYPE batch_source ADD VALUE 'tagmeasure';

CREATE TABLE tag_measurement (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id           uuid NOT NULL,
    snapshot_at      timestamptz NOT NULL,
    rule_set_version text NOT NULL,
    operator         text NOT NULL CHECK (operator IN (
        'mass', 'overlap', 'equality', 'containment',
        'reconstructibility', 'part_coverage', 'complement'
    )),
    operands         text[] NOT NULL,
    numbers          jsonb NOT NULL,
    state            text NOT NULL CHECK (state IN ('sufficient', 'insufficient', 'gray')),
    caveats          text[] NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_tag_measurement_run ON tag_measurement (run_id);
CREATE INDEX idx_tag_measurement_operator ON tag_measurement (operator);
CREATE INDEX idx_tag_measurement_operands ON tag_measurement USING GIN (operands);

-- +goose Down
DROP TABLE tag_measurement;

DELETE FROM task WHERE task_type = 'tagmeasure';
DELETE FROM batch WHERE source = 'tagmeasure';

ALTER TABLE task ALTER COLUMN task_type TYPE TEXT USING task_type::text;
ALTER TABLE batch ALTER COLUMN source TYPE TEXT USING source::text;

DROP TYPE task_type;
DROP TYPE batch_source;

CREATE TYPE task_type AS ENUM ('consume', 'enrich', 'thumbnail', 'backup', 'mirror', 'config', 'tagreconcile', 'thumbbackfill');
CREATE TYPE batch_source AS ENUM ('cli', 'api', 'upload', 'polling', 'reenrich', 'orphaned-restore', 'thumbbackfill', 'backup', 'mirror', 'config', 'tagreconcile');

ALTER TABLE task ALTER COLUMN task_type TYPE task_type USING task_type::task_type;
ALTER TABLE batch ALTER COLUMN source TYPE batch_source USING source::batch_source;

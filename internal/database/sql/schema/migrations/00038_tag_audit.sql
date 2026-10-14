-- +goose Up
ALTER TYPE task_type ADD VALUE 'tagaudit';
ALTER TYPE batch_source ADD VALUE 'tagaudit';

ALTER TABLE tag_measurement DROP CONSTRAINT tag_measurement_operator_check;
ALTER TABLE tag_measurement ADD CONSTRAINT tag_measurement_operator_check CHECK (operator IN (
    'mass', 'overlap', 'equality', 'containment',
    'reconstructibility', 'part_coverage', 'complement', 'coverage'
));

CREATE TABLE tag_coverage_event (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id      bigint NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    tag_name         text NOT NULL,
    class            text NOT NULL CHECK (class IN (
        'explicit', 'pending_split', 'pending_canonicalization', 'missing', 'gray', 'pending_coverage', 'not_applies'
    )),
    source           text NOT NULL CHECK (source IN ('trigger', 'lexical', 'skip')),
    verdict          jsonb NOT NULL DEFAULT '{}'::jsonb,
    verdict_model    text,
    text_hash        text NOT NULL DEFAULT '',
    criteria_version text NOT NULL,
    action           text NOT NULL CHECK (action IN ('fill', 'flag', 'none', 'proposal')),
    review_outcome   text CHECK (review_outcome IN ('applied', 'not_applies', 'measured_miss')),
    provisional      boolean NOT NULL DEFAULT false,
    rule_set_version text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_tag_coverage_event_fresh
    ON tag_coverage_event (document_id, tag_name, text_hash, criteria_version)
    WHERE verdict_model IS NOT NULL;
CREATE INDEX idx_tag_coverage_event_class ON tag_coverage_event (class);
CREATE INDEX idx_tag_coverage_event_document ON tag_coverage_event (document_id);
CREATE INDEX idx_tag_coverage_event_tag ON tag_coverage_event (tag_name);
CREATE INDEX idx_tag_coverage_event_open
    ON tag_coverage_event (created_at DESC)
    WHERE action IN ('flag', 'proposal') AND review_outcome IS NULL;

CREATE TABLE tag_audit_register (
    document_id bigint PRIMARY KEY REFERENCES document(id) ON DELETE CASCADE,
    audit_count bigint NOT NULL DEFAULT 0
);

CREATE TABLE tag_audit_sweep (
    id          smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    sweep_count bigint NOT NULL DEFAULT 0
);

INSERT INTO tag_audit_sweep (id, sweep_count) VALUES (1, 0);

-- +goose Down
DROP TABLE tag_audit_sweep;
DROP TABLE tag_audit_register;
DROP TABLE tag_coverage_event;

ALTER TABLE tag_measurement DROP CONSTRAINT tag_measurement_operator_check;
ALTER TABLE tag_measurement ADD CONSTRAINT tag_measurement_operator_check CHECK (operator IN (
    'mass', 'overlap', 'equality', 'containment',
    'reconstructibility', 'part_coverage', 'complement'
));

DELETE FROM task WHERE task_type = 'tagaudit';
DELETE FROM batch WHERE source = 'tagaudit';

ALTER TABLE task ALTER COLUMN task_type TYPE TEXT USING task_type::text;
ALTER TABLE batch ALTER COLUMN source TYPE TEXT USING source::text;

DROP TYPE task_type;
DROP TYPE batch_source;

CREATE TYPE task_type AS ENUM ('consume', 'enrich', 'thumbnail', 'backup', 'mirror', 'config', 'tagreconcile', 'thumbbackfill', 'tagmeasure', 'tagidentity');
CREATE TYPE batch_source AS ENUM ('cli', 'api', 'upload', 'polling', 'reenrich', 'orphaned-restore', 'thumbbackfill', 'backup', 'mirror', 'config', 'tagreconcile', 'tagmeasure', 'tagidentity');

ALTER TABLE task ALTER COLUMN task_type TYPE task_type USING task_type::task_type;
ALTER TABLE batch ALTER COLUMN source TYPE batch_source USING source::batch_source;

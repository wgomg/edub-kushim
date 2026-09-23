-- +goose Up
ALTER TYPE task_type ADD VALUE 'tagidentity';
ALTER TYPE batch_source ADD VALUE 'tagidentity';

CREATE TABLE tag_alias (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alias_name       TEXT NOT NULL UNIQUE,
    canonical_tag_id BIGINT NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
    rule_set_version TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tag_alias_canonical_tag ON tag_alias (canonical_tag_id);

CREATE TABLE tag_identity_event (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind             TEXT NOT NULL CHECK (kind IN (
        'base_form', 'fold_gate', 'conventional_form', 'same_intent'
    )),
    candidate        TEXT NOT NULL,
    partner          TEXT,
    criteria         JSONB NOT NULL DEFAULT '{}'::jsonb,
    verdict          TEXT,
    model            TEXT,
    rule_set_version TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tag_identity_event_candidate
    ON tag_identity_event (candidate, rule_set_version, created_at DESC);
CREATE INDEX idx_tag_identity_event_partner
    ON tag_identity_event (partner, rule_set_version, created_at DESC);
-- serves the pending backlog scan and count without sorting the whole table
CREATE INDEX idx_tag_identity_event_pending_created
    ON tag_identity_event (created_at)
    WHERE verdict IS NULL;
CREATE UNIQUE INDEX idx_tag_identity_event_pending
    ON tag_identity_event (kind, candidate, COALESCE(partner, ''), rule_set_version)
    WHERE verdict IS NULL;

-- +goose Down
DROP TABLE tag_identity_event;
DROP TABLE tag_alias;

DELETE FROM task WHERE task_type = 'tagidentity';
DELETE FROM batch WHERE source = 'tagidentity';

ALTER TABLE task ALTER COLUMN task_type TYPE TEXT USING task_type::text;
ALTER TABLE batch ALTER COLUMN source TYPE TEXT USING source::text;

DROP TYPE task_type;
DROP TYPE batch_source;

CREATE TYPE task_type AS ENUM ('consume', 'enrich', 'thumbnail', 'backup', 'mirror', 'config', 'tagreconcile', 'thumbbackfill', 'tagmeasure');
CREATE TYPE batch_source AS ENUM ('cli', 'api', 'upload', 'polling', 'reenrich', 'orphaned-restore', 'thumbbackfill', 'backup', 'mirror', 'config', 'tagreconcile', 'tagmeasure');

ALTER TABLE task ALTER COLUMN task_type TYPE task_type USING task_type::task_type;
ALTER TABLE batch ALTER COLUMN source TYPE batch_source USING source::batch_source;

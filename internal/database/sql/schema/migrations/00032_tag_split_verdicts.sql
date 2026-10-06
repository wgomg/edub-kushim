-- +goose Up
CREATE TABLE tag_part_dimension_event (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id BIGINT NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    value TEXT NOT NULL,
    criteria JSONB NOT NULL,
    dimensional BOOLEAN NOT NULL,
    verdict_model TEXT,
    policy_version TEXT NOT NULL,
    verdict_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tag_part_dimension_event_value
    ON tag_part_dimension_event (value, policy_version, verdict_at DESC);
CREATE UNIQUE INDEX idx_tag_part_dimension_event_fresh_value
    ON tag_part_dimension_event (value, policy_version)
    WHERE verdict_model IS NOT NULL;

CREATE TABLE tag_compound_status_event (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id BIGINT NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    candidate TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('named_unit', 'composition', 'no_dimension', 'defer')),
    criteria JSONB NOT NULL,
    verdict_model TEXT,
    split_criteria_version TEXT NOT NULL,
    verdict_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tag_compound_status_event_candidate
    ON tag_compound_status_event (candidate, split_criteria_version, verdict_at DESC);
CREATE UNIQUE INDEX idx_tag_compound_status_event_fresh_candidate
    ON tag_compound_status_event (candidate, split_criteria_version)
    WHERE verdict_model IS NOT NULL;

ALTER TABLE tag_composition_event ADD COLUMN reconstructible BOOLEAN;

-- +goose Down
ALTER TABLE tag_composition_event DROP COLUMN reconstructible;
DROP TABLE tag_compound_status_event;
DROP TABLE tag_part_dimension_event;

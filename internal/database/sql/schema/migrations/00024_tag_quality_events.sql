-- +goose Up
CREATE TABLE tag_quality_event (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id BIGINT NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    tag_name TEXT NOT NULL,
    criteria JSONB NOT NULL,
    pass BOOLEAN NOT NULL,
    verdict_model TEXT,
    policy_version TEXT NOT NULL,
    verdict_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tag_quality_event_name
    ON tag_quality_event (tag_name, policy_version, verdict_at DESC);
CREATE UNIQUE INDEX idx_tag_quality_event_fresh_name
    ON tag_quality_event (tag_name, policy_version)
    WHERE verdict_model IS NOT NULL;

-- +goose Down
DROP TABLE tag_quality_event;

-- +goose Up
CREATE TYPE tag_verdict AS ENUM ('same', 'variant', 'related');

CREATE TABLE tag_verdict_event (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id BIGINT NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    query TEXT NOT NULL,
    target TEXT NOT NULL,
    sim DOUBLE PRECISION NOT NULL,
    verdict tag_verdict NOT NULL,
    verdict_model TEXT,
    policy_version TEXT NOT NULL,
    verdict_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tag_verdict_event_pair
    ON tag_verdict_event (query, target, policy_version, verdict_at DESC);
CREATE INDEX idx_tag_verdict_event_stale
    ON tag_verdict_event (policy_version, verdict_at);
CREATE UNIQUE INDEX idx_tag_verdict_event_fresh_pair
    ON tag_verdict_event (query, target, policy_version)
    WHERE verdict_model IS NOT NULL;

-- +goose Down
DROP TABLE tag_verdict_event;
DROP TYPE tag_verdict;
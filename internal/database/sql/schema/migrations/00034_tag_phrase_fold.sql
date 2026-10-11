-- +goose Up
CREATE TABLE tag_phrase_fold_event (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id BIGINT REFERENCES document(id) ON DELETE CASCADE,
    phrase TEXT NOT NULL,
    part TEXT NOT NULL,
    anchor TEXT NOT NULL,
    criteria JSONB NOT NULL,
    fold BOOLEAN NOT NULL,
    verdict_model TEXT,
    criteria_version TEXT NOT NULL,
    verdict_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tag_phrase_fold_event_key
    ON tag_phrase_fold_event (phrase, part, anchor, criteria_version, verdict_at DESC);
CREATE UNIQUE INDEX idx_tag_phrase_fold_event_fresh_key
    ON tag_phrase_fold_event (phrase, part, anchor, criteria_version)
    WHERE verdict_model IS NOT NULL;

-- +goose Down
DROP TABLE tag_phrase_fold_event;

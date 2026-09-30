-- +goose Up
CREATE TABLE tag_composition_event (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id BIGINT NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    candidate TEXT NOT NULL,
    parts JSONB NOT NULL DEFAULT '[]'::jsonb,
    disposition TEXT NOT NULL CHECK (disposition IN ('split', 'keep', 'drop')),
    reason TEXT NOT NULL,
    criteria_version TEXT NOT NULL,
    model TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tag_composition_event_candidate
    ON tag_composition_event (candidate, criteria_version, created_at DESC);
-- serves the document-delete cascade without scanning the growing table
CREATE INDEX idx_tag_composition_event_document
    ON tag_composition_event (document_id);

-- +goose Down
DROP TABLE tag_composition_event;

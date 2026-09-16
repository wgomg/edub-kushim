-- +goose Up
ALTER TABLE tag ADD COLUMN promoted_at TIMESTAMPTZ;

CREATE INDEX idx_tag_provisional
    ON tag (created_at)
    WHERE promoted_at IS NULL;

UPDATE tag t
SET promoted_at = coalesce(t.created_at, now())
WHERE EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id);

-- +goose Down
DROP INDEX idx_tag_provisional;
ALTER TABLE tag DROP COLUMN promoted_at;

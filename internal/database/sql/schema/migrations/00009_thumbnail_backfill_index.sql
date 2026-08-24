-- +goose Up
-- Partial index backing the scheduled thumbnail backfill sweep
-- (WHERE has_thumbnail = FALSE AND deleted_at IS NULL ORDER BY id).
CREATE INDEX idx_document_no_thumbnail ON document(id)
    WHERE has_thumbnail = FALSE AND deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_document_no_thumbnail;
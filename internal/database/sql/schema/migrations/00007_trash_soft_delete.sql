-- +goose Up
ALTER TABLE document ADD COLUMN deleted_at TIMESTAMPTZ;
CREATE INDEX idx_document_deleted_at ON document(deleted_at) WHERE deleted_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_document_deleted_at;
ALTER TABLE document DROP COLUMN IF EXISTS deleted_at;

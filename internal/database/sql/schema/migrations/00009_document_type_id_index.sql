-- +goose Up
CREATE INDEX IF NOT EXISTS idx_document_doctype ON document(document_type_id);

-- +goose Down
DROP INDEX IF EXISTS idx_document_doctype;

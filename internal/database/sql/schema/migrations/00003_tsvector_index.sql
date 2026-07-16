-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_document_tsv
  ON document USING GIN (text_search_vector);

-- +goose Down
DROP INDEX IF EXISTS idx_document_tsv;

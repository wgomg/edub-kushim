-- +goose Up
ALTER TABLE document_tag
    ADD COLUMN source text NOT NULL DEFAULT 'enrich';

-- +goose Down
ALTER TABLE document_tag
    DROP COLUMN source;

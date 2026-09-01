-- +goose Up
ALTER TABLE document ADD COLUMN processed_size BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE document DROP COLUMN processed_size;
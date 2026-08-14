-- +goose Up
ALTER TABLE document ADD COLUMN has_thumbnail BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE document DROP COLUMN IF EXISTS has_thumbnail;

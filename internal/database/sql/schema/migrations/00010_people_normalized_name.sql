-- +goose Up
ALTER TABLE people ADD COLUMN IF NOT EXISTS normalized_name TEXT;

-- +goose Down
ALTER TABLE people DROP COLUMN IF EXISTS normalized_name;

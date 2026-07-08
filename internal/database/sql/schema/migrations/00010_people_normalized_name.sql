-- +goose Up
ALTER TABLE people ADD COLUMN normalized_name TEXT;

-- +goose Down
ALTER TABLE people DROP COLUMN normalized_name;

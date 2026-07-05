-- +goose Up
ALTER TABLE user ADD COLUMN role TEXT NOT NULL DEFAULT 'viewer';

-- +goose Down
ALTER TABLE user DROP COLUMN role;

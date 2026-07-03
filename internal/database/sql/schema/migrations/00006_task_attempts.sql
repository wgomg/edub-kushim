-- +goose Up
ALTER TABLE task ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;

-- +goose Down
-- SQLite cannot DROP COLUMN; column stays but is harmless.

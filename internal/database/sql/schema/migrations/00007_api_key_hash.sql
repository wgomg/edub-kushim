-- +goose Up
-- NOTE: api_key was never populated by application code, so the rename is safe.
-- Requires single-instance deployment or blue-green cutover — old binaries
-- referencing the old column name will fail during a rolling update.
ALTER TABLE user RENAME COLUMN api_key TO api_key_hash;
ALTER TABLE user ADD COLUMN api_key_prefix TEXT;
ALTER TABLE user ADD COLUMN api_key_created_at DATETIME;

-- +goose Down
ALTER TABLE user DROP COLUMN api_key_created_at;
ALTER TABLE user DROP COLUMN api_key_prefix;
ALTER TABLE user RENAME COLUMN api_key_hash TO api_key;

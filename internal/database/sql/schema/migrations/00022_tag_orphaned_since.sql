-- +goose Up
ALTER TABLE tag ADD COLUMN orphaned_since timestamptz;

-- +goose Down
ALTER TABLE tag DROP COLUMN orphaned_since;

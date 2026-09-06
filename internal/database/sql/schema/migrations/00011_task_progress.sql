-- +goose Up
ALTER TABLE task ADD COLUMN progress JSONB;

-- +goose Down
ALTER TABLE task DROP COLUMN progress;
-- +goose Up
ALTER TABLE tag ADD COLUMN accepted_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE tag DROP COLUMN accepted_at;

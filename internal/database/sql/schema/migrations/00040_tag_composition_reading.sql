-- +goose Up
ALTER TABLE tag_composition_event ADD COLUMN reading JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE tag_composition_event DROP COLUMN reading;

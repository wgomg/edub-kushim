-- +goose Up
ALTER TABLE tag_quality_event ADD COLUMN outcome TEXT;

-- +goose Down
ALTER TABLE tag_quality_event DROP COLUMN outcome;

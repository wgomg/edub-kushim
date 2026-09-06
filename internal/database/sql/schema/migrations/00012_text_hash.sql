-- +goose Up
ALTER TABLE document ADD COLUMN text_hash text;

-- +goose Down
ALTER TABLE document DROP COLUMN text_hash;
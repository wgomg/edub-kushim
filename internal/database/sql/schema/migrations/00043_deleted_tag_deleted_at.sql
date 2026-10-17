-- +goose Up
ALTER TABLE deleted_tag RENAME COLUMN created_at TO deleted_at;

-- +goose Down
ALTER TABLE deleted_tag RENAME COLUMN deleted_at TO created_at;

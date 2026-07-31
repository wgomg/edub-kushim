-- +goose Up
ALTER TABLE document RENAME COLUMN mime_type TO original_type;
ALTER TABLE orphaned_file RENAME COLUMN mime_type TO original_type;

-- +goose Down
ALTER TABLE document RENAME COLUMN original_type TO mime_type;
ALTER TABLE orphaned_file RENAME COLUMN original_type TO mime_type;

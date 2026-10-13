-- +goose Up
ALTER TABLE tag_composition_event ALTER COLUMN document_id DROP NOT NULL;

-- +goose Down
-- document_id stays nullable on rollback: no prior code requires NOT NULL, and sweep-originated NULL rows
-- cannot be removed from the append-only composition journal

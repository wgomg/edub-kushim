-- +goose Up
-- the tag's birth provenance: which producer created the row. Existing rows were analyzer-born, so the
-- default backfills them; the write path stamps split parts and knowledge base containers at creation.
ALTER TABLE tag ADD COLUMN origin TEXT NOT NULL DEFAULT 'analyzer';

-- +goose Down
ALTER TABLE tag DROP COLUMN origin;

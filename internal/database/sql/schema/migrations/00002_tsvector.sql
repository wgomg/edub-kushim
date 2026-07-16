-- +goose Up
ALTER TABLE document ADD COLUMN text_search_vector tsvector
  GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(text_content, ''))
  ) STORED;

-- +goose Down
ALTER TABLE document DROP COLUMN IF EXISTS text_search_vector;

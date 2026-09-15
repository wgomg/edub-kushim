-- +goose Up
CREATE TABLE deleted_tag (
    name        text PRIMARY KEY,
    kind        text NOT NULL CHECK (kind IN ('blocked', 'merged')),
    target_name text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE deleted_tag;

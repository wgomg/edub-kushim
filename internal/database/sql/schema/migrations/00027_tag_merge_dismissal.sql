-- +goose Up
-- "C" collation: the service canonicalizes pairs with Go byte order, which
-- diverges from the database's default collation for some letters (e.g. ß, ø).
CREATE TABLE tag_merge_dismissal (
    tag_a      text NOT NULL,
    tag_b      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tag_a, tag_b),
    CHECK (tag_a COLLATE "C" < tag_b COLLATE "C")
);

-- +goose Down
DROP TABLE tag_merge_dismissal;

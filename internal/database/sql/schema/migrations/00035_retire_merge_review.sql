-- +goose Up
-- the similarity-based merge-review is retired: nothing writes the merged
-- kind anymore, so its rows and column go and the blocklist is blocked-only
DROP TABLE IF EXISTS tag_merge_dismissal;

DELETE FROM deleted_tag WHERE kind = 'merged';

ALTER TABLE deleted_tag DROP CONSTRAINT deleted_tag_kind_check;
ALTER TABLE deleted_tag DROP COLUMN target_name;
ALTER TABLE deleted_tag ADD CONSTRAINT deleted_tag_kind_check CHECK (kind = 'blocked');

-- +goose Down
ALTER TABLE deleted_tag DROP CONSTRAINT deleted_tag_kind_check;
ALTER TABLE deleted_tag ADD COLUMN target_name text;
ALTER TABLE deleted_tag ADD CONSTRAINT deleted_tag_kind_check CHECK (kind IN ('blocked', 'merged'));

CREATE TABLE tag_merge_dismissal (
    tag_a      text NOT NULL,
    tag_b      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tag_a, tag_b),
    CHECK (tag_a COLLATE "C" < tag_b COLLATE "C")
);

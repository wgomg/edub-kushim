-- +goose Up
CREATE INDEX idx_tag_orphaned_since ON tag(orphaned_since) WHERE orphaned_since IS NOT NULL;

-- +goose Down
DROP INDEX idx_tag_orphaned_since;

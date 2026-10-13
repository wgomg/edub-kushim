-- +goose Up
CREATE INDEX IF NOT EXISTS idx_tag_measurement_snapshot ON tag_measurement (snapshot_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_tag_measurement_snapshot;

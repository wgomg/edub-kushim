-- +goose Up
CREATE TABLE IF NOT EXISTS batch (
    id         TEXT PRIMARY KEY,
    source     TEXT NOT NULL DEFAULT 'unknown',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO batch (id, source)
SELECT DISTINCT batch_id, 'unknown' FROM task WHERE batch_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS batch_owner (
    batch_id       TEXT PRIMARY KEY REFERENCES batch(id),
    owner_id       TEXT NOT NULL,
    pid            INTEGER NOT NULL,
    acquired_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_batch_owner_owner ON batch_owner(owner_id);
CREATE INDEX IF NOT EXISTS idx_batch_owner_heartbeat ON batch_owner(last_heartbeat);

-- One-time: after upgrade all processes restart, so every 'processing' task is an
-- orphan. Reset to 'pending' so manual adoption (CLI or WebUI) can resume them.
-- Preserve started_at to retain timing history.
UPDATE task SET status = 'pending' WHERE status = 'processing';

-- +goose Down
DROP TABLE IF EXISTS batch_owner;
DROP TABLE IF EXISTS batch;

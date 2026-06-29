-- +goose Up
CREATE TABLE IF NOT EXISTS orphaned_file (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    document_key TEXT NOT NULL,
    document_key_type TEXT NOT NULL CHECK (document_key_type IN ('uuid', 'dbid')),
    file_path TEXT NOT NULL,
    original_path TEXT NOT NULL,
    source_dir TEXT NOT NULL CHECK (source_dir IN ('originals', 'processed')),
    file_size INTEGER NOT NULL DEFAULT 0,
    mime_type TEXT NOT NULL DEFAULT 'application/pdf',
    detected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'deleted', 'restored', 'reingested')),
    action_at DATETIME,
    action_type TEXT CHECK (action_type IN ('delete', 'restore', 'move_to_inbox'))
);

CREATE INDEX IF NOT EXISTS idx_orphaned_status ON orphaned_file(status);
CREATE INDEX IF NOT EXISTS idx_orphaned_detected ON orphaned_file(detected_at);

-- +goose Down
DROP TABLE IF EXISTS orphaned_file;

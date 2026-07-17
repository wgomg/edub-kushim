-- +goose Up
CREATE TABLE backup_lock (
    id          int PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    running     boolean NOT NULL DEFAULT false,
    started_at  timestamptz
);

INSERT INTO backup_lock (id, running) VALUES (1, false);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION is_backup_running() RETURNS boolean AS $$
BEGIN
  RETURN EXISTS (
    SELECT 1 FROM backup_lock
    WHERE id = 1 AND running = true AND started_at > NOW() - INTERVAL '30 minutes'
  );
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS is_backup_running();
DROP TABLE IF EXISTS backup_lock;

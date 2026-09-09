-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION is_maintenance_lock_held() RETURNS boolean AS $$
BEGIN
  RETURN EXISTS (
    SELECT 1 FROM backup_lock
    WHERE id = 1 AND running = true AND started_at > NOW() - INTERVAL '30 minutes'
  );
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP FUNCTION IF EXISTS is_backup_running();

-- +goose Down
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

DROP FUNCTION IF EXISTS is_maintenance_lock_held();

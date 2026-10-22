-- +goose Up

-- carry the held row into the renamed lock and seed the storage lock; the staleness window moves into
-- lock_is_held, which replaces is_maintenance_lock_held()

UPDATE system_signal SET name = 'maintenance', modified_at = CURRENT_TIMESTAMP
WHERE name = 'maintenance_lock';

INSERT INTO system_signal (name, type, value) VALUES ('storage', 'bool', 'false');

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_is_held(lock_name text) RETURNS boolean AS $$
BEGIN
  RETURN EXISTS (
    SELECT 1 FROM system_signal
    WHERE name = lock_name
      AND value = 'true'
      AND (data->>'started_at')::timestamptz > NOW() - INTERVAL '30 minutes'
  );
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP FUNCTION IF EXISTS is_maintenance_lock_held();

-- +goose Down
DELETE FROM system_signal WHERE name = 'storage';

UPDATE system_signal SET name = 'maintenance_lock', modified_at = CURRENT_TIMESTAMP
WHERE name = 'maintenance';

DROP FUNCTION IF EXISTS lock_is_held(text);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION is_maintenance_lock_held() RETURNS boolean AS $$
BEGIN
  RETURN EXISTS (
    SELECT 1 FROM system_signal
    WHERE name = 'maintenance_lock'
      AND value = 'true'
      AND (data->>'started_at')::timestamptz > NOW() - INTERVAL '30 minutes'
  );
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

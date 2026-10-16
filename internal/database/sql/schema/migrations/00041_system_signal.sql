-- +goose Up
CREATE TABLE system_signal (
    id          smallint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text NOT NULL UNIQUE,
    type        text NOT NULL,
    value       text NOT NULL,
    data        jsonb,
    modified_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO system_signal (name, type, value)
SELECT 'tag_audit_sweep', 'int', sweep_count::text
FROM tag_audit_sweep
WHERE id = 1;

-- carry a held lock so a holder running while this migration applies keeps its state
INSERT INTO system_signal (name, type, value, data)
SELECT 'maintenance_lock', 'bool', running::text,
       jsonb_build_object('started_at', started_at, 'owner_token', owner_token)
FROM backup_lock
WHERE id = 1;

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

DROP TABLE tag_audit_sweep;
DROP TABLE backup_lock;

-- +goose Down
CREATE TABLE backup_lock (
    id          int PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    running     boolean NOT NULL DEFAULT false,
    started_at  timestamptz,
    owner_token uuid
);

INSERT INTO backup_lock (id, running, started_at, owner_token)
SELECT 1,
       system_signal.value = 'true',
       (system_signal.data->>'started_at')::timestamptz,
       (system_signal.data->>'owner_token')::uuid
FROM system_signal
WHERE system_signal.name = 'maintenance_lock';

CREATE TABLE tag_audit_sweep (
    id          smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    sweep_count bigint NOT NULL DEFAULT 0
);

INSERT INTO tag_audit_sweep (id, sweep_count)
SELECT 1, value::bigint
FROM system_signal
WHERE name = 'tag_audit_sweep';

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

DROP TABLE system_signal;

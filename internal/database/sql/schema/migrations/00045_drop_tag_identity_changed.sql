-- +goose Up
-- the identity pass clears its flag at open, but nothing reads it: its work set is the pending-event
-- count, already the pass's first read, and a signal gate would strand budget-remainder events in a
-- quiet store, so the flag can have no consumer
DELETE FROM system_signal WHERE name = 'tag_identity_changed';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tag_store_changed() RETURNS trigger AS $$
BEGIN
    INSERT INTO system_signal (name, type, value)
    SELECT s.name, 'bool', 'true'
    FROM unnest(ARRAY['tag_measure_changed', 'tag_reconcile_changed',
                      'tag_audit_changed']) AS s(name)
    ON CONFLICT (name) DO UPDATE SET value = 'true', modified_at = CURRENT_TIMESTAMP;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tag_store_changed() RETURNS trigger AS $$
BEGIN
    INSERT INTO system_signal (name, type, value)
    SELECT s.name, 'bool', 'true'
    FROM unnest(ARRAY['tag_measure_changed', 'tag_reconcile_changed',
                      'tag_identity_changed', 'tag_audit_changed']) AS s(name)
    ON CONFLICT (name) DO UPDATE SET value = 'true', modified_at = CURRENT_TIMESTAMP;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

INSERT INTO system_signal (name, type, value) VALUES ('tag_identity_changed', 'bool', 'true');

-- +goose Up
-- timestamp coverage for rows whose change carried no stamp: document_tag and document_people never
-- had one, and a rename overwrote tag.name in place, leaving no trace (promotion and demotion keep
-- their dedicated stamps and bump modified_at too); existing rows take the migration instant
ALTER TABLE document_tag ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE document_people ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE tag ADD COLUMN modified_at timestamptz NOT NULL DEFAULT now();

-- the store-changed record: one flag per tag action, set by every vocabulary mutation and cleared by
-- that action's run as it opens; seeded at 'true' so the clear path is a plain UPDATE
INSERT INTO system_signal (name, type, value) VALUES
    ('tag_measure_changed', 'bool', 'true'),
    ('tag_reconcile_changed', 'bool', 'true'),
    ('tag_identity_changed', 'bool', 'true'),
    ('tag_audit_changed', 'bool', 'true');

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

-- the semantic-column guards keep the seeder's no-op rewrites and the orphan-mark bookkeeping silent
-- while real mutations fire; the INSERT/DELETE arms carry no guard because a WHEN on a trigger that
-- can fire on INSERT cannot reference OLD; deleted_tag needs none (both its write shapes change the row)
CREATE TRIGGER tag_store_changed_tag_insert_delete
AFTER INSERT OR DELETE ON tag
FOR EACH ROW
EXECUTE FUNCTION tag_store_changed();

CREATE TRIGGER tag_store_changed_tag_update
AFTER UPDATE ON tag
FOR EACH ROW
WHEN ((OLD.name, OLD.promoted_at, OLD.demoted_at, OLD.demoted_reason)
      IS DISTINCT FROM
      (NEW.name, NEW.promoted_at, NEW.demoted_at, NEW.demoted_reason))
EXECUTE FUNCTION tag_store_changed();

CREATE TRIGGER tag_store_changed_document_tag_insert_delete
AFTER INSERT OR DELETE ON document_tag
FOR EACH ROW
EXECUTE FUNCTION tag_store_changed();

CREATE TRIGGER tag_store_changed_document_tag_update
AFTER UPDATE ON document_tag
FOR EACH ROW
WHEN ((OLD.tag_id, OLD.source) IS DISTINCT FROM (NEW.tag_id, NEW.source))
EXECUTE FUNCTION tag_store_changed();

CREATE TRIGGER tag_store_changed_tag_alias_insert_delete
AFTER INSERT OR DELETE ON tag_alias
FOR EACH ROW
EXECUTE FUNCTION tag_store_changed();

CREATE TRIGGER tag_store_changed_tag_alias_update
AFTER UPDATE ON tag_alias
FOR EACH ROW
WHEN ((OLD.alias_name, OLD.canonical_tag_id)
      IS DISTINCT FROM (NEW.alias_name, NEW.canonical_tag_id))
EXECUTE FUNCTION tag_store_changed();

CREATE TRIGGER tag_store_changed_deleted_tag
AFTER INSERT OR UPDATE OR DELETE ON deleted_tag
FOR EACH ROW
EXECUTE FUNCTION tag_store_changed();

-- +goose Down
DROP TRIGGER tag_store_changed_deleted_tag ON deleted_tag;
DROP TRIGGER tag_store_changed_tag_alias_update ON tag_alias;
DROP TRIGGER tag_store_changed_tag_alias_insert_delete ON tag_alias;
DROP TRIGGER tag_store_changed_document_tag_update ON document_tag;
DROP TRIGGER tag_store_changed_document_tag_insert_delete ON document_tag;
DROP TRIGGER tag_store_changed_tag_update ON tag;
DROP TRIGGER tag_store_changed_tag_insert_delete ON tag;
DROP FUNCTION tag_store_changed();
DELETE FROM system_signal WHERE name IN ('tag_measure_changed', 'tag_reconcile_changed',
                                        'tag_identity_changed', 'tag_audit_changed');
ALTER TABLE tag DROP COLUMN modified_at;
ALTER TABLE document_people DROP COLUMN created_at;
ALTER TABLE document_tag DROP COLUMN created_at;

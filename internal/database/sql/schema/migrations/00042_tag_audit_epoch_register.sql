-- +goose Up
ALTER TABLE tag_audit_register RENAME COLUMN audit_count TO last_audited_sweep;
-- every registered document has been audited at least once, so the current sweep is the honest stamp;
-- the ledger's older counts are not comparable to sweep numbers
UPDATE tag_audit_register
SET last_audited_sweep = (SELECT value::bigint FROM system_signal WHERE name = 'tag_audit_sweep');

-- +goose Down
ALTER TABLE tag_audit_register RENAME COLUMN last_audited_sweep TO audit_count;

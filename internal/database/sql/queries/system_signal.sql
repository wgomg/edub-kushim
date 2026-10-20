-- The store-changed flags, read and cleared by the measurement's, reconcile's, and audit's opening gates;
-- the rows are seeded by migration 00044, so the queries are UPDATE-only, like the sweep counter's
-- name: GetTagStoreSignal :one
SELECT value FROM system_signal WHERE name = $1;

-- name: SetTagStoreSignal :exec
UPDATE system_signal
SET value = @value::text, modified_at = CURRENT_TIMESTAMP
WHERE name = @name::text;

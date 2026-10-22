-- +goose Up
-- Re-key migration tasks queued under the old single config type. New values of an enum cannot be used
-- in the transaction that adds them, so this mapping lives in its own migration after 00047.
UPDATE task
SET task_type = (payload->>'op')::task_type,
    dedup_key = payload->>'op'
WHERE task_type = 'config'
  AND status IN ('pending', 'failed')
  AND payload->>'op' IN ('migrate-db', 'migrate-storage');

-- +goose Down
-- Restore the config task shape the pre-00047 code dispatches on: type config, the migration op in the
-- payload, and the old dedup key vocabulary. Runs before 00047's Down, which deletes any remaining migrate
-- task rows.
UPDATE task
SET payload = jsonb_set(COALESCE(payload, '{}'::jsonb), '{op}', to_jsonb(task_type::text)),
    dedup_key = 'config:' || task_type::text,
    task_type = 'config'
WHERE task_type IN ('migrate-db', 'migrate-storage');

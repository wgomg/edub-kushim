-- name: IsLockHeld :one
SELECT CASE WHEN lock_is_held(@lock_name::text) THEN 1 ELSE 0 END;

-- name: LockRowsForUpdate :many
SELECT name FROM system_signal
WHERE name = ANY(@lock_names::text[])
ORDER BY name
FOR UPDATE;

-- name: CountHeldLocks :one
SELECT COUNT(*) FROM system_signal
WHERE name = ANY(@lock_names::text[])
  AND lock_is_held(name);

-- a lock row carries owner_token only for token holders: manual CLI holders acquire with no token, and
-- release and touch match its absence, since IS NOT DISTINCT FROM treats the missing key as NULL; every
-- acquisition records its batch in batch_id, and the release queries return the released names so the caller
-- can notify the waiters in the same transaction

-- name: AcquireLocks :execrows
UPDATE system_signal
SET value = 'true',
    data = jsonb_build_object(
        'started_at', to_jsonb(NOW()),
        'owner_token', sqlc.narg(owner_token)::uuid::text,
        'batch_id', sqlc.narg(batch_id)::text
    ),
    modified_at = CURRENT_TIMESTAMP
WHERE name = ANY(@lock_names::text[])
  AND (value = 'false' OR (data->>'started_at')::timestamptz <= NOW() - INTERVAL '30 minutes');

-- name: ReleaseLocksForToken :many
UPDATE system_signal
SET value = 'false', data = NULL, modified_at = CURRENT_TIMESTAMP
WHERE name = ANY(@lock_names::text[])
  AND value = 'true'
  AND (data->>'owner_token') IS NOT DISTINCT FROM sqlc.narg(owner_token)::uuid::text
RETURNING name;

-- name: ReleaseLocksForBatch :many
UPDATE system_signal
SET value = 'false', data = NULL, modified_at = CURRENT_TIMESTAMP
WHERE name = ANY(@lock_names::text[])
  AND value = 'true'
  AND data->>'batch_id' = @batch_id::text
RETURNING name;

-- name: ReleaseLocksForStaleHolders :many
UPDATE system_signal
SET value = 'false', data = NULL, modified_at = CURRENT_TIMESTAMP
WHERE name = ANY(@lock_names::text[])
  AND value = 'true'
  AND (data->>'started_at')::timestamptz <= NOW() - INTERVAL '30 minutes'
  AND (data->>'owner_token') IN (
    SELECT claim_token::text FROM task
    WHERE status = 'processing'
      AND task_type = ANY(@types::task_type[])
  )
RETURNING name;

-- name: TouchLocksForToken :execrows
UPDATE system_signal
SET data = jsonb_set(data, '{started_at}', to_jsonb(NOW())),
    modified_at = CURRENT_TIMESTAMP
WHERE name = ANY(@lock_names::text[])
  AND value = 'true'
  AND (data->>'owner_token') IS NOT DISTINCT FROM sqlc.narg(owner_token)::uuid::text;

-- name: NotifyLocksReleased :exec
SELECT pg_notify('lock_released', released.name)
FROM unnest(@lock_names::text[]) AS released(name);

-- name: QuarantineStaleBlockingTasks :execrows
UPDATE task SET
    status = 'failed',
    error = 'Max retries exceeded (' || attempts || ')',
    completed_at = CURRENT_TIMESTAMP
WHERE task.status = 'processing'
  AND task.task_type = ANY(@types::task_type[])
  AND task.attempts >= @attempts
  AND task.started_at < @started_at
  AND NOT EXISTS (
    SELECT 1 FROM system_signal s
    WHERE s.name = ANY(@lock_names::text[])
      AND s.value = 'true'
      AND (s.data->>'started_at')::timestamptz > NOW() - INTERVAL '30 minutes'
      AND ((s.data->>'owner_token') IS NOT DISTINCT FROM task.claim_token::text
           OR s.data->>'batch_id' = task.batch_id)
  );

-- name: ResetStaleBlockingTasks :execrows
UPDATE task SET
    status = 'pending',
    attempts = attempts + 1,
    progress = NULL
WHERE task.status = 'processing'
  AND task.task_type = ANY(@types::task_type[])
  AND task.attempts < @attempts
  AND task.started_at < @started_at
  AND NOT EXISTS (
    SELECT 1 FROM system_signal s
    WHERE s.name = ANY(@lock_names::text[])
      AND s.value = 'true'
      AND (s.data->>'started_at')::timestamptz > NOW() - INTERVAL '30 minutes'
      AND ((s.data->>'owner_token') IS NOT DISTINCT FROM task.claim_token::text
           OR s.data->>'batch_id' = task.batch_id)
  );

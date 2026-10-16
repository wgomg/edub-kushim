-- name: IsMaintenanceLocked :one
SELECT CASE WHEN is_maintenance_lock_held() THEN 1 ELSE 0 END;

-- name: AcquireBackupLock :execrows
UPDATE system_signal
SET value = 'true',
    data = jsonb_build_object('started_at', to_jsonb(NOW())),
    modified_at = CURRENT_TIMESTAMP
WHERE name = 'maintenance_lock'
  AND (value = 'false' OR (data->>'started_at')::timestamptz <= NOW() - INTERVAL '30 minutes');

-- the lock carries an owner_token only for task holders; the backup paths acquire without one, and the
-- release and touch match that absence, since IS NOT DISTINCT FROM treats the missing key as NULL

-- name: ReleaseBackupLock :execrows
UPDATE system_signal
SET value = 'false', data = NULL, modified_at = CURRENT_TIMESTAMP
WHERE name = 'maintenance_lock'
  AND value = 'true'
  AND (data->>'owner_token') IS NOT DISTINCT FROM sqlc.narg(owner_token)::uuid::text;

-- name: AcquireMaintenanceLockForTask :execrows
UPDATE system_signal
SET value = 'true',
    data = jsonb_build_object('started_at', to_jsonb(NOW()), 'owner_token', sqlc.narg(owner_token)::uuid::text),
    modified_at = CURRENT_TIMESTAMP
WHERE name = 'maintenance_lock'
  AND (value = 'false' OR (data->>'started_at')::timestamptz <= NOW() - INTERVAL '30 minutes');

-- name: ReleaseMaintenanceLockForTask :execrows
UPDATE system_signal
SET value = 'false', data = NULL, modified_at = CURRENT_TIMESTAMP
WHERE name = 'maintenance_lock'
  AND value = 'true'
  AND (data->>'owner_token') IS NOT DISTINCT FROM sqlc.narg(owner_token)::uuid::text;

-- name: TouchMaintenanceLockForTask :execrows
UPDATE system_signal
SET data = jsonb_set(data, '{started_at}', to_jsonb(NOW())),
    modified_at = CURRENT_TIMESTAMP
WHERE name = 'maintenance_lock'
  AND value = 'true'
  AND (data->>'owner_token') IS NOT DISTINCT FROM sqlc.narg(owner_token)::uuid::text;

-- name: ReleaseMaintenanceLockForStaleHolders :execrows
UPDATE system_signal
SET value = 'false', data = NULL, modified_at = CURRENT_TIMESTAMP
WHERE name = 'maintenance_lock'
  AND value = 'true'
  AND (data->>'started_at')::timestamptz <= NOW() - INTERVAL '30 minutes'
  AND (data->>'owner_token') IN (
    SELECT claim_token::text FROM task
    WHERE status = 'processing'
      AND task_type = ANY(@types::task_type[])
  );

-- name: ReleaseMaintenanceLockForBatch :execrows
UPDATE system_signal
SET value = 'false', data = NULL, modified_at = CURRENT_TIMESTAMP
WHERE name = 'maintenance_lock'
  AND value = 'true'
  AND (data->>'owner_token') IN (
    SELECT claim_token::text FROM task
    WHERE batch_id = $1 AND status = 'processing'
      AND task_type = ANY(@types::task_type[])
  );

-- name: QuarantineStaleBlockingTasks :execrows
UPDATE task SET
    status = 'failed',
    error = 'Max retries exceeded (' || attempts || ')',
    completed_at = CURRENT_TIMESTAMP
FROM system_signal
WHERE system_signal.name = 'maintenance_lock'
  AND task.status = 'processing'
  AND task.task_type = ANY(@types::task_type[])
  AND task.attempts >= $1
  AND task.started_at < $2
  AND (
    (system_signal.data->>'owner_token') IS DISTINCT FROM task.claim_token::text
    OR (system_signal.data->>'started_at')::timestamptz <= NOW() - INTERVAL '30 minutes'
  );

-- name: ResetStaleBlockingTasks :execrows
UPDATE task SET
    status = 'pending',
    attempts = attempts + 1,
    progress = NULL
FROM system_signal
WHERE system_signal.name = 'maintenance_lock'
  AND task.status = 'processing'
  AND task.task_type = ANY(@types::task_type[])
  AND task.attempts < $1
  AND task.started_at < $2
  AND (
    (system_signal.data->>'owner_token') IS DISTINCT FROM task.claim_token::text
    OR (system_signal.data->>'started_at')::timestamptz <= NOW() - INTERVAL '30 minutes'
  );

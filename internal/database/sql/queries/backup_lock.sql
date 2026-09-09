-- name: IsBackupLocked :one
SELECT CASE WHEN is_maintenance_lock_held() THEN 1 ELSE 0 END;

-- name: AcquireBackupLock :execrows
UPDATE backup_lock SET running = true, started_at = NOW(), owner_token = NULL
WHERE id = 1 AND (NOT running OR started_at <= NOW() - INTERVAL '30 minutes');

-- name: ReleaseBackupLock :execrows
UPDATE backup_lock SET running = false, started_at = NULL, owner_token = NULL
WHERE id = 1 AND running = true AND owner_token IS NOT DISTINCT FROM $1;

-- name: AcquireMaintenanceLockForTask :execrows
UPDATE backup_lock SET running = true, started_at = NOW(), owner_token = $1
WHERE id = 1 AND (NOT running OR started_at <= NOW() - INTERVAL '30 minutes');

-- name: ReleaseMaintenanceLockForTask :execrows
UPDATE backup_lock SET running = false, started_at = NULL, owner_token = NULL
WHERE id = 1 AND running = true AND owner_token IS NOT DISTINCT FROM $1;

-- name: TouchMaintenanceLockForTask :execrows
UPDATE backup_lock SET started_at = NOW()
WHERE id = 1 AND running = true AND owner_token IS NOT DISTINCT FROM $1;
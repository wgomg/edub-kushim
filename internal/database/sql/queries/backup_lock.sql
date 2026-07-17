-- name: IsBackupLocked :one
SELECT CASE WHEN is_backup_running() THEN 1 ELSE 0 END;

-- name: AcquireBackupLock :execrows
UPDATE backup_lock SET running = true, started_at = NOW()
WHERE id = 1 AND (NOT running OR started_at <= NOW() - INTERVAL '30 minutes');

-- name: ReleaseBackupLock :execrows
UPDATE backup_lock SET running = false, started_at = NULL WHERE id = 1 AND running = true;

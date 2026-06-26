-- name: CreateBatch :execresult
INSERT OR IGNORE INTO batch (id, source) VALUES (?, ?);

-- name: GetBatchOwner :one
SELECT batch_id, owner_id, pid, acquired_at, last_heartbeat FROM batch_owner WHERE batch_id = ?;

-- Two-step acquire: try INSERT first, then UPDATE with stale check.
-- The raw ON CONFLICT ... WHERE pattern has ? count issues with sqlc's parser.

-- name: TryInsertBatchOwner :execrows
INSERT INTO batch_owner (batch_id, owner_id, pid, acquired_at, last_heartbeat)
VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(batch_id) DO NOTHING;

-- name: UpdateBatchOwnerIfStale :execrows
UPDATE batch_owner SET
  owner_id = ?,
  pid = ?,
  acquired_at = CURRENT_TIMESTAMP,
  last_heartbeat = CURRENT_TIMESTAMP
WHERE batch_id = ? AND (last_heartbeat < ? OR owner_id = ?);

-- name: AcquireBatchOwnerForce :execrows
INSERT INTO batch_owner (batch_id, owner_id, pid, acquired_at, last_heartbeat)
VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(batch_id) DO UPDATE SET
  owner_id = excluded.owner_id,
  pid = excluded.pid,
  acquired_at = CURRENT_TIMESTAMP,
  last_heartbeat = CURRENT_TIMESTAMP;

-- name: HeartbeatBatchOwner :execrows
UPDATE batch_owner SET last_heartbeat = CURRENT_TIMESTAMP WHERE owner_id = ?;

-- name: ReleaseBatchOwner :execrows
DELETE FROM batch_owner WHERE batch_id = ? AND owner_id = ?;

-- name: ResetProcessingTasksByBatch :execrows
UPDATE task SET status = 'pending'
WHERE batch_id = ? AND status = 'processing';

-- name: CleanupCompletedBatches :execrows
DELETE FROM batch_owner WHERE batch_id IN (
  SELECT b.batch_id FROM batch_owner b
  WHERE NOT EXISTS (
    SELECT 1 FROM task t WHERE t.batch_id = b.batch_id
      AND t.status IN ('pending', 'processing', 'waiting')
  )
);

-- name: GetNextPendingTaskOfTypeForOwner :one
SELECT id FROM task
WHERE status = 'pending' AND task_type = ?
  AND batch_id IN (SELECT batch_id FROM batch_owner WHERE owner_id = ?)
ORDER BY created_at LIMIT 1;

-- name: ListBatchOverviews :many
SELECT
    b.id AS batch_id,
    b.source,
    b.created_at AS batch_created_at,
    COUNT(t.id) AS total,
    COALESCE(SUM(CASE WHEN t.status = 'waiting'   THEN 1 ELSE 0 END), 0) AS waiting,
    COALESCE(SUM(CASE WHEN t.status = 'pending'   THEN 1 ELSE 0 END), 0) AS pending,
    COALESCE(SUM(CASE WHEN t.status = 'processing' THEN 1 ELSE 0 END), 0) AS processing,
    COALESCE(SUM(CASE WHEN t.status = 'completed' THEN 1 ELSE 0 END), 0) AS completed,
    COALESCE(SUM(CASE WHEN t.status = 'failed'    THEN 1 ELSE 0 END), 0) AS failed,
    COALESCE(SUM(CASE WHEN t.status = 'cancelled' THEN 1 ELSE 0 END), 0) AS cancelled,
    COALESCE(SUM(CASE WHEN t.status = 'discarded' THEN 1 ELSE 0 END), 0) AS discarded,
    MIN(t.started_at) AS first_started_at,
    MAX(t.completed_at) AS last_completed_at,
    bo.last_heartbeat AS owner_last_heartbeat,
    bo.pid AS owner_pid
FROM batch b
LEFT JOIN task t ON t.batch_id = b.id
LEFT JOIN batch_owner bo ON bo.batch_id = b.id
GROUP BY b.id
ORDER BY b.created_at DESC
LIMIT ? OFFSET ?;

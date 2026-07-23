-- name: CreateBatch :exec
INSERT INTO batch (id, source, status) VALUES ($1, $2, $3)
ON CONFLICT (id) DO NOTHING;

-- name: GetBatchOwner :one
SELECT batch_id, owner_id, pid, acquired_at, last_heartbeat FROM batch_owner WHERE batch_id = $1;

-- Two-step acquire: try INSERT first, then UPDATE with stale check.

-- name: TryInsertBatchOwner :execrows
INSERT INTO batch_owner (batch_id, owner_id, pid, acquired_at, last_heartbeat)
VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(batch_id) DO NOTHING;

-- name: UpdateBatchOwnerIfStale :execrows
UPDATE batch_owner SET
  owner_id = $1,
  pid = $2,
  acquired_at = CURRENT_TIMESTAMP,
  last_heartbeat = CURRENT_TIMESTAMP
WHERE batch_id = $3 AND (last_heartbeat < $4 OR owner_id = $5);

-- name: AcquireBatchOwnerForce :execrows
INSERT INTO batch_owner (batch_id, owner_id, pid, acquired_at, last_heartbeat)
VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(batch_id) DO UPDATE SET
  owner_id = excluded.owner_id,
  pid = excluded.pid,
  acquired_at = CURRENT_TIMESTAMP,
  last_heartbeat = CURRENT_TIMESTAMP;

-- name: HeartbeatBatchOwner :execrows
UPDATE batch_owner SET last_heartbeat = CURRENT_TIMESTAMP WHERE owner_id = $1;

-- name: ReleaseBatchOwner :execrows
DELETE FROM batch_owner WHERE batch_id = $1 AND owner_id = $2;

-- name: DeleteBatchOwnerByBatchID :execrows
DELETE FROM batch_owner WHERE batch_id = $1;

-- name: QuarantineProcessingTasksByBatch :execrows
UPDATE task SET
    status = 'failed',
    error = 'Max retries exceeded (' || attempts || ')',
    completed_at = CURRENT_TIMESTAMP
WHERE batch_id = $1 AND status = 'processing' AND attempts >= $2;

-- name: ResetProcessingTasksByBatch :execrows
UPDATE task SET
    status = 'pending',
    attempts = attempts + 1
WHERE batch_id = $1 AND status = 'processing' AND attempts < $2;

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
WHERE status = 'pending' AND task_type = $1
  AND batch_id IN (SELECT batch_id FROM batch_owner WHERE owner_id = $2)
ORDER BY created_at LIMIT 1;

-- name: GetNextPendingTaskOfTypeForOwnerWithGate :one
SELECT id FROM task
WHERE status = 'pending' AND task_type = $1
  AND batch_id IN (SELECT batch_id FROM batch_owner WHERE owner_id = $2)
  AND NOT is_backup_running()
ORDER BY created_at LIMIT 1;

-- name: GetBatch :one
SELECT * FROM batch WHERE id = $1;

-- name: CountQueuedBatches :one
SELECT COUNT(*) FROM batch WHERE status = 'queued';

-- name: GetNextQueuedBatch :one
SELECT * FROM batch WHERE status = 'queued' ORDER BY created_at LIMIT 1;

-- name: RequeueBatch :exec
UPDATE batch SET status = 'queued' WHERE id = $1;

-- name: SetBatchProcessing :exec
UPDATE batch SET status = 'processing' WHERE id = $1;

-- name: SetBatchCompleted :exec
UPDATE batch SET status = 'completed' WHERE id = $1;

-- name: SetBatchFailed :exec
UPDATE batch SET status = 'failed' WHERE id = $1;

-- name: SetBatchPaused :exec
UPDATE batch SET status = 'paused' WHERE id = $1;

-- name: SetBatchCancelled :exec
UPDATE batch SET status = 'cancelled' WHERE id = $1;

-- name: CountLiveBatches :one
SELECT COUNT(*) FROM batch_owner
WHERE last_heartbeat > CURRENT_TIMESTAMP - INTERVAL '15 seconds';

-- name: ListStaleBatchOwners :many
SELECT bo.batch_id, bo.owner_id, bo.pid FROM batch_owner bo
WHERE bo.last_heartbeat < CURRENT_TIMESTAMP - INTERVAL '15 seconds'
AND EXISTS (SELECT 1 FROM task t
            WHERE t.batch_id = bo.batch_id
            AND t.status IN ('pending', 'processing', 'waiting'));

-- name: GetQuarantinedConsumeTaskPayloads :many
SELECT task_id, payload FROM task
WHERE batch_id = $1 AND status = 'failed' AND error LIKE 'Max retries exceeded%';

-- name: ListBatchOverviews :many
SELECT
    b.id AS batch_id,
    b.source,
    b.created_at AS batch_created_at,
    b.status AS batch_status,
    COALESCE(sub.total, 0) AS total,
    COALESCE(sub.waiting, 0) AS waiting,
    COALESCE(sub.pending, 0) AS pending,
    COALESCE(sub.processing, 0) AS processing,
    COALESCE(sub.completed, 0) AS completed,
    COALESCE(sub.failed, 0) AS failed,
    COALESCE(sub.cancelled, 0) AS cancelled,
    COALESCE(sub.discarded, 0) AS discarded,
    sub.first_started_at AS first_started_at,
    sub.last_completed_at AS last_completed_at,
    bo.last_heartbeat AS owner_last_heartbeat,
    bo.pid AS owner_pid
FROM batch b
LEFT JOIN LATERAL (
    SELECT
        COUNT(*) AS total,
        SUM(CASE WHEN t.status = 'waiting'   THEN 1 ELSE 0 END) AS waiting,
        SUM(CASE WHEN t.status = 'pending'   THEN 1 ELSE 0 END) AS pending,
        SUM(CASE WHEN t.status = 'processing' THEN 1 ELSE 0 END) AS processing,
        SUM(CASE WHEN t.status = 'completed' THEN 1 ELSE 0 END) AS completed,
        SUM(CASE WHEN t.status = 'failed'    THEN 1 ELSE 0 END) AS failed,
        SUM(CASE WHEN t.status = 'cancelled' THEN 1 ELSE 0 END) AS cancelled,
        SUM(CASE WHEN t.status = 'discarded' THEN 1 ELSE 0 END) AS discarded,
        MIN(t.started_at) AS first_started_at,
        MAX(t.completed_at) AS last_completed_at
    FROM task t WHERE t.batch_id = b.id
) sub ON true
LEFT JOIN batch_owner bo ON bo.batch_id = b.id
ORDER BY b.created_at DESC
LIMIT $1 OFFSET $2;

-- name: GetTask :one
SELECT * FROM task WHERE id = $1;

-- name: GetTaskByTaskID :one
SELECT * FROM task WHERE task_id = $1;

-- name: GetTaskByBatchID :many
SELECT * FROM task WHERE batch_id = $1 ORDER BY created_at;

-- name: GetNextPendingTask :one
SELECT id FROM task WHERE status = 'pending' ORDER BY created_at LIMIT 1;

-- name: GetNextPendingTaskOfType :one
SELECT id FROM task
WHERE status = 'pending' AND task_type = $1
ORDER BY created_at LIMIT 1;

-- name: GetNextPendingTaskOfTypeWithGate :one
SELECT id FROM task
WHERE status = 'pending' AND task_type = $1
  AND NOT is_backup_running()
ORDER BY created_at LIMIT 1;

-- name: ListTasks :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListTasksByStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListTasksByBatch :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE batch_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListTasksByBatchAndStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE batch_id = $1 AND status = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: ListAllTasks :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task ORDER BY created_at DESC;

-- name: ListAllTasksByStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE status = $1 ORDER BY created_at DESC;

-- name: ListAllTasksByBatch :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE batch_id = $1 ORDER BY created_at DESC;

-- name: ListAllTasksByBatchAndStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE batch_id = $1 AND status = $2 ORDER BY created_at DESC;

-- name: ListTasksByBatchAndStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE batch_id = $1 AND status = $2 AND task_type = $3 ORDER BY created_at DESC LIMIT $4 OFFSET $5;

-- name: ListAllTasksByBatchAndStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE batch_id = $1 AND status = $2 AND task_type = $3 ORDER BY created_at DESC;

-- name: ListTasksByBatchAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE batch_id = $1 AND task_type = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: ListAllTasksByBatchAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE batch_id = $1 AND task_type = $2 ORDER BY created_at DESC;

-- name: ListTasksByStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE status = $1 AND task_type = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: ListAllTasksByStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE status = $1 AND task_type = $2 ORDER BY created_at DESC;

-- name: ListTasksByType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE task_type = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAllTasksByType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE task_type = $1 ORDER BY created_at DESC;

-- name: CountProcessingTasks :one
SELECT COUNT(*) FROM task WHERE status = 'processing' AND task_type IN ('consume', 'enrich');

-- name: CountTasksByBatchAndStatus :one
SELECT COUNT(*) FROM task WHERE batch_id = $1 AND status = $2;

-- name: CreateTask :one
INSERT INTO task (
    task_id, task_type, status, batch_id, payload, dedup_key
) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id;

-- name: ClaimTask :execrows
UPDATE task SET
    status = 'processing',
    started_at = CURRENT_TIMESTAMP
WHERE id = $1 AND status = 'pending';

-- name: CompleteTask :execrows
UPDATE task SET
    status = 'completed',
    result = $1,
    completed_at = CURRENT_TIMESTAMP,
    attempts = 0
WHERE id = $2 AND status = 'processing';

-- name: FailTask :exec
UPDATE task SET
    status = 'failed',
    completed_at = CURRENT_TIMESTAMP,
    error = $1
WHERE id = $2;

-- name: RetryFailedTasksByBatch :execrows
UPDATE task SET
    status = 'pending',
    result = NULL,
    error = NULL,
    started_at = NULL,
    completed_at = NULL,
    attempts = 0
WHERE batch_id = $1 AND status = 'failed';

-- name: GetConfigTaskByDedupKey :one
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts
FROM task WHERE task_type = 'config' AND dedup_key = $1
ORDER BY created_at DESC LIMIT 1;

-- name: RetryTask :exec
UPDATE task SET
    status = 'pending',
    result = NULL,
    error = NULL,
    started_at = NULL,
    completed_at = NULL,
    attempts = 0
WHERE id = $1;

-- name: DeleteConfigTaskByDedupKey :execrows
DELETE FROM task
WHERE task_type = 'config' AND dedup_key = $1
  AND status IN ('pending', 'failed');

-- name: SetEnrichTaskPending :exec
UPDATE task SET
    status = 'pending',
    payload = $1,
    error = NULL,
    completed_at = NULL
WHERE id = $2 AND status IN ('waiting', 'discarded') AND task_type = 'enrich';

-- name: DiscardEnrichTask :execrows
UPDATE task SET
    status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = $1
WHERE id = $2 AND status = 'waiting' AND task_type = 'enrich';

-- name: SetEnrichTaskWaiting :execrows
UPDATE task SET
    status = 'waiting',
    error = NULL,
    completed_at = NULL
WHERE task_id = $1 AND status = 'discarded' AND task_type = 'enrich';

-- name: RestoreDiscardedEnrichTasks :execrows
UPDATE task AS e
SET status = 'waiting', error = NULL, completed_at = NULL
FROM task AS c
WHERE e.task_type = 'enrich' AND e.status = 'discarded'
  AND c.task_type = 'consume' AND c.status = 'pending'
  AND e.task_id = c.payload->>'on_completed';

-- name: RestoreDiscardedEnrichTasksByBatch :execrows
UPDATE task AS e
SET status = 'waiting', error = NULL, completed_at = NULL
FROM task AS c
WHERE e.task_type = 'enrich' AND e.status = 'discarded'
  AND c.task_type = 'consume' AND c.status = 'pending'
  AND c.batch_id = $1
  AND e.task_id = c.payload->>'on_completed';

-- name: DiscardWaitingEnrichesOfFailedConsumes :execrows
UPDATE task AS e
SET status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = COALESCE(c.error, 'parent consume task failed')
FROM task AS c
WHERE e.task_type = 'enrich' AND e.status = 'waiting'
  AND c.task_type = 'consume' AND c.status = 'failed'
  AND c.batch_id = $1
  AND e.task_id = c.payload->>'on_completed';

-- name: DiscardWaitingEnrichesOfFailedConsumesGlobal :execrows
UPDATE task AS e
SET status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = COALESCE(c.error, 'parent consume task failed')
FROM task AS c
WHERE e.task_type = 'enrich' AND e.status = 'waiting'
  AND c.task_type = 'consume' AND c.status = 'failed'
  AND e.task_id = c.payload->>'on_completed';

-- name: DiscardEnrichTaskByTaskID :execrows
UPDATE task SET
    status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = $1
WHERE task_id = $2 AND status = 'waiting' AND task_type = 'enrich';

-- name: DeleteTask :exec
DELETE FROM task WHERE id = $1;

-- name: CancelPendingTasksByBatch :execrows
UPDATE task SET status = 'cancelled', completed_at = CURRENT_TIMESTAMP
WHERE batch_id = $1 AND status = 'pending';

-- name: CancelProcessingTasksByBatch :execrows
UPDATE task SET status = 'cancelled', completed_at = CURRENT_TIMESTAMP
WHERE batch_id = $1 AND status = 'processing';

-- name: ListDistinctBatchIDs :many
SELECT batch_id FROM task
WHERE batch_id IS NOT NULL
GROUP BY batch_id
ORDER BY MAX(created_at) DESC
LIMIT $1 OFFSET $2;

-- name: ListDistinctBatchIDsByStatus :many
SELECT batch_id FROM task
WHERE batch_id IS NOT NULL AND status = $1
GROUP BY batch_id
ORDER BY MAX(created_at) DESC
LIMIT $2 OFFSET $3;

-- name: CountDistinctBatches :one
SELECT COUNT(DISTINCT batch_id) FROM task WHERE batch_id IS NOT NULL;

-- name: CountAllTasks :one
SELECT COUNT(*) FROM task;

-- name: CountTasksByStatus :many
SELECT status, COUNT(*) as count FROM task GROUP BY status;

-- name: QuarantineStaleProcessingTasks :execrows
UPDATE task SET
    status = 'failed',
    error = 'Max retries exceeded (' || attempts || ')',
    completed_at = CURRENT_TIMESTAMP
WHERE status = 'processing' AND attempts >= $1 AND started_at < $2;

-- name: ResetStaleProcessingTasks :execrows
UPDATE task SET
    status = 'pending',
    attempts = attempts + 1
WHERE status = 'processing' AND attempts < $1 AND started_at < $2;

-- name: CountRecentBackupTasksByMode :one
SELECT COUNT(*) FROM task
WHERE task_type = 'backup' AND created_at > NOW() - ($1 || ' minutes')::INTERVAL
  AND dedup_key LIKE 'backup:' || $2 || ':%';

-- name: CountActiveBackupTasks :one
SELECT COUNT(*) FROM task
WHERE task_type = 'backup' AND status IN ('pending', 'processing');

-- name: GetLastCompletedBackupByMode :one
SELECT completed_at FROM task
WHERE task_type = 'backup' AND status = 'completed'
  AND dedup_key LIKE 'backup:' || $1 || ':%'
ORDER BY completed_at DESC
LIMIT 1;

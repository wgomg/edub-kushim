-- name: GetTask :one
SELECT * FROM task WHERE id = ?;

-- name: GetTaskByTaskID :one
SELECT * FROM task WHERE task_id = ?;

-- name: GetTaskByBatchID :many
SELECT * FROM task WHERE batch_id = ? ORDER BY created_at;

-- name: GetNextPendingTask :one
SELECT id FROM task WHERE status = 'pending' ORDER BY created_at LIMIT 1;

-- name: GetNextPendingTaskOfType :one
SELECT id FROM task
WHERE status = 'pending' AND task_type = ?
ORDER BY created_at LIMIT 1;

-- name: ListTasks :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListTasksByStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListTasksByBatch :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListTasksByBatchAndStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? AND status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListAllTasks :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task ORDER BY created_at DESC;

-- name: ListAllTasksByStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE status = ? ORDER BY created_at DESC;

-- name: ListAllTasksByBatch :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? ORDER BY created_at DESC;

-- name: ListAllTasksByBatchAndStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? AND status = ? ORDER BY created_at DESC;

-- name: ListTasksByBatchAndStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? AND status = ? AND task_type = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListAllTasksByBatchAndStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? AND status = ? AND task_type = ? ORDER BY created_at DESC;

-- name: ListTasksByBatchAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? AND task_type = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListAllTasksByBatchAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? AND task_type = ? ORDER BY created_at DESC;

-- name: ListTasksByStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE status = ? AND task_type = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListAllTasksByStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE status = ? AND task_type = ? ORDER BY created_at DESC;

-- name: ListTasksByType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE task_type = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListAllTasksByType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE task_type = ? ORDER BY created_at DESC;

-- name: CountTasksByBatchAndStatus :one
SELECT COUNT(*) FROM task WHERE batch_id = ? AND status = ?;

-- name: CreateTask :execresult
INSERT INTO task (
    task_id, task_type, status, batch_id, payload, dedup_key
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ClaimTask :execrows
UPDATE task SET
    status = 'processing',
    started_at = CURRENT_TIMESTAMP
WHERE id = ? AND status = 'pending';

-- name: CompleteTask :exec
UPDATE task SET
    status = 'completed',
    result = ?,
    completed_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: FailTask :exec
UPDATE task SET
    status = 'failed',
    completed_at = CURRENT_TIMESTAMP,
    error = ?
WHERE id = ?;

-- name: RetryFailedTasksByBatch :execrows
UPDATE task SET
    status = 'pending',
    result = NULL,
    error = NULL,
    started_at = NULL,
    completed_at = NULL
WHERE batch_id = ? AND status = 'failed';

-- name: GetConfigTaskByDedupKey :one
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE task_type = 'config' AND dedup_key = ?
ORDER BY created_at DESC LIMIT 1;

-- name: RetryTask :exec
UPDATE task SET
    status = 'pending',
    result = NULL,
    error = NULL,
    started_at = NULL,
    completed_at = NULL
WHERE id = ?;

-- name: SetEnrichTaskPending :exec
UPDATE task SET
    status = 'pending',
    payload = ?,
    error = NULL,
    completed_at = NULL
WHERE id = ? AND status IN ('waiting', 'discarded') AND task_type = 'enrich';

-- name: DiscardEnrichTask :exec
UPDATE task SET
    status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = ?
WHERE id = ? AND status = 'waiting' AND task_type = 'enrich';

-- name: DeleteTask :exec
DELETE FROM task WHERE id = ?;

-- name: CancelPendingTasksByBatch :execrows
UPDATE task SET status = 'cancelled', completed_at = CURRENT_TIMESTAMP
WHERE batch_id = ? AND status = 'pending';

-- name: CancelProcessingTasksByBatch :execrows
UPDATE task SET status = 'cancelled', completed_at = CURRENT_TIMESTAMP
WHERE batch_id = ? AND status = 'processing';

-- name: ListDistinctBatchIDs :many
SELECT batch_id FROM task
WHERE batch_id IS NOT NULL
GROUP BY batch_id
ORDER BY MAX(created_at) DESC
LIMIT ? OFFSET ?;

-- name: ListDistinctBatchIDsByStatus :many
SELECT batch_id FROM task
WHERE batch_id IS NOT NULL AND status = ?
GROUP BY batch_id
ORDER BY MAX(created_at) DESC
LIMIT ? OFFSET ?;

-- name: CountDistinctBatches :one
SELECT COUNT(DISTINCT batch_id) FROM task WHERE batch_id IS NOT NULL;

-- name: CountAllTasks :one
SELECT COUNT(*) FROM task;

-- name: CountTasksByStatus :many
SELECT status, COUNT(*) as count FROM task GROUP BY status;

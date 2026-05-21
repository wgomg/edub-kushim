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
FROM task WHERE batch_id = ? ORDER BY created_at;

-- name: ListTasksByBatchAndStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? AND status = ? ORDER BY created_at;

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

-- name: RetryTask :exec
UPDATE task SET
    status = 'pending',
    result = NULL,
    error = NULL,
    started_at = NULL,
    completed_at = NULL
WHERE id = ?;

-- name: DeleteTask :exec
DELETE FROM task WHERE id = ?;

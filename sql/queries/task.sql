-- name: GetTask :one
SELECT * FROM task WHERE id = ?;

-- name: GetTaskByTaskID :one
SELECT * FROM task WHERE task_id = ?;

-- name: GetTaskByBatchID :many
SELECT * FROM task WHERE batch_id = ? ORDER BY created_at;

-- name: GetNextPendingTask :one
SELECT id FROM task WHERE status = 'pending' ORDER BY created_at LIMIT 1;

-- name: ListTasks :many
SELECT id, task_id, task_name, status, document_id, batch_id, file_path,
       created_at, started_at, completed_at, error
FROM task ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListTasksByStatus :many
SELECT id, task_id, task_name, status, document_id, batch_id, file_path,
       created_at, started_at, completed_at, error
FROM task WHERE status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListTasksByDocument :many
SELECT id, task_id, task_name, status, document_id, batch_id, file_path,
       created_at, started_at, completed_at, error
FROM task WHERE document_id = ? ORDER BY created_at DESC;

-- name: ListTasksByBatch :many
SELECT id, task_id, task_name, status, document_id, batch_id, file_path,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? ORDER BY created_at;

-- name: ListTasksByBatchAndStatus :many
SELECT id, task_id, task_name, status, document_id, batch_id, file_path,
       created_at, started_at, completed_at, error
FROM task WHERE batch_id = ? AND status = ? ORDER BY created_at;

-- name: CountTasksByBatchAndStatus :one
SELECT COUNT(*) FROM task WHERE batch_id = ? AND status = ?;

-- name: CreateTask :execresult
INSERT INTO task (
    task_id, task_name, status, document_id, batch_id, file_path
) VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateTaskStatus :exec
UPDATE task SET status = ?, error = ? WHERE id = ?;

-- name: StartTask :exec
UPDATE task SET
    status = 'processing',
    started_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: ClaimTask :execrows
UPDATE task SET
    status = 'processing',
    started_at = CURRENT_TIMESTAMP
WHERE id = ? AND status = 'pending';

-- name: CompleteTask :exec
UPDATE task SET
    status = 'completed',
    document_id = ?,
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
    error = NULL,
    started_at = NULL,
    completed_at = NULL
WHERE id = ?;

-- name: DeleteTask :exec
DELETE FROM task WHERE id = ?;

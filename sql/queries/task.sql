-- name: GetTask :one
SELECT * FROM task WHERE id = ?;

-- name: GetTaskByTaskID :one
SELECT * FROM task WHERE task_id = ?;

-- name: ListTasks :many
SELECT * FROM task ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListTasksByStatus :many
SELECT * FROM task WHERE status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListTasksByDocument :many
SELECT * FROM task WHERE document_id = ? ORDER BY created_at DESC;

-- name: CreateTask :execresult
INSERT INTO task (
    task_id, task_name, status, document_id
) VALUES (?, ?, ?, ?);

-- name: UpdateTaskStatus :exec
UPDATE task SET status = ?, error = ? WHERE id = ?;

-- name: StartTask :exec
UPDATE task SET
    status = 'processing',
    started_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: CompleteTask :exec
UPDATE task SET
    status = 'completed',
    completed_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: FailTask :exec
UPDATE task SET
    status = 'failed',
    completed_at = CURRENT_TIMESTAMP,
    error = ?
WHERE id = ?;

-- name: DeleteTask :exec
DELETE FROM task WHERE id = ?;

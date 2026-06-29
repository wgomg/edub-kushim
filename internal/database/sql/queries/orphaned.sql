-- name: CreateOrphanedFile :execresult
INSERT INTO orphaned_file (document_key, document_key_type, file_path, original_path, source_dir, file_size, mime_type)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListOrphanedFiles :many
SELECT * FROM orphaned_file WHERE status = 'pending' ORDER BY detected_at DESC;

-- name: GetOrphanedFile :one
SELECT * FROM orphaned_file WHERE id = ?;

-- name: MarkOrphanedFileDeleted :exec
UPDATE orphaned_file SET status = 'deleted', action_at = CURRENT_TIMESTAMP, action_type = 'delete' WHERE id = ?;

-- name: MarkOrphanedFileRestored :exec
UPDATE orphaned_file SET status = 'restored', action_at = CURRENT_TIMESTAMP, action_type = 'restore' WHERE id = ?;

-- name: MarkOrphanedFileReingested :exec
UPDATE orphaned_file SET status = 'reingested', action_at = CURRENT_TIMESTAMP, action_type = 'move_to_inbox' WHERE id = ?;

-- name: MarkAllOrphanedFilesDeleted :exec
UPDATE orphaned_file SET status = 'deleted', action_at = CURRENT_TIMESTAMP, action_type = 'delete' WHERE status = 'pending';

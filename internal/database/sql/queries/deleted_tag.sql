-- name: ListAllDeletedTags :many
SELECT name, kind, target_name FROM deleted_tag ORDER BY created_at DESC;

-- name: ListDeletedTags :many
SELECT name, kind, target_name, created_at FROM deleted_tag
WHERE (@name_prefix::text = '' OR name LIKE @name_prefix::text || '%')
ORDER BY created_at DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountDeletedTags :one
SELECT COUNT(*) FROM deleted_tag
WHERE (@name_prefix::text = '' OR name LIKE @name_prefix::text || '%');

-- name: UpsertDeletedTag :exec
INSERT INTO deleted_tag (name, kind, target_name)
VALUES (@name, @kind, @target_name)
ON CONFLICT (name) DO UPDATE
SET kind = EXCLUDED.kind, target_name = EXCLUDED.target_name, created_at = now();

-- name: GetDeletedTag :one
SELECT * FROM deleted_tag WHERE name = @name;

-- name: DeleteDeletedTag :exec
DELETE FROM deleted_tag WHERE name = @name;

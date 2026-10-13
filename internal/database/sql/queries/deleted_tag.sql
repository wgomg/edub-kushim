-- name: ListAllDeletedTags :many
SELECT name, kind FROM deleted_tag ORDER BY created_at DESC;

-- name: ListDeletedTags :many
SELECT name, kind, created_at FROM deleted_tag
WHERE (@name_prefix::text = '' OR name LIKE @name_prefix::text || '%')
ORDER BY created_at DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountDeletedTags :one
SELECT COUNT(*) FROM deleted_tag
WHERE (@name_prefix::text = '' OR name LIKE @name_prefix::text || '%');

-- name: UpsertDeletedTag :exec
INSERT INTO deleted_tag (name, kind)
VALUES (@name, @kind)
ON CONFLICT (name) DO UPDATE
SET kind = EXCLUDED.kind, created_at = now();

-- name: GetDeletedTag :one
SELECT * FROM deleted_tag WHERE name = @name;

-- name: DeleteDeletedTag :exec
DELETE FROM deleted_tag WHERE name = @name;

-- name: GetTag :one
SELECT * FROM tag WHERE id = ?;

-- name: ListTags :many
SELECT * FROM tag ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListAllTags :many
SELECT * FROM tag ORDER BY created_at DESC;

-- name: ListAllTagsNames :many
SELECT name FROM tag ORDER BY created_at DESC;

-- name: CreateTag :execresult
INSERT OR IGNORE INTO tag (
    name
) VALUES (?);

-- name: UpdateTag :exec
UPDATE tag SET
    name = ?
WHERE id = ?;

-- name: SearchTagsByName :many
SELECT * FROM tag WHERE name LIKE ? ORDER BY name ASC LIMIT ?;

-- name: DeleteTag :exec
DELETE FROM tag WHERE id = ?;

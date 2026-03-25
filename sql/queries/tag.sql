-- name: GetTag :one
SELECT * FROM tag WHERE id = ?;

-- name: ListTags :many
SELECT * FROM tag ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreateTag :execresult
INSERT INTO tag (
    name
) VALUES (?);

-- name: UpdateTag :exec
UPDATE tag SET
    name = ?
WHERE id = ?;

-- name: DeleteTag :exec
DELETE FROM tag WHERE id = ?;

-- name: GetAuthor :one
SELECT * FROM author WHERE id = ?;

-- name: ListAuthors :many
SELECT * FROM author ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreateAuthor :execresult
INSERT INTO author (
    name
) VALUES (?);

-- name: UpdateAuthor :exec
UPDATE author SET
    name = ?
WHERE id = ?;

-- name: DeleteAuthor :exec
DELETE FROM author WHERE id = ?;

-- name: GetPeople :one
SELECT * FROM people WHERE id = ?;

-- name: ListAllPeople :many
SELECT * FROM people ORDER BY created_at DESC;

-- name: ListPeople :many
SELECT * FROM people ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreatePeople :execresult
INSERT OR IGNORE INTO people (
    name
) VALUES (?);

-- name: UpdatePeople :exec
UPDATE people SET
    name = ?
WHERE id = ?;

-- name: DeletePeople :exec
DELETE FROM people WHERE id = ?;

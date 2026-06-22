-- name: GetPeople :one
SELECT * FROM people WHERE id = ?;

-- name: ListAllPeople :many
SELECT * FROM people ORDER BY created_at DESC;

-- name: ListPeople :many
SELECT * FROM people ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreatePeople :execresult
INSERT OR IGNORE INTO people (
    name, name_native
) VALUES (?, ?);

-- name: UpdatePeople :exec
UPDATE people SET
    name = ?
WHERE id = ?;

-- name: UpdatePeopleNative :exec
UPDATE people SET
    name_native = ?
WHERE id = ? AND name_native IS NULL;

-- name: GetPeopleByName :one
SELECT * FROM people WHERE name = ?;

-- name: SearchPeopleByName :many
SELECT * FROM people WHERE name LIKE ? ORDER BY name ASC LIMIT ?;

-- name: UpdatePeopleFull :exec
UPDATE people SET name = ?, name_native = ? WHERE id = ?;

-- name: DeletePeople :exec
DELETE FROM people WHERE id = ?;

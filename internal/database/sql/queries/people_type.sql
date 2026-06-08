-- name: GetPeopleType :one
SELECT * FROM people_type WHERE id = ?;

-- name: ListPeopleTypes :many
SELECT * FROM people_type ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListAllPeopleTypes :many
SELECT * FROM people_type ORDER BY created_at DESC;

-- name: ListAllPeopleTypesNames :many
SELECT name FROM people_type ORDER BY created_at DESC;

-- name: CreatePeopleType :execresult
INSERT INTO people_type (name, description) VALUES (?, ?);

-- name: UpdatePeopleType :exec
UPDATE people_type SET name = ?, description = ? WHERE id = ?;

-- name: DeletePeopleType :exec
DELETE FROM people_type WHERE id = ?;

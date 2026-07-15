-- name: GetPeopleType :one
SELECT * FROM people_type WHERE id = $1;

-- name: ListPeopleTypes :many
SELECT * FROM people_type ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListAllPeopleTypes :many
SELECT * FROM people_type ORDER BY created_at DESC;

-- name: ListAllPeopleTypesNames :many
SELECT name FROM people_type ORDER BY created_at DESC;

-- name: CreatePeopleType :one
INSERT INTO people_type (name, description) VALUES ($1, $2) RETURNING id;

-- name: UpdatePeopleType :exec
UPDATE people_type SET name = $1, description = $2 WHERE id = $3;

-- name: GetPeopleTypeByName :one
SELECT * FROM people_type WHERE name = $1;

-- name: SearchPeopleTypeByName :many
SELECT * FROM people_type WHERE name LIKE $1 ORDER BY name ASC LIMIT $2;

-- name: DeletePeopleType :exec
DELETE FROM people_type WHERE id = $1;

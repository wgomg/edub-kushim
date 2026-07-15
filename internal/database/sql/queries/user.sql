-- name: GetUser :one
SELECT * FROM "user" WHERE id = $1;

-- name: GetUserByUsername :one
SELECT * FROM "user" WHERE username = $1;

-- name: GetUserByAPIKeyHash :one
SELECT * FROM "user" WHERE api_key_hash = $1;

-- name: ListUsers :many
SELECT id, username, role, api_key_prefix, api_key_created_at, created_at
FROM "user" ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateUser :one
INSERT INTO "user" (
    username,
    password_hash,
    role,
    api_key_hash,
    api_key_prefix,
    api_key_created_at
) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id;

-- name: UpdateUser :exec
UPDATE "user" SET
    username = $1
WHERE id = $2;

-- name: UpdateUserPassword :exec
UPDATE "user" SET
    password_hash = $1
WHERE id = $2;

-- name: UpdateUserCredentials :exec
UPDATE "user" SET username = $1, password_hash = $2 WHERE id = $3;

-- name: UpdateUserAPIKey :execrows
UPDATE "user" SET
    api_key_hash = $1,
    api_key_prefix = $2,
    api_key_created_at = $3
WHERE id = $4;

-- name: RevokeUserAPIKey :execrows
UPDATE "user" SET
    api_key_hash = NULL,
    api_key_prefix = NULL,
    api_key_created_at = NULL
WHERE id = $1;

-- name: UpdateUserRole :exec
UPDATE "user" SET role = $1 WHERE id = $2;

-- name: CountUsers :one
SELECT COUNT(*) FROM "user";

-- name: DeleteUser :exec
DELETE FROM "user" WHERE id = $1;

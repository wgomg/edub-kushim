-- name: GetUser :one
SELECT * FROM user WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM user WHERE username = ?;

-- name: GetUserByAPIKeyHash :one
SELECT * FROM user WHERE api_key_hash = ?;

-- name: ListUsers :many
SELECT id, username, api_key_prefix, api_key_created_at, created_at
FROM user ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreateUser :execresult
INSERT INTO user (
    username,
    password_hash,
    api_key_hash,
    api_key_prefix,
    api_key_created_at
) VALUES (?, ?, ?, ?, ?);

-- name: UpdateUser :exec
UPDATE user SET
    username = ?
WHERE id = ?;

-- name: UpdateUserPassword :exec
UPDATE user SET
    password_hash = ?
WHERE id = ?;

-- name: UpdateUserCredentials :exec
UPDATE user SET username = ?, password_hash = ? WHERE id = ?;

-- name: UpdateUserAPIKey :execresult
UPDATE user SET
    api_key_hash = ?,
    api_key_prefix = ?,
    api_key_created_at = ?
WHERE id = ?;

-- name: RevokeUserAPIKey :execresult
UPDATE user SET
    api_key_hash = NULL,
    api_key_prefix = NULL,
    api_key_created_at = NULL
WHERE id = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM user;

-- name: DeleteUser :exec
DELETE FROM user WHERE id = ?;

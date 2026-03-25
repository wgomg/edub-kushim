-- name: GetUser :one
SELECT * FROM user WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM user WHERE username = ?;

-- name: GetUserByAPIKey :one
SELECT * FROM user WHERE api_key = ?;

-- name: ListUsers :many
SELECT * FROM user ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreateUser :execresult
INSERT INTO user (
    username,
    password_hash,
    api_key
) VALUES (?, ?, ?);

-- name: UpdateUser :exec
UPDATE user SET
    username = ?,
    api_key = ?
WHERE id = ?;

-- name: UpdateUserPassword :exec
UPDATE user SET
    password_hash = ?
WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM user WHERE id = ?;

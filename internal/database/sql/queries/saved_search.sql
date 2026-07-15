-- name: CreateSavedSearch :execresult
INSERT INTO saved_search (name, filter_json) VALUES ($1, $2);

-- name: ListSavedSearches :many
SELECT id, name, filter_json, created_at FROM saved_search ORDER BY created_at DESC;

-- name: DeleteSavedSearch :exec
DELETE FROM saved_search WHERE id = $1;

-- name: GetTag :one
SELECT * FROM tag WHERE id = $1;

-- name: GetTagByName :one
SELECT * FROM tag WHERE name = $1;

-- name: ListTags :many
SELECT * FROM tag ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListAllTags :many
SELECT * FROM tag ORDER BY created_at DESC;

-- name: ListAllTagsNames :many
SELECT name FROM tag ORDER BY created_at DESC;

-- name: CreateTag :one
INSERT INTO tag (name)
VALUES ($1)
ON CONFLICT (name) DO NOTHING
RETURNING id;

-- name: UpdateTag :exec
UPDATE tag SET
    name = $1
WHERE id = $2;

-- name: CountTags :one
SELECT COUNT(*) FROM tag;

-- name: CountTagsByName :one
SELECT COUNT(*) FROM tag WHERE name LIKE $1;

-- name: SearchTagsByName :many
SELECT * FROM tag WHERE name LIKE $1 ORDER BY name ASC LIMIT $2 OFFSET $3;

-- name: ListTagsWithDocumentCount :many
SELECT t.*, COUNT(dt.document_id) AS document_count
FROM tag t
LEFT JOIN document_tag dt ON t.id = dt.tag_id
GROUP BY t.id
ORDER BY t.created_at DESC LIMIT $1 OFFSET $2;

-- name: SearchTagsByNameWithDocumentCount :many
SELECT t.*, COUNT(dt.document_id) AS document_count
FROM tag t
LEFT JOIN document_tag dt ON t.id = dt.tag_id
WHERE t.name LIKE $1
GROUP BY t.id
ORDER BY t.name ASC LIMIT $2 OFFSET $3;

-- name: DeleteTag :exec
DELETE FROM tag WHERE id = $1;

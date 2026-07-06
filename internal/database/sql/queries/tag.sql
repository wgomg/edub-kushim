-- name: GetTag :one
SELECT * FROM tag WHERE id = ?;

-- name: GetTagByName :one
SELECT * FROM tag WHERE name = ?;

-- name: ListTags :many
SELECT * FROM tag ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListAllTags :many
SELECT * FROM tag ORDER BY created_at DESC;

-- name: ListAllTagsNames :many
SELECT name FROM tag ORDER BY created_at DESC;

-- name: CreateTag :execresult
INSERT OR IGNORE INTO tag (
    name
) VALUES (?);

-- name: UpdateTag :exec
UPDATE tag SET
    name = ?
WHERE id = ?;

-- name: CountTags :one
SELECT COUNT(*) FROM tag;

-- name: CountTagsByName :one
SELECT COUNT(*) FROM tag WHERE name LIKE ?;

-- name: SearchTagsByName :many
SELECT * FROM tag WHERE name LIKE ? ORDER BY name ASC LIMIT ? OFFSET ?;

-- name: ListTagsWithDocumentCount :many
SELECT t.*, COUNT(dt.document_id) AS document_count
FROM tag t
LEFT JOIN document_tag dt ON t.id = dt.tag_id
GROUP BY t.id
ORDER BY t.created_at DESC LIMIT ? OFFSET ?;

-- name: SearchTagsByNameWithDocumentCount :many
SELECT t.*, COUNT(dt.document_id) AS document_count
FROM tag t
LEFT JOIN document_tag dt ON t.id = dt.tag_id
WHERE t.name LIKE ?
GROUP BY t.id
ORDER BY t.name ASC LIMIT ? OFFSET ?;

-- name: DeleteTag :exec
DELETE FROM tag WHERE id = ?;

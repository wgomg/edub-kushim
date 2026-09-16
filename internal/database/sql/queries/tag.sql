-- name: GetTag :one
SELECT * FROM tag WHERE id = $1;

-- name: GetTagForUpdate :one
SELECT * FROM tag WHERE id = $1 FOR UPDATE;

-- name: GetTagByName :one
SELECT * FROM tag WHERE name = $1;

-- name: ListTags :many
SELECT * FROM tag ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListAllTags :many
SELECT * FROM tag ORDER BY created_at DESC;

-- name: ListPromotedTagNames :many
SELECT name FROM tag
WHERE promoted_at IS NOT NULL
ORDER BY created_at DESC;

-- name: CreateTag :one
INSERT INTO tag (name)
VALUES ($1)
ON CONFLICT (name) DO NOTHING
RETURNING id;

-- name: UpdateTag :exec
UPDATE tag SET
    name = $1
WHERE id = $2;

-- name: PromoteTags :execrows
UPDATE tag SET promoted_at = now()
WHERE id = ANY(@tag_ids::bigint[])
  AND promoted_at IS NULL;

-- name: CountTags :one
SELECT COUNT(*) FROM tag;

-- name: CountTagsByName :one
SELECT COUNT(*) FROM tag WHERE name LIKE $1;

-- name: SearchTagsByName :many
SELECT * FROM tag WHERE name LIKE $1 ORDER BY name ASC LIMIT $2 OFFSET $3;

-- name: ListAllTagsWithDocumentCount :many
SELECT t.*, COUNT(dt.document_id) AS document_count
FROM tag t
LEFT JOIN document_tag dt ON t.id = dt.tag_id
GROUP BY t.id
ORDER BY t.created_at DESC;

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

-- name: MarkOrphanTags :execrows
UPDATE tag t SET orphaned_since = now()
WHERE t.orphaned_since IS NULL
  AND NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id);

-- name: ClearOrphanTagMarks :execrows
UPDATE tag t SET orphaned_since = NULL
WHERE t.orphaned_since IS NOT NULL
  AND EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id);

-- name: ClearOrphanTagMarksByTagIDs :exec
UPDATE tag SET orphaned_since = NULL
WHERE id = ANY(@tag_ids::bigint[])
  AND orphaned_since IS NOT NULL;

-- name: ListOrphanTags :many
SELECT t.name FROM tag t
WHERE t.orphaned_since IS NOT NULL
  AND t.orphaned_since < @orphaned_before::timestamptz
  AND NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id)
ORDER BY t.name;

-- name: DeleteOrphanTags :many
DELETE FROM tag t
WHERE t.orphaned_since IS NOT NULL
  AND t.orphaned_since < @orphaned_before::timestamptz
  AND NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id)
RETURNING t.name;

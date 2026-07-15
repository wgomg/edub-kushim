-- name: GetDocumentType :one
SELECT * FROM document_type WHERE id = $1;

-- name: ListDocumentTypes :many
SELECT * FROM document_type ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateDocumentType :one
INSERT INTO document_type (name) VALUES ($1) RETURNING id;

-- name: UpdateDocumentType :exec
UPDATE document_type SET name = $1 WHERE id = $2;

-- name: GetDocumentTypeByName :one
SELECT * FROM document_type WHERE name = $1;

-- name: SearchDocumentTypeByName :many
SELECT * FROM document_type WHERE name LIKE $1 ORDER BY name ASC LIMIT $2;

-- name: CreateDocumentTypeFull :one
INSERT INTO document_type (name, description) VALUES ($1, $2) RETURNING id;

-- name: UpdateDocumentTypeFull :exec
UPDATE document_type SET name = $1, description = $2 WHERE id = $3;

-- name: DeleteDocumentType :exec
DELETE FROM document_type WHERE id = $1;

-- name: ListAllDocumentTypes :many
SELECT * FROM document_type ORDER BY created_at DESC;

-- name: ListDocumentTypesWithDocumentCount :many
SELECT dt.*, COUNT(d.id) AS document_count
FROM document_type dt
LEFT JOIN document d ON dt.id = d.document_type_id
GROUP BY dt.id
ORDER BY dt.created_at DESC LIMIT $1 OFFSET $2;

-- name: SearchDocumentTypeByNameWithDocumentCount :many
SELECT dt.*, COUNT(d.id) AS document_count
FROM document_type dt
LEFT JOIN document d ON dt.id = d.document_type_id
WHERE dt.name LIKE $1
GROUP BY dt.id
ORDER BY dt.name ASC LIMIT $2;

-- name: ListAllDocumentTypesWithDocumentCount :many
SELECT dt.*, COUNT(d.id) AS document_count
FROM document_type dt
LEFT JOIN document d ON dt.id = d.document_type_id
GROUP BY dt.id
ORDER BY dt.created_at DESC;

-- name: ListAllDocumentTypesNames :many
SELECT name FROM document_type ORDER BY created_at DESC;

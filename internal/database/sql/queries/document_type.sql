-- name: GetDocumentType :one
SELECT * FROM document_type WHERE id = ?;

-- name: ListDocumentTypes :many
SELECT * FROM document_type ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreateDocumentType :execresult
INSERT INTO document_type (name) VALUES (?);

-- name: UpdateDocumentType :exec
UPDATE document_type SET name = ? WHERE id = ?;

-- name: GetDocumentTypeByName :one
SELECT * FROM document_type WHERE name = ?;

-- name: SearchDocumentTypeByName :many
SELECT * FROM document_type WHERE name LIKE ? ORDER BY name ASC LIMIT ?;

-- name: CreateDocumentTypeFull :execresult
INSERT INTO document_type (name, description) VALUES (?, ?);

-- name: UpdateDocumentTypeFull :exec
UPDATE document_type SET name = ?, description = ? WHERE id = ?;

-- name: DeleteDocumentType :exec
DELETE FROM document_type WHERE id = ?;

-- name: ListAllDocumentTypes :many
SELECT * FROM document_type ORDER BY created_at DESC;

-- name: ListAllDocumentTypesNames :many
SELECT name FROM document_type ORDER BY created_at DESC;

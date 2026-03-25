-- name: GetDocumentType :one
SELECT * FROM document_type WHERE id = ?;

-- name: ListDocumentTypes :many
SELECT * FROM document_type ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreateDocumentType :execresult
INSERT INTO document_type (name) VALUES (?);

-- name: UpdateDocumentType :exec
UPDATE document_type SET name = ? WHERE id = ?;

-- name: DeleteDocumentType :exec
DELETE FROM document_type WHERE id = ?;

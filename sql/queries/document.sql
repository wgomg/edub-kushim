-- name: GetDocument :one
SELECT * FROM document WHERE id = ?;

-- name: ListDocuments :many
SELECT * FROM document ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreateDocument :execresult
INSERT INTO document (
    title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: UpdateDocument :exec
UPDATE document SET
    storage_path = ?,
    modified_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteDocument :exec
DELETE FROM document WHERE id = ?;

-- name: GetDocumentWithDetails :one
SELECT d.*, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.id = ?;

-- name: SearchDocumentsByTitle :many
SELECT * FROM document
WHERE title LIKE ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: GetDocumentByMD5Checksum :many
SELECT * FROM document WHERE md5_checksum = ?;

-- name: GetDocumentBySHA512Checksum :one
SELECT * FROM document WHERE sha512_checksum = ?;

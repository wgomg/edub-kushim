-- name: GetDocument :one
SELECT id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count,
       char_count, created_at, modified_at, document_type_id, original_path, storage_path, text_content
FROM document WHERE id = ?;

-- name: ListDocuments :many
SELECT id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count,
       char_count, created_at, modified_at, document_type_id, original_path, storage_path
FROM document ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreateDocument :execresult
INSERT INTO document (
    title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count,
    char_count, original_path, storage_path, text_content
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateDocumentPaths :exec
UPDATE document SET
    original_path = ?,
    storage_path = ?,
    modified_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteDocument :exec
DELETE FROM document WHERE id = ?;

-- name: GetDocumentWithDetails :one
SELECT d.id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size, d.page_count, d.word_count,
       d.char_count, d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path,
       d.text_content, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.id = ?;

-- name: GetDocumentWithText :one
SELECT d.id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size, d.page_count, d.word_count,
       d.char_count, d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path,
       d.text_content, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.id = ?;

-- name: SearchDocumentsByTitle :many
SELECT id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count, char_count,
       created_at, modified_at, document_type_id, original_path, storage_path
FROM document
WHERE title LIKE ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: GetDocumentByMD5Checksum :many
SELECT id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count, char_count,
       created_at, modified_at, document_type_id, original_path, storage_path
FROM document WHERE md5_checksum = ?;

-- name: GetDocumentBySHA512Checksum :one
SELECT id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count, char_count,
       created_at, modified_at, document_type_id, original_path, storage_path
FROM document WHERE sha512_checksum = ?;

-- name: SumDocumentFileSizes :one
SELECT CAST(COALESCE(SUM(file_size), 0) AS INTEGER) AS total_bytes FROM document;

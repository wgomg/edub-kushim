-- name: GetDocument :one
SELECT id, document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path, text_content
FROM document WHERE document_id = $1;

-- name: GetDocumentById :one
SELECT id, document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path, text_content
FROM document WHERE id = $1;

-- name: ListDocuments :many
SELECT id, document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path
FROM document ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateDocument :execresult
INSERT INTO document (
    document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count,
    char_count, language, original_path, storage_path, text_content
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: UpdateDocumentMetadata :exec
UPDATE document SET
    title = $1,
    document_type_id = $2,
    language = $3,
    modified_at = CURRENT_TIMESTAMP
WHERE document_id = $4;

-- name: UpdateDocumentEditable :exec
UPDATE document SET
    title = $1,
    document_type_id = $2,
    language = $3,
    text_content = $4,
    modified_at = CURRENT_TIMESTAMP
WHERE document_id = $5;

-- name: UpdateDocumentMetadataById :exec
UPDATE document SET
    title = $1,
    document_type_id = $2,
    language = $3,
    modified_at = CURRENT_TIMESTAMP
WHERE id = $4;

-- name: UpdateDocumentPaths :exec
UPDATE document SET
    original_path = $1,
    storage_path = $2,
    modified_at = CURRENT_TIMESTAMP
WHERE document_id = $3;

-- name: UpdateDocumentPathsById :exec
UPDATE document SET
    original_path = $1,
    storage_path = $2,
    modified_at = CURRENT_TIMESTAMP
WHERE id = $3;

-- name: DeleteDocument :exec
DELETE FROM document WHERE document_id = $1;

-- name: DeleteDocumentById :exec
DELETE FROM document WHERE id = $1;

-- name: GetDocumentWithDetails :one
SELECT d.id, d.document_id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size,
       d.page_count, d.word_count, d.char_count, d.language, d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path, d.text_content, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.document_id = $1;

-- name: GetDocumentWithDetailsById :one
SELECT d.id, d.document_id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size,
       d.page_count, d.word_count, d.char_count, d.language, d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path, d.text_content, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.id = $1;

-- name: GetDocumentWithText :one
SELECT d.id, d.document_id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size,
       d.page_count, d.word_count, d.char_count, d.language, d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path, d.text_content, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.document_id = $1;

-- name: GetDocumentWithTextById :one
SELECT d.id, d.document_id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size,
       d.page_count, d.word_count, d.char_count, d.language, d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path, d.text_content, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.id = $1;

-- name: SearchDocumentsByTitle :many
SELECT id, document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path
FROM document
WHERE title LIKE $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetDocumentByMD5Checksum :many
SELECT id, document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path
FROM document WHERE md5_checksum = $1;

-- name: GetDocumentBySHA512Checksum :one
SELECT id, document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path
FROM document WHERE sha512_checksum = $1;

-- name: CountAllDocuments :one
SELECT COUNT(*) FROM document;

-- name: SumDocumentFileSizes :one
SELECT CAST(COALESCE(SUM(file_size), 0) AS BIGINT) AS total_bytes FROM document;

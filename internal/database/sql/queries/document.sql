-- name: GetDocument :one
SELECT id, document_id, title, md5_checksum, sha512_checksum, original_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path, text_content,
       text_search_vector, has_thumbnail
FROM document WHERE document_id = $1 AND deleted_at IS NULL;

-- name: GetDocumentThumbnailMeta :one
SELECT document_id, created_at, has_thumbnail
FROM document WHERE document_id = $1 AND deleted_at IS NULL;

-- name: GetDocumentById :one
SELECT id, document_id, title, md5_checksum, sha512_checksum, original_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path, text_content,
       text_search_vector, has_thumbnail
FROM document WHERE id = $1 AND deleted_at IS NULL;

-- name: ListDocuments :many
SELECT id, document_id, title, md5_checksum, sha512_checksum, original_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path, has_thumbnail
FROM document WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateDocument :one
INSERT INTO document (
    document_id, title, md5_checksum, sha512_checksum, original_type, file_size, page_count, word_count,
    char_count, language, original_path, storage_path, text_content
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id;

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

-- name: SetDocumentHasThumbnail :exec
UPDATE document SET
    has_thumbnail = TRUE,
    modified_at = CURRENT_TIMESTAMP
WHERE document_id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteDocument :exec
UPDATE document SET
    deleted_at = CURRENT_TIMESTAMP,
    original_path = $2,
    storage_path = $3,
    modified_at = CURRENT_TIMESTAMP
WHERE document_id = $1 AND deleted_at IS NULL;

-- name: GetDocumentWithDetails :one
SELECT d.id, d.document_id, d.title, d.md5_checksum, d.sha512_checksum, d.original_type, d.file_size,
       d.page_count, d.word_count, d.char_count, d.language, d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path, d.text_content, d.has_thumbnail, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.document_id = $1 AND d.deleted_at IS NULL;

-- name: GetDocumentWithDetailsById :one
SELECT d.id, d.document_id, d.title, d.md5_checksum, d.sha512_checksum, d.original_type, d.file_size,
       d.page_count, d.word_count, d.char_count, d.language, d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path, d.text_content, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.id = $1 AND d.deleted_at IS NULL;

-- name: GetDocumentWithText :one
SELECT d.id, d.document_id, d.title, d.md5_checksum, d.sha512_checksum, d.original_type, d.file_size,
       d.page_count, d.word_count, d.char_count, d.language, d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path, d.text_content, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.document_id = $1 AND d.deleted_at IS NULL;

-- name: GetDocumentWithTextById :one
SELECT d.id, d.document_id, d.title, d.md5_checksum, d.sha512_checksum, d.original_type, d.file_size,
       d.page_count, d.word_count, d.char_count, d.language, d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path, d.text_content, dt.name as document_type_name
FROM document d
LEFT JOIN document_type dt ON d.document_type_id = dt.id
WHERE d.id = $1 AND d.deleted_at IS NULL;

-- name: SearchDocumentsByTitle :many
SELECT id, document_id, title, md5_checksum, sha512_checksum, original_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path
FROM document
WHERE title LIKE $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetDocumentByMD5Checksum :many
SELECT id, document_id, title, md5_checksum, sha512_checksum, original_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path
FROM document WHERE md5_checksum = $1 AND deleted_at IS NULL;

-- name: GetDocumentBySHA512Checksum :one
SELECT id, document_id, title, md5_checksum, sha512_checksum, original_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, document_type_id, original_path, storage_path
FROM document WHERE sha512_checksum = $1 AND deleted_at IS NULL;

-- name: CountAllDocuments :one
SELECT COUNT(*) FROM document WHERE deleted_at IS NULL;

-- name: SumDocumentFileSizes :one
SELECT CAST(COALESCE(SUM(file_size), 0) AS BIGINT) AS total_bytes FROM document WHERE deleted_at IS NULL;

-- name: GetTrashDocument :one
SELECT id, document_id, title, md5_checksum, sha512_checksum, original_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, deleted_at, document_type_id, original_path, storage_path,
       text_content, text_search_vector
FROM document WHERE document_id = $1 AND deleted_at IS NOT NULL;

-- name: ListTrashDocuments :many
SELECT id, document_id, title, md5_checksum, sha512_checksum, original_type, file_size, page_count, word_count,
       char_count, language, created_at, modified_at, deleted_at, document_type_id, original_path, storage_path
FROM document WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC LIMIT $1 OFFSET $2;

-- name: CountTrashDocuments :one
SELECT COUNT(*) FROM document WHERE deleted_at IS NOT NULL;

-- name: RestoreDocument :exec
UPDATE document SET
    deleted_at = NULL,
    original_path = $2,
    storage_path = $3,
    modified_at = CURRENT_TIMESTAMP
WHERE document_id = $1 AND deleted_at IS NOT NULL;

-- name: PermanentlyDeleteDocument :exec
DELETE FROM document WHERE document_id = $1 AND deleted_at IS NOT NULL;

-- name: PurgeExpiredDocuments :execrows
DELETE FROM document WHERE deleted_at < CURRENT_TIMESTAMP - ($1::text || ' days')::INTERVAL;

-- name: ListDocumentsWithoutThumbnails :many
SELECT id, document_id, storage_path
FROM document
WHERE has_thumbnail = FALSE AND deleted_at IS NULL AND id > $1
ORDER BY id
LIMIT $2;

-- name: ListDocumentsWithoutThumbnailsByBatch :many
SELECT d.document_id, d.storage_path
FROM document d
JOIN task t ON t.payload->>'document_id' = d.document_id
WHERE t.batch_id = $1
  AND t.task_type = 'consume'
  AND t.status = 'completed'
  AND d.has_thumbnail = FALSE
  AND d.deleted_at IS NULL
ORDER BY d.id;

-- name: GetDocumentWithoutThumbnail :one
SELECT document_id, storage_path
FROM document
WHERE document_id = $1
  AND has_thumbnail = FALSE
  AND deleted_at IS NULL;

-- name: SetDocumentsDocumentType :many
UPDATE document SET
    document_type_id = $1,
    modified_at = CURRENT_TIMESTAMP
WHERE document_id = ANY($2::text[])
  AND deleted_at IS NULL
RETURNING document_id;

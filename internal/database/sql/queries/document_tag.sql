-- name: GetDocumentTags :many
SELECT t.* FROM tag t
JOIN document_tag dt ON t.id = dt.tag_id
WHERE dt.document_id = $1;

-- name: GetTagDocuments :many
SELECT d.id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size,
       d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path
FROM document d
JOIN document_tag dt ON d.id = dt.document_id
WHERE dt.tag_id = $1;

-- name: AddDocumentTag :exec
INSERT INTO document_tag (document_id, tag_id)
VALUES ($1, $2)
ON CONFLICT (document_id, tag_id) DO NOTHING;

-- name: RemoveDocumentTag :exec
DELETE FROM document_tag WHERE document_id = $1 AND tag_id = $2;

-- name: ClearDocumentTags :exec
DELETE FROM document_tag WHERE document_id = $1;

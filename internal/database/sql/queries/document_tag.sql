-- name: GetDocumentTags :many
SELECT t.* FROM tag t
JOIN document_tag dt ON t.id = dt.tag_id
WHERE dt.document_id = $1;

-- name: GetTagDocuments :many
SELECT d.id, d.title, d.md5_checksum, d.sha512_checksum, d.original_type, d.file_size,
       d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path
FROM document d
JOIN document_tag dt ON d.id = dt.document_id
WHERE dt.tag_id = $1 AND d.deleted_at IS NULL;

-- name: AddDocumentTag :exec
INSERT INTO document_tag (document_id, tag_id)
VALUES ($1, $2)
ON CONFLICT (document_id, tag_id) DO NOTHING;

-- name: RemoveDocumentTag :exec
DELETE FROM document_tag WHERE document_id = $1 AND tag_id = $2;

-- name: ClearDocumentTags :exec
DELETE FROM document_tag WHERE document_id = $1;

-- name: ReassignDocumentTags :execrows
UPDATE document_tag dt
SET tag_id = @target_id
WHERE dt.tag_id = @source_id
  AND NOT EXISTS (
      SELECT 1 FROM document_tag dt2
      WHERE dt2.document_id = dt.document_id AND dt2.tag_id = @target_id
  );

-- name: RepointDocumentTagsForDocs :execrows
UPDATE document_tag dt
SET tag_id = @target_id
WHERE dt.tag_id = @source_id
  AND dt.document_id = ANY(@document_ids::bigint[])
  AND NOT EXISTS (
      SELECT 1 FROM document_tag dt2
      WHERE dt2.document_id = dt.document_id AND dt2.tag_id = @target_id
  );

-- name: DeleteDocumentTagsForTag :execrows
DELETE FROM document_tag WHERE tag_id = $1;

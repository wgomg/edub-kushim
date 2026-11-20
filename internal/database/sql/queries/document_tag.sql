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
INSERT INTO document_tag (document_id, tag_id, source)
VALUES ($1, $2, $3)
ON CONFLICT (document_id, tag_id) DO NOTHING;

-- name: UpsertManualDocumentTag :exec
INSERT INTO document_tag (document_id, tag_id, source)
VALUES ($1, $2, 'manual')
ON CONFLICT (document_id, tag_id) DO UPDATE SET source = 'manual';

-- name: RemoveDocumentTag :exec
DELETE FROM document_tag WHERE document_id = $1 AND tag_id = $2;

-- name: ClearDocumentTags :exec
DELETE FROM document_tag WHERE document_id = $1;

-- name: ClearEnrichDocumentTags :exec
DELETE FROM document_tag WHERE document_id = $1 AND source = 'enrich';

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

-- name: GetTagDocumentIDs :many
SELECT dt.document_id FROM document_tag dt WHERE dt.tag_id = $1;

-- name: AddComponentTagsToTagDocuments :execrows
INSERT INTO document_tag (document_id, tag_id)
SELECT dt.document_id, c.tag_id
FROM document_tag dt
CROSS JOIN unnest(@component_ids::bigint[]) AS c(tag_id)
WHERE dt.tag_id = @joint_id
ON CONFLICT (document_id, tag_id) DO NOTHING;

-- name: DeleteDocumentTagsForTag :execrows
DELETE FROM document_tag WHERE tag_id = $1;

-- name: CountTagDocumentsByTagIDs :many
SELECT tag_id, COUNT(*) AS document_count
FROM document_tag
WHERE tag_id = ANY(@tag_ids::bigint[])
GROUP BY tag_id;

-- name: CountLiveTagDocumentsByTagIDs :many
-- A NULL @tag_ids reads the whole store's live mass per tag.
SELECT dt.tag_id, COUNT(*) AS document_count
FROM document_tag dt
JOIN document d ON d.id = dt.document_id
WHERE d.deleted_at IS NULL
  AND (@tag_ids::bigint[] IS NULL OR dt.tag_id = ANY(@tag_ids::bigint[]))
GROUP BY dt.tag_id;

-- name: CountLiveTagDocumentsByNames :many
SELECT t.name, COUNT(d.id) AS document_count
FROM tag t
LEFT JOIN document_tag dt ON dt.tag_id = t.id
LEFT JOIN document d ON d.id = dt.document_id AND d.deleted_at IS NULL
WHERE t.name = ANY(@tag_names::text[])
GROUP BY t.name;

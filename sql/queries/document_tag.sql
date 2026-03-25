-- name: GetDocumentTags :many
SELECT t.* FROM tag t
JOIN document_tag dt ON t.id = dt.tag_id
WHERE dt.document_id = ?;

-- name: GetTagDocuments :many
SELECT d.* FROM document d
JOIN document_tag dt ON d.id = dt.document_id
WHERE dt.tag_id = ?;

-- name: AddDocumentTag :exec
INSERT INTO document_tag (document_id, tag_id) VALUES (?, ?);

-- name: RemoveDocumentTag :exec
DELETE FROM document_tag WHERE document_id = ? AND tag_id = ?;

-- name: ClearDocumentTags :exec
DELETE FROM document_tag WHERE document_id = ?;

-- name: GetDocumentAuthors :many
SELECT a.* FROM author a
JOIN document_author da ON a.id = da.author_id
WHERE da.document_id = ?;

-- name: GetAuthorDocuments :many
SELECT d.id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size,
       d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path
FROM document d
JOIN document_author da ON d.id = da.document_id
WHERE da.author_id = ?;

-- name: AddDocumentAuthor :exec
INSERT INTO document_author (document_id, author_id) VALUES (?, ?);

-- name: RemoveDocumentAuthor :exec
DELETE FROM document_author WHERE document_id = ? AND author_id = ?;

-- name: ClearDocumentAuthors :exec
DELETE FROM document_author WHERE document_id = ?;

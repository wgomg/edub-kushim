-- name: GetDocumentAuthors :many
SELECT a.* FROM author a
JOIN document_author da ON a.id = da.author_id
WHERE da.document_id = ?;

-- name: GetAuthorDocuments :many
SELECT d.* FROM document d
JOIN document_author da ON d.id = da.document_id
WHERE da.author_id = ?;

-- name: AddDocumentAuthor :exec
INSERT INTO document_author (document_id, author_id) VALUES (?, ?);

-- name: RemoveDocumentAuthor :exec
DELETE FROM document_author WHERE document_id = ? AND author_id = ?;

-- name: ClearDocumentAuthors :exec
DELETE FROM document_author WHERE document_id = ?;

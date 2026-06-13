-- name: GetDocumentPeople :many
SELECT p.*, dp.people_type_id FROM people p
JOIN document_people dp ON p.id = dp.people_id
WHERE dp.document_id = ?;

-- name: GetDocumentPeopleWithType :many
SELECT p.id, p.name, p.name_native, p.created_at AS people_created_at,
       pt.id AS people_type_id, pt.name AS people_type_name, pt.description AS people_type_description
FROM people p
JOIN document_people dp ON p.id = dp.people_id
JOIN people_type pt ON dp.people_type_id = pt.id
WHERE dp.document_id = ?;

-- name: GetPeopleDocuments :many
SELECT d.id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size,
       d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path
FROM document d
JOIN document_people dp ON d.id = dp.document_id
WHERE dp.people_id = ?;

-- name: AddDocumentPeople :exec
INSERT OR IGNORE INTO document_people (document_id, people_id, people_type_id) VALUES (?, ?, ?);

-- name: RemoveDocumentPeople :exec
DELETE FROM document_people WHERE document_id = ? AND people_id = ?;

-- name: ClearDocumentPeople :exec
DELETE FROM document_people WHERE document_id = ?;

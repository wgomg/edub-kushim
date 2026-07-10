-- name: GetPeople :one
SELECT * FROM people WHERE id = ?;

-- name: ListAllPeople :many
SELECT * FROM people ORDER BY created_at DESC;

-- name: ListPeople :many
SELECT * FROM people ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CreatePeople :execresult
INSERT OR IGNORE INTO people (
    name, name_native, normalized_name
) VALUES (?, ?, ?);

-- name: UpdatePeople :exec
UPDATE people SET
    name = ?, normalized_name = ?
WHERE id = ?;

-- name: UpdatePeopleNative :exec
UPDATE people SET
    name_native = ?
WHERE id = ? AND name_native IS NULL;

-- name: GetPeopleByName :one
SELECT * FROM people WHERE name = ?;

-- name: GetPeopleByNormalizedName :one
SELECT * FROM people WHERE normalized_name = ?;

-- name: SearchPeopleByName :many
SELECT * FROM people WHERE name LIKE ? ORDER BY name ASC LIMIT ?;

-- name: UpdatePeopleFull :exec
UPDATE people SET name = ?, name_native = ?, normalized_name = ? WHERE id = ?;

-- name: ListPeopleWithDocumentCount :many
SELECT p.*, COUNT(dp.document_id) AS document_count
FROM people p
LEFT JOIN document_people dp ON p.id = dp.people_id
GROUP BY p.id
ORDER BY p.created_at DESC LIMIT ? OFFSET ?;

-- name: SearchPeopleByNameWithDocumentCount :many
SELECT p.*, COUNT(dp.document_id) AS document_count
FROM people p
LEFT JOIN document_people dp ON p.id = dp.people_id
WHERE p.name LIKE ?
GROUP BY p.id
ORDER BY p.name ASC LIMIT ?;

-- name: DeletePeople :exec
DELETE FROM people WHERE id = ?;

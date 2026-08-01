-- name: GetPeople :one
SELECT * FROM people WHERE id = $1;

-- name: ListAllPeople :many
SELECT * FROM people ORDER BY created_at DESC;

-- name: ListPeople :many
SELECT * FROM people ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreatePeople :one
INSERT INTO people (name, name_native, normalized_name)
VALUES ($1, $2, $3)
ON CONFLICT (name) DO NOTHING
RETURNING id;

-- name: UpdatePeople :exec
UPDATE people SET
    name = $1, normalized_name = $2
WHERE id = $3;

-- name: UpdatePeopleNative :exec
UPDATE people SET
    name_native = $1
WHERE id = $2 AND name_native IS NULL;

-- name: GetPeopleByName :one
SELECT * FROM people WHERE name = $1;

-- name: GetPeopleByNormalizedName :one
SELECT * FROM people WHERE normalized_name = $1;

-- name: SearchPeopleByName :many
SELECT * FROM people WHERE name LIKE $1 ORDER BY name ASC LIMIT $2;

-- name: UpdatePeopleFull :exec
UPDATE people SET name = $1, name_native = $2, normalized_name = $3 WHERE id = $4;

-- name: ListPeopleWithDocumentCount :many
SELECT p.*, COUNT(dp.document_id) AS document_count
FROM people p
LEFT JOIN document_people dp ON p.id = dp.people_id
LEFT JOIN document d ON dp.document_id = d.id AND d.deleted_at IS NULL
GROUP BY p.id
ORDER BY p.created_at DESC LIMIT $1 OFFSET $2;

-- name: CountPeople :one
SELECT COUNT(*) FROM people;

-- name: CountPeopleByName :one
SELECT COUNT(*) FROM people WHERE name LIKE $1;

-- name: SearchPeopleByNameWithDocumentCount :many
SELECT p.*, COUNT(dp.document_id) AS document_count
FROM people p
LEFT JOIN document_people dp ON p.id = dp.people_id
LEFT JOIN document d ON dp.document_id = d.id AND d.deleted_at IS NULL
WHERE p.name LIKE $1
GROUP BY p.id
ORDER BY p.name ASC LIMIT $2 OFFSET $3;

-- name: DeletePeople :exec
DELETE FROM people WHERE id = $1;

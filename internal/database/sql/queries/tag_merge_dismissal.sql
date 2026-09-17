-- name: ListTagMergeDismissals :many
SELECT tag_a, tag_b, created_at
FROM tag_merge_dismissal
ORDER BY created_at DESC;

-- name: UpsertTagMergeDismissal :exec
INSERT INTO tag_merge_dismissal (tag_a, tag_b)
VALUES ($1, $2)
ON CONFLICT (tag_a, tag_b) DO NOTHING;

-- name: DeleteTagMergeDismissal :execrows
DELETE FROM tag_merge_dismissal
WHERE tag_a = $1 AND tag_b = $2;

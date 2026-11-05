-- name: UpsertTagOperatorSplit :exec
INSERT INTO tag_operator_split (candidate, parts)
VALUES (@candidate, @parts)
ON CONFLICT (candidate) DO UPDATE
SET parts = EXCLUDED.parts,
    created_at = now();

-- name: DeleteTagOperatorSplit :execrows
DELETE FROM tag_operator_split WHERE candidate = @candidate;

-- name: ListTagOperatorSplitsForNames :many
SELECT candidate, parts
FROM tag_operator_split
WHERE candidate = ANY(@candidates::text[]);

-- name: ListTagOperatorSplits :many
SELECT candidate, parts, created_at
FROM tag_operator_split
ORDER BY candidate
LIMIT @page_limit OFFSET @page_offset;

-- name: CountTagOperatorSplits :one
SELECT COUNT(*) FROM tag_operator_split;

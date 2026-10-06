-- name: InsertTagPartDimensionEvent :execrows
INSERT INTO tag_part_dimension_event
    (document_id, value, criteria, dimensional, verdict_model, policy_version)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (value, policy_version) WHERE verdict_model IS NOT NULL DO NOTHING;

-- name: LatestTagPartVerdictsForValues :many
SELECT DISTINCT ON (value)
       value, criteria, dimensional, verdict_model, policy_version, verdict_at
FROM tag_part_dimension_event
WHERE policy_version = @policy_version
  AND value = ANY(@part_values::text[])
ORDER BY value, verdict_at DESC;

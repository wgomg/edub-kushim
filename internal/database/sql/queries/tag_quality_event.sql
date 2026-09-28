-- name: InsertTagQualityEvent :execrows
INSERT INTO tag_quality_event
    (document_id, tag_name, criteria, pass, outcome, verdict_model, policy_version)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tag_name, policy_version) WHERE verdict_model IS NOT NULL DO NOTHING;

-- name: LatestTagQualityEventsForNames :many
SELECT DISTINCT ON (tag_name)
       tag_name, criteria, pass, outcome, verdict_model, policy_version, verdict_at
FROM tag_quality_event
WHERE policy_version = @policy_version
  AND tag_name = ANY(@tag_names::text[])
ORDER BY tag_name, verdict_at DESC;

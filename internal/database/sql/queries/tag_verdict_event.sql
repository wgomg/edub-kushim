-- name: InsertTagVerdictEvent :execrows
INSERT INTO tag_verdict_event
    (document_id, query, target, sim, verdict, verdict_model, policy_version)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (query, target, policy_version) WHERE verdict_model IS NOT NULL DO NOTHING;

-- name: LatestTagVerdictsForPairs :many
SELECT DISTINCT ON (query, target)
       query, target, verdict, verdict_model, policy_version, verdict_at
FROM tag_verdict_event
WHERE policy_version = @policy_version
  AND (query, target) IN (SELECT unnest(@queries::text[]), unnest(@targets::text[]))
ORDER BY query, target, verdict_at DESC;
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

-- name: TagPairEvidenceStats :many
SELECT query, target,
       COUNT(DISTINCT document_id) AS document_count,
       COUNT(*) AS event_count,
       COUNT(*) FILTER (WHERE verdict = 'same') AS same_count,
       COUNT(*) FILTER (WHERE verdict = 'variant') AS variant_count,
       COUNT(*) FILTER (WHERE verdict = 'related') AS related_count
FROM tag_verdict_event
WHERE (query, target) IN (SELECT unnest(@queries::text[]), unnest(@targets::text[]))
GROUP BY query, target;
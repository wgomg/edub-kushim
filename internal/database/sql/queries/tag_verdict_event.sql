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

-- name: TagVerdictPairsWithoutFreshVerdict :many
SELECT DISTINCT ON (tve.query, tve.target)
       tve.query, tve.target, tve.document_id, tve.sim, tve.verdict,
       tve.verdict_model, tve.policy_version, tve.verdict_at
FROM tag_verdict_event tve
WHERE NOT EXISTS (
    SELECT 1 FROM tag_verdict_event f
    WHERE f.query = tve.query AND f.target = tve.target
      AND f.policy_version = $1 AND f.verdict_model IS NOT NULL
)
ORDER BY tve.query, tve.target, tve.verdict_at DESC;

-- name: TagVerdictPairsOlderThan :many
SELECT DISTINCT ON (query, target)
       query, target, document_id, sim, verdict, verdict_model, policy_version, verdict_at
FROM tag_verdict_event
WHERE policy_version = $1 AND verdict_model IS NOT NULL AND verdict_at < $2
ORDER BY query, target, verdict_at DESC;

-- name: TagVerdictPairsByVerdict :many
SELECT DISTINCT ON (query, target)
       query, target, document_id, sim, verdict, verdict_model, policy_version, verdict_at
FROM tag_verdict_event
WHERE policy_version = $1 AND verdict_model IS NOT NULL
  AND verdict IN ('variant', 'related')
ORDER BY query, target, verdict_at DESC;

-- name: TagVerdictPairsWithMissingTags :many
SELECT DISTINCT tve.query, tve.target,
       NOT EXISTS (SELECT 1 FROM tag t WHERE t.name = tve.query) AS query_missing,
       NOT EXISTS (SELECT 1 FROM tag t WHERE t.name = tve.target) AS target_missing
FROM tag_verdict_event tve
WHERE NOT EXISTS (SELECT 1 FROM tag t WHERE t.name = tve.query)
   OR NOT EXISTS (SELECT 1 FROM tag t WHERE t.name = tve.target);

-- name: TagVerdictQueriesWithRows :many
SELECT DISTINCT query FROM tag_verdict_event
WHERE query = ANY($1::text[]) OR target = ANY($1::text[]);

-- name: LatestTagVerdictRowsForPairs :many
SELECT DISTINCT ON (query, target)
       query, target, document_id, sim, verdict, verdict_model, policy_version, verdict_at
FROM tag_verdict_event
WHERE (query, target) IN (SELECT unnest($1::text[]), unnest($2::text[]))
ORDER BY query, target, verdict_at DESC;

-- name: DocumentIDsForPairs :many
SELECT DISTINCT document_id
FROM tag_verdict_event
WHERE (query, target) IN (SELECT unnest($1::text[]), unnest($2::text[]))
ORDER BY document_id;

-- name: DeleteFreshTagVerdictsForPairs :execrows
DELETE FROM tag_verdict_event
WHERE verdict_model IS NOT NULL
  AND (query, target) IN (SELECT unnest($1::text[]), unnest($2::text[]));

-- name: UpsertFreshTagVerdict :execrows
INSERT INTO tag_verdict_event
    (document_id, query, target, sim, verdict, verdict_model, policy_version)
VALUES (@document_id, @query, @target, @sim, @verdict, @verdict_model, @policy_version)
ON CONFLICT (query, target, policy_version) WHERE verdict_model IS NOT NULL
DO UPDATE SET verdict = EXCLUDED.verdict, verdict_model = EXCLUDED.verdict_model,
              sim = EXCLUDED.sim, verdict_at = now();
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

-- name: ListRetentionStaleDeferredPartValues :many
-- the retention leg of the split re-ask pass: an abstention that has stood past the retention window; the
-- (policy_version, verdict_at) index serves both the filter and the ORDER BY, so LIMIT stops the scan
SELECT pd.value
FROM tag_part_dimension_event pd
WHERE pd.policy_version = @policy_version
  AND pd.criteria->>'defer' = 'true'
  AND pd.verdict_model IS NOT NULL
  AND pd.verdict_at < @stale_before
ORDER BY pd.verdict_at
LIMIT @page_limit;

-- name: ListYoungDeferredPartValues :many
-- the multi-compound leg, restricted to abstentions younger than the retention window (older ones are
-- already eligible above): a defer withholds more than one distinct compound among the composition events
-- judged while it was in force; the created_at bound keeps the correlated count inside the defer's own
-- lifetime instead of scanning the whole journal per row
SELECT pd.value
FROM tag_part_dimension_event pd
WHERE pd.policy_version = @policy_version
  AND pd.criteria->>'defer' = 'true'
  AND pd.verdict_model IS NOT NULL
  AND pd.verdict_at >= @stale_before
  AND (SELECT count(DISTINCT ce.candidate)
       FROM tag_composition_event ce
       WHERE ce.criteria_version = @split_criteria_version
         AND ce.created_at >= pd.verdict_at
         AND (' ' || ce.candidate || ' ') LIKE ('% ' || pd.value || ' %')) > 1
ORDER BY pd.verdict_at
LIMIT @page_limit;

-- name: DeleteDeferredPartVerdicts :execrows
-- removes the stale abstentions a re-ask supersedes; only model-backed defer rows under the current
-- policy version are ever deleted, so a decided verdict and any other generation's row stay put
DELETE FROM tag_part_dimension_event
WHERE policy_version = @policy_version
  AND value = ANY(@part_values::text[])
  AND criteria->>'defer' = 'true'
  AND verdict_model IS NOT NULL;

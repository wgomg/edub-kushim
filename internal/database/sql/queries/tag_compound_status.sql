-- name: InsertTagCompoundStatusEvent :execrows
INSERT INTO tag_compound_status_event
    (document_id, candidate, status, criteria, verdict_model, split_criteria_version)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (candidate, split_criteria_version) WHERE verdict_model IS NOT NULL DO NOTHING;

-- name: LatestTagCompoundStatusForCandidates :many
SELECT DISTINCT ON (candidate)
       candidate, status, criteria, verdict_model, split_criteria_version, verdict_at
FROM tag_compound_status_event
WHERE split_criteria_version = @split_criteria_version
  AND candidate = ANY(@candidates::text[])
ORDER BY candidate, verdict_at DESC;

-- name: ListEligibleDeferredCompoundCandidates :many
-- the split re-ask pass's compound leg: cached abstentions that have stood past the retention window
SELECT s.candidate
FROM tag_compound_status_event s
WHERE s.split_criteria_version = @split_criteria_version
  AND s.status = 'defer'
  AND s.verdict_model IS NOT NULL
  AND s.verdict_at < @stale_before
ORDER BY s.verdict_at
LIMIT @page_limit;

-- name: DeleteDeferredCompoundStatuses :execrows
-- removes the stale abstentions a re-ask supersedes; only model-backed defer rows under the current
-- criteria version are ever deleted
DELETE FROM tag_compound_status_event
WHERE split_criteria_version = @split_criteria_version
  AND candidate = ANY(@candidates::text[])
  AND status = 'defer'
  AND verdict_model IS NOT NULL;

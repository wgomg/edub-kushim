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

-- name: InsertTagCompositionEvent :exec
INSERT INTO tag_composition_event
    (document_id, candidate, parts, disposition, reason, criteria_version, model, reconstructible)
VALUES (@document_id, @candidate, @parts, @disposition, @reason, @criteria_version, @model, @reconstructible);

-- name: LatestCompositionReasonsForNames :many
SELECT DISTINCT ON (candidate) candidate, disposition, reason
FROM tag_composition_event
WHERE criteria_version = @criteria_version
  AND candidate = ANY(@candidates::text[])
ORDER BY candidate, created_at DESC;

-- name: LatestCompositionEventsForNames :many
SELECT DISTINCT ON (candidate)
       candidate, parts, disposition, reason, criteria_version, model, reconstructible, created_at
FROM tag_composition_event
WHERE candidate = ANY(@candidates::text[])
ORDER BY candidate, created_at DESC;

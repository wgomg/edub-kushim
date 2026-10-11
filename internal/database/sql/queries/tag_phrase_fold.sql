-- name: InsertTagPhraseFoldEvent :execrows
INSERT INTO tag_phrase_fold_event
    (document_id, phrase, part, anchor, criteria, fold, verdict_model, criteria_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (phrase, part, anchor, criteria_version) WHERE verdict_model IS NOT NULL DO NOTHING;

-- name: LatestTagPhraseFoldVerdictsForPhrases :many
SELECT DISTINCT ON (phrase, part, anchor)
       phrase, part, anchor, criteria, fold, verdict_model, criteria_version, verdict_at
FROM tag_phrase_fold_event
WHERE criteria_version = @criteria_version
  AND phrase = ANY(@phrases::text[])
ORDER BY phrase, part, anchor, verdict_at DESC;

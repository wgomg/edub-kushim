-- name: InsertTagCoverageEvent :execrows
INSERT INTO tag_coverage_event
    (document_id, tag_name, class, source, verdict, verdict_model, text_hash,
     criteria_version, action, provisional, rule_set_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (document_id, tag_name, text_hash, criteria_version) WHERE verdict_model IS NOT NULL DO NOTHING;

-- name: InsertTagCoverageAction :exec
INSERT INTO tag_coverage_event
    (document_id, tag_name, class, source, verdict, verdict_model, text_hash,
     criteria_version, action, review_outcome, provisional, rule_set_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: LatestTagCoverageReviewOutcome :one
SELECT review_outcome, text_hash
FROM tag_coverage_event
WHERE document_id = @document_id
  AND tag_name = @tag_name
  AND review_outcome IS NOT NULL
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: LatestTagCoverageVerdict :one
SELECT verdict, verdict_model, class, provisional, text_hash, review_outcome
FROM tag_coverage_event
WHERE document_id = @document_id
  AND tag_name = @tag_name
  AND text_hash = @text_hash
  AND criteria_version = @criteria_version
  AND verdict_model IS NOT NULL
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: LatestTagCoverageEventForPair :one
SELECT text_hash, criteria_version, source, class
FROM tag_coverage_event
WHERE document_id = @document_id
  AND tag_name = @tag_name
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: PendingCoverageNamesForDocuments :many
SELECT document_id, tag_name
FROM tag_coverage_event
WHERE document_id = ANY(@document_ids::bigint[])
  AND class = 'pending_coverage';

-- name: InsertTagCoverageEventsBulk :execrows
INSERT INTO tag_coverage_event
    (document_id, tag_name, class, source, verdict, verdict_model, text_hash,
     criteria_version, action, provisional, rule_set_version)
SELECT d.document_id, n.tag_name, @class, @source, '{}'::jsonb, NULL, '',
       @criteria_version, @action, false, @rule_set_version
FROM unnest(@document_ids::bigint[]) WITH ORDINALITY AS d(document_id, ord)
JOIN unnest(@tag_names::text[]) WITH ORDINALITY AS n(tag_name, ord) USING (ord)
ON CONFLICT DO NOTHING;

-- name: ListOpenTagCoverageFlags :many
SELECT e.id, e.document_id, e.tag_name, e.class, e.source, e.verdict, e.verdict_model,
       e.text_hash, e.criteria_version, e.action, e.review_outcome, e.provisional,
       e.rule_set_version, e.created_at, d.title AS document_title
FROM tag_coverage_event e
JOIN document d ON d.id = e.document_id
WHERE e.action IN ('flag', 'proposal') AND e.review_outcome IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM tag_coverage_event newer
      WHERE newer.document_id = e.document_id AND newer.tag_name = e.tag_name AND newer.id > e.id
  )
ORDER BY e.created_at DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountOpenTagCoverageFlags :one
SELECT COUNT(*)
FROM tag_coverage_event e
WHERE e.action IN ('flag', 'proposal') AND e.review_outcome IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM tag_coverage_event newer
      WHERE newer.document_id = e.document_id AND newer.tag_name = e.tag_name AND newer.id > e.id
  );

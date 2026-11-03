-- name: InsertTagQualityEvent :execrows
INSERT INTO tag_quality_event
    (document_id, tag_name, criteria, pass, outcome, verdict_model, policy_version)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tag_name, policy_version) WHERE verdict_model IS NOT NULL DO NOTHING;

-- name: LatestTagQualityEventsForNames :many
SELECT DISTINCT ON (e.tag_name)
       e.tag_name, e.criteria, e.pass, e.outcome, e.verdict_model, e.policy_version, e.verdict_at,
       (MIN(e.verdict_at) OVER (PARTITION BY e.tag_name))::timestamptz AS first_verdict_at,
       coalesce((SELECT MAX(f.verdict_at) FROM tag_quality_event f
                 WHERE f.tag_name = e.tag_name
                   AND f.policy_version = @policy_version
                   AND f.verdict_model IS NOT NULL), 'epoch'::timestamptz)::timestamptz AS fresh_verdict_at
FROM tag_quality_event e
WHERE e.policy_version = @policy_version
  AND e.tag_name = ANY(@tag_names::text[])
ORDER BY e.tag_name, e.verdict_at DESC;

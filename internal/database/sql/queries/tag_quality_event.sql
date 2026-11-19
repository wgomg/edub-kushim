-- name: InsertTagQualityEvent :execrows
INSERT INTO tag_quality_event
    (document_id, tag_name, criteria, pass, outcome, verdict_model, policy_version)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tag_name, policy_version) WHERE verdict_model IS NOT NULL DO NOTHING;

-- name: LatestTagQualityEventsForNames :many
SELECT DISTINCT ON (e.tag_name)
       e.tag_name, e.criteria, e.pass, e.outcome, e.verdict_model, e.policy_version, e.verdict_at,
       (MIN(e.verdict_at) OVER (PARTITION BY e.tag_name))::timestamptz AS first_verdict_at
FROM tag_quality_event e
WHERE e.policy_version = @policy_version
  AND e.tag_name = ANY(@tag_names::text[])
ORDER BY e.tag_name, e.verdict_at DESC;

-- name: LatestQualityEventsAnyVersionForNames :many
SELECT DISTINCT ON (e.tag_name) e.tag_name, e.criteria
FROM tag_quality_event e
WHERE e.tag_name = ANY(@tag_names::text[])
ORDER BY e.tag_name, e.verdict_at DESC;

-- name: KBNotSubjectRetireProbe :one
-- mirrors tags.KBNotSubjectRetire with analyzer.TagQualityCriteria.Complete(): the person_class and
-- atomic/not_form_genre presence checks keep superseded or incomplete rows from making the reconcile run
-- permanently due
SELECT EXISTS (
    SELECT 1
    FROM tag t
    JOIN LATERAL (
        SELECT e.criteria
        FROM tag_quality_event e
        WHERE e.tag_name = t.name
        ORDER BY e.verdict_at DESC
        LIMIT 1
    ) latest ON true
    WHERE t.origin = @origin::text
      AND t.accepted_at IS NULL
      AND t.standing_source <> @operator_standing::text
      AND latest.criteria ->> 'subject_matter' = 'false'
      AND coalesce(latest.criteria ->> 'defer', 'false') = 'false'
      AND latest.criteria ->> 'person_class' = ANY(@person_classes::text[])
      AND latest.criteria ->> 'atomic' IS NOT NULL
      AND latest.criteria ->> 'not_form_genre' IS NOT NULL
);

-- name: DeleteQualityEventsByName :execrows
DELETE FROM tag_quality_event
WHERE tag_name = ANY(@names::text[]);

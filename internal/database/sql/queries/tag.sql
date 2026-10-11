-- name: GetTag :one
SELECT * FROM tag WHERE id = $1;

-- name: GetTagForUpdate :one
SELECT * FROM tag WHERE id = $1 FOR UPDATE;

-- name: GetTagForUpdateByName :one
SELECT * FROM tag WHERE name = $1 FOR UPDATE;

-- name: GetTagByName :one
SELECT * FROM tag WHERE name = $1;

-- name: ListTags :many
SELECT * FROM tag ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListAllTags :many
SELECT * FROM tag ORDER BY created_at DESC;

-- name: ListPromotedTagNames :many
SELECT name FROM tag
WHERE promoted_at IS NOT NULL
ORDER BY created_at DESC;

-- name: ListStoreTagKeys :many
SELECT name AS key FROM tag
UNION
SELECT alias_name AS key FROM tag_alias;

-- name: CreateTag :one
INSERT INTO tag (name)
VALUES ($1)
ON CONFLICT (name) DO NOTHING
RETURNING id;

-- name: UpdateTag :exec
UPDATE tag SET
    name = $1
WHERE id = $2;

-- name: PromoteTags :execrows
UPDATE tag SET promoted_at = now()
WHERE id = ANY(@tag_ids::bigint[])
  AND promoted_at IS NULL;

-- name: CountTags :one
SELECT COUNT(*) FROM tag;

-- name: CountTagsByName :one
SELECT COUNT(*) FROM tag WHERE name LIKE $1;

-- name: SearchTagsByName :many
SELECT * FROM tag WHERE name LIKE $1 ORDER BY name ASC LIMIT $2 OFFSET $3;

-- name: ListAllTagsWithDocumentCount :many
SELECT t.*, COUNT(dt.document_id) AS document_count
FROM tag t
LEFT JOIN document_tag dt ON t.id = dt.tag_id
GROUP BY t.id
ORDER BY t.created_at DESC;

-- name: ListTagsWithDocumentCount :many
SELECT t.*, COUNT(dt.document_id) AS document_count
FROM tag t
LEFT JOIN document_tag dt ON t.id = dt.tag_id
GROUP BY t.id
ORDER BY t.created_at DESC LIMIT $1 OFFSET $2;

-- name: SearchTagsByNameWithDocumentCount :many
SELECT t.*, COUNT(dt.document_id) AS document_count
FROM tag t
LEFT JOIN document_tag dt ON t.id = dt.tag_id
WHERE t.name LIKE $1
GROUP BY t.id
ORDER BY t.name ASC LIMIT $2 OFFSET $3;

-- name: DeleteTag :exec
DELETE FROM tag WHERE id = $1;

-- name: MarkOrphanTags :execrows
UPDATE tag t SET orphaned_since = now()
WHERE t.orphaned_since IS NULL
  AND NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id);

-- name: ClearOrphanTagMarks :execrows
UPDATE tag t SET orphaned_since = NULL
WHERE t.orphaned_since IS NOT NULL
  AND EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id);

-- name: ClearOrphanTagMarksByTagIDs :exec
UPDATE tag SET orphaned_since = NULL
WHERE id = ANY(@tag_ids::bigint[])
  AND orphaned_since IS NOT NULL;

-- name: ListProvisionalTagsWithDocumentCount :many
SELECT t.id, t.name, coalesce(t.created_at, now()) AS created_at,
       (SELECT COUNT(*) FROM document_tag dt
        JOIN document d ON d.id = dt.document_id
        WHERE dt.tag_id = t.id AND d.deleted_at IS NULL) AS document_count
FROM tag t
LEFT JOIN LATERAL (
    SELECT e.criteria, e.pass, e.outcome
    FROM tag_quality_event e
    WHERE e.tag_name = t.name AND e.policy_version = @policy_version
    ORDER BY e.verdict_at DESC
    LIMIT 1
) qe ON true
WHERE t.promoted_at IS NULL
  AND EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id)
  AND (@subject_matter_state::text = ''
       OR coalesce(qe.criteria ->> 'subject_matter', 'absent') = @subject_matter_state::text)
  AND (@dimension_state::text = ''
       OR coalesce(qe.criteria ->> 'dimension', 'absent') = @dimension_state::text)
  AND (@not_person_state::text = ''
       OR coalesce(qe.criteria ->> 'not_person', 'absent') = @not_person_state::text)
  AND (@atomic_state::text = ''
       OR coalesce(qe.criteria ->> 'atomic', 'absent') = @atomic_state::text)
  AND (@not_form_genre_state::text = ''
       OR coalesce(qe.criteria ->> 'not_form_genre', 'absent') = @not_form_genre_state::text)
  AND (@defer_state::text = ''
       OR coalesce(qe.criteria ->> 'defer', 'absent') = @defer_state::text)
  -- legacy rows carry no outcome: derive it the same way the ui's verdict
  -- outcome does; the pass guard keeps unjudged tags in the 'none' class
  AND (@verdict_state::text = ''
       OR (@verdict_state::text = 'none' AND qe.pass IS NULL)
       OR (@verdict_state::text <> 'none'
           AND qe.pass IS NOT NULL
           AND coalesce(qe.outcome,
                        CASE WHEN qe.pass THEN 'admit'
                             WHEN (qe.criteria ->> 'defer')::boolean IS TRUE THEN 'defer'
                             ELSE 'reject' END) = @verdict_state::text))
ORDER BY coalesce(t.created_at, now()) DESC, t.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountProvisionalTags :one
SELECT COUNT(*) FROM tag t
LEFT JOIN LATERAL (
    SELECT e.criteria, e.pass, e.outcome
    FROM tag_quality_event e
    WHERE e.tag_name = t.name AND e.policy_version = @policy_version
    ORDER BY e.verdict_at DESC
    LIMIT 1
) qe ON true
WHERE t.promoted_at IS NULL
  AND EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id)
  AND (@subject_matter_state::text = ''
       OR coalesce(qe.criteria ->> 'subject_matter', 'absent') = @subject_matter_state::text)
  AND (@dimension_state::text = ''
       OR coalesce(qe.criteria ->> 'dimension', 'absent') = @dimension_state::text)
  AND (@not_person_state::text = ''
       OR coalesce(qe.criteria ->> 'not_person', 'absent') = @not_person_state::text)
  AND (@atomic_state::text = ''
       OR coalesce(qe.criteria ->> 'atomic', 'absent') = @atomic_state::text)
  AND (@not_form_genre_state::text = ''
       OR coalesce(qe.criteria ->> 'not_form_genre', 'absent') = @not_form_genre_state::text)
  AND (@defer_state::text = ''
       OR coalesce(qe.criteria ->> 'defer', 'absent') = @defer_state::text)
  -- legacy rows carry no outcome: derive it the same way the ui's verdict
  -- outcome does; the pass guard keeps unjudged tags in the 'none' class
  AND (@verdict_state::text = ''
       OR (@verdict_state::text = 'none' AND qe.pass IS NULL)
       OR (@verdict_state::text <> 'none'
           AND qe.pass IS NOT NULL
           AND coalesce(qe.outcome,
                        CASE WHEN qe.pass THEN 'admit'
                             WHEN (qe.criteria ->> 'defer')::boolean IS TRUE THEN 'defer'
                             ELSE 'reject' END) = @verdict_state::text));

-- name: ListProvisionalTagsOlderThan :many
SELECT t.id, t.name, t.created_at FROM tag t
WHERE t.promoted_at IS NULL
  AND t.created_at < @cutoff::timestamptz
ORDER BY t.created_at;

-- name: FirstDocumentIDForTag :one
SELECT dt.document_id FROM document_tag dt
JOIN document d ON d.id = dt.document_id
WHERE dt.tag_id = $1 AND d.deleted_at IS NULL
ORDER BY dt.document_id
LIMIT 1;

-- name: ListOrphanTags :many
SELECT t.name FROM tag t
WHERE t.orphaned_since IS NOT NULL
  AND t.orphaned_since < @orphaned_before::timestamptz
  AND NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id)
ORDER BY t.name;

-- name: DeleteOrphanTags :many
DELETE FROM tag t
WHERE t.orphaned_since IS NOT NULL
  AND t.orphaned_since < @orphaned_before::timestamptz
  AND NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id)
RETURNING t.name;

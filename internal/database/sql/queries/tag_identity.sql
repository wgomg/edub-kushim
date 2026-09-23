-- name: InsertTagAlias :execrows
INSERT INTO tag_alias (alias_name, canonical_tag_id, rule_set_version)
VALUES ($1, $2, $3)
ON CONFLICT (alias_name) DO NOTHING;

-- name: GetTagAliasByName :one
SELECT * FROM tag_alias WHERE alias_name = $1;

-- name: ResolveTagAliasNames :many
SELECT ta.alias_name, t.name AS canonical_name
FROM tag_alias ta
JOIN tag t ON t.id = ta.canonical_tag_id
WHERE ta.alias_name = ANY(@names::text[]);

-- name: RetargetTagAliases :execrows
UPDATE tag_alias SET canonical_tag_id = @new_tag_id WHERE canonical_tag_id = @old_tag_id;

-- name: InsertTagIdentityEvent :execrows
INSERT INTO tag_identity_event (kind, candidate, partner, criteria, verdict, model, rule_set_version)
VALUES (@kind, @candidate, @partner, @criteria, @verdict, @model, @rule_set_version)
ON CONFLICT (kind, candidate, COALESCE(partner, ''), rule_set_version) WHERE verdict IS NULL DO NOTHING;

-- name: LatestTagIdentityEventsForCandidates :many
SELECT DISTINCT ON (kind, candidate, COALESCE(partner, ''))
       id, kind, candidate, partner, criteria, verdict, model, rule_set_version, created_at
FROM tag_identity_event
WHERE rule_set_version = @rule_set_version
  AND verdict IS NOT NULL
  AND candidate = ANY(@candidates::text[])
ORDER BY kind, candidate, COALESCE(partner, ''), created_at DESC;

-- name: ListPendingTagIdentityEvents :many
SELECT * FROM tag_identity_event
WHERE verdict IS NULL
ORDER BY created_at
LIMIT @page_limit OFFSET @page_offset;

-- name: CountPendingTagIdentityEvents :one
SELECT COUNT(*) FROM tag_identity_event WHERE verdict IS NULL;

-- name: ResolveTagIdentityEvent :execrows
UPDATE tag_identity_event
SET verdict = @verdict,
    model = @model,
    criteria = @criteria
WHERE id = @id AND verdict IS NULL;

-- name: PendingTagIdentityCountsByNames :many
SELECT name, COUNT(*) AS pending_count
FROM (
    SELECT candidate AS name FROM tag_identity_event WHERE verdict IS NULL AND candidate = ANY(@names::text[])
    UNION ALL
    SELECT partner AS name FROM tag_identity_event WHERE verdict IS NULL AND partner = ANY(@names::text[])
) pending
GROUP BY name;

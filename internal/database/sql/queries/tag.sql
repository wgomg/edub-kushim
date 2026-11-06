-- name: GetTag :one
SELECT * FROM tag WHERE id = $1;

-- name: GetTagForUpdate :one
SELECT * FROM tag WHERE id = $1 FOR UPDATE;

-- name: GetTagForUpdateByName :one
SELECT * FROM tag WHERE name = $1 FOR UPDATE;

-- name: GetTagByName :one
SELECT * FROM tag WHERE name = $1;

-- name: ListTagIDsByNames :many
SELECT id, name FROM tag WHERE name = ANY(@names::text[]);

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
    name = $1,
    modified_at = now()
WHERE id = $2;

-- name: PromoteTags :execrows
UPDATE tag SET
    promoted_at = now(),
    demoted_at = NULL,
    demoted_reason = NULL,
    accepted_at = NULL,
    modified_at = now()
WHERE id = ANY(@tag_ids::bigint[])
  AND promoted_at IS NULL;

-- name: DemoteTags :execrows
UPDATE tag SET
    promoted_at = NULL,
    demoted_at = now(),
    demoted_reason = @reason::text,
    modified_at = now()
WHERE id = ANY(@tag_ids::bigint[])
  AND promoted_at IS NOT NULL
  AND NOT seeded;

-- name: AcceptTag :exec
UPDATE tag SET accepted_at = now()
WHERE id = $1
  AND promoted_at IS NULL
  AND accepted_at IS NULL;

-- name: ListPromotedTagsForDemotion :many
SELECT id, name, promoted_at, created_at FROM tag
WHERE promoted_at IS NOT NULL
  AND NOT seeded
ORDER BY promoted_at;

-- name: CountTags :one
SELECT COUNT(*) FROM tag;

-- name: CountTagsByName :one
SELECT COUNT(*) FROM tag t
WHERE (t.name ILIKE $1
    OR EXISTS (SELECT 1 FROM tag_alias ta WHERE ta.canonical_tag_id = t.id AND ta.alias_name ILIKE $1));

-- name: SearchTagsByName :many
SELECT * FROM tag WHERE name ILIKE $1 ORDER BY name ASC LIMIT $2 OFFSET $3;

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
WHERE (t.name ILIKE $1
    OR EXISTS (SELECT 1 FROM tag_alias ta WHERE ta.canonical_tag_id = t.id AND ta.alias_name ILIKE $1))
GROUP BY t.id
ORDER BY t.name ASC LIMIT $2 OFFSET $3;

-- name: DeleteTag :exec
DELETE FROM tag WHERE id = $1;

-- name: MarkOrphanTags :execrows
UPDATE tag t SET orphaned_since = now()
WHERE t.orphaned_since IS NULL
  AND NOT t.seeded
  AND NOT EXISTS (
      SELECT 1 FROM document_tag dt
      JOIN document d ON d.id = dt.document_id
      WHERE dt.tag_id = t.id AND d.deleted_at IS NULL
  );

-- name: ClearOrphanTagMarks :execrows
UPDATE tag t SET orphaned_since = NULL
WHERE t.orphaned_since IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM document_tag dt
      JOIN document d ON d.id = dt.document_id
      WHERE dt.tag_id = t.id AND d.deleted_at IS NULL
  );

-- name: ClearOrphanTagMarksByTagIDs :exec
UPDATE tag SET orphaned_since = NULL
WHERE id = ANY(@tag_ids::bigint[])
  AND orphaned_since IS NOT NULL;

-- name: ListProvisionalTagsWithDocumentCount :many
SELECT v.id, v.name, v.created_at, v.demoted_at, v.demoted_reason, v.accepted_at,
       v.fresh_verdict_at, v.document_count
FROM (
    SELECT t.id, t.name, coalesce(t.created_at, now()) AS created_at,
           t.demoted_at, t.demoted_reason, t.accepted_at,
           coalesce((SELECT MAX(f.verdict_at) FROM tag_quality_event f
                     WHERE f.tag_name = t.name AND f.policy_version = @policy_version
                       AND f.verdict_model IS NOT NULL), 'epoch'::timestamptz)::timestamptz AS fresh_verdict_at,
           (SELECT COUNT(*) FROM document_tag dt
            JOIN document d ON d.id = dt.document_id
            WHERE dt.tag_id = t.id AND d.deleted_at IS NULL) AS document_count,
           qe.pass IS NULL AS no_verdict,
           coalesce(qe.outcome,
                    CASE WHEN qe.pass THEN 'admit'
                         WHEN (qe.criteria ->> 'defer')::boolean IS TRUE THEN 'defer'
                         ELSE 'reject' END) AS outcome_class,
           (t.accepted_at IS NOT NULL
            AND t.accepted_at > coalesce((SELECT MAX(f.verdict_at) FROM tag_quality_event f
                                          WHERE f.tag_name = t.name
                                            AND f.policy_version = @policy_version
                                            AND f.verdict_model IS NOT NULL),
                                         'epoch'::timestamptz)) AS accepted,
           flag.identity_pending, flag.identity_hold, flag.split_hold,
           flag.degraded_unversioned, flag.composition_blocks, flag.demoted
    FROM tag t
    LEFT JOIN LATERAL (
        SELECT e.criteria, e.pass, e.outcome
        FROM tag_quality_event e
        WHERE e.tag_name = t.name AND e.policy_version = @policy_version
        ORDER BY e.verdict_at DESC
        LIMIT 1
    ) qe ON true
    -- the journal flags are read only by the journal-backed state values and by the facet counts; the
    -- journal_flags gate keeps them out of the unfiltered plan and out of all the legacy verdict-only
    -- paths; each EXISTS mirrors the Go helper the matching response pill reads, so a drift in just
    -- one cannot possibly diverge from the other: identity_pending mirrors PendingTagIdentityCountsByNames,
    -- identity_hold mirrors ListIdentityHeldNames plus the refusal pairing in IdentityHeldNames, and
    -- split_hold mirrors CompositionReview's compositionHolds reasons; the composition_blocks mirrors
    -- CompositionActionFor returning a non-empty action, which is what isBareDeferral tests; a relational
    -- keep resting on the promoted-parts proxy is store-maturity pending, not a review obligation, so the
    -- split_hold predicate excludes it exactly as proxyRelationalKeep does in the Go read
    LEFT JOIN LATERAL (
        SELECT
            CASE WHEN @journal_flags::boolean THEN
                EXISTS (SELECT 1 FROM tag_identity_event ie
                        WHERE ie.verdict IS NULL
                          AND (ie.candidate = t.name OR ie.partner = t.name))
            ELSE false END AS identity_pending,
            CASE WHEN @journal_flags::boolean THEN
                EXISTS (SELECT 1 FROM (
                            SELECT DISTINCT ON (ie.candidate, ie.kind)
                                   ie.kind, ie.verdict, ie.criteria ->> 'proposed_fold' AS anchor
                            FROM tag_identity_event ie
                            WHERE ie.rule_set_version = @identity_rule_set_version
                              AND ie.verdict IS NOT NULL
                              AND ie.kind = ANY(@identity_hold_kinds::text[])
                              AND ie.candidate = t.name
                              AND EXISTS (SELECT 1 FROM tag anchor_tag
                                          WHERE anchor_tag.name = ie.criteria ->> 'proposed_fold')
                            ORDER BY ie.candidate, ie.kind, ie.created_at DESC
                        ) held
                        WHERE held.kind = 'fold_gate' AND held.verdict = 'rejected'
                           OR held.kind = 'base_form' AND held.verdict = 'stand')
            ELSE false END AS identity_hold,
            CASE WHEN @journal_flags::boolean THEN
                EXISTS (SELECT 1 FROM (
                            SELECT DISTINCT ON (ce.candidate) ce.reason, ce.reading
                            FROM tag_composition_event ce
                            WHERE ce.criteria_version = @split_criteria_version
                              AND ce.candidate = t.name
                            ORDER BY ce.candidate, ce.created_at DESC
                        ) held
                        WHERE held.reason = ANY(@composition_hold_reasons::text[])
                          AND NOT (held.reason = 'relational_keep'
                                   AND coalesce(held.reading ->> 'source', '') = 'proxy'))
            ELSE false END AS split_hold,
            CASE WHEN @journal_flags::boolean THEN
                EXISTS (SELECT 1 FROM (
                            SELECT DISTINCT ON (ce.candidate) ce.reason
                            FROM tag_composition_event ce
                            WHERE ce.candidate = t.name
                            ORDER BY ce.candidate, ce.created_at DESC
                        ) latest
                        WHERE latest.reason = 'judge_unavailable')
            ELSE false END AS degraded_unversioned,
            CASE WHEN @journal_flags::boolean THEN
                EXISTS (SELECT 1 FROM (
                            SELECT DISTINCT ON (ce.candidate) ce.reason, ce.disposition
                            FROM tag_composition_event ce
                            WHERE ce.candidate = t.name
                            ORDER BY ce.candidate, ce.created_at DESC
                        ) latest
                        WHERE latest.disposition = 'drop'
                           OR latest.reason = ANY(@composition_hold_reasons::text[]))
            ELSE false END AS composition_blocks,
            t.demoted_at IS NOT NULL AS demoted
    ) flag ON true
    WHERE t.promoted_at IS NULL
      AND EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id)
      AND (@name_prefix::text = '' OR t.name ILIKE @name_prefix::text || '%')
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
) v
  -- legacy rows carry no outcome: derive it the same way the ui's verdict outcome does; the pass guard
  -- keeps unjudged tags in the 'none' class; an operator acceptance newer than the tag's fresh verdict
  -- waives the deferral, so the tag reads as admitted
WHERE (@verdict_state::text = ''
       OR (@verdict_state::text = 'none' AND v.no_verdict)
       OR (@verdict_state::text <> 'none'
           AND NOT v.no_verdict
           AND CASE WHEN v.accepted AND v.outcome_class = 'defer' THEN 'admit'
                    ELSE v.outcome_class END = @verdict_state::text))
  -- state values are alternatives, so one matching flag is enough; the accepted class is its own state,
  -- so the verdict classes exclude an active acceptance
  AND (coalesce(cardinality(@states::text[]), 0) = 0
       OR ('no_verdict' = ANY(@states::text[]) AND v.no_verdict)
       OR ('admitted' = ANY(@states::text[])
           AND NOT v.no_verdict AND NOT v.accepted AND v.outcome_class = 'admit')
       OR ('deferred' = ANY(@states::text[])
           AND NOT v.no_verdict AND NOT v.accepted AND v.outcome_class = 'defer')
       OR ('rejected' = ANY(@states::text[])
           AND NOT v.no_verdict AND NOT v.accepted AND v.outcome_class = 'reject')
       OR ('accepted' = ANY(@states::text[]) AND v.accepted)
       OR ('identity_pending' = ANY(@states::text[]) AND v.identity_pending)
       OR ('identity_hold' = ANY(@states::text[]) AND v.identity_hold)
       OR ('split_hold' = ANY(@states::text[]) AND v.split_hold)
       OR ('degraded' = ANY(@states::text[]) AND v.degraded_unversioned AND NOT v.split_hold)
       OR ('demoted' = ANY(@states::text[]) AND v.demoted)
       OR ('needs_decision' = ANY(@states::text[])
           AND NOT v.no_verdict AND NOT v.accepted AND v.outcome_class = 'defer'
           AND NOT v.demoted AND NOT v.identity_hold AND NOT v.composition_blocks)
       OR ('unblocked' = ANY(@states::text[])
           AND ((NOT v.no_verdict AND NOT v.accepted AND v.outcome_class = 'admit')
                OR v.accepted)
           AND NOT v.identity_pending AND NOT v.identity_hold AND NOT v.split_hold
           AND NOT v.degraded_unversioned AND NOT v.demoted))
-- one boolean parameter cannot be spliced into an ORDER BY, so each direction gets its own key and the
-- inactive arm collapses to NULL for every row, leaving the active direction to decide the order
ORDER BY CASE WHEN @oldest_first::boolean THEN v.created_at END ASC,
         CASE WHEN @oldest_first::boolean THEN v.id END ASC,
         CASE WHEN NOT @oldest_first::boolean THEN v.created_at END DESC,
         CASE WHEN NOT @oldest_first::boolean THEN v.id END DESC
LIMIT @page_limit OFFSET @page_offset;

-- counts the provisional queue page total and, when journal flags are requested, the per-state review
-- facet counts; the total filter group must mirror exactly the where clause group of the list query,
-- while the facet columns count the name and rubric base set under those same base filters, and read
-- zero when journal flags are off
-- name: CountProvisionalTags :one
SELECT
    count(*) FILTER (WHERE
        (@verdict_state::text = ''
         OR (@verdict_state::text = 'none' AND v.no_verdict)
         OR (@verdict_state::text <> 'none'
             AND NOT v.no_verdict
             AND CASE WHEN v.accepted AND v.outcome_class = 'defer' THEN 'admit'
                      ELSE v.outcome_class END = @verdict_state::text))
        AND (coalesce(cardinality(@states::text[]), 0) = 0
             OR ('no_verdict' = ANY(@states::text[]) AND v.no_verdict)
             OR ('admitted' = ANY(@states::text[])
                 AND NOT v.no_verdict AND NOT v.accepted AND v.outcome_class = 'admit')
             OR ('deferred' = ANY(@states::text[])
                 AND NOT v.no_verdict AND NOT v.accepted AND v.outcome_class = 'defer')
             OR ('rejected' = ANY(@states::text[])
                 AND NOT v.no_verdict AND NOT v.accepted AND v.outcome_class = 'reject')
             OR ('accepted' = ANY(@states::text[]) AND v.accepted)
             OR ('identity_pending' = ANY(@states::text[]) AND v.identity_pending)
             OR ('identity_hold' = ANY(@states::text[]) AND v.identity_hold)
             OR ('split_hold' = ANY(@states::text[]) AND v.split_hold)
             OR ('degraded' = ANY(@states::text[])
                 AND v.degraded_unversioned AND NOT v.split_hold)
             OR ('demoted' = ANY(@states::text[]) AND v.demoted)
             OR ('needs_decision' = ANY(@states::text[])
                 AND NOT v.no_verdict AND NOT v.accepted AND v.outcome_class = 'defer'
                 AND NOT v.demoted AND NOT v.identity_hold AND NOT v.composition_blocks)
             OR ('unblocked' = ANY(@states::text[])
                 AND ((NOT v.no_verdict AND NOT v.accepted AND v.outcome_class = 'admit')
                      OR v.accepted)
                 AND NOT v.identity_pending AND NOT v.identity_hold AND NOT v.split_hold
                 AND NOT v.degraded_unversioned AND NOT v.demoted))) AS total,
    count(*) FILTER (WHERE v.no_verdict) AS no_verdict,
    count(*) FILTER (WHERE NOT v.no_verdict AND NOT v.accepted
                          AND v.outcome_class = 'admit') AS admitted,
    count(*) FILTER (WHERE NOT v.no_verdict AND NOT v.accepted
                          AND v.outcome_class = 'defer') AS deferred,
    count(*) FILTER (WHERE NOT v.no_verdict AND NOT v.accepted
                          AND v.outcome_class = 'reject') AS rejected,
    count(*) FILTER (WHERE v.accepted) AS accepted,
    count(*) FILTER (WHERE v.identity_pending) AS identity_pending,
    count(*) FILTER (WHERE v.identity_hold) AS identity_hold,
    count(*) FILTER (WHERE v.split_hold) AS split_hold,
    count(*) FILTER (WHERE v.degraded_unversioned AND NOT v.split_hold) AS degraded,
    count(*) FILTER (WHERE v.demoted) AS demoted,
    count(*) FILTER (WHERE NOT v.no_verdict AND NOT v.accepted
                          AND v.outcome_class = 'defer'
                          AND NOT v.demoted AND NOT v.identity_hold
                          AND NOT v.composition_blocks) AS needs_decision,
    count(*) FILTER (WHERE ((NOT v.no_verdict AND NOT v.accepted
                             AND v.outcome_class = 'admit') OR v.accepted)
                          AND NOT v.identity_pending AND NOT v.identity_hold
                          AND NOT v.split_hold AND NOT v.degraded_unversioned
                          AND NOT v.demoted) AS unblocked
FROM (
    SELECT qe.pass IS NULL AS no_verdict,
           coalesce(qe.outcome,
                    CASE WHEN qe.pass THEN 'admit'
                         WHEN (qe.criteria ->> 'defer')::boolean IS TRUE THEN 'defer'
                         ELSE 'reject' END) AS outcome_class,
           (t.accepted_at IS NOT NULL
            AND t.accepted_at > coalesce((SELECT MAX(f.verdict_at) FROM tag_quality_event f
                                          WHERE f.tag_name = t.name
                                            AND f.policy_version = @policy_version
                                            AND f.verdict_model IS NOT NULL),
                                         'epoch'::timestamptz)) AS accepted,
           flag.identity_pending, flag.identity_hold, flag.split_hold,
           flag.degraded_unversioned, flag.composition_blocks, flag.demoted
    FROM tag t
    LEFT JOIN LATERAL (
        SELECT e.criteria, e.pass, e.outcome
        FROM tag_quality_event e
        WHERE e.tag_name = t.name AND e.policy_version = @policy_version
        ORDER BY e.verdict_at DESC
        LIMIT 1
    ) qe ON true
    LEFT JOIN LATERAL (
        SELECT
            CASE WHEN @journal_flags::boolean THEN
                EXISTS (SELECT 1 FROM tag_identity_event ie
                        WHERE ie.verdict IS NULL
                          AND (ie.candidate = t.name OR ie.partner = t.name))
            ELSE false END AS identity_pending,
            CASE WHEN @journal_flags::boolean THEN
                EXISTS (SELECT 1 FROM (
                            SELECT DISTINCT ON (ie.candidate, ie.kind)
                                   ie.kind, ie.verdict, ie.criteria ->> 'proposed_fold' AS anchor
                            FROM tag_identity_event ie
                            WHERE ie.rule_set_version = @identity_rule_set_version
                              AND ie.verdict IS NOT NULL
                              AND ie.kind = ANY(@identity_hold_kinds::text[])
                              AND ie.candidate = t.name
                              AND EXISTS (SELECT 1 FROM tag anchor_tag
                                          WHERE anchor_tag.name = ie.criteria ->> 'proposed_fold')
                            ORDER BY ie.candidate, ie.kind, ie.created_at DESC
                        ) held
                        WHERE held.kind = 'fold_gate' AND held.verdict = 'rejected'
                           OR held.kind = 'base_form' AND held.verdict = 'stand')
            ELSE false END AS identity_hold,
            CASE WHEN @journal_flags::boolean THEN
                EXISTS (SELECT 1 FROM (
                            SELECT DISTINCT ON (ce.candidate) ce.reason, ce.reading
                            FROM tag_composition_event ce
                            WHERE ce.criteria_version = @split_criteria_version
                              AND ce.candidate = t.name
                            ORDER BY ce.candidate, ce.created_at DESC
                        ) held
                        WHERE held.reason = ANY(@composition_hold_reasons::text[])
                          AND NOT (held.reason = 'relational_keep'
                                   AND coalesce(held.reading ->> 'source', '') = 'proxy'))
            ELSE false END AS split_hold,
            CASE WHEN @journal_flags::boolean THEN
                EXISTS (SELECT 1 FROM (
                            SELECT DISTINCT ON (ce.candidate) ce.reason
                            FROM tag_composition_event ce
                            WHERE ce.candidate = t.name
                            ORDER BY ce.candidate, ce.created_at DESC
                        ) latest
                        WHERE latest.reason = 'judge_unavailable')
            ELSE false END AS degraded_unversioned,
            CASE WHEN @journal_flags::boolean THEN
                EXISTS (SELECT 1 FROM (
                            SELECT DISTINCT ON (ce.candidate) ce.reason, ce.disposition
                            FROM tag_composition_event ce
                            WHERE ce.candidate = t.name
                            ORDER BY ce.candidate, ce.created_at DESC
                        ) latest
                        WHERE latest.disposition = 'drop'
                           OR latest.reason = ANY(@composition_hold_reasons::text[]))
            ELSE false END AS composition_blocks,
            t.demoted_at IS NOT NULL AS demoted
    ) flag ON true
    WHERE t.promoted_at IS NULL
      AND EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id)
      AND (@name_prefix::text = '' OR t.name ILIKE @name_prefix::text || '%')
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
) v;

-- name: ListProvisionalTagsOlderThan :many
SELECT t.id, t.name, t.created_at, t.accepted_at FROM tag t
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
  AND NOT t.seeded
  AND NOT EXISTS (
      SELECT 1 FROM document_tag dt
      JOIN document d ON d.id = dt.document_id
      WHERE dt.tag_id = t.id AND d.deleted_at IS NULL
  )
ORDER BY t.name;

-- name: DeleteOrphanTags :many
DELETE FROM tag t
WHERE t.orphaned_since IS NOT NULL
  AND t.orphaned_since < @orphaned_before::timestamptz
  AND NOT t.seeded
  AND NOT EXISTS (
      SELECT 1 FROM document_tag dt
      JOIN document d ON d.id = dt.document_id
      WHERE dt.tag_id = t.id AND d.deleted_at IS NULL
  )
RETURNING t.name;

-- name: ListRetentionCandidates :many
SELECT t.id, t.name, t.created_at
FROM tag t
WHERE t.promoted_at IS NULL
  AND NOT t.seeded
  AND t.created_at < @cutoff::timestamptz
  AND NOT EXISTS (SELECT 1 FROM tag_alias ta WHERE ta.canonical_tag_id = t.id)
ORDER BY t.created_at;

-- name: DeleteRetentionTags :many
DELETE FROM tag t
WHERE t.id = ANY(@tag_ids::bigint[])
  AND NOT t.seeded
  AND t.promoted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM tag_alias ta WHERE ta.canonical_tag_id = t.id)
RETURNING t.name;

-- the reconcile opening gate's probes: each answers whether its arm's work set is non-empty, mirroring the
-- arm query beside it, so a signal-clear pass can report zero work without building the store view

-- name: ListStoredCompoundCandidates :many
SELECT name, promoted_at FROM tag
WHERE NOT seeded
ORDER BY name;

-- name: OrphanTagProbe :one
SELECT EXISTS (
    SELECT 1 FROM tag t
    WHERE t.orphaned_since IS NOT NULL
      AND t.orphaned_since < @orphaned_before::timestamptz
      AND NOT t.seeded
      AND NOT EXISTS (
          SELECT 1 FROM document_tag dt
          JOIN document d ON d.id = dt.document_id
          WHERE dt.tag_id = t.id AND d.deleted_at IS NULL
      )
);

-- name: RetentionTagProbe :one
SELECT EXISTS (
    SELECT 1 FROM tag t
    WHERE t.promoted_at IS NULL
      AND NOT t.seeded
      AND t.created_at < @cutoff::timestamptz
      AND NOT EXISTS (SELECT 1 FROM tag_alias ta WHERE ta.canonical_tag_id = t.id)
);

-- name: ProvisionalTagProbe :one
SELECT EXISTS (
    SELECT 1 FROM tag t
    WHERE t.promoted_at IS NULL
      AND t.created_at < @cutoff::timestamptz
);

-- name: PromotedTagMassDecayProbe :one
SELECT EXISTS (
    SELECT 1 FROM tag t
    WHERE t.promoted_at IS NOT NULL
      AND NOT t.seeded
      AND (sqlc.narg('demoted_before')::timestamptz IS NULL
           OR coalesce(t.created_at, now()) <= sqlc.narg('demoted_before')::timestamptz)
      AND (SELECT COUNT(*)
           FROM document_tag dt
           JOIN document d ON d.id = dt.document_id
           WHERE dt.tag_id = t.id AND d.deleted_at IS NULL) < @floor::bigint
);

-- name: PromotedTagRejudgeProbe :one
SELECT EXISTS (
    SELECT 1 FROM tag t
    WHERE t.promoted_at IS NOT NULL
      AND NOT t.seeded
      AND NOT EXISTS (
          SELECT 1 FROM tag_quality_event e
          WHERE e.tag_name = t.name AND e.policy_version = @policy_version::text
      )
);

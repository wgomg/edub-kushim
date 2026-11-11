-- +goose Up
-- one decision of record per rule-set version, candidate, and hold kind: the latest judge row, with
-- refusals an operator separation already adjudicated for the same fold dropped. The queue's identity_hold
-- flag and the Go hold lookup both read this view, so the operator-precedence rule lives in one place and
-- the two readers cannot drift apart.
CREATE VIEW tag_identity_hold_decision AS
SELECT DISTINCT ON (ie.rule_set_version, ie.candidate, ie.kind)
       ie.rule_set_version,
       ie.candidate,
       ie.kind,
       ie.verdict,
       (ie.criteria ->> 'proposed_fold')::text AS anchor,
       (COALESCE(ie.criteria ->> 'fold_class', ''))::text AS fold_class,
       ie.created_at
FROM tag_identity_event ie
WHERE ie.verdict IS NOT NULL
  AND NOT (ie.verdict IN ('rejected', 'stand')
           AND EXISTS (SELECT 1 FROM tag_identity_event sep
                       WHERE sep.candidate = ie.candidate
                         AND sep.kind = ie.kind
                         AND sep.verdict = 'separate'
                         AND sep.model = 'operator'
                         AND (sep.criteria ->> 'proposed_fold') = (ie.criteria ->> 'proposed_fold')))
ORDER BY ie.rule_set_version, ie.candidate, ie.kind, ie.created_at DESC;

-- +goose Down
DROP VIEW IF EXISTS tag_identity_hold_decision;

-- +goose Up
-- an operator split is a standing decision, not model evidence: it must outlive re-emissions and
-- criteria-version bumps, so it lives in its own table rather than the version-filtered journal
CREATE TABLE tag_operator_split (
    candidate TEXT PRIMARY KEY,
    parts TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- backfill the operator splits already performed: the latest operator-initiated journal row per
-- candidate decides, and only a split leaves a rule (a later operator keep revokes it)
INSERT INTO tag_operator_split (candidate, parts, created_at)
SELECT latest.candidate,
       ARRAY(SELECT jsonb_array_elements_text(latest.parts)),
       latest.created_at
FROM (
    SELECT DISTINCT ON (candidate) candidate, parts, disposition, created_at
    FROM tag_composition_event
    WHERE reason IN ('operator', 'operator_keep')
    ORDER BY candidate, created_at DESC
) AS latest
WHERE latest.disposition = 'split';

-- +goose Down
DROP TABLE tag_operator_split;

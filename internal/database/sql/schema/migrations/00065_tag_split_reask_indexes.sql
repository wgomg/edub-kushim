-- +goose Up
-- the split re-ask pass reads its retention leg with policy_version and orders by verdict_at, which the
-- value-keyed fresh index cannot serve; this one lets LIMIT stop the scan instead of filtering the whole
-- deferred set
CREATE INDEX idx_tag_part_dimension_event_policy_verdict_at
    ON tag_part_dimension_event (policy_version, verdict_at);

-- the re-ask pass's multi-compound leg counts composition events judged while a defer was in force; the
-- criteria_version + created_at range bound is what keeps that count off a full journal scan
CREATE INDEX idx_tag_composition_event_criteria_created_at
    ON tag_composition_event (criteria_version, created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_tag_composition_event_criteria_created_at;
DROP INDEX IF EXISTS idx_tag_part_dimension_event_policy_verdict_at;

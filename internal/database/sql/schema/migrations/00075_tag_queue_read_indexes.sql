-- +goose Up
-- version 74 is skipped because a local dev database already records a different version 74, and goose
-- tracks migrations by number, so reusing it would silently skip these indexes there
-- the provisional queue probes pending identity events by candidate and by partner; these two partial
-- indexes answer each side of the OR without heap re-checks of verdict IS NULL
CREATE INDEX idx_tag_identity_event_pending_candidate
    ON tag_identity_event (candidate) WHERE verdict IS NULL;
CREATE INDEX idx_tag_identity_event_pending_partner
    ON tag_identity_event (partner) WHERE verdict IS NULL;

-- the hold read groups a candidate's decided events by rule-set version and by kind, ordered in time;
-- this index matches that order so the per-candidate path needs no sort node, and the tiny separation
-- index serves the operator-precedence check the hold view runs per decision
CREATE INDEX idx_tag_identity_event_decided
    ON tag_identity_event (candidate, rule_set_version, kind, created_at DESC)
    WHERE verdict IS NOT NULL;
CREATE INDEX idx_tag_identity_event_operator_separation
    ON tag_identity_event (candidate, kind)
    WHERE verdict = 'separate' AND model = 'operator';

-- +goose Down
DROP INDEX IF EXISTS idx_tag_identity_event_operator_separation;
DROP INDEX IF EXISTS idx_tag_identity_event_decided;
DROP INDEX IF EXISTS idx_tag_identity_event_pending_partner;
DROP INDEX IF EXISTS idx_tag_identity_event_pending_candidate;

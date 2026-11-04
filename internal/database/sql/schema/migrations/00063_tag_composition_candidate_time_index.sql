-- +goose Up
-- the provisional queue's latest-composition read orders a candidate's events by time while filtering
-- criteria_version separately, which the (candidate, criteria_version, created_at) existing composite
-- index cannot serve; this one can, so the DISTINCT ON reads stay in index order without a sort
CREATE INDEX idx_tag_composition_event_candidate_time
    ON tag_composition_event (candidate, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_tag_composition_event_candidate_time;

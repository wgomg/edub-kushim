-- +goose Up
-- operator standing must outlive criteria-version bumps: the demotion sweep records fresh verdicts as
-- evidence but never revokes a promotion an operator granted, so the provenance of the current standing
-- must be readable from the row itself. 'auto' is the floor/sweep path and 'operator' the human-approve,
-- registration, and acceptance-governed promotions.
ALTER TABLE tag ADD COLUMN standing_source TEXT NOT NULL DEFAULT 'auto'
    CHECK (standing_source IN ('auto', 'operator'));

-- +goose Down
ALTER TABLE tag DROP COLUMN IF EXISTS standing_source;

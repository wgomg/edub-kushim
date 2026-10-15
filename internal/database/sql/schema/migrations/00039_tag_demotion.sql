-- +goose Up
ALTER TABLE tag ADD COLUMN demoted_at TIMESTAMPTZ;
ALTER TABLE tag ADD COLUMN demoted_reason TEXT;
ALTER TABLE tag ADD COLUMN seeded BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX idx_tag_promoted_demotable ON tag (promoted_at)
WHERE promoted_at IS NOT NULL AND NOT seeded;

-- +goose Down
DROP INDEX idx_tag_promoted_demotable;
ALTER TABLE tag DROP COLUMN seeded;
ALTER TABLE tag DROP COLUMN demoted_reason;
ALTER TABLE tag DROP COLUMN demoted_at;

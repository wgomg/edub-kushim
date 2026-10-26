-- +goose Up

-- the execution guard is the batch lease owner now that every execution is leased; the per-task claim token
-- no longer authorizes anything, so the column goes

ALTER TABLE task DROP COLUMN claim_token;

-- +goose Down

ALTER TABLE task ADD COLUMN claim_token uuid;

-- +goose Up

-- a production batch may declare the tasks that follow its settle: the declaration is data on the batch row,
-- so the settle transaction creates the declared follow-up batch inside the terminal write instead of an
-- executor enqueueing it after the fact

ALTER TYPE batch_origin ADD VALUE IF NOT EXISTS 'trigger';

ALTER TABLE batch ADD COLUMN follow_up JSONB;

-- +goose Down

ALTER TABLE batch DROP COLUMN follow_up;

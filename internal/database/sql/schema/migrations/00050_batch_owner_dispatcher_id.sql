-- +goose Up

-- the dispatcher owns each lease it inserts; the identity scopes the slot budget to the process that
-- claimed the batch, so manual runs and other dispatcher hosts never consume its slots

ALTER TABLE batch_owner ADD COLUMN dispatcher_id UUID;

-- +goose Down

ALTER TABLE batch_owner DROP COLUMN dispatcher_id;

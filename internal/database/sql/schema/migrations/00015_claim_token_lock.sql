-- +goose Up
ALTER TABLE backup_lock ADD COLUMN owner_token uuid;
ALTER TABLE task ADD COLUMN claim_token uuid;

-- +goose Down
ALTER TABLE task DROP COLUMN claim_token;
ALTER TABLE backup_lock DROP COLUMN owner_token;
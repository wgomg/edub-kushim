-- +goose Up
-- the id's identity sequence is exhausted: the store-change trigger proposes three flag rows on every
-- vocabulary mutation and each proposal burns a nextval before the conflict check, so the smallint
-- ceiling (32767) caps the store's lifetime tag mutations while only a handful of rows ever exist;
-- name is the key and the id serves nothing, so the column and its capped sequence go away
ALTER TABLE system_signal DROP COLUMN id;

-- +goose Down
ALTER TABLE system_signal ADD COLUMN id smallint GENERATED ALWAYS AS IDENTITY PRIMARY KEY;

-- +goose Up
ALTER TABLE tag_identity_event DROP CONSTRAINT IF EXISTS tag_identity_event_kind_check;
ALTER TABLE tag_identity_event ADD CONSTRAINT tag_identity_event_kind_check CHECK (kind IN (
    'base_form', 'fold_gate', 'conventional_form', 'same_intent', 'relation_gate'
));

-- +goose Down
DELETE FROM tag_identity_event WHERE kind = 'relation_gate';
ALTER TABLE tag_identity_event DROP CONSTRAINT IF EXISTS tag_identity_event_kind_check;
ALTER TABLE tag_identity_event ADD CONSTRAINT tag_identity_event_kind_check CHECK (kind IN (
    'base_form', 'fold_gate', 'conventional_form', 'same_intent'
));

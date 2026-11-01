-- +goose Up
-- functional index backing the structured people filter, whose case-insensitive literal comparison on
-- lower(name) cannot use the plain name index; pattern searches do not read this index
CREATE INDEX idx_people_name_lower ON people (lower(name));

-- +goose Down
DROP INDEX IF EXISTS idx_people_name_lower;

-- +goose Up
UPDATE tag t
SET promoted_at = NULL
WHERE t.promoted_at IS NOT NULL
  AND t.promoted_at = t.created_at
  AND (SELECT COUNT(*) FROM document_tag dt WHERE dt.tag_id = t.id) < 2;

-- +goose Down
UPDATE tag t
SET promoted_at = coalesce(t.created_at, now())
WHERE t.promoted_at IS NULL
  AND EXISTS (SELECT 1 FROM document_tag dt WHERE dt.tag_id = t.id);

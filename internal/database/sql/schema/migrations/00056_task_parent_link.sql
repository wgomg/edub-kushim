-- +goose Up

-- the child names its parent: the successor link is data, so every linkage query joins on it instead of
-- parsing payload keys or listing task types

ALTER TABLE task ADD COLUMN parent_task_id TEXT;

-- only the states read through the link are linked: terminal rows are never queried by it
UPDATE task child
SET parent_task_id = child.payload->>'waiting_for'
FROM task parent
WHERE parent.task_id = child.payload->>'waiting_for'
  AND child.parent_task_id IS NULL
  AND child.status IN ('waiting', 'discarded');

-- the old activation injected document_id and storage_path at run time; a waiting or discarded thumbnail from
-- before the link existed must declare what it now inherits, or activation discards it as unplannable
UPDATE task child
SET payload = child.payload
    || jsonb_build_object('document_id', parent.payload->'document_id',
                          'inherit', '["storage_path"]'::jsonb)
FROM task parent
WHERE parent.task_id = child.parent_task_id
  AND parent.payload ? 'document_id'
  AND child.task_type = 'thumbnail'
  AND child.status IN ('waiting', 'discarded')
  AND NOT (child.payload ? 'document_id');

CREATE INDEX idx_task_parent ON task(parent_task_id);

-- +goose Down

DROP INDEX IF EXISTS idx_task_parent;

ALTER TABLE task DROP COLUMN parent_task_id;

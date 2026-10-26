-- +goose Up

-- one authorization predicate for every write an execution makes under its batch lease: the task-complete,
-- task-fail and batch-pause guards must agree, so they call this function instead of repeating the subquery

-- +goose StatementBegin
CREATE FUNCTION batch_leased_by(p_batch_id text, p_owner_id text) RETURNS boolean AS $$
BEGIN
  RETURN EXISTS (
    SELECT 1 FROM batch_owner bo
    WHERE bo.batch_id = p_batch_id AND bo.owner_id = p_owner_id
  );
END;
$$ LANGUAGE plpgsql STABLE;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION IF EXISTS batch_leased_by(text, text);

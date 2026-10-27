-- +goose Up

-- one orphan predicate for every read surface: the batch list, the single-batch summary and the dashboard
-- count must agree, so they call this function instead of repeating the lease-freshness subquery

-- +goose StatementBegin
CREATE FUNCTION batch_orphaned(p_batch_id text, p_stale_after_seconds int) RETURNS boolean AS $$
BEGIN
  RETURN EXISTS (
    SELECT 1 FROM batch b
    WHERE b.id = p_batch_id
      AND b.status = 'processing'
      AND NOT EXISTS (
        SELECT 1 FROM batch_owner bo
        WHERE bo.batch_id = p_batch_id
          AND bo.last_heartbeat >= CURRENT_TIMESTAMP - make_interval(secs => p_stale_after_seconds)
      )
  );
END;
$$ LANGUAGE plpgsql STABLE;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION IF EXISTS batch_orphaned(text, int);

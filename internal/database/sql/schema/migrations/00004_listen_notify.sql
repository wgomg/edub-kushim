-- +goose Up
CREATE OR REPLACE FUNCTION notify_batch_queued()
RETURNS trigger AS $$
BEGIN
  IF NEW.status = 'queued' THEN
    PERFORM pg_notify('batch_queued', NEW.id);
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER batch_queued_notify
AFTER INSERT OR UPDATE ON batch
FOR EACH ROW EXECUTE FUNCTION notify_batch_queued();

-- +goose Down
DROP TRIGGER IF EXISTS batch_queued_notify ON batch;
DROP FUNCTION IF EXISTS notify_batch_queued();

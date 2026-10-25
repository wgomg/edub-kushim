-- name: CreateBatch :exec
INSERT INTO batch (id, origin, schedule, status) VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO NOTHING;

-- name: GetBatchOwner :one
-- stale is decided by the database clock so every lease-liveness decision shares one clock.
SELECT batch_id, owner_id, pid, acquired_at, last_heartbeat,
       last_heartbeat < CURRENT_TIMESTAMP - make_interval(secs => @stale_after_seconds::int) AS stale
FROM batch_owner WHERE batch_id = $1;

-- name: TryInsertBatchOwner :execrows
INSERT INTO batch_owner (batch_id, owner_id, pid, dispatcher_id, acquired_at, last_heartbeat)
VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(batch_id) DO NOTHING;

-- name: HeartbeatBatchOwner :execrows
UPDATE batch_owner SET last_heartbeat = CURRENT_TIMESTAMP WHERE owner_id = $1;

-- name: AdoptBatchOwner :execrows
-- the dispatcher claims the lease with pid 0 and the child adopts it once: the owner token must still
-- match and the lease must still be unadopted, so a cancelled, requeued or reclaimed batch, or a replayed
-- execution token, refuses the adoption
UPDATE batch_owner SET pid = @pid, last_heartbeat = CURRENT_TIMESTAMP
WHERE batch_id = @batch_id AND owner_id = @owner_id AND pid = 0;

-- name: ReleaseBatchOwner :execrows
DELETE FROM batch_owner WHERE batch_id = $1 AND owner_id = $2;

-- name: ReleaseStaleBatchOwner :execrows
-- the reaper's release re-verifies what its snapshot identified: the same row, still owned by the listed
-- pid and still stale, so a lease that was adopted or heartbeated after the snapshot survives recovery
DELETE FROM batch_owner
WHERE batch_id = @batch_id AND owner_id = @owner_id AND pid = @pid
  AND last_heartbeat < CURRENT_TIMESTAMP - make_interval(secs => @stale_after_seconds::int);

-- name: DeleteBatchOwnerByBatchID :execrows
DELETE FROM batch_owner WHERE batch_id = $1;

-- name: QuarantineProcessingTasksByBatch :many
UPDATE task SET
    status = 'failed',
    error = 'Max retries exceeded (' || attempts || ')',
    completed_at = CURRENT_TIMESTAMP
WHERE batch_id = $1 AND status = 'processing' AND attempts >= $2
RETURNING task_id, task_type;

-- name: ResetProcessingTasksByBatch :execrows
UPDATE task SET
    status = 'pending',
    attempts = attempts + 1,
    progress = NULL
WHERE batch_id = $1 AND status = 'processing' AND attempts < $2;

-- name: GetNextPendingTaskOfTypeForOwner :one
SELECT t.id FROM task t
WHERE t.status = 'pending' AND t.task_type = @task_type
  AND t.batch_id IN (SELECT batch_id FROM batch_owner WHERE owner_id = @owner_id)
  AND EXISTS (SELECT 1 FROM batch b WHERE b.id = t.batch_id AND b.status IN ('queued', 'processing'))
  AND NOT EXISTS (
    SELECT 1 FROM system_signal s
    WHERE s.name = ANY(@wait_locks::text[]) AND lock_is_held(s.name)
  )
ORDER BY t.created_at LIMIT 1;

-- name: GetBatch :one
SELECT * FROM batch WHERE id = $1;

-- name: CountQueuedConsumeBatches :one
-- The non-consume task types (whose batches own their lifecycle in their handlers and never occupy a
-- consume slot) are owned by the registry and passed in, so this list cannot drift from
-- types.NonConsumeTaskTypes.
SELECT COUNT(*) FROM batch b
WHERE b.status = 'queued'
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id)
  AND NOT EXISTS (
    SELECT 1 FROM task t
    WHERE t.batch_id = b.id AND t.task_type::text = ANY(@non_consume_types::text[])
  );

-- name: ListQueuedBatchesByAge :many
-- the dispatcher filters eligibility, backoff and claimability in Go over the oldest-first rows; the task
-- type set is the batch's composition, so the catalog stays the single source of dispatch policy
SELECT b.id AS batch_id, b.created_at AS batch_created_at,
       ARRAY_AGG(DISTINCT t.task_type::text)::text[] AS task_types
FROM batch b
JOIN task t ON t.batch_id = b.id
WHERE b.status = 'queued'
GROUP BY b.id, b.created_at
ORDER BY b.created_at;

-- a claim locks the batch row and flips it into processing only on the first claim; processing stays allowed
-- so the remaining tasks of a multi-task batch stay claimable, while any other status refuses the claim
-- name: LockBatchForClaim :one
SELECT status FROM batch WHERE id = $1 FOR UPDATE;

-- name: MarkBatchClaimed :execrows
UPDATE batch SET status = 'processing'
WHERE id = $1 AND status = 'queued';

-- the status is computed inside the write so a task that fails between a read and the settle cannot be lost;
-- any active task, a non-processing batch or a foreign lease refuses the write and leaves the row untouched
-- name: SettleBatch :one
UPDATE batch SET status = CASE
    WHEN EXISTS (SELECT 1 FROM task t WHERE t.batch_id = batch.id AND t.status = 'failed')
      THEN 'failed'::batch_status
    ELSE 'completed'::batch_status
  END
WHERE batch.id = @id
  AND batch.status = 'processing'
  AND NOT EXISTS (
    SELECT 1 FROM task t
    WHERE t.batch_id = batch.id AND t.status IN ('pending', 'processing', 'waiting')
  )
  AND (
    NOT EXISTS (SELECT 1 FROM batch_owner bo WHERE bo.batch_id = batch.id)
    OR EXISTS (
      SELECT 1 FROM batch_owner bo
      WHERE bo.batch_id = batch.id AND bo.owner_id = sqlc.narg(owner_id)::text
    )
  )
RETURNING status;

-- pause authorizes like the settle: a leased execution must still own the lease, and a pool execution must
-- still hold a live claim of this batch, so a stale executor cannot pause the new run that replaced it
-- name: SetBatchPaused :execrows
UPDATE batch SET status = 'paused'
WHERE batch.id = @id
  AND batch.status = 'processing'
  AND (
    EXISTS (
      SELECT 1 FROM batch_owner bo
      WHERE bo.batch_id = batch.id AND bo.owner_id = sqlc.narg(owner_id)::text
    )
    OR (
      sqlc.narg(owner_id)::text IS NULL
      AND EXISTS (
        SELECT 1 FROM task t
        WHERE t.batch_id = batch.id AND t.claim_token = sqlc.narg(claim_token)::uuid
      )
    )
  );

-- name: SetBatchCancelled :execrows
UPDATE batch SET status = 'cancelled'
WHERE id = $1 AND status IN ('queued', 'processing', 'paused');

-- name: SetBatchFailed :execrows
-- the dispatcher's bounded launch-failure exit: the batch never started, so it is failed once its tasks
-- were cancelled in the same transaction
UPDATE batch SET status = 'failed'
WHERE id = $1 AND status IN ('queued', 'processing');

-- name: RequeueProcessingBatch :execrows
UPDATE batch SET status = 'queued' WHERE id = $1 AND status = 'processing';

-- name: ResumeBatch :execrows
UPDATE batch SET status = 'queued'
WHERE id = $1 AND status IN ('failed', 'completed', 'cancelled', 'paused');

-- name: BatchTaskStatusCounts :one
SELECT
    COUNT(*) FILTER (WHERE status = 'waiting')::bigint AS waiting,
    COUNT(*) FILTER (WHERE status = 'pending')::bigint AS pending,
    COUNT(*) FILTER (WHERE status = 'processing')::bigint AS processing,
    COUNT(*) FILTER (WHERE status = 'completed')::bigint AS completed,
    COUNT(*) FILTER (WHERE status = 'failed')::bigint AS failed,
    COUNT(*) FILTER (WHERE status = 'cancelled')::bigint AS cancelled,
    COUNT(*) FILTER (WHERE status = 'discarded')::bigint AS discarded
FROM task WHERE batch_id = $1;

-- name: CountLiveBatches :one
SELECT COUNT(*) FROM batch_owner
WHERE last_heartbeat > CURRENT_TIMESTAMP - make_interval(secs => @stale_after_seconds::int);

-- name: CountLiveNoHoldsBatches :one
-- the dispatcher's slot budget only charges batches that acquire no locks; a lock-holding batch is never
-- charged a slot because its locks already serialize it, so it is excluded here. The identity filter keeps
-- the budget per dispatcher process: manual runs and other hosts carry other ids (or none)
SELECT COUNT(*) FROM batch_owner bo
WHERE bo.dispatcher_id = @dispatcher_id::uuid
  AND bo.last_heartbeat > CURRENT_TIMESTAMP - make_interval(secs => @stale_after_seconds::int)
  AND NOT EXISTS (
    SELECT 1 FROM system_signal s
    WHERE s.value = 'true'
      AND s.data->>'batch_id' = bo.batch_id
      AND lock_is_held(s.name)
  );

-- name: ListStaleBatchOwners :many
SELECT bo.batch_id, bo.owner_id, bo.pid FROM batch_owner bo
WHERE bo.last_heartbeat < CURRENT_TIMESTAMP - make_interval(secs => @stale_after_seconds::int)
AND EXISTS (SELECT 1 FROM task t
            WHERE t.batch_id = bo.batch_id
            AND t.status IN ('pending', 'processing', 'waiting'));

-- name: GetConsumeTaskPayloadsByIDs :many
SELECT task_id, payload FROM task
WHERE task_id = ANY(@task_ids::text[]) AND task_type = 'consume';

-- name: GetPermanentlyFailedConsumeTaskPayloads :many
SELECT task_id, payload FROM task
WHERE batch_id = @batch_id AND task_type = 'consume'
  AND status = 'failed' AND attempts >= @attempts;

-- name: BatchHasNonConsumeTask :one
SELECT EXISTS (
    SELECT 1 FROM task t
    WHERE t.batch_id = $1 AND t.task_type::text = ANY(@non_consume_types::text[])
);

-- name: ListBatchOverviews :many
SELECT
    b.id AS batch_id,
    b.origin,
    b.schedule,
    b.created_at AS batch_created_at,
    b.status AS batch_status,
    COALESCE(sub.total, 0) AS total,
    COALESCE(sub.waiting, 0) AS waiting,
    COALESCE(sub.pending, 0) AS pending,
    COALESCE(sub.processing, 0) AS processing,
    COALESCE(sub.completed, 0) AS completed,
    COALESCE(sub.failed, 0) AS failed,
    COALESCE(sub.cancelled, 0) AS cancelled,
    COALESCE(sub.discarded, 0) AS discarded,
    sub.first_started_at AS first_started_at,
    sub.last_completed_at AS last_completed_at,
    bo.last_heartbeat AS owner_last_heartbeat,
    bo.pid AS owner_pid,
    (bo.last_heartbeat IS NOT NULL
      AND bo.last_heartbeat < CURRENT_TIMESTAMP - make_interval(secs => @stale_after_seconds::int)) AS owner_stale,
    EXISTS (
        SELECT 1 FROM task t2
        WHERE t2.batch_id = b.id AND t2.task_type::text = ANY(@non_consume_types::text[])
    ) AS non_consume
FROM batch b
LEFT JOIN LATERAL (
    SELECT
        COUNT(*) AS total,
        SUM(CASE WHEN t.status = 'waiting'   THEN 1 ELSE 0 END) AS waiting,
        SUM(CASE WHEN t.status = 'pending'   THEN 1 ELSE 0 END) AS pending,
        SUM(CASE WHEN t.status = 'processing' THEN 1 ELSE 0 END) AS processing,
        SUM(CASE WHEN t.status = 'completed' THEN 1 ELSE 0 END) AS completed,
        SUM(CASE WHEN t.status = 'failed'    THEN 1 ELSE 0 END) AS failed,
        SUM(CASE WHEN t.status = 'cancelled' THEN 1 ELSE 0 END) AS cancelled,
        SUM(CASE WHEN t.status = 'discarded' THEN 1 ELSE 0 END) AS discarded,
        MIN(t.started_at) AS first_started_at,
        MAX(t.completed_at) AS last_completed_at
    FROM task t WHERE t.batch_id = b.id
) sub ON true
LEFT JOIN batch_owner bo ON bo.batch_id = b.id
ORDER BY b.created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountPausedBatches :one
SELECT COUNT(*) FROM batch WHERE status = 'paused';

-- name: ListPausedBatches :many
SELECT id FROM batch WHERE status = 'paused' ORDER BY created_at;

-- name: CountActiveThumbnailBackfillBatches :one
SELECT COUNT(*) FROM batch b
WHERE b.status IN ('queued', 'processing')
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'thumbbackfill');

-- name: GetLastThumbnailBackfillBatchCreatedAt :one
SELECT b.created_at FROM batch b
WHERE EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'thumbbackfill')
ORDER BY b.created_at DESC
LIMIT 1;

-- name: CountActiveTagReconcileBatches :one
SELECT COUNT(*) FROM batch b
WHERE b.status IN ('queued', 'processing')
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'tagreconcile');

-- name: GetLastTagReconcileBatchCreatedAt :one
SELECT b.created_at FROM batch b
WHERE EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'tagreconcile')
ORDER BY b.created_at DESC
LIMIT 1;

-- name: CountActiveTagMeasureBatches :one
SELECT COUNT(*) FROM batch b
WHERE b.status IN ('queued', 'processing')
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'tagmeasure');

-- name: GetLastTagMeasureBatchCreatedAt :one
SELECT b.created_at FROM batch b
WHERE b.status = 'completed'
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'tagmeasure')
ORDER BY b.created_at DESC
LIMIT 1;

-- name: CountActiveTagAuditBatches :one
SELECT COUNT(*) FROM batch b
WHERE b.status IN ('queued', 'processing')
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'tagaudit');

-- name: GetLastTagAuditBatchCreatedAt :one
SELECT b.created_at FROM batch b
WHERE b.status = 'completed'
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'tagaudit')
ORDER BY b.created_at DESC
LIMIT 1;

-- name: CountActiveTagIdentityBatches :one
SELECT COUNT(*) FROM batch b
WHERE b.status IN ('queued', 'processing')
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'tagidentity');

-- name: GetLastTagIdentityBatchCreatedAt :one
SELECT b.created_at FROM batch b
WHERE b.status = 'completed'
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'tagidentity')
ORDER BY b.created_at DESC
LIMIT 1;

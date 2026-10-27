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

-- pause authorizes like the settle: the execution must still own the lease, so a stale executor cannot
-- pause the new run that replaced it
-- name: SetBatchPaused :execrows
UPDATE batch SET status = 'paused'
WHERE batch.id = @id
  AND batch.status = 'processing'
  AND batch_leased_by(batch.id, sqlc.narg(owner_id)::text);

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

-- name: ListBatchOverviews :many
-- the batch table is the only listing source: the list, the dashboard, the single-batch summary and the CLI
-- read this one aggregate shape, and the orphan flag comes from the single batch_orphaned definition
SELECT
    b.id AS batch_id,
    b.origin,
    b.schedule,
    b.created_at AS batch_created_at,
    b.status AS batch_status,
    COALESCE(sub.total, 0)::bigint AS total,
    COALESCE(sub.waiting, 0)::bigint AS waiting,
    COALESCE(sub.pending, 0)::bigint AS pending,
    COALESCE(sub.processing, 0)::bigint AS processing,
    COALESCE(sub.completed, 0)::bigint AS completed,
    COALESCE(sub.failed, 0)::bigint AS failed,
    COALESCE(sub.cancelled, 0)::bigint AS cancelled,
    COALESCE(sub.discarded, 0)::bigint AS discarded,
    sub.first_started_at AS first_started_at,
    sub.last_completed_at AS last_completed_at,
    bo.last_heartbeat AS owner_last_heartbeat,
    bo.pid AS owner_pid,
    (bo.last_heartbeat IS NOT NULL
      AND bo.last_heartbeat < CURRENT_TIMESTAMP - make_interval(secs => @stale_after_seconds::int)) AS owner_stale,
    batch_orphaned(b.id, @stale_after_seconds::int) AS orphaned
FROM batch b
LEFT JOIN LATERAL (
    SELECT
        COUNT(*)::bigint AS total,
        COUNT(*) FILTER (WHERE t.status = 'waiting')::bigint AS waiting,
        COUNT(*) FILTER (WHERE t.status = 'pending')::bigint AS pending,
        COUNT(*) FILTER (WHERE t.status = 'processing')::bigint AS processing,
        COUNT(*) FILTER (WHERE t.status = 'completed')::bigint AS completed,
        COUNT(*) FILTER (WHERE t.status = 'failed')::bigint AS failed,
        COUNT(*) FILTER (WHERE t.status = 'cancelled')::bigint AS cancelled,
        COUNT(*) FILTER (WHERE t.status = 'discarded')::bigint AS discarded,
        MIN(t.started_at) AS first_started_at,
        MAX(t.completed_at) AS last_completed_at
    FROM task t WHERE t.batch_id = b.id
) sub ON true
LEFT JOIN batch_owner bo ON bo.batch_id = b.id
WHERE (sqlc.narg('batch_id')::text IS NULL OR b.id = sqlc.narg('batch_id')::text)
  AND (sqlc.narg('status')::text IS NULL OR b.status = sqlc.narg('status')::batch_status)
ORDER BY b.created_at DESC
LIMIT @row_limit OFFSET @row_offset;

-- name: CountBatches :one
SELECT COUNT(*)::bigint FROM batch;

-- name: CountActiveBatches :one
-- non-terminal batches are the ones the dashboard's active card reports
SELECT COUNT(*)::bigint FROM batch WHERE status IN ('queued', 'processing', 'paused');

-- name: CountOrphanedBatches :one
-- the status pre-filter keeps the per-row function off non-processing batches; the function is the rule
SELECT COUNT(*)::bigint FROM batch b
WHERE b.status = 'processing' AND batch_orphaned(b.id, @stale_after_seconds::int);

-- name: CountPausedBatches :one
SELECT COUNT(*) FROM batch WHERE status = 'paused';

-- name: ListPausedBatches :many
SELECT id FROM batch WHERE status = 'paused' ORDER BY created_at;

-- name: CountActiveTagAuditBatches :one
SELECT COUNT(*) FROM batch b
WHERE b.status IN ('queued', 'processing')
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'tagaudit');

-- name: CountActiveTagIdentityBatches :one
SELECT COUNT(*) FROM batch b
WHERE b.status IN ('queued', 'processing')
  AND EXISTS (SELECT 1 FROM task t WHERE t.batch_id = b.id AND t.task_type = 'tagidentity');

-- the scheduler derives due-ness and active state centrally from the batch table: it filters by the
-- schedule name instead of testing each run's task composition; the cast makes the name a plain text
-- argument rather than mirroring the column's nullability
-- name: CountActiveBatchesForSchedule :one
SELECT COUNT(*) FROM batch
WHERE schedule = @schedule::text AND status IN ('queued', 'processing');

-- name: GetLastTerminalBatchCreatedAtForSchedule :one
SELECT created_at FROM batch
WHERE schedule = @schedule::text AND status IN ('completed', 'failed', 'cancelled')
ORDER BY created_at DESC
LIMIT 1;

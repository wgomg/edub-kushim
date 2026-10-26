-- name: GetTask :one
SELECT * FROM task WHERE id = $1;

-- name: GetTaskByTaskID :one
SELECT * FROM task WHERE task_id = $1;

-- name: GetTaskByBatchID :many
SELECT * FROM task WHERE batch_id = $1 ORDER BY created_at;

-- name: GetNextPendingTask :one
SELECT id FROM task WHERE status = 'pending' ORDER BY created_at LIMIT 1;

-- name: ListBatchTaskTypes :many
SELECT DISTINCT task_type FROM task WHERE batch_id = $1 ORDER BY task_type;

-- name: ListTasks :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListTasksByStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListTasksByBatch :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE batch_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListTasksByBatchAndStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE batch_id = $1 AND status = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: ListAllTasks :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task ORDER BY created_at DESC;

-- name: ListAllTasksByStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE status = $1 ORDER BY created_at DESC;

-- name: ListAllTasksByBatch :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE batch_id = $1 ORDER BY created_at DESC;

-- name: ListBatchTaskProgress :many
SELECT task_id, task_type, status, payload, error, progress
FROM task WHERE batch_id = $1 ORDER BY created_at DESC;

-- name: ListAllTasksByBatchAndStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE batch_id = $1 AND status = $2 ORDER BY created_at DESC;

-- name: ListTasksByBatchAndStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE batch_id = $1 AND status = $2 AND task_type = $3 ORDER BY created_at DESC LIMIT $4 OFFSET $5;

-- name: ListAllTasksByBatchAndStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE batch_id = $1 AND status = $2 AND task_type = $3 ORDER BY created_at DESC;

-- name: ListTasksByBatchAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE batch_id = $1 AND task_type = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: ListAllTasksByBatchAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE batch_id = $1 AND task_type = $2 ORDER BY created_at DESC;

-- name: ListTasksByStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE status = $1 AND task_type = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: ListAllTasksByStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE status = $1 AND task_type = $2 ORDER BY created_at DESC;

-- name: ListTasksByType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE task_type = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAllTasksByType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE task_type = $1 ORDER BY created_at DESC;

-- name: ListActiveTasks :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task
WHERE status IN ('pending', 'processing', 'waiting')
ORDER BY
  CASE status WHEN 'processing' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END,
  started_at ASC NULLS LAST,
  created_at ASC
LIMIT $1 OFFSET $2;

-- name: CountProcessingTasks :one
SELECT COUNT(*) FROM task
WHERE status = 'processing'
  AND task_type = ANY(@types::task_type[]);

-- name: CountTasksByBatchAndStatus :one
SELECT COUNT(*) FROM task WHERE batch_id = $1 AND status = $2;

-- name: CreateTask :one
INSERT INTO task (
    task_id, task_type, status, batch_id, payload, dedup_key
) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id;

-- name: ClaimTask :execrows
UPDATE task SET
    status = 'processing',
    started_at = CURRENT_TIMESTAMP,
    progress = NULL
WHERE id = $1 AND status = 'pending';

-- name: CompleteBlockingTask :execrows
-- the transition is guarded by the owning execution's lease: a task of a batch that is no longer leased to
-- this execution (paused, cancelled, reclaimed or re-claimed by a new run) refuses the write
UPDATE task t SET
    status = 'completed',
    result = $1,
    completed_at = CURRENT_TIMESTAMP,
    attempts = 0
WHERE t.id = $2 AND t.status = 'processing'
  AND batch_leased_by(t.batch_id, sqlc.arg(owner_id)::text);

-- name: FailBlockingTask :execrows
UPDATE task t SET
    status = 'failed',
    completed_at = CURRENT_TIMESTAMP,
    error = $1
WHERE t.id = $2 AND t.status = 'processing'
  AND batch_leased_by(t.batch_id, sqlc.arg(owner_id)::text);

-- name: UpdateTaskProgress :exec
UPDATE task SET progress = $2
WHERE task_id = $1 AND status = 'processing';

-- name: RetryFailedTasksByBatch :execrows
UPDATE task SET
    status = 'pending',
    result = NULL,
    error = NULL,
    started_at = NULL,
    completed_at = NULL,
    attempts = 0
WHERE batch_id = $1 AND status = 'failed';

-- name: CountTasksByStatusAndType :one
SELECT COUNT(*) FROM task WHERE status = $1 AND task_type = $2;

-- name: GetConfigTaskByDedupKey :one
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress
FROM task WHERE task_type = @task_type AND dedup_key = @dedup_key
ORDER BY created_at DESC LIMIT 1;

-- name: DeleteConfigTaskByDedupKey :execrows
DELETE FROM task
WHERE task_type = @task_type AND dedup_key = @dedup_key
  AND status IN ('pending', 'failed');

-- name: SetEnrichTaskPending :exec
UPDATE task SET
    status = 'pending',
    payload = @payload,
    dedup_key = @dedup_key,
    error = NULL,
    completed_at = NULL
WHERE id = @id AND status IN ('waiting', 'discarded') AND task_type IN ('enrich', 'thumbnail');

-- name: DiscardEnrichTask :execrows
UPDATE task SET
    status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = $1
WHERE id = $2 AND status = 'waiting' AND task_type IN ('enrich', 'thumbnail');

-- name: SetEnrichTaskWaiting :execrows
UPDATE task SET
    status = 'waiting',
    error = NULL,
    completed_at = NULL
WHERE task_id = $1 AND status = 'discarded' AND task_type IN ('enrich', 'thumbnail');

-- name: RestoreDiscardedEnrichTasks :execrows
UPDATE task AS e
SET status = 'waiting', error = NULL, completed_at = NULL
FROM task AS c
WHERE e.task_type IN ('enrich', 'thumbnail') AND e.status = 'discarded'
  AND c.task_type = 'consume' AND c.status = 'pending'
  AND (e.task_id = c.payload->>'on_completed' OR e.task_id = c.payload->>'on_completed_thumbnail');

-- name: RestoreDiscardedEnrichTasksByBatch :execrows
UPDATE task AS e
SET status = 'waiting', error = NULL, completed_at = NULL
FROM task AS c
WHERE e.task_type IN ('enrich', 'thumbnail') AND e.status = 'discarded'
  AND c.task_type = 'consume' AND c.status = 'pending'
  AND c.batch_id = $1
  AND (e.task_id = c.payload->>'on_completed' OR e.task_id = c.payload->>'on_completed_thumbnail');

-- a waiting child whose parent consume is no longer pending or processing can never be activated: the parent
-- completed without activating it, failed, or was cancelled, so the waiting row would strand the settle
-- name: DiscardOrphanedWaitingEnriches :execrows
UPDATE task AS e
SET status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = COALESCE(c.error, 'parent consume task did not complete')
FROM task AS c
WHERE e.task_type IN ('enrich', 'thumbnail') AND e.status = 'waiting'
  AND c.task_type = 'consume' AND c.status NOT IN ('pending', 'processing')
  AND c.batch_id = $1
  AND (e.task_id = c.payload->>'on_completed' OR e.task_id = c.payload->>'on_completed_thumbnail');

-- name: DiscardOrphanedWaitingEnrichesGlobal :execrows
UPDATE task AS e
SET status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = COALESCE(c.error, 'parent consume task did not complete')
FROM task AS c
WHERE e.task_type IN ('enrich', 'thumbnail') AND e.status = 'waiting'
  AND c.task_type = 'consume' AND c.status NOT IN ('pending', 'processing')
  AND (e.task_id = c.payload->>'on_completed' OR e.task_id = c.payload->>'on_completed_thumbnail');

-- name: DiscardEnrichTaskByTaskID :execrows
UPDATE task SET
    status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = $1
WHERE task_id = $2 AND status = 'waiting' AND task_type IN ('enrich', 'thumbnail');

-- name: DeleteTask :exec
DELETE FROM task WHERE id = $1;

-- name: CancelPendingTasksByBatch :execrows
UPDATE task SET status = 'cancelled', completed_at = CURRENT_TIMESTAMP
WHERE batch_id = $1 AND status IN ('pending', 'waiting');

-- name: CancelProcessingTasksByBatch :execrows
UPDATE task SET status = 'cancelled', completed_at = CURRENT_TIMESTAMP
WHERE batch_id = $1 AND status = 'processing';

-- name: ListDistinctBatchIDs :many
SELECT batch_id FROM task
WHERE batch_id IS NOT NULL
GROUP BY batch_id
ORDER BY MAX(created_at) DESC
LIMIT $1 OFFSET $2;

-- name: ListDistinctBatchIDsByStatus :many
SELECT batch_id FROM task
WHERE batch_id IS NOT NULL AND status = $1
GROUP BY batch_id
ORDER BY MAX(created_at) DESC
LIMIT $2 OFFSET $3;

-- name: CountDistinctBatches :one
SELECT COUNT(DISTINCT batch_id) FROM task WHERE batch_id IS NOT NULL;

-- name: CountAllTasks :one
SELECT COUNT(*) FROM task;

-- name: CountTasksByStatus :many
SELECT status, COUNT(*) as count FROM task GROUP BY status;

-- name: QuarantineStaleProcessingTasks :many
UPDATE task SET
    status = 'failed',
    error = 'Max retries exceeded (' || attempts || ')',
    completed_at = CURRENT_TIMESTAMP
WHERE status = 'processing' AND attempts >= @attempts
  AND started_at < NOW() - make_interval(secs => @stale_after_seconds::int)
  AND task_type <> ALL(@types::task_type[])
RETURNING task_id, task_type;

-- name: ResetStaleProcessingTasks :execrows
UPDATE task SET
    status = 'pending',
    attempts = attempts + 1,
    progress = NULL
WHERE status = 'processing' AND attempts < @attempts
  AND started_at < NOW() - make_interval(secs => @stale_after_seconds::int)
  AND task_type <> ALL(@types::task_type[]);

-- name: CountRecentBackupTasksByMode :one
SELECT COUNT(*) FROM task
WHERE task_type = 'backup' AND created_at > NOW() - ($1 || ' minutes')::INTERVAL
  AND dedup_key LIKE 'backup:' || $2 || ':%';

-- name: CountActiveBackupTasks :one
SELECT COUNT(*) FROM task
WHERE task_type = 'backup' AND status IN ('pending', 'processing');

-- name: GetLastCompletedBackupByMode :one
SELECT completed_at, payload FROM task
WHERE task_type = 'backup' AND status = 'completed'
  AND dedup_key LIKE 'backup:' || $1 || ':%'
ORDER BY completed_at DESC
LIMIT 1;

-- name: CountRecentMirrorTasks :one
SELECT COUNT(*) FROM task
WHERE task_type = 'mirror' AND created_at > NOW() - ($1 || ' minutes')::INTERVAL
  AND dedup_key LIKE 'mirror:%';

-- name: CountActiveMirrorTasks :one
SELECT COUNT(*) FROM task
WHERE task_type = 'mirror' AND status IN ('pending', 'processing');

-- name: GetLastCompletedMirror :one
SELECT completed_at, payload FROM task
WHERE task_type = 'mirror' AND status = 'completed'
  AND dedup_key LIKE 'mirror:%'
ORDER BY completed_at DESC
LIMIT 1;

-- name: GetLastCompletedTagReconcileTask :one
SELECT completed_at FROM task
WHERE task_type = 'tagreconcile' AND status = 'completed'
ORDER BY completed_at DESC
LIMIT 1;

-- name: GetLastCompletedTagReconcileTaskWithResult :one
SELECT task_id, completed_at, result FROM task
WHERE task_type = 'tagreconcile' AND status = 'completed'
ORDER BY completed_at DESC
LIMIT 1;

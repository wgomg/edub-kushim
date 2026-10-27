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
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListTasksByStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListTasksByBatch :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task WHERE batch_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAllTasksByBatch :many
-- the ListAll prefix marks an unpaginated read of the filtered shape, not an unfiltered table scan:
-- this is one batch's whole task set, used by the settle-time enrich bookend
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task WHERE batch_id = $1 ORDER BY created_at DESC;

-- name: ListTasksByBatchAndStatus :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task WHERE batch_id = $1 AND status = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: ListBatchTaskProgress :many
SELECT task_id, task_type, status, payload, error, progress
FROM task WHERE batch_id = $1 ORDER BY created_at DESC;

-- name: ListTasksByBatchAndStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task WHERE batch_id = $1 AND status = $2 AND task_type = $3 ORDER BY created_at DESC LIMIT $4 OFFSET $5;

-- name: ListTasksByBatchAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task WHERE batch_id = $1 AND task_type = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: ListTasksByStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task WHERE status = $1 AND task_type = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: ListAllTasksByStatusAndType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task WHERE status = $1 AND task_type = $2 ORDER BY created_at DESC;

-- name: ListTasksByType :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task WHERE task_type = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListActiveTasks :many
SELECT id, task_id, task_type, status, batch_id, payload, result, dedup_key,
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
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
    task_id, task_type, status, batch_id, payload, dedup_key, parent_task_id
) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id;

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
       created_at, started_at, completed_at, error, attempts, progress, parent_task_id
FROM task WHERE task_type = @task_type AND dedup_key = @dedup_key
ORDER BY created_at DESC LIMIT 1;

-- name: DeleteConfigTaskByDedupKey :execrows
DELETE FROM task
WHERE task_type = @task_type AND dedup_key = @dedup_key
  AND status IN ('pending', 'failed');

-- name: ListWaitingSuccessorsByParent :many
-- one parent's waiting successors in enqueue order: activation is deterministic, and two same-key successors
-- resolve first-wins with the second discarded as a duplicate of the first
SELECT id, task_id, task_type, payload
FROM task
WHERE parent_task_id = @parent_task_id::text AND status = 'waiting'
ORDER BY created_at, id;

-- name: SetWaitingSuccessorPending :execrows
-- the activation flip carries the merged payload and the key derived from it; the waiting guard turns a
-- concurrently transitioned successor into a no-op instead of an overwrite
UPDATE task SET
    status = 'pending',
    payload = @payload,
    dedup_key = @dedup_key::text,
    error = NULL,
    completed_at = NULL
WHERE id = @id AND status = 'waiting';

-- name: DiscardWaitingSuccessor :execrows
UPDATE task SET
    status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = @error::text
WHERE id = @id AND status = 'waiting';

-- name: DiscardWaitingSuccessorBranch :execrows
-- a successor that will never run cannot activate its own successors: discarding one waiting successor
-- discards its awaiting branch transitively, following the link down the chain
WITH RECURSIVE branch(task_id, id) AS (
    SELECT task_id, id FROM task
    WHERE parent_task_id = @parent_task_id::text AND status = 'waiting'
    UNION ALL
    SELECT t.task_id, t.id FROM task t
    JOIN branch b ON t.parent_task_id = b.task_id
    WHERE t.status = 'waiting'
)
UPDATE task SET
    status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = @error::text
WHERE id IN (SELECT id FROM branch) AND status = 'waiting';

-- name: RestoreDiscardedSuccessors :execrows
-- a discarded successor of a live parent returns to waiting: pending means the parent was retried, waiting
-- means the chain has not reached it yet; the recursion re-arms the tail, and the dedup index re-decides at
-- activation
WITH RECURSIVE restored(task_id, id) AS (
    SELECT c.task_id, c.id FROM task c
    JOIN task p ON c.parent_task_id = p.task_id
    WHERE c.status = 'discarded' AND p.status IN ('pending', 'waiting')
    UNION ALL
    SELECT c.task_id, c.id FROM task c
    JOIN restored r ON c.parent_task_id = r.task_id
    WHERE c.status = 'discarded'
)
UPDATE task SET
    status = 'waiting',
    error = NULL,
    completed_at = NULL
WHERE id IN (SELECT id FROM restored) AND status = 'discarded';

-- name: RestoreDiscardedSuccessorsByBatch :execrows
WITH RECURSIVE restored(task_id, id) AS (
    SELECT c.task_id, c.id FROM task c
    JOIN task p ON c.parent_task_id = p.task_id
    WHERE c.status = 'discarded' AND p.status IN ('pending', 'waiting')
      AND p.batch_id = @batch_id::text
    UNION ALL
    SELECT c.task_id, c.id FROM task c
    JOIN restored r ON c.parent_task_id = r.task_id
    WHERE c.status = 'discarded'
)
UPDATE task SET
    status = 'waiting',
    error = NULL,
    completed_at = NULL
WHERE id IN (SELECT id FROM restored) AND status = 'discarded';

-- name: DiscardOrphanedWaitingSuccessors :execrows
-- the repair net: a waiting successor whose parent ended without activating it can never run. completed is
-- residue (activation commits with the completion); failed is normally cascaded in the same transaction, but
-- the quarantine paths fail a task in place without discarding its branch, so the recursion follows the link
-- and one pass clears a whole dead branch instead of leaving its tail blocking the settle
WITH RECURSIVE dead(task_id, id, reason) AS (
    SELECT c.task_id, c.id, COALESCE(p.error, 'parent task did not complete')
    FROM task c
    JOIN task p ON c.parent_task_id = p.task_id
    WHERE c.status = 'waiting'
      AND p.status IN ('completed', 'failed', 'cancelled', 'discarded')
      AND p.batch_id = @batch_id::text
    UNION ALL
    SELECT c.task_id, c.id, d.reason
    FROM task c
    JOIN dead d ON c.parent_task_id = d.task_id
    WHERE c.status = 'waiting'
)
UPDATE task
SET status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = dead.reason
FROM dead
WHERE task.id = dead.id AND task.status = 'waiting';

-- name: DiscardOrphanedWaitingSuccessorsGlobal :execrows
WITH RECURSIVE dead(task_id, id, reason) AS (
    SELECT c.task_id, c.id, COALESCE(p.error, 'parent task did not complete')
    FROM task c
    JOIN task p ON c.parent_task_id = p.task_id
    WHERE c.status = 'waiting'
      AND p.status IN ('completed', 'failed', 'cancelled', 'discarded')
    UNION ALL
    SELECT c.task_id, c.id, d.reason
    FROM task c
    JOIN dead d ON c.parent_task_id = d.task_id
    WHERE c.status = 'waiting'
)
UPDATE task
SET status = 'discarded',
    completed_at = CURRENT_TIMESTAMP,
    error = dead.reason
FROM dead
WHERE task.id = dead.id AND task.status = 'waiting';

-- name: ListWaitingSuccessorsWithParentStatus :many
-- the settle-refusal diagnostic: which waiting rows remain and in what state their parents are
SELECT c.task_id, c.task_type, p.task_id AS parent_task_id, p.status AS parent_status
FROM task c
JOIN task p ON c.parent_task_id = p.task_id
WHERE c.batch_id = @batch_id::text AND c.status = 'waiting'
ORDER BY c.created_at, c.id;

-- name: DeleteTask :exec
DELETE FROM task WHERE id = $1;

-- name: CancelPendingTasksByBatch :execrows
UPDATE task SET status = 'cancelled', completed_at = CURRENT_TIMESTAMP
WHERE batch_id = $1 AND status IN ('pending', 'waiting');

-- name: CancelProcessingTasksByBatch :execrows
UPDATE task SET status = 'cancelled', completed_at = CURRENT_TIMESTAMP
WHERE batch_id = $1 AND status = 'processing';

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

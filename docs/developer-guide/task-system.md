# Developer Guide — The Task System and Queue Semantics

This guide explains how edub-kushim processes work in the background: the
task lifecycle, the optimistic claim mechanism, batch ownership with
heartbeats, the queue daemon, deduplication, reclamation after crashes, and
the backup gate. It is the *conceptual* companion to
`docs/reference/task-system.md` (the file inventory): it answers "why can't a
task be lost or run twice?"

Audience: developers touching `internal/task/`, `internal/commands/queue.go`,
`internal/commands/consume.go`, or the task/batch queries.

---

## Table of contents

1. [Orientation](#1-orientation)
2. [The task table and statuses](#2-the-task-table-and-statuses)
3. [The optimistic claim](#3-the-optimistic-claim)
4. [The runner loop](#4-the-runner-loop)
5. [Batch ownership: leases with heartbeats](#5-batch-ownership-leases-with-heartbeats)
6. [The queue daemon](#6-the-queue-daemon)
7. [The consume command](#7-the-consume-command)
8. [Parent/child choreography: waiting tasks](#8-parentchild-choreography-waiting-tasks)
9. [Deduplication](#9-deduplication)
10. [Reclamation after crashes](#10-reclamation-after-crashes)
11. [Retry and resume](#11-retry-and-resume)
12. [The backup gate](#12-the-backup-gate)
13. [Configuration knobs](#13-configuration-knobs)
14. [Gotchas](#14-gotchas)

---

## 1. Orientation

The architecture is deliberately **database-as-queue**: there is no in-memory
job queue, no Redis, no message broker. Tasks are rows in the `task` table;
workers *claim* rows with guarded UPDATEs; a `batch_owner` table holds
per-batch leases; a daemon (`kushim queue`) watches for queued batches, forks
one `kushim consume --batch <id>` child per batch, and reclaims anything that
dies.

The cast of characters:

| Component | Files | Role |
|---|---|---|
| `Dispatcher` | `internal/task/dispatcher.go` | creates tasks (`Enqueue`) |
| `Registry` | `internal/task/registry.go` | task-type → handler map |
| `Handler` | `internal/task/handler.go` | does the work, returns JSON result |
| `Runner` | `internal/task/runner.go` | claims + dispatches + completes |
| `Pool` | `internal/pool/pool.go` | N worker goroutines calling `Runner.Next` |
| `Owner` / `Heartbeat` | `internal/task/batch.go`, `heartbeat.go` | batch lease management |
| queue daemon | `internal/commands/queue.go` | orchestrates batches |
| consume command | `internal/commands/consume.go` | runs one batch |

The six task types (`internal/commands/container.go:127-134`): `consume`,
`enrich`, `thumbnail`, `config`, `backup`, `mirror`.

---

## 2. The task table and statuses

Schema (`internal/database/sql/schema/migrations/00001_baseline.sql:41-55`):

```sql
CREATE TABLE task (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id TEXT NOT NULL UNIQUE,
    task_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    batch_id TEXT,
    payload JSONB,
    result JSONB,
    dedup_key TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error TEXT,
    attempts INTEGER NOT NULL DEFAULT 0
);
```

Seven statuses, all plain strings (no CHECK constraint — the code owns the
vocabulary): `pending`, `processing`, `waiting`, `completed`, `failed`,
`discarded`, `cancelled`.

The lifecycle:

```
                     +--> completed
pending --claim--> processing --error--> failed
   ^                    |
   | (retry/reset)      +--stale sweep--+--> pending (attempts+1) or failed (quarantine)
waiting --activate--> pending
waiting --parent failed--> discarded
discarded --consume retried/reset--> waiting
pending/processing --cancel--> cancelled
```

`waiting` is the special one: a child enrich or thumbnail task parked until
its parent consume task completes (§8). `discarded` is "never ran, never will"
— it doesn't count as a failure. The `discarded → waiting` restore exists so a
retried consume (manual retry or automatic crash-reset) re-arms its enrich
and thumbnail children and the `status = 'waiting'` guard on the discard
query keeps working on subsequent failures.

---

## 3. The optimistic claim

Workers claim tasks with a **compare-and-swap UPDATE**, not a lock
(`internal/database/sql/queries/task.sql:99-104`):

```sql
-- name: ClaimTask :execrows
UPDATE task SET
    status = 'processing',
    started_at = CURRENT_TIMESTAMP
WHERE id = $1 AND status = 'pending';
```

The `WHERE status = 'pending'` is the entire concurrency control: of N racing
workers, exactly one flips the row and affects 1 row; the rest affect 0. The
Go side checks `RowsAffected` (that's what sqlc's `:execrows` annotation
means) and treats 0 as "someone else got it" (`internal/task/store.go:58-96`):

```go
rows, err := s.queries.ClaimTask(ctx, id)
if rows == 0 {
	return database.Task{}, sql.ErrNoRows
}
```

This is the project-wide pattern: **every concurrency-sensitive transition is
a guarded UPDATE with rows-affected acknowledgment** — `ClaimTask`,
`CompleteTask`, `TryInsertBatchOwner`, `UpdateBatchOwnerIfStale`,
`HeartbeatBatchOwner`, `ReleaseBatchOwner`, `AcquireBackupLock`. Nothing is
ever "SELECT then UPDATE"; the guard and the write are one statement.

Why not `SELECT ... FOR UPDATE SKIP LOCKED`? The UPDATE-guard needs no
explicit unlock, holds the row lock for a single statement (not the whole
handler), and is idempotent across retries — a worker that dies mid-task
leaves a `processing` row that the stale sweep (§10) finds.

---

## 4. The runner loop

`Runner.Next` (`internal/task/runner.go:28-93`) is called by every pool
worker tick. The flow:

1. **Claim** — `store.ClaimNextPending(ctx, taskType)`; `sql.ErrNoRows` means
   idle, return nil quietly (`runner.go:30-35`).
2. **Validate** — nil payload → `FailTask("task has nil payload")`
   (`runner.go:37-46`); unknown type → `FailTask` with the registry error
   (`runner.go:48-52`).
3. **Dispatch** — `h.Handle(ctx, task)`.
4. **Complete** — `completeTaskWithRetry` (`runner.go:95-125`): `CompleteTask`
   with **3 attempts and exponential backoff (50ms → 100ms → 200ms)** for
   transient DB contention:

```go
const maxAttempts = 3
backoff := 50 * time.Millisecond

for attempt := 1; ; attempt++ {
	rows, err := r.store.CompleteTask(ctx, id, result)
	if err == nil && rows > 0 { return nil }
	if err == nil {
		// rows == 0: the task was already transitioned (e.g. by the
		// stale-task sweep). The handler's work is done; nothing more
		// to record.
		return nil
	}
	if attempt >= maxAttempts { return err }
	select {
	case <-ctx.Done(): return ctx.Err()
	case <-time.After(backoff): backoff *= 2
	}
}
```

   Note `rows == 0` is *success* — the stale sweep may have already moved the
   task; the handler's work is done either way.
5. **Complete-failure fallback** (`runner.go:76-90`): if completing failed
   after retries, the task is marked **failed** with
   `"complete task failed after retries: ..."` — visible and retryable rather
   than stuck in `processing` forever.

Handler errors extract metadata via `errors.As` to `*task.Error`
(`runner.go:60-74`, type at `internal/task/errors.go:3-10`):

```go
type Error struct {
	ReqID      string
	Err        error
	PauseBatch bool
}
```

`ReqID` correlates logs across the LLM call; `PauseBatch` triggers
`PauseBatch(batchID)` — the credit-exhaustion flow (§12 of the LLM guide).

---

## 5. Batch ownership: leases with heartbeats

Tasks belong to batches, batches belong to *one worker process at a time*.
The `batch_owner` table is the lease table
(`00001_baseline.sql:104-110`): one row per batch with `owner_id`, `pid`,
`acquired_at`, `last_heartbeat`.

### Acquire: two-step, then force

`Owner.Acquire` (`internal/task/batch.go:52-83`) implements the comment in
the SQL (`batch.sql:8-9` — "try INSERT first, then UPDATE with stale check"):

```sql
-- name: TryInsertBatchOwner :execrows
INSERT INTO batch_owner (batch_id, owner_id, pid, acquired_at, last_heartbeat)
VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(batch_id) DO NOTHING;

-- name: UpdateBatchOwnerIfStale :execrows
UPDATE batch_owner SET
  owner_id = $1, pid = $2, ...
WHERE batch_id = $3 AND (last_heartbeat < $4 OR owner_id = $5);
```

If the INSERT affects 0 rows, someone owns the batch — steal only if the
incumbent is stale (`last_heartbeat < cutoff`) or **we already own it** (the
`owner_id = $5` clause makes re-acquire idempotent). Otherwise
`ErrBatchLocked` (`batch.go:16`). The staleness constant is
`StaleAfter = 15 * time.Second` (`batch.go:14`).

`AcquireBatchOwnerForce` (`batch.sql:23-30`) is the unconditional upsert with
`excluded` — used by the queue daemon's placeholder and the `--force` flag.

### Keep-alive: heartbeat every 5 seconds

`Heartbeat.Start` (`internal/task/heartbeat.go:28-63`) runs a ticker that
updates `last_heartbeat` for `owner_id`:

```go
ticker := time.NewTicker(h.interval)
defer ticker.Stop()
for {
	select {
	case <-ticker.C:
		rows, err := h.owner.Heartbeat(ctx)
		...
	case <-h.done:   // closed via sync.Once
		return
	case <-ctx.Done():
		return
	}
}
```

5s interval, 15s staleness = **3 missed beats before the lease is stealable**.
A heartbeat that updates 0 rows logs "row missing or owner_id mismatch" —
someone took the lease away; the worker keeps running but its claims
(§5, owner-scoped) will find nothing.

### The lease gates claiming

`Store.ClaimNextPending` only claims tasks from batches **this process owns**
(`batch.sql:63-74`):

```sql
-- name: GetNextPendingTaskOfTypeForOwner :one
SELECT id FROM task
WHERE status = 'pending' AND task_type = $1
  AND batch_id IN (SELECT batch_id FROM batch_owner WHERE owner_id = $2)
ORDER BY created_at LIMIT 1;
```

The `ownerID` is a per-process UUID set via `SetRunnerOwnerID`
(`consume.go:154-156`). Batches without an owner row are invisible to
workers — the lease table *partitions the work*.

### Release and cleanup

`ReleaseBatchOwner` deletes only where **both** batch and owner match
(`batch.sql:35-36`) — a worker can't accidentally release someone else's
lease. `CleanupCompletedBatches` (`batch.sql:54-61`) removes owner rows whose
batch has no pending/processing/waiting tasks left.

---

## 6. The queue daemon

`kushim queue` (`internal/commands/queue.go`) is the orchestrator. It never
processes tasks itself.

### The wake-up loop

Three mechanisms keep it responsive without busy-polling
(`queue.go:111-150`):

```go
go runPollingLoop(ctx, c, client, batchSvc, maxConcurrent)

notifyCh := make(chan struct{}, 4)
go listenForBatchNotifications(ctx, config.BuildPostgresDSN(c.cfg.Load().Db), notifyCh, c.logger)

safetyInterval := 30 * time.Second
safetyTimer := time.NewTimer(safetyInterval)
defer safetyTimer.Stop()

for {
	select {
	case <-notifyCh:
		if err := consumeNextQueuedBatch(...); err != nil { ... }
		if !safetyTimer.Stop() {
			select { case <-safetyTimer.C: default: }
		}
		safetyTimer.Reset(safetyInterval)

	case <-safetyTimer.C:
		if err := consumeNextQueuedBatch(...); err != nil { ... }
		safetyTimer.Reset(safetyInterval)
	...
```

1. **Postgres NOTIFY** (`internal/commands/notify.go`): a dedicated
   single-connection pool does `LISTEN batch_queued` +
   `WaitForNotification`, pushing a token into `notifyCh`. The trigger lives
   in the database (`00004_listen_notify.sql`) and fires on `INSERT`/`UPDATE`
   of a `queued` batch.
2. **The 30s safety timer**: NOTIFY is a *hint* — it can be missed, dropped
   (the non-blocking send at `notify.go:68-71`), or lost on reconnect. The
   timer guarantees a poll at least every 30s, and it's **reset on every
   notify** so a busy batch flow doesn't double-poll.
3. **The 5s housekeeping ticker**: config reload
   (`c.cfg.Store(newCfg)`), backup pool start/stop, backup scheduling, and
   stale-task reclamation (throttled to `StaleTaskAfter/10` seconds, min 60s
   — `queue.go:182-193`).

### Forking workers

`consumeNextQueuedBatch` (`queue.go:266-316`) is the daemon's core: check the
live-owner count against `MaxConcurrentBatches` (`CountLiveBatches`), take
the next queued batch, mark it `processing`, **pre-create a placeholder owner
row** (`owner_id = "queue-" + batch.ID[:8]`, the daemon's own PID), and fork:

```go
// --force so the child overwrites our placeholder owner row.
cmd := exec.Command(os.Args[0], "consume", "--batch", batch.ID, "--force")
cmd.Stdin = nil; cmd.Stdout = nil; cmd.Stderr = nil
if err := cmd.Start(); err != nil {
	logger.Error(nil, "fork consume for batch %s: %v — requeueing", batch.ID, err)
	... requeue ...
	return nil
}
go func() { cmd.Wait() }()
```

The placeholder row is the crash-safety trick: if the child dies between fork
and acquire, stale reclamation finds a stale owner row (the daemon's PID) and
recovers the batch. The child's `--force` overwrites the placeholder with its
own lease.

### Reclaiming stale batches

`reclaimStaleBatches` (`queue.go:207-253`) runs on the housekeeping ticker:
for each owner stale > 15s whose batch still has work
(`ListStaleBatchOwners`): SIGTERM the (still-alive) owner PID, reset its
processing tasks (§10), quarantine failed files, decide the batch's fate —
`failed` if no work remains, `queued` otherwise — and delete the owner row so
the batch can be re-forked.

---

## 7. The consume command

`kushim consume --batch <id>` (`internal/commands/consume.go`) is the
per-batch worker. Batch-mode flow (`consume.go:75-183`):

1. **Paused-batch hard stop** (`consume.go:112-117`): a paused batch refuses
   to run, with a message about resolving the LLM billing issue.
2. **Acquire the lease** with `--force` escalation (`consume.go:119-137`):
   `ErrBatchLocked` without `--force` prints the incumbent PID and exits;
   with `--force` it calls `AcquireForce` (the `excluded` upsert).
3. **Entry repair** (`consume.go:141-147`): reset orphaned processing tasks
   from previous crashes (`ResetProcessingByBatch`) and quarantine failed
   files — the batch is healed before work starts.
4. **Heartbeat + pools**: `SetRunnerOwnerID(ownerID)`, start the heartbeat,
   start the consume and enrich pools (`pollBatch`), and monitor.
5. **Terminal status** (`setBatchTerminalStatus`, `consume.go:628-655`):
   `failed` if any task failed, else `completed` — skipping already-terminal
   states. `ErrBatchPaused` is swallowed as success (the pause was the
   outcome).
6. **Cleanup**: stop heartbeat, release the lease with a 5s timeout, trigger
   a post-batch orphan scan (`triggerOrphanScan`, 5-minute budget).

`pollBatch` (`consume.go:532-626`) is the progress monitor: a 500ms ticker
watches the batch row, prints per-file transitions (pending→processing,
→completed, →failed) from a `previous` map, detects the pause transition
(`ErrBatchPaused`), and prints the summary when `remain == 0`.

Standalone mode (`consume.go:191-319`, no daemon) refuses to run while any
paused batch exists, scans the inbox, enqueues the consume+enrich task pairs,
and — only if no other batch is queued — runs the same
owner/heartbeat/pollBatch sequence in-process.

---

## 8. Parent/child choreography: waiting tasks

A consume task creates its enrich task *immediately*, in `waiting` state,
with mutual references (`handlers/consume.go:224-244`):

```go
consumePayload, _ := json.Marshal(map[string]any{
	"file_path":    f.OriginalPath,
	"file_index":   i + 1,
	"on_completed": enrichTaskID,      // child's task ID
	"document_id":  documentID,
})
enrichPayload, _ := json.Marshal(map[string]any{
	"waiting_for": consumeTaskID,      // parent's task ID
	...
})
dispatcher.Enqueue(ctx, "enrich", batchID, enrichPayload, enrichTaskID, "waiting")
```

On consume success, the child is **activated** (`activateChildEnrich`,
`handlers/consume.go:107-121`): it validates the `waiting_for` link, injects
`document_id` into the payload, and flips the child to `pending`:

```sql
-- name: SetEnrichTaskPending :exec
UPDATE task SET
    status = 'pending',
    payload = $1,
    error = NULL,
    completed_at = NULL
WHERE id = $2 AND status IN ('waiting', 'discarded') AND task_type = 'enrich';
```

The `IN ('waiting', 'discarded')` guard means a discarded child can also be
re-armed. On consume failure, the child is **discarded**
(`deactivateChildEnrich`, `handlers/consume.go:120-153`) — marked
`discarded` with the parent's error, so the enrich never runs against a
document that doesn't exist, and it doesn't count as a failed task.

The waiting→discarded transition is guarded (`status = 'waiting'`), so the
parent's failure can't clobber a child that some other path already
activated. A discard that matches 0 rows (child already discarded or
activated) is a benign no-op logged at info level.

**Every consume failure path discards the child**, not just failures inside
`Process` — the handler wraps all error returns in `withDiscardAttempt`
(`handlers/consume.go:158-166`), covering the early exits too: payload
unmarshal failure (with a best-effort lenient re-parse of `on_completed` via
`recoverOnCompleted`), an empty `file_path`, and `FileFromPath` failures
(the realistic crash-retry case: the inbox file was already moved). If the
discard itself fails (lookup error, nil payload, `waiting_for` mismatch, DB
error), the failure is appended to the returned task error
(`"; additionally failed to discard enrich task <id>: ..."`) so it lands in
the task's `error` field and is visible in the UI/API.

On retry or reset, the child is restored **discarded → waiting**:

```sql
-- name: SetEnrichTaskWaiting :execrows
UPDATE task SET
    status = 'waiting',
    error = NULL,
    completed_at = NULL
WHERE task_id = $1 AND status = 'discarded' AND task_type = 'enrich';
```

The restore is targeted by `task_id` (the consume payload's `on_completed`),
so it cannot race a claim. It runs after the individual task retry
(`crud.go:127-150`), and as join-based `UPDATE ... FROM` statements inside
the same transaction as the batch retry / batch resets
(`RestoreDiscardedEnrichTasks` / `RestoreDiscardedEnrichTasksByBatch`,
§10-11). The invariant this upholds: an enrich is never left `discarded`
while its consume is `pending` again, and it is never `waiting` while its
consume is terminal-`failed` (that half is enforced by the sweeps, §10).

---

## 9. Deduplication

Dedup is enforced by the **partial unique index**
(`00001_baseline.sql:127-128`):

```sql
CREATE UNIQUE INDEX idx_task_dedup ON task(task_type, dedup_key)
    WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL;
```

Uniqueness holds **only while the task is live** — once it completes/fails,
the row drops out of the index and the same key becomes insertable again
(that's how a re-ingest after a crash works). The Go side
(`internal/task/handler.go:12-14`, `registry.go:28-37`) computes keys via the
optional `Dedupable` interface:

```go
type Dedupable interface {
	DedupKey(payload json.RawMessage) string
}
```

Key formats in use:

| Task type | Key | Source |
|---|---|---|
| consume | `consume:<md5>` (scan/restore paths) or the file path (direct CLI) | `scan.go:90-97`, `service/orphaned.go:183` |
| enrich | `enrich:doc:<document_id>` | `handlers/enrich.go:52-61`, `service/enrich.go:44` |
| backup | `backup:<UTC date>` | `handlers/backup.go:36-38` |
| mirror | `mirror:<UTC date>` | `handlers/mirror.go` |
| config | `config:tessdata:<lang>` / `config:hugot` / `config:migrate-db` / `config:migrate-storage` | `configtask/configtask.go` |

The waiting (child) enrich tasks store **no dedup key** — dedup only kicks in
once activated, when the payload gains `document_id`. A duplicate insert
surfaces as a unique-violation → `errs.KindConflict` → "re-enrich already
queued" (`commands/enrich.go:37-45`).

---

## 10. Reclamation after crashes

Two sweeps recover from dead workers, both driven by the **attempts budget**
(`Consumer.Reclaim.MaxRetries`, default 3) and the **staleness cutoff**
(`StaleTaskAfter`, default 600s — a task is only stale if it's been in
`processing` longer than that, so a slow-but-alive task isn't touched):

```sql
-- name: QuarantineStaleProcessingTasks :execrows
UPDATE task SET
    status = 'failed',
    error = 'Max retries exceeded (' || attempts || ')',
    completed_at = CURRENT_TIMESTAMP
WHERE status = 'processing' AND attempts >= $1 AND started_at < $2;

-- name: ResetStaleProcessingTasks :execrows
UPDATE task SET
    status = 'pending',
    attempts = attempts + 1
WHERE status = 'processing' AND attempts < $1 AND started_at < $2;
```

(`task.sql:218-229`; per-batch variants at `batch.sql:41-52`.) Below the
budget → requeue with `attempts + 1`; at/above it → `failed` with a
recognizable error string. The global sweep runs from the queue daemon's
housekeeping ticker; the per-batch variant runs at consume entry and batch
resume.

`attempts` increments **only** on reclamation resets — a normal handler
failure goes straight to `failed` (visible, manually retryable) without
burning budget.

Failed consume tasks get their files moved to the quarantine dir via
`GetQuarantinedConsumeTaskPayloads` + `QuarantineFailedFiles`
(`consumption/consumer.go:423-448`). The same function then runs one
batch-scoped sweep (`DiscardWaitingEnrichesOfFailedConsumes`) that discards
**every** still-`waiting` enrich whose consume in the batch is `failed`,
copying the parent's error text — broader than the old per-task discard,
which only handled `'Max retries exceeded'` tasks. The sweep runs
unconditionally (even with zero quarantined rows), so it also catches legacy
stuck data. The global stale-task sweep (`ResetStaleProcessingTasks`,
`service/batch.go:455-496`) runs a global variant of the same sweep, since
its quarantined consumes have no batch-level recovery point.

Both sweeps also **restore**: the reset half (consume back to `pending`)
pairs with `RestoreDiscardedEnrichTasks` (global) or
`RestoreDiscardedEnrichTasksByBatch` (per-batch) inside the same
transaction — a discarded enrich whose consume is `pending` again flips back
to `waiting`. Quarantined consumes (`failed`) are naturally excluded by the
`status = 'pending'` filter, and their enriches are discarded by the sweep
instead.

### Per-task panic recovery

Panics inside `Runner.Next` after the claim succeeds are caught by a deferred
`recover()` (`runner.go:37-48`): the task is failed immediately via `FailTask`
with `panic: <text>` recorded in `task.error` — the real panic text, not the
sweep's generic `'Max retries exceeded'` — and the REQID is logged. The error
is returned to the pool, which logs it and keeps the worker running; a plain
error return never restarts the worker (only an uncaught panic in `runWorker`
does). The fail-write uses a detached `context.Background()` with a 10s
timeout so it lands even when the panic coincides with shutdown. The sweeps
above remain the backstop for hard crashes (kill -9, OOM) where no Go-level
recover can run: the recover handles panics promptly with attribution, the
sweeps handle process death after the cutoff.

---

## 11. Retry and resume

- **`kushim task retry <id>` / `POST /api/v1/tasks/{id}/retry`** — resets one
  task to `pending` (clears result/error/attempts); guarded to `failed` tasks
  only (`crud.go:127-150`; 409 otherwise). For consume tasks with an
  `on_completed` payload field, the paired `discarded` enrich is restored to
  `waiting` via `SetEnrichTaskWaiting`; if that restore fails it is logged
  and the retry still succeeds (activation accepts `discarded` anyway).
- **`POST /api/v1/batches/{id}/retry`** — `RetryFailed`
  (`service/batch.go:211-235`): now transactional — `RetryFailedTasksByBatch`
  then `RestoreDiscardedEnrichTasksByBatch` commit together, so the restore
  can't race task claiming.
- **`POST /api/v1/batches/{id}/resume`** (`api/handlers/task.go:495-547`) —
  the full recovery: refuses if the batch is settled (no pending work) or
  locked by a live owner (409), then `ResetProcessingTasksByBatch` +
  `RequeueBatch` (`status = 'queued'`) — the daemon's notify/timer picks it
  up and re-forks.
- **Re-enrich** (`service/enrich.go:26-50`) — a fresh `queued` batch with one
  `pending` enrich task; the dedup index rejects duplicates.

The reset paths also restore: `ResetProcessingTasksByBatch`
(`service/batch.go:418-453`), `Owner.ResetProcessingByBatch`
(`internal/task/batch.go:119-154`), and the global
`ResetStaleProcessingTasks` each run their scoped restore inside the same
transaction as the reset, so a `discarded` enrich is re-armed exactly when
its consume returns to `pending`.

The wizard also auto-resumes config setup on boot
(`internal/wizard/server.go:43-53`).

---

## 12. The backup gate

Backups must not race the pipeline. The mechanism is a **single-row lock
table** plus a SQL function used as a claim gate
(`00005_backup_lock.sql` + `task.sql:18-22`):

```sql
UPDATE backup_lock SET running = true, started_at = NOW()
WHERE id = 1 AND (NOT running OR started_at <= NOW() - INTERVAL '30 minutes');
```

- Acquiring affects 0 rows if the lock is held and fresh — the same
  rows-affected discipline as task claims. The 30-minute staleness window
  means a crashed backup (no release) auto-expires.
- `is_backup_running()` is called in the **task-claim queries** themselves
  (`GetNextPendingTaskOfTypeWithGate`): while a backup holds the lock, no
  new consume/enrich task is claimed — workers go idle instead of mutating
  documents mid-backup.
- The backup handler (`internal/task/handlers/backup.go`) holds the
  lock for its whole run (release via `defer`), then **drains** in-flight
  work (`database.WaitForTaskDrain`, `internal/database/migrate.go`: 5s ticker on
  `CountProcessingTasks` until zero) before snapshotting, then applies
  retention. The `migrate-db` and `migrate-storage` config tasks use the same drain helper
  before copying the database or moving storage files.
- Scheduling: the queue daemon's ticker enqueues a `backup` task when
  `IsBackupDue` and the lock is free; the `backup:<date>` dedup key prevents
  same-day duplicates.

The **mirror** task (`internal/task/handlers/mirror.go`) participates in the
same gate: it acquires the backup lock, drains in-flight work, and runs
`rsync -a --delete` over the storage tree (see `internal/mirror/`). Because a
large rsync can exceed the 30-minute staleness window, the handler runs a
heartbeat that refreshes `backup_lock.started_at` every 5 minutes
(`TouchBackupLock`) for the rsync itself — the gate never goes stale
mid-mirror. The heartbeat starts only *after* `WaitForTaskDrain` returns, so
the 30-minute staleness recovery still applies during a long drain (a
continuously busy pipeline cannot keep the lock fresh forever and block
backups/migrations). Scheduling mirrors `IsBackupDue` as `IsMirrorDue` (same
`NextBackupTime` anchoring, keyed on the last completed `mirror:<date>` task);
the manual `kushim mirror` command bypasses task dedup but still waits for the
lock and drain, and writes the `.edub-mirror.json` diagnostics file into the
destination. Both the handler and the CLI share `mirror.RunLocked`, which owns
the drain → heartbeat → rsync → state-file sequence.

---

## 13. Configuration knobs

| Key (config) | Default | Meaning |
|---|---|---|
| `consumer.workers` | — | consume pool size (per-batch process) |
| `enricher.workers` | `1` | enrich pool size |
| `srv.max_concurrent_batches` | `4` | daemon's `CountLiveBatches` cap |
| `consumer.reclaim.enabled` | `true` | stale sweeps on/off |
| `consumer.reclaim.max_retries` | `3` | the attempts budget |
| `consumer.reclaim.stale_task_after` | `600` (s) | processing-staleness cutoff (≥ 60) |
| `backup.enabled` / `backup.schedules` | — | backup scheduling (per-mode: full/database/documents, each with interval/time/path/keep) |
| `mirror.enabled` / `mirror.path` / `mirror.interval` / `mirror.time` | — | mirror scheduling (rsync --delete over storage; requires rsync on PATH) |
| `polling.interval` (min) | — | inbox scan cadence (floor 1 min) |
| `polling.active_windows` | — | only scan within these windows |

---

## 14. Gotchas

- **`rows == 0` is usually success, not failure** — for claims it means
  "someone else won" (idle, not error); for completes it means "already
  transitioned". Only treat 0 rows as an error where the contract says so
  (e.g. `AcquireBackupLock`).
- **`FailTask` has no status guard** — it can overwrite a `completed` task if
  called from a stale path. The runner's ordering (complete first, fail only
  as fallback) exists to minimize this; keep it.
- **`attempts` only grows on reclamation resets** — never bump it on ordinary
  handler failures; the budget exists to bound crash loops, not retries.
- **Owner-scoped claiming is the lease's teeth** — a worker whose lease was
  stolen (heartbeat 0 rows) keeps running but claims nothing. Don't remove
  the `owner_id` gate "for simplicity".
- **The placeholder owner row is the daemon's crash net** — if a child dies
  before acquiring, reclamation finds the placeholder (daemon PID) stale.
  Removing the pre-create breaks that window.
- **`--force` has two meanings**: steal-an-active-lease (consume CLI) and
  overwrite-my-own-placeholder (daemon fork). Both use
  `AcquireBatchOwnerForce`; the daemon's use is safe only because the
  placeholder's `owner_id` is its own synthetic id.
- **NOTIFY is a hint; the 30s timer is the contract.** The non-blocking send
  drops notifications when the daemon is busy — never "fix" that by making
  the channel blocking, or a full channel stalls the daemon.
- **`waiting` tasks are invisible to normal claims** (they're not `pending`)
  and must be activated or discarded — an orphaned `waiting` row means the
  parent's completion handler didn't run. The reclaim sweeps only ever
  *discard* waiting enriches of terminal-failed consumes; they never
  activate them.
- **The enrich invariant is enforced at recovery points, not continuously**:
  "enrich is never `waiting` while its consume is terminal-`failed`" holds
  because the handler discards on every failure path, and the sweeps
  (`QuarantineFailedFiles`, `ResetStaleProcessingTasks`) mop up anything the
  handler missed (crashes, legacy data). A consume that reached `failed`
  without a sweep running in between can briefly show a `waiting` enrich —
  the next recovery point closes it.
- **The individual-retry restore is log-only on failure** — if
  `SetEnrichTaskWaiting` fails after a `task retry`, the retry still succeeds
  and the enrich stays `discarded`; it is re-armed later by activation
  (`SetEnrichTaskPending` accepts `discarded`) or by the next batch
  recovery-point restore.
- **Paused batches refuse to run** (`consume.go:112-117`) and pause is sticky
  — resume is an explicit API/CLI action after the billing issue is fixed.
- **`CountLiveBatches` counts heartbeats, not processes** — a batch whose
  worker is mid-task but heartbeating normally counts as live; only 15s of
  silence frees the slot.

---

*Last verified against the tree: 2026-08-03. If code and doc disagree, code wins.*

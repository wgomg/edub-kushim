# Developer Guide — The Task System and Queue Semantics

This guide explains how edub-kushim processes work in the background: the
task lifecycle, the optimistic claim mechanism, batch ownership with
heartbeats, the queue daemon, deduplication, reclamation after crashes, and
the backup gate. It is the *conceptual* companion to
`docs/reference/task-system.md` (the file inventory): it answers "why can't a
task be lost or run twice?"

Audience: developers touching `internal/task/`, `internal/ops/queue/queue.go`,
`internal/ops/consume/consume.go`, or the task/batch queries.

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
12. [The resource locks](#12-the-resource-locks)
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
| `Enqueuer` | `internal/task/enqueue.go` | the single creation path (`EnqueueBatch`/`EnqueueTask`) over `BatchSpec`/`TaskSpec` |
| `TypeDefinition` | `internal/task/type_definition.go` | one task type's enqueue contract: locks, dedup key, preconditions, builder |
| `Registry` | `internal/task/registry.go` | task-type → handler map |
| `Handler` | `internal/task/handler.go` | does the work, returns JSON result; `RunBlocking` wraps the lock-holding lifecycle |
| `Runner` | `internal/task/runner.go` | claims + dispatches + completes |
| `Pool` | `internal/task/pool.go` | N worker goroutines calling `Nexter.Next` |
| `Owner` / `Heartbeat` | `internal/task/batch.go`, `heartbeat.go` | batch lease management |
| queue daemon | `internal/ops/queue/queue.go` | orchestrates batches |
| consume command | `internal/ops/consume/consume.go` | runs one batch |

The thirteen task types (declared in `internal/types`, registered in
`internal/ops/container/container.go`): `consume`, `enrich`, `thumbnail`,
`config`, `migrate-db`, `migrate-storage`, `backup`, `mirror`,
`tagreconcile`, `thumbbackfill`, `tagmeasure`, `tagidentity`, `tagaudit`.

---

## 2. The task table and statuses

Schema (`internal/database/sql/schema/migrations/00001_baseline.sql:41-55`,
with enum columns from migration `00013`):

```sql
CREATE TABLE task (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id TEXT NOT NULL UNIQUE,
    task_type task_type NOT NULL,
    status task_status NOT NULL DEFAULT 'pending',
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

Seven statuses, enforced by the `task_status` enum: `pending`, `processing`,
`waiting`, `completed`, `failed`, `discarded`, `cancelled`. The vocabulary is
closed at the database level (enum columns, migration `00013`) and mirrored
by the Go registry in `internal/types` — the single declaration point in Go
(`types.Task.Type.*`, `types.Task.Status.*`, `types.Batch.Origin.*`,
`types.Batch.Status.*`), wired into sqlc-generated code via column
overrides. `batch.origin`, `batch.status`, `task.task_type`, and
`task.status` are all enum columns; an unregistered value fails the insert
(and migration `00013` aborts loudly on pre-existing legacy values).

The `progress` column (migration 00011) is a nullable JSONB snapshot of the
current pipeline step, written by the owning handler while the task is
`processing`:

```json
{"step": "ocr", "detail": "page 3/12", "pct": 25.0, "updated_at": "2026-09-05T22:00:00Z"}
```

`step` is required; `detail` and `pct` are optional. It is a display aid
only — the pipeline never reads it back. `ClaimTask` clears it so each
attempt starts clean, and `UpdateTaskProgress` only writes while
`status = 'processing'`, so a write racing the stale-sweep reset is a no-op.
The reclaim/retry paths (`ResetProcessingTasksByBatch`,
`ResetStaleProcessingTasks`, `RetryTask`) also clear `progress` alongside
the status flip, so a re-queued task never displays a stale step from a
previous attempt. `ProgressTracker` (`internal/task/progress.go`) is the
single writer per task and throttles writes to ≥1/s unless the step
changes; the JSONB shape is the shared `task.ProgressSnapshot` struct that
the API and CLI readers decode. Step vocabularies:
consume (`duplicate-check`, `convert`, `extract-text`, `ocr`, `optimize`,
`place`, `commit`, `activate-children`), enrich (`reduce`, `tag-match`,
`analyze`, `consolidate`, `persist`), thumbnail (`render`, `save`), backup
(`drain-wait`, `snapshot`, `retention`), mirror (`drain-wait`, `sync`),
config (`download`, `migrate-db`, `migrate-storage`).

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
    started_at = CURRENT_TIMESTAMP,
    progress = NULL,
    claim_token = $2
WHERE id = $1 AND status = 'pending';
```

The `WHERE status = 'pending'` is the entire concurrency control: of N racing
workers, exactly one flips the row and affects 1 row; the rest affect 0. The
Go side checks `RowsAffected` (that's what sqlc's `:execrows` annotation
means) and treats 0 as "someone else got it" (`internal/task/store.go:58-96`).
The `claim_token` column is written only by the claims of lock-holding
types (§12) and is NULL for every other task type.

```go
rows, err := s.queries.ClaimTask(ctx, id)
if rows == 0 {
	return database.Task{}, sql.ErrNoRows
}
```

This is the project-wide pattern: **every concurrency-sensitive transition is
a guarded UPDATE with rows-affected acknowledgment** — `ClaimTask`,
`CompleteTask`, `TryInsertBatchOwner`, `UpdateBatchOwnerIfStale`,
`HeartbeatBatchOwner`, `ReleaseBatchOwner`, `AcquireLocks`. Nothing is
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
(`batch.sql:64-69`):

```sql
-- name: GetNextPendingTaskOfTypeForOwner :one
SELECT id FROM task
WHERE status = 'pending' AND task_type = @task_type
  AND batch_id IN (SELECT batch_id FROM batch_owner WHERE owner_id = @owner_id)
  AND NOT EXISTS (
    SELECT 1 FROM system_signal s
    WHERE s.name = ANY(@wait_locks::text[]) AND lock_is_held(s.name)
  )
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

`kushim queue` (`internal/ops/queue/queue.go`) is the orchestrator. It never
processes tasks itself.

### The wake-up loop

Three mechanisms keep it responsive without busy-polling
(`queue.go:133-316`):

```go
go runPollingLoop(ctx, c, client, batchSvc, maxConcurrent)

notifyCh := make(chan struct{}, 4)
go listenForBatchNotifications(ctx, config.BuildPostgresDSN(c.GetConfig().Db), notifyCh, c.GetLogger())

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

1. **Postgres NOTIFY** (`internal/ops/queue/notify.go`): a dedicated
   single-connection pool `LISTEN`s on `batch_queued` and `lock_released`,
   pushing a token into `notifyCh` on either. The `batch_queued` trigger
   lives in the database (`00004_listen_notify.sql`) and fires on
   `INSERT`/`UPDATE` of a `queued` batch; a released resource lock fires
   `lock_released` at commit (the release queries call `pg_notify`), so work
   deferred on a lock wakes as soon as the lock frees.
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
	logger.Error("", "fork consume for batch %s: %v — requeueing", batch.ID, err)
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

`kushim consume --batch <id>` (`internal/ops/consume/consume.go`) is the
per-batch worker. Batch-mode flow (`consume.go:95-200`):

1. **Paused-batch hard stop** (`consume.go:114-119`): a paused batch refuses
   to run, with a message about resolving the LLM billing issue.
2. **Acquire the lease** with `--force` escalation (`consume.go:131-144`):
   `ErrBatchLocked` without `--force` prints the incumbent PID and exits;
   with `--force` it calls `AcquireForce` (the `excluded` upsert).
3. **Entry repair** (`consume.go:141-147`): reset orphaned processing tasks
   from previous crashes (`ResetProcessingByBatch`) and quarantine failed
   files — the batch is healed before work starts.
4. **Heartbeat + pools**: `SetRunnerOwnerID(ownerID)`, start the heartbeat,
   start the consume and enrich pools (`pollBatch`), and monitor.
5. **Terminal status** (`setBatchTerminalStatus`, `consume.go:655-689`):
   `failed` if any task failed, else `completed` — already-terminal states skip
   only the settle write, never the bookend. The settle logs one INFO bookend
   from the batch's task listing: completed/failed/cancelled counts plus the
   aggregated per-document enrich counts when any completed enrich result
   decoded. `ErrBatchPaused` is swallowed as success (the pause was the
   outcome).
6. **Cleanup**: stop heartbeat, release the lease with a 5s timeout, trigger
   a post-batch orphan scan (`triggerOrphanScan`, 5-minute budget).

`pollBatch` (`consume.go:526-646`) is the progress monitor: a 500ms ticker
watches the batch row, prints per-file transitions (pending→processing,
→completed, →failed) from a `previous` map, detects the pause transition
(`ErrBatchPaused`), and prints the summary when `remain == 0`. While a task
is `processing`, it also prints live `progress.step`/`detail` updates (e.g.
`ocr page 3/12`) whenever the recorded progress changes.

### Non-consume batches: backup, mirror, config

Backup and mirror tasks get real batch rows (origin `schedule`, status
`queued`) so they flow through the batches UI instead of being invisible
orphans (`maybeScheduleTask`, `queue.go`); config batches already did this.
The queue daemon never forks consume workers for these batches:
`GetNextQueuedBatch` excludes batches carrying a non-consume task type
(`config`, the two migrations, `backup`, `mirror`, and the four tag passes),
so no watchdog holds a `max_concurrent_batches` slot for the duration of a
long backup.

The handlers own the lifecycle directly, keeping the status accurate even
when `kushim queue` is not running: `MarkBatchProcessing` at start and
`FinalizeBatchStatus` (completed/failed) at the end, once no sibling tasks
remain active (`internal/task/batch.go`); the settle also logs one INFO
summary line with the final status and the completed/failed/cancelled task
counts (a failed count query is logged and never blocks the settle).
Because these batches have no `batch_owner` row in that case, the service
layer forces `Orphaned = false` for batches with a non-consume task —
otherwise the UI would show a bogus orphan state and a Resume button that
forks a consume worker (`internal/service/batch.go`).

Standalone mode (`consume.go:191-319`, no daemon) refuses to run while any
paused batch exists, scans the inbox, enqueues the consume+enrich task pairs,
and — only if no other batch is queued — runs the same
owner/heartbeat/pollBatch sequence in-process.

---

## 8. Parent/child choreography: waiting tasks

A consume task creates its enrich task *immediately*, in `waiting` state,
with mutual references (assembly shared by every ingest boundary,
`internal/documents/consumption/ingest.go`):

```go
consumePayload, _ := json.Marshal(map[string]any{
	"file_path":    file.Path,
	"file_index":   file.Index,
	"on_completed": enrichTaskID,      // child's task ID
	"document_id":  documentID,
	"md5":          md5,
})
enrichPayload, _ := json.Marshal(map[string]any{
	"waiting_for": consumeTaskID,      // parent's task ID
	...
})
result, err := enqueuer.EnqueueBatch(ctx, spec, tasks) // the waiting child rides along
```

On consume success, the child is **activated** (`activateChildEnrich`,
`internal/documents/consumption/consume.go:99-135`): it validates the `waiting_for` link, injects
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
(`deactivateChildEnrich`, `internal/documents/consumption/consume.go:136-170`) — marked
`discarded` with the parent's error, so the enrich never runs against a
document that doesn't exist, and it doesn't count as a failed task.

The waiting→discarded transition is guarded (`status = 'waiting'`), so the
parent's failure can't clobber a child that some other path already
activated. A discard that matches 0 rows (child already discarded or
activated) is a benign no-op logged at info level.

**Every consume failure path discards the child**, not just failures inside
`Process` — the handler wraps all error returns in `withDiscardAttempt`
(`internal/documents/consumption/consume.go:247-260`), covering the early exits too: payload
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
(that's how a re-ingest after a crash works). The Go side derives keys in the
task type catalog (`internal/ops/container/task_type_definitions.go`), one
derivation per task type; `EnqueueBatch` applies it to pending tasks.

Key formats in use:

| Task type | Key |
|---|---|
| consume | `consume:<md5>` |
| enrich | `enrich:doc:<document_id>` |
| thumbnail / thumbbackfill | `thumbnail:doc:<document_id>` (no key when the payload carries no document id) |
| backup | `backup:<mode>:<UTC date>` (mode defaults to `full`) |
| mirror | `mirror:<UTC date>` |
| tag passes | fixed per type: `tagreconcile`, `tagmeasure`, `tagidentity`, `tagaudit` |
| config | `config:tessdata:<lang>` / `config:hugot` |
| migrations | fixed per type: `migrate-db`, `migrate-storage` |

The waiting (child) enrich tasks store **no dedup key**, and never gain one:
the column is only written on insert, and a waiting child's insert carries
none, so an activated child stays dedup-free. A duplicate enqueue of keyed
work loses the dedup conflict: `EnqueueBatch` skips the task, no batch is
created, and the boundary reports the work as already queued ("re-enrich
already queued", `internal/ops/cli/enrich.go`). Only a violation of the dedup
index counts as a skip — a collision on any other unique constraint (the task
id above all) aborts the enqueue and creates nothing.

---

## 10. Reclamation after crashes

Two sweeps recover from dead workers, both driven by the **attempts budget**
(`Consumer.Reclaim.MaxRetries`, default 3) and the **staleness cutoff**
(`StaleTaskAfter`, default 600s — a task is only stale if it's been in
`processing` longer than that, so a slow-but-alive task isn't touched). The
age transitions below apply to the workloads with no declared holds (consume,
enrich, thumbnail); the lock-holding types (backup, mirror, the four tag
passes, thumbbackfill, migrate-db, migrate-storage) are swept by lock-aware
variants instead:

```sql
-- name: QuarantineStaleProcessingTasks :execrows
UPDATE task SET
    status = 'failed',
    error = 'Max retries exceeded (' || attempts || ')',
    completed_at = CURRENT_TIMESTAMP
WHERE status = 'processing' AND attempts >= $1 AND started_at < $2
  AND task_type <> ALL(@types::task_type[]);

-- name: ResetStaleProcessingTasks :execrows
UPDATE task SET
    status = 'pending',
    attempts = attempts + 1,
    progress = NULL
WHERE status = 'processing' AND attempts < $1 AND started_at < $2
  AND task_type <> ALL(@types::task_type[]);
```

(`task.sql`; per-batch variants at `batch.sql:41-52`.) Below the budget →
requeue with `attempts + 1`; at/above it → `failed` with a recognizable
error string. The global sweep runs from the queue daemon's housekeeping
ticker; the per-batch variant runs at consume entry and batch resume.

**Lock-holding recovery is gated on lock ownership.** A lock-holding task is
swept only when no fresh resource-lock row belongs to its execution: the
sweep's exemption matches a lock whose `owner_token` equals the task's
`claim_token` (a claim-held lock) or whose `batch_id` equals the task's batch
(a batch-held lock — the consume child of a `thumbbackfill` batch holds
`storage` for the whole run under its own batch-owner token), with the row's
`started_at` key inside the 30-minute freshness window. A live holder
(5-minute heartbeat refreshing) keeps both the task row and the lock; a
crashed holder is recovered exactly once. The sweep transaction releases
stale holders' locks first (`ReleaseLocksForStaleHolders` — locks past the
window whose `owner_token` belongs to a `processing` lock-holding claim, so
the release must precede the resets), then applies the transitions:

```sql
-- the NOT EXISTS exemption both sweeps carry
NOT EXISTS (
    SELECT 1 FROM system_signal s
    WHERE s.name = ANY(@lock_names::text[])
      AND s.value = 'true'
      AND (s.data->>'started_at')::timestamptz > NOW() - INTERVAL '30 minutes'
      AND ((s.data->>'owner_token') IS NOT DISTINCT FROM task.claim_token::text
           OR s.data->>'batch_id' = task.batch_id)
);
```

(`QuarantineStaleBlockingTasks` / `ResetStaleBlockingTasks` in
`sql/queries/lock.sql`; the freshness expression is the same boundary the
acquire queries use to steal, so "the sweep says stale" and "an acquire would
succeed" never disagree. Per-batch resets release the batch's locks by id in
the same transaction, which also covers a crashed child's run-held lock.
Manual NULL-token locks match neither disjunct — the window is the manual
holder's only recovery path.)

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
`service/batch.go:490`) runs a global variant of the same sweep, since
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
- **`POST /api/v1/batches/{id}/resume`** (`api/handlers/task.go:525-574`) —
  the full recovery: refuses if the batch is settled (no pending work) or
  locked by a live owner (409), then `ResetProcessingTasksByBatch` +
  `RequeueBatch` (`status = 'queued'`) — the daemon's notify/timer picks it
  up and re-forks.
- **Re-enrich** (`service/enrich.go:29-53` single, `:55-89` bulk) — a fresh
  `queued` batch with one `pending` enrich task per document; the dedup index
  rejects duplicates (the bulk run counts them as skipped).

The reset paths also restore: `ResetProcessingTasksByBatch`
(`service/batch.go:418-453`), `Owner.ResetProcessingByBatch`
(`internal/task/batch.go:119-154`), and the global
`ResetStaleProcessingTasks` each run their scoped restore inside the same
transaction as the reset, so a `discarded` enrich is re-armed exactly when
its consume returns to `pending`.

The wizard also auto-resumes config setup on boot
(`internal/api/wizard/server.go:43-53`).

---

## 12. The resource locks

Pipeline work and bulk maintenance are serialized by **named resource locks**.
`internal/types` declares the closed set — two names — and, per task type, the
locks an execution **holds** for its whole batch run and the locks a claim
must see **free** before it may start (`types.LockDeclarationFor`). The
lifecycle lives in `internal/task/locks.go` on top of two `system_signal`
rows keyed by lock name (`maintenance`, `storage`): a `value` held flag and
JSONB `data` carrying `owner_token` (a UUID for task- and batch-held locks;
absent for the NULL-token manual holders), `batch_id` (recorded by every
acquisition so reclamation can release a batch's locks wholesale), and a
heartbeat timestamp. `lock_is_held(name)` says a lock is held iff the row is
marked held and its timestamp is inside **NOW() − 30 minutes** — the database
clock is the only clock (migration `00049_named_resource_locks.sql`) — and a
holder refreshes through 5-minute heartbeats. The two-level rule:

- The task claim gates every type on its declared waits: the selection query
  refuses a task whose waits are not all free (`NOT EXISTS … lock_is_held`).
- Types with declared holds claim through the token protocol: the claim, its
  acquisition and the release are one transaction
  (`Store.claimBlockingNextPending`, `internal/task/store.go`). Acquisition
  is all-or-nothing (`task.AcquireLocks`, backed by `AcquireLocks` in
  `sql/queries/lock.sql`): `LockRowsForUpdate` row-locks the
  `holds ∪ waits` rows in canonical name order, the waits must all be free,
  and each hold row is overwritten only if free or past the window. A
  conflict rolls the claim back — the task stays `pending` — and
  `ErrLockBusy` surfaces, mapped by the runner to a quiet `nil` so the pool's
  next tick retries instead of failing the task. No execution ever holds one
  lock while waiting for another, so no deadlock is possible; two same-day
  maintenance runs serialize with both eventually completing.
- The release is the task's leave-`processing` moment:
  `CompleteBlockingTask` / `FailBlockingTask` release the locks by claim
  token in the same tx as the transition (`ReleaseLocksForToken`); batch
  resets, cancel and retry release by batch id (`ReleaseLocksForBatch`).
- The drain set is derived, not listed: `types.TaskTypesWaitingOn(holds...)`
  is the in-flight work a holder must let finish — a `maintenance` holder
  drains `consume`/`enrich`/`thumbnail`/`thumbbackfill`, a `storage` holder
  drains `thumbnail` — and `database.WaitForTaskDrain` polls it to zero on a
  5s ticker before the body runs. `database.StartLockHeartbeat` (`TouchLocksForToken`) keeps every
  held lock fresh for the run; a holder that stops heartbeating becomes
  takeable after the 30-minute window, the last-resort recovery when no reaper
  is running (the daemon's sweeps recover task- and batch-held locks promptly;
  the window is the manual holder's only mechanism).
- The shared handler lifecycle is `task.RunBlocking`
  (`internal/task/handler.go`): mark batch processing → `HoldClaimed`
  (heartbeat + drain) → body → the runner's token-guarded transition. The
  backup handler and the four tag passes run through it; the `migrate-db` and
  `migrate-storage` handlers use `HoldClaimed` over their own batch
  bookkeeping (their acquisition happens at claim), and drain before copying
  the database or moving storage files.

Locks live as long as the *execution*, not one task. A batch whose task types
declare holds gets them acquired **for the run**: the consume child of a
`thumbbackfill` batch acquires `storage` all-or-nothing under its own
batch-owner token once the batch's waits are free (deferring in-process until
they are), drains `thumbnail`, and then runs its pool with claims that verify
waits but skip acquisition (`Store.SetBatchHoldsAcquired`). New `thumbnail`
claims from ingest batches defer until the backfill settles — per-document
thumbnails never write while the storage tree is being bulk-walked. The
release happens when the run ends; reclamation releases by batch id, and the
stale sweep's exemption covers both the claim token and the batch id (§10).

Direct CLI commands run under the manual holder path (`task.RunDirect`,
`internal/task/direct.go`): acquire the policy's locks — `LocksForDirectMode`
defaults to the queued set — all-or-nothing with a NULL token, heartbeat, drain, run the body, release. Waiting is the default; `--no-wait`
exits non-zero instead of waiting for a lock or drain.
`kushim backup` works with polling enabled — it waits for the lock and drains
rather than refusing. `kushim restore` and `kushim migrate database` keep
their quiescence preconditions because they replace state: they refuse while
the maintenance lock is held or gated tasks are processing (plus polling and
daemon PID checks for restore), try-acquire with a NULL token, and never wait
or drain.

Scheduling: the daemon's backup/mirror scheduling, the anchored scheduler and
the polling scan skip while `IsLockHeld(maintenance)`; a `storage`-only
holder does not pause scheduling or polling. Released locks notify
`lock_released`, which wakes the daemon's dispatch tick alongside
`batch_queued`. The queue daemon enqueues a `backup` task when `IsBackupDue`
and the lock is free; the `backup:<mode>:<date>` dedup key prevents
same-day duplicates per mode. The enqueue payload records the schedule
(`schedule: {interval, time}`) that produced the task; `IsBackupDue` parses
it from the last completed task and, when it differs from the current config
timing, re-phases the next run to the closest upcoming occurrence of the new
timing instead of waiting a full interval (tasks completed before this
change carry no recorded schedule and keep the steady rule).

The **mirror** task (`internal/backup/mirror_task.go`) participates in the
same gate: it is claimed with the locks held, drains in-flight work, and runs
`rsync -a --delete` over the storage tree (see `internal/backup/`). Because a
large rsync can exceed the 30-minute staleness window, the handler runs a
heartbeat that refreshes the lock row's heartbeat timestamp every 5 minutes
(`TouchLocksForToken`, guarded by the claim token) for the rsync
itself — the gate never goes stale mid-mirror. The heartbeat is started by the
shared `database.StartLockHeartbeat` helper *before*
`WaitForTaskDrain` returns, so the drain-wait itself is covered too; the
backup, tagreconcile, tagmeasure, tagidentity and tagaudit handlers get that sequence —
heartbeat, drain, body, batch finalize — from the shared `task.RunBlocking`
lifecycle (`internal/task/handler.go`). The queued mirror handler composes
`backup.RunLocked`, which owns the heartbeat → drain → rsync → state-file
sequence over the claim token; the direct `kushim mirror` command runs under
the manual holder path instead — waiting for the lock and draining by
default, `--no-wait` to fail fast — and writes
the `.edub-mirror.json` diagnostics file into the
destination. Scheduling mirrors `IsBackupDue` as `IsMirrorDue` (same
`NextBackupTime` anchoring, keyed on the last completed `mirror:<date>` task,
with the same payload-derived re-phase on schedule edits).

The **tagmeasure** task (`internal/tags/tagmeasure.go`) is a
lock-holding task too: claimed with the maintenance lock held, drained via
`WaitForTaskDrain`, heartbeat across the whole run — and it additionally forks
`kushim tagmeasure --run <batch-id>` and waits, so the pass's long
repeatable-read transaction runs in its own process while the parent keeps the
lock fresh; the child's printed report line becomes the task result and its
exit code decides completion/failure. The pass opens by checking the
`tag_measure_changed` flag: with the flag clear and a completed run under the
current rule set it short-circuits to a zero-work report, writing nothing;
otherwise its opening transaction clears the flag and the sweep runs, re-arming
the flag if the sweep fails. A manual `kushim tagmeasure` enqueues the same
queued task through `curation.TagMeasure` (dedup key `tagmeasure`), and the pool
always claims — `enricher.tag_measure.enabled` gates only the schedule
that enqueues runs automatically.

The **tagidentity** task (`internal/tags/admission/tagidentity.go`) follows the
same fork-and-wait shape: claimed with the lock held, drained via
`WaitForTaskDrain`, heartbeat across the run, then forks
`kushim tagidentity --run <batch-id>`; the child resolves pending identity
evidence through `admission.IdentityResolver` and prints the
`tagidentity-report: <json>` line (pending/resolved/folded/aliased/deferred/
failed) that becomes the task result. The review surface's reevaluate action, a
manual `kushim tagidentity` (which enqueues), and the pass's own anchored
schedule all create the same queued task. Its pool runs
unconditionally, and the schedule — a fixed daily 03:30 pass with no config
keys — fires only while `enricher.tag_adjudicator.enabled` is set, so pending
claims wait for the next run when the judge is off.

The **tagaudit** task (`internal/tags/admission/tagaudit.go`) follows the same
drain + heartbeat + fork shape as `tagmeasure`: claimed with the lock held,
drained via `WaitForTaskDrain`, heartbeat across the run, then forks
`kushim tagaudit --run <batch-id>`; the child runs the coverage-verification
pass (`admission.AuditRunner`) and prints the `tagaudit-report: <json>` line
(sweep, sampled/flagged counts, coverage tallies by class, fills/flags/
proposals, judge call counts, pending coverage) that becomes the task result.
Runs come from the anchored schedule (disabled by default), a manual
`kushim tagaudit` (which enqueues), and the reevaluate endpoint; the pool always
claims — `enricher.tag_audit.enabled` gates only automatic enqueuing.

The **tagreconcile** task (`internal/tags/reconcile/tagreconcile.go`) is a
lock-holding task too: claimed with the maintenance lock held, drained via
`WaitForTaskDrain` under the same 5-minute heartbeat, then forks
`kushim tagreconcile --run <batch-id>`; the child builds the reconciler and runs
one pass, printing the `tagreconcile-report: <json>` line that becomes the task
result (the exit code decides completion/failure). It dedups on the fixed key
`tagreconcile`, so an active run blocks the next trigger, and the daemon's pool
runs unconditionally — `enricher.tag_reconcile.enabled` gates only the schedule
that enqueues runs automatically, while a manual `kushim tagreconcile` enqueues
through the queue the same way.

The three tag passes with a store-changed flag open through `tags.OpenStoreSignal`
(`internal/tags/store_signal.go`): one committed transaction reads the pass's
flag (`tag_measure_changed`, `tag_reconcile_changed`, `tag_audit_changed`) and
clears it when set, so a mutation landing during a run re-arms the flag for the
next one, and a failed run re-arms it so the failure is never read as an
unchanged store. Only the measurement gates its work on the flag alone — no
completed run under the current rule set also forces a sweep. The reconcile
needs the signal plus a work-probe set (`reconcile/gate.go`): aged pairs, grace
windows and capped remainders are work a quiet store still owes, so the signal
alone cannot rule work out; a clear flag with every probe empty reports
`zero_work` and skips the store-wide reads. The audit gates only its store-wide
pending-coverage derivation on the flag — the sweep itself stays pool-driven (a
document is audited at most once per sweep), and an unchanged vocabulary reuses
the durable pending-coverage rows already written. The identity pass reads no
flag: its work set is the pending-event count, already its first read, and a
signal gate would strand capped remainders in a quiet store. A pass's own
applies re-arm the flags through the table triggers (migration `00044`;
`00045` removed the identity flag), which is the record's honest reading of
"changed since it last ran".

The **thumbbackfill** task (`internal/documents/consumption/thumbnail.go`'s
per-document `ThumbnailTaskHandler`) holds its lock at batch level: its
dedicated pool runs inside the forked consume child, which acquires
`storage` for the whole run after verifying `maintenance` is free, drains
in-flight thumbnail tasks before starting, and runs the same 5-minute
heartbeat across the run; its per-task claims verify the maintenance wait but
skip acquisition (§12, batch-held locks). `ThumbnailBackfill`
(`internal/service/thumbnail_backfill.go`) creates `thumbbackfill` tasks
(dedup key `thumbnail:doc:<uuid>`) in `thumbbackfill` batches.

Direct CLI holders (`kushim backup`, `kushim restore`, `kushim mirror`,
`kushim migrate`) are NULL-token lock rows: their release is guarded by
`owner_token`-or-`batch_id` matching a NULL, so it can never drop a loaded
task- or child-held lock.

A lock-holding task longer than 30 minutes keeps its locks alive via the
5-minute heartbeat, and the age sweep leaves it alone: lock-holding recovery
is gated on lock ownership (§10), so a live heartbeating holder is never reset
mid-run.

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
  (e.g. the guarded lock acquire).
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

*Last verified against the tree: 2026-10-22. If code and doc disagree, code wins.*

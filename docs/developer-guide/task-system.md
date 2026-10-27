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
8. [Successor choreography: waiting tasks](#8-successor-choreography-waiting-tasks)
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
per-batch leases; a daemon (`kushim queue`) watches for queued batches, claims
each one in a guarded transaction, forks the batch's owning task CLI as
`<cli> --from-batch <id> --exec-token <token>` (every remaining task type has
a batch-mode CLI; the ingest family's children hang from their batch, so a
mixed batch resolves to `consume`), and reclaims anything that dies.

The cast of characters:

| Component | Files | Role |
|---|---|---|
| `Enqueuer` | `internal/task/enqueue.go` | the single creation path (`EnqueueBatch`/`EnqueueTask`) over `BatchSpec`/`TaskSpec` |
| `TypeDefinition` | `internal/task/type_definition.go` | one task type's enqueue and dispatch contract: queued and direct locks, dedup hook, preconditions, batch-mode CLI surface and hosts |
| `Registry` | `internal/task/registry.go` | task-type → handler map |
| `Handler` | `internal/task/handler.go` | pure work function: does the job, returns the JSON result or an error; performs no batch-status write, lock or settle |
| `Runner` | `internal/task/execute.go` | claims under the batch lease, dispatches, completes/fails and settles; the batch's locks, drain and heartbeat belong to `RunBatch` |
| `Owner` / `Heartbeat` | `internal/task/batch.go`, `heartbeat.go` | batch lease management |
| batch dispatcher / launcher | `internal/ops/dispatch/dispatcher.go`, `launcher.go` | claims queued batches (status, lease and holds in one transaction) and forks the batch's owning task CLI in batch mode; the wizard's setup-time dispatcher runs the migration handlers in-process instead |
| queue daemon | `internal/ops/queue/queue.go` | orchestrates batches |
| task CLIs | `internal/ops/consume/consume.go`, `internal/ops/cli/` (enrich, thumbnails, backup, mirror, migrate and the four tag verbs) | batch mode: one claimed batch through the shared execution library; direct mode: the action now, no queue rows |

The twelve task types (declared in `internal/types`; the task CLI that runs a
type assembles the registry for it — the queue daemon and `kushim batch run`
fork the type's CLI instead of registering anything):
`consume`, `enrich`, `thumbnail`,
`migrate-db`, `migrate-storage`, `backup`, `mirror`,
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
`ResetStaleProcessingTasks`, the retry/resume
resets) also clear `progress` alongside
the status flip, so a re-queued task never displays a stale step from a
previous attempt. `ProgressTracker` (`internal/task/progress.go`) is the
single writer per task and throttles writes to ≥1/s unless the step
changes; the JSONB shape is the shared `task.ProgressSnapshot` struct that
the API and CLI readers decode. Step vocabularies:
consume (`duplicate-check`, `convert`, `extract-text`, `ocr`, `optimize`,
`place`, `commit`), enrich (`reduce`, `tag-match`,
`analyze`, `consolidate`, `persist`), thumbnail (`render`, `save`), backup
(`drain-wait`, `snapshot`, `retention`), mirror (`drain-wait`, `sync`),
migrations (progress reported by the two migration handlers).

The lifecycle:

```
                     +--> completed
pending --claim--> processing --error--> failed
   ^                    |
   | (retry/reset)      +--stale sweep--+--> pending (attempts+1) or failed (quarantine)
waiting --activate--> pending
waiting --parent failed--> discarded
discarded --restore (successor of a live parent)--> waiting
pending/processing --cancel--> cancelled
```

`waiting` is the special one: a successor task parked until the parent that
will activate it completes (§8 — ingest builds such successors for enrich and
thumbnail, and any enqueue may chain more). `discarded` is "never ran, never
will" — it doesn't count as a failure. The `discarded → waiting` restore exists
so a retried task (manual retry or automatic crash-reset) re-arms its discarded
successors transitively, and the `status = 'waiting'` guard on the activation
and discard queries keeps each transition single-sourced: restore owns
`discarded → waiting`, activation owns `waiting → pending`.

---

## 3. The optimistic claim

Workers claim tasks with a **compare-and-swap UPDATE**, not a lock
(`internal/database/sql/queries/task.sql:99-104`):

```sql
-- name: ClaimTask :execrows
UPDATE task SET
    status = 'processing',
    started_at = CURRENT_TIMESTAMP,
    progress = NULL
WHERE id = $1 AND status = 'pending';
```

The `WHERE status = 'pending'` is the entire concurrency control: of N racing
workers, exactly one flips the row and affects 1 row; the rest affect 0. The
Go side checks `RowsAffected` (that's what sqlc's `:execrows` annotation
means) and treats 0 as "someone else got it" (`internal/task/store.go`).

**Every task claim runs under a leased execution and claims its batch in the
same transaction.** The selection is owner-scoped — it only sees tasks of
batches whose `batch_owner` row carries the execution's lease owner (§5) — and
that same owner id is the task's execution guard; it authorizes the guarded
transitions and the settle (§4). The claim transaction also locks the batch row
(`LockBatchForClaim`) — the selected task's batch must be `queued` or
`processing`, it flips `queued → processing` on the first claim
(`MarkBatchClaimed`, with the row locked for the transaction), and zero rows
rolls the whole claim back. A batch in any other state (`paused`, terminal,
or missing) can neither be claimed from nor stall the queue: the selection
queries only consider pending tasks whose batch is claimable, and a task
whose batch is no longer leased to this execution is invisible to it.

The claim acquires nothing: the batch claim (§5) took the batch's declared
holds outside the task queue, and the selection verifies the task's waits —
a deferred wait simply yields no row.

This is the project-wide pattern: **every concurrency-sensitive transition is
a guarded UPDATE with rows-affected acknowledgment** — `ClaimTask`,
`CompleteBlockingTask`, `FailBlockingTask`, the batch lifecycle writes
(`SettleBatch`, `SetBatchPaused`, `SetBatchCancelled`, `MarkBatchClaimed`,
`ResumeBatch`, `RequeueProcessingBatch`), `TryInsertBatchOwner`,
`HeartbeatBatchOwner`, `ReleaseBatchOwner`,
`AcquireLocks`. Nothing is ever "SELECT then UPDATE"; the guard and the write
are one statement.

This is the project-wide pattern: **every concurrency-sensitive transition is
a guarded UPDATE with rows-affected acknowledgment** — `ClaimTask`,
`CompleteBlockingTask`, `FailBlockingTask`, the batch lifecycle writes
(`SettleBatch`, `SetBatchPaused`, `SetBatchCancelled`, `MarkBatchClaimed`,
`ResumeBatch`, `RequeueProcessingBatch`), `TryInsertBatchOwner`,
`HeartbeatBatchOwner`, `ReleaseBatchOwner`,
`AcquireLocks`. Nothing is ever "SELECT then UPDATE"; the guard and the write
are one statement.

Why not `SELECT ... FOR UPDATE SKIP LOCKED`? The UPDATE-guard needs no
explicit unlock, holds the row lock for a single statement (not the whole
handler), and is idempotent across retries — a worker that dies mid-task
leaves a `processing` row that the stale sweep (§10) finds.

---

## 4. The runner loop

`Runner.Next` (`internal/task/execute.go`) is called by the run's worker sets
on their fixed claim cadence. The flow:

1. **Claim with guard** — `store.ClaimNextPending(ctx, taskType)` returns the
   task row and its guard token; the claim transaction owns the claim write
   and the guarded batch flip (§3), and the selection is owner-scoped (§5).
   `sql.ErrNoRows` means idle — the runner returns nil quietly; a store
   without an execution owner fails the claim loudly.
2. **Validate** — nil payload → guarded fail.
3. **Run** — `h.Handle(ctx, task)`; handlers are pure: no batch write, no
   lock, no settle.
4. **Complete** — `completeTaskWithRetry`: the lease-guarded
   `CompleteBlockingTask` with **3 attempts and exponential backoff
   (50ms → 100ms → 200ms)** for transient DB contention. Zero rows is *success*
   — the claim was taken (cancelled, swept, reclaimed) and the executor's
   guard no longer matches; the settle afterward is refused by the same
   guards, so the write attempt is safe in every case.
5. **Settle** — the runner settles after **every** transition through
   `SettleBatch` (§5): for a multi-task batch the active-task guard leaves it
   to the last task, for a single-task batch it closes immediately. A
   settle write that fails is logged; `SettleBatch`'s guards are the recovery
   path, not the caller's responsibility.
6. **Complete-failure fallback** — if completion fails after retries, the
   oriented task is marked **failed** under the guard with
   `"complete task failed after retries: ..."` (visible, retryable) and a
   fail-write that itself errors is logged; the sweeper covers the process
   anyway via `FailTask`.

The batch's locks, drain and heartbeat belong to `RunBatch` (§7); a worker
set runs claims that verify waits and skip acquisition.

Handler errors unwrap to `*task.Error` (`internal/task/errors.go`):

```go
type Error struct {
	ReqID      string
	Err        error
	PauseBatch bool
}
```

`ReqID` correlates logs across the LLM call. `PauseBatch` is the
credit-exhaustion flow (§12 of the LLM guide) and has a strict ordering: the
task fails first (guarded), **then** `PauseBatch` runs, and the terminal
settle is **skipped** — otherwise a failure of the batch's last active task
would settle it `failed` and the pause would refuse a batch that is no longer
`processing`. A pause is terminal for the run: the batch stays `paused` until
an explicit resume (§11). Every other handler error follows the ordinary
fail-then-settle path.

---

## 5. Batch ownership: leases with heartbeats

Tasks belong to batches, batches belong to *one worker process at a time*.
The `batch_owner` table is the lease table
(`00001_baseline.sql:104-110`, extended by migration `00050`): one row per
batch with `owner_id`, `pid`, `dispatcher_id`, `acquired_at`,
`last_heartbeat`. `dispatcher_id` is the claiming dispatcher's process
identity (a UUID minted per process); it is NULL for manual runs, so the
dispatcher's slot budget counts only its own executions.

### Claim and adopt

Leases are created in exactly one place: the dispatcher's `task.ClaimBatch`
inserts the lease inside the claim transaction with **pid 0** — never alive
under `task.IsAlive`, so the claim-to-adoption window cannot make reclamation
signal a process. The forked child then **adopts** it (`Owner.Adopt`:
`AdoptBatchOwner` sets the child's pid only while the lease still carries the
same owner token and is still unadopted — one time; an already-adopted,
released or re-owned lease refuses, so a cancelled, requeued or reclaimed
batch cannot be resurrected). There is no self-claim path: a batch-mode child
only ever runs with the execution token a launcher claimed for it. An
execution the hosting process runs itself — the wizard's setup-time migration
runner — keeps the claim's pid-0 lease for its whole run, heartbeats it and
never adopts a pid, so cancel and the reaper can never signal the hosting
process; interruption arrives through the batch-status monitor and the
guarded transitions landing zero rows.

```sql
-- name: TryInsertBatchOwner :execrows
INSERT INTO batch_owner (batch_id, owner_id, pid, dispatcher_id, acquired_at, last_heartbeat)
VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(batch_id) DO NOTHING;

-- name: AdoptBatchOwner :execrows
UPDATE batch_owner SET pid = @pid, last_heartbeat = CURRENT_TIMESTAMP
WHERE batch_id = @batch_id AND owner_id = @owner_id AND pid = 0;
```

### Keep-alive: heartbeat every 5 seconds

`Heartbeat.Start` (`internal/task/heartbeat.go`) runs a ticker that
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
(owner-scoped) will find nothing.

### The lease gates claiming

`Store.ClaimNextPending` claims tasks only from batches **this process owns**
(`GetNextPendingTaskOfTypeForOwner`; see §3 for the claimability filter), and
a store without an owner id cannot claim at all — the claim fails loudly
instead of selecting owner-less work:

```sql
-- name: GetNextPendingTaskOfTypeForOwner :one
SELECT t.id FROM task t
WHERE t.status = 'pending' AND t.task_type = @task_type
  AND t.batch_id IN (SELECT batch_id FROM batch_owner WHERE owner_id = @owner_id)
  AND EXISTS (SELECT 1 FROM batch b
              WHERE b.id = t.batch_id
                AND b.status IN ('queued', 'processing'))
  AND NOT EXISTS (
    SELECT 1 FROM system_signal s
    WHERE s.name = ANY(@wait_locks::text[]) AND lock_is_held(s.name)
  )
ORDER BY t.created_at LIMIT 1;
```

The `ownerID` is the execution token the launcher claimed with, set on the
store by the shared batch execution (`task.RunBatch`). A batch without an
owner row is invisible to every runner — the lease table partitions the
leased work, and there is no owner-less claim path left.

### Release and cleanup

`ReleaseBatchOwner` deletes only where **both** batch and owner match — a
worker can't accidentally release someone else's lease. The lifecycle owns
release: a settle releases the batch's locks (by id, transactional with the
terminal write), the execution's teardown releases its owner row, and pause
and cancel release both. `ResumeBatch` drops a stale ownership row (by the
same pair match) before requeueing.

---

## 6. The queue daemon

`kushim queue` (`internal/ops/queue/queue.go`) is the orchestrator. It never
processes tasks itself.

### The wake-up loop

Three mechanisms keep it responsive without busy-polling
(`queue.go:145-186`):

```go
go d.runPollingLoop(ctx)

notifyCh := make(chan struct{}, 4)
go dispatch.ListenForBatchNotifications(ctx, config.BuildPostgresDSN(c.GetConfig().Db), notifyCh, c.GetLogger())

safetyInterval := 30 * time.Second
safetyTimer := time.NewTimer(safetyInterval)
defer safetyTimer.Stop()

for {
	select {
	case <-notifyCh:
		if err := d.dispatcher.Dispatch(ctx, d.client.Load(), d.batchSvc); err != nil { ... }
		if !safetyTimer.Stop() {
			select { case <-safetyTimer.C: default: }
		}
		safetyTimer.Reset(safetyInterval)

	case <-safetyTimer.C:
		if err := d.dispatcher.Dispatch(ctx, d.client.Load(), d.batchSvc); err != nil { ... }
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
   (`c.cfg.Store(newCfg)`), the declarative scheduler
   (`internal/ops/queue/scheduler.go`), and stale-task reclamation (throttled
   to `StaleTaskAfter/10` seconds, min 60s).

### The dispatcher and the launcher

`dispatch.Dispatcher` (`internal/ops/dispatch/dispatcher.go`) is the daemon's only
queue claimant. The daemon hands it the task catalog, the logger, the host, the
batch slot budget and the daemon's wake callback at construction; per wake it
lists the queued batches oldest-first with their
task types and sweeps them in order; each candidate's dispatchability comes
from its type definitions — the `CLI`, `BatchSelector` and `Hosts` fields —
so a batch whose types are not dispatchable on this host is skipped. The
queue daemon is fork-only; the wizard's setup-time dispatcher is the one
in-process exception, covering the two migration types before a daemon
exists (§7). The slot budget limits only
batches that acquire no locks: `CountLiveNoHoldsBatches` counts the leases
carrying **this dispatcher's identity** (`dispatcher_id` on the lease), a
lock-holding batch is claimed regardless of the budget (its locks serialize
it), and a budget-skipped no-holds candidate does not stop the sweep — a
later holding candidate is still claimed (decision-grade invariant: an
unclaimable or uncharged batch never blocks the ones behind it).

For each claimable candidate the launcher claims it through
`task.ClaimBatch` — one transaction: row-lock the batch, require `queued`,
insert the pid-0 lease under a fresh **execution token**, acquire the batch's
declared holds (verifying only the waits of the holding types; a batch that
holds nothing claims freely and its tasks gate per claim) — then forks
`kushim consume --from-batch <id> --exec-token <token>` (or the batch's owning
CLI — `enrich` for re-enrichment batches, `thumbnails` for the
thumbnail family, `kushim migrate` for a migration batch, and each
maintenance type's own verb for backup, mirror and the four tag passes). A
batch whose locks are
busy is refused (`ErrLockBusy`) and the sweep moves on.

Launch failures — the fork's `Start` error, or a child that exits before
adopting (its lease still carries pid 0, so no task was ever claimed) — are
bounded: the lease and locks are released, the batch is requeued to cool down
(5s, then 10s), and the third consecutive failure fails the batch through
`task.FailUnstartedBatch` (the launch-failure terminal write: under the lease
fence it cancels the tasks that never ran, releases the locks and writes
`failed` in one transaction) instead of looping. A launch that ran clears the
failure streak. The dispatcher's wait goroutine classifies a non-zero child
exit by the lease row: pid 0 under this token ⇒ launch failure; anything else
⇒ the child owned its outcome.

`LaunchQueuedBatch` (`internal/ops/dispatch/launcher.go`) is the manual launcher
behind `kushim batch run <id>`: the same claim and fork, in the foreground,
propagating a non-zero child exit. It claims only `queued` batches — a
paused one points at `batch resume`, a processing one at its lease holder or
the reaper, a terminal one refuses — and performs no budget check, because
the budget governs automatic dispatch only.

### Reclaiming stale batches

`reclaimStaleBatches` runs on the housekeeping ticker: for each owner stale
> 15s whose batch still has work (`ListStaleBatchOwners`), it **releases the
listed ownership row first** — by the row its snapshot described: the
`(batch_id, owner_id, pid)` triple plus the same staleness re-check
(`ReleaseStaleBatchOwner`), so an owner that adopted or heartbeated since the
snapshot keeps its lease and the whole iteration is skipped on zero rows.
Only then does it signal `SIGTERM` to the released owner's PID (if alive) —
the signal can never hit a process that revalidated its lease after the
snapshot. It then resets the batch's processing tasks (§10), moves the
quarantined files by the returned task identities, and decides the batch's
fate: the guarded settle when no work remains (completed/failed by task
outcomes), the guarded requeue otherwise. Any recovery step failing after the
release hands the batch back to the dispatcher through the same guarded
requeue, so a released batch can never be left `processing` without a lease
and without a dispatcher to claim it. `reclaimStaleTasks` moves the inbox
files of the identities the global sweep quarantined at once, instead of
waiting for the batch's next run.

---

## 7. The task CLIs and the shared execution library

Every task CLI has a **batch mode** (`--from-batch <id> --exec-token <token>`
— the dispatcher and `kushim batch run` fork it) and a **direct mode** (no
flags: the action runs now, creating no batch or task rows): the ingest
family (`internal/ops/consume/consume.go`, `internal/ops/cli/enrich.go`,
`internal/ops/cli/thumbnails.go`), `kushim backup`, `kushim mirror`,
`kushim migrate` and the four tag passes (`internal/ops/cli/tag*.go`). One
execution settles the whole batch, so a mixed ingest batch (consume
tasks with enrich/thumbnail children) forks `consume` and runs every type in
its batch through the shared library; a migration batch forks `kushim migrate
--from-batch`, which registers both migration handlers and runs whichever the
batch carries — its lease and every settle write stay in the source database,
because the handler only rewrites `config.yaml`.

### Batch mode: the shared claimed-batch executor

`consume.RunClaimedBatch` (`internal/ops/consume/consume.go`) is the executor
all three batch modes share: it parses the execution token (required — the
bare selector is not a self-claim path and errors pointing at
`kushim batch run`), takes the task store and the handler registry the calling
command assembled (the handlers act on the same store the run does), assembles
the `task.BatchRunDeps` over them, silences the logger,
watches signals, streams the progress reporter and calls `task.RunBatch`. The
paused pre-check prints the resume hint before adoption; the mid-run pause
prints the re-queue hint. Cancel exits non-zero; pause exits zero.

`task.RunBatch` (`internal/task/execute.go`) is the leased child, one
function:

1. **Paused-batch hard stop**: a paused batch returns the paused result
   without adopting.
2. **Own the batch**: `Owner.Adopt` records the child's pid over the
   dispatcher's claim — one-time, refusing a cancelled, requeued or reclaimed
   batch; it never acquires, because the claim already holds the locks. The
   owner heartbeat starts here, before repair and drain: those steps can
   outlast the 15 s lease window, and a stale lease makes the reaper release
   the batch and signal this still-live child.
3. **Entry repair**: reset orphaned processing tasks from previous crashes
   (`ResetProcessingTasksForClaim`, which keeps the claim's batch holds) and
   run the host's quarantine hook over the returned consume task identities —
   the batch is healed before work starts. A repair failure is fatal:
   continuing with orphaned `processing` rows means no worker can claim them
   and the run can never reach its completion condition. Quarantine failures
   stay best-effort.
4. **Holds**: a batch whose types hold locks (thumbbackfill batches hold
   `storage`) keeps them fresh — lock heartbeat over the execution token,
   drain of the types waiting on them, and the store's batch-holds flag so
   claims verify their waits without re-acquiring. An ingest batch holds
   nothing and skips all of it.
5. **Task loop**: one worker set per task type in the batch, sized once at
   start from the type definition's `Concurrency` (below 1 means one worker);
   each worker polls `Runner.Next` on the fixed claim cadence — owner-scoped
   claims, per-task waits verified, handler dispatched, settle after every
   transition. A monitor tick watches the batch row: `paused` stops the loop
   (the pause already released lease and locks), a non-`processing` status
   stops it without settling, and zero pending+processing tasks ends the run.
6. **Terminal status** (`task.SettleBatch`): the single terminal writer, from
    task outcomes — `failed` if any task failed, else `completed`; already-
    terminal states return their stored status without a write. A settle error
    is logged and the batch requeued; a settle refused with tasks still active
    is logged loudly (the wrapper also warns on stdout) and the batch is left
    to operator recovery, because no retry would clear it — the refusal log
    reports the status breakdown and every waiting successor with its parent's
    state (`ListWaitingSuccessorsWithParentStatus`) so an operator can see why.
    Before the final settle the run sweeps the batch's failed consume files
    (`QuarantineFailedFiles` without an id set) and discards orphaned waiting
    successors (`DiscardOrphanedWaitingSuccessors`), so a settle refused over
    `waiting` tasks is practically unreachable (§10).
7. **Cleanup**: stop heartbeats, release the lease and the execution's holds
   with a bounded context, then requeue the batch if the run aborted before
   settling — a released lease is invisible to the reaper, so the child hands
   the batch back to the queue itself (the guarded `processing → queued`
   write refuses a batch a settle, pause or cancel already finished). The
   settle and pause transactions release the batch locks themselves.

Command-level extras live with their verbs: the enrich bookend
(`enrichment.LogBatchBookend`, the aggregated per-document enrich counts over
decoded completed enrich results) runs for the consume and enrich wrappers,
and the post-batch orphan scan (`triggerOrphanScan`, 5-minute budget) runs for
the consume wrapper — ingestion is what creates orphans.

### Direct mode

No flags means the command runs the selected work immediately under
`task.RunDirect` with the type's direct-mode lock declaration — `consume` and
`enrich` hold `maintenance`; `thumbnails` holds `storage` and waits
`maintenance` — and never enqueues. Direct `consume` scans the inbox and, per
file, runs the registered consume/enrich/thumbnail handlers with synthetic
tasks (payloads without child links), reporting each step and continuing past
failures; a provider credit/balance error (`PauseBatch`-flagged) aborts the
remaining run; the command exits non-zero when anything failed. Direct
`enrich` selects documents (one id, `--limit N`, or `--all` over live
documents); direct `thumbnails` selects through the thumbnail service
(`--all`) or the no-thumbnail lookup (`--document <id>`), gated by
`consumer.thumbnail.enabled` with `--force`. `--no-wait` makes a cron
invocation exit non-zero instead of waiting for a lock or in-flight work.
Enqueueing the same work is `kushim batch create`'s job.

### The progress reporter

`streamBatchProgress` is a read-only goroutine: a 500ms ticker prints per-file
transitions (pending→processing, →completed, →failed) from a `previous` map,
live `progress.step`/`detail` updates while a task is `processing` (e.g.
`ocr page 3/12`), and the summary when no pending or processing task is left.
The library owns pause, completion and settlement; the reporter only observes.

### Non-consume batches: backup, mirror, migrations

Backup and mirror tasks get real batch rows (origin `schedule`, status
`queued`) so they flow through the batches UI instead of being invisible
orphans. The queue daemon dispatches them
like any other batch — the claim acquires the type's holds under the
execution token and forks `kushim backup --from-batch` or
`kushim mirror --from-batch`, which adopt the claim's lease with the child's
pid; migrate-db and migrate-storage batches dispatch the same way through
`kushim migrate --from-batch`. Only the wizard's setup-time dispatcher runs
migrations in-process (a pid-0 lease the hosted run heartbeats), so the
UI sees a true owner either way.

These batches run through the same runner as consume work: the handler is a
pure work function and the runner claims under the execution's lease owner,
flips the batch `queued → processing` in the claim transaction, and settles
through `SettleBatch` at the end — the status is accurate even when no queue
daemon is watching.

Manual ingest needs no daemon: direct `kushim consume` runs the inbox now
under the maintenance lock (no queue rows, `--no-wait` for unattended runs),
and `kushim batch create consume` enqueues an ingest batch for the dispatcher
— refusing while credit-paused batches exist, the same no-bypass rule the
polling scan enforces.

---

## 8. Successor choreography: waiting tasks

Ingest creates its enrich and thumbnail successors *immediately*, in `waiting`
state, linked through the `task.parent_task_id` column (migration `00056`;
assembly shared by every ingest boundary,
`internal/documents/consumption/ingest.go`):

```go
consumePayload, _ := json.Marshal(map[string]any{
	"file_path":   file.Path,
	"file_index":  file.Index,
	"document_id": documentID,
	"md5":         md5,
})
enrichPayload, _ := json.Marshal(map[string]any{
	"file_name":   filepath.Base(file.Path),
	"file_index":  file.Index,
	"document_id": documentID,
})

tasks := []task.TaskSpec{
	{Type: types.Task.Type.Consume, TaskID: consumeTaskID, Payload: consumePayload},
	{Type: types.Task.Type.Enrich, ParentTaskID: consumeTaskID, Payload: enrichPayload, Status: types.Task.Status.Waiting},
}
result, err := enqueuer.EnqueueBatch(ctx, spec, tasks)
```

The successor names its parent; the parent names nothing. `ParentTaskID` must
refer to an earlier spec in the same enqueue — a generated id is unknowable to
a later spec — so a parent of waiting successors always carries an explicit
`TaskID`, while the waiting successors themselves let the enqueue generate
theirs. A parent that loses a dedup conflict skips its waiting successors with
it. Waiting successors are the task system's general chain mechanism: any
enqueue may append a waiting spec naming an earlier one, and the choreography
below applies to chains unchanged.

Each successor's payload carries everything its handler needs that exists at
enqueue time; the thumbnail declares the one field its run produces through
the `inherit` contract (`task.InheritPayloadKey`, an array of parent-result
field names): activation copies the named fields out of the parent's result
into the successor's payload. A malformed list or a parent result missing a
declared field discards that successor with the reason instead of activating
it under an unusable payload; nothing between enqueue and activation writes a
key — waiting successors carry none.

On the parent's success the successors are **activated** in the same
transaction as the parent's completed write (`Store.CompleteTask`,
`internal/task/store.go`): the runner assembles one activation plan per
waiting successor (declared fields merged, dedup key derived through the
catalog — the same derivation `EnqueueBatch` applies, resolved from the batch
run's own catalog) and the store flips each successor to `pending` under its
own savepoint:

```sql
-- name: SetWaitingSuccessorPending :execrows
UPDATE task SET
    status = 'pending',
    payload = @payload,
    dedup_key = @dedup_key::text,
    error = NULL,
    completed_at = NULL
WHERE id = @id AND status = 'waiting';
```

The `status = 'waiting'` guard makes activation single-sourced: only a
`waiting` row becomes `pending`, and a successor another path transitioned
first is left untouched instead of overwritten. A key that collides with live
work rolls back that successor's savepoint and **discards it as the
duplicate**, together with its own awaiting branch — the surviving work
already covers the step, and a step that will not run cannot activate its own
successors — instead of failing the parent, whose real work is done. A
successor the plan cannot complete (missing definition, missing inherited
field, empty key) is likewise discarded with the reason inside the same
transaction; the parent stays completed and the reason lands on the successor
row and in the run log.

On the parent's failure, the whole waiting branch below it is **discarded**
atomically with the failed write (`Store.FailTask`, recursive
`DiscardWaitingSuccessorBranch`): a task that will not complete can never
activate its successors, so the branch shares the parent's terminal outcome
and error text, and a failure never strands a waiting row mid-chain.

On retry or reset, the branch is restored **discarded → waiting**:

```sql
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
UPDATE task SET status = 'waiting', error = NULL, completed_at = NULL
WHERE id IN (SELECT id FROM restored) AND status = 'discarded';
```

Restore is recursion over the link, seeded by every discarded successor whose
parent is live again (`pending`, or `waiting` further up the chain), and it
runs inside the same transaction as the recovery it belongs to (§10-11), with
`ResumeBatch` re-running it after the retry so the retried tasks and their
successors surface atomically with the requeue. The dedup index re-decides at
activation, so restore never has to distinguish a cascade-discard from a
duplicate-discard. The invariant this upholds, generalized to chains: a
successor is never left `discarded` under a live parent, and never `waiting`
under a dead one (the sweeps enforce that half, §10).

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
(that's how a re-ingest after a crash works). The Go side derives keys through
the task catalog (`internal/task/catalog.go`), one derivation per task type,
supplied by the owning producer package when a process boundary constructs its
catalog: the catalog's static table carries the policy, the boundary wires the
hooks, and a process refusing to enqueue a type does not import its producer.
`EnqueueBatch` refuses a pending task whose type carries no hook in the
process's catalog instead of creating keyless work, applies the hook to pending
tasks, and the batch run's catalog serves activation the same way (§8): the
successor plans derive their keys through it, so activated successors carry
keys too.

Key formats in use:

| Task type | Key |
|---|---|
| consume | `consume:<md5>` |
| enrich | `enrich:doc:<document_id>` |
| thumbnail / thumbbackfill | `thumbnail:doc:<document_id>` (no key when the payload carries no document id) |
| backup | `backup:<mode>:<UTC date>` (mode defaults to `full`) |
| mirror | `mirror:<UTC date>` |
| tag passes | fixed per type: `tagreconcile`, `tagmeasure`, `tagidentity`, `tagaudit` |
| migrations | fixed per type: `migrate-db`, `migrate-storage` |

Activation is the only path that puts a key on a waiting successor, and a
conflicting activation loses to the index: the completion transaction writes
`pending` + key per successor under its own savepoint, so a duplicate
activation of already-live work discards the duplicate — and its awaiting
branch — instead of failing its parent (§8). A duplicate
enqueue of keyed work loses the dedup conflict: `EnqueueBatch` skips the
task, no batch is created, and the boundary reports the work as already
queued ("re-enrich already queued", `internal/ops/cli/enrich.go`). Only a
violation of the dedup index counts as a skip — a collision on any other
unique constraint (the task id above all) aborts the enqueue and creates
nothing.

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
```

(`task.sql`; the staleness is decided by the database clock — a Go-derived
cutoff timestamp cannot disagree with the lease window. Per-batch variants
at `batch.sql`.) Below the budget →
requeue with `attempts + 1`; at/above it → `failed` with a recognizable
error string; the sweeps **return the task identities they quarantined**
(`task_id` + `task_type`), which is what the file quarantine and the recovery
logging consume. The global sweep runs from the queue daemon's housekeeping
ticker; the per-batch variant runs at consume entry, batch resume and batch
reclamation.

**Lock-holding recovery is gated on lock ownership.** A lock-holding task is
swept only when no fresh resource-lock row belongs to its batch: the sweep's
exemption matches a lock whose `batch_id` equals the task's batch (a
batch-held lock — the dispatcher's claim for a `thumbbackfill` batch holds
`storage` for the whole run under its execution token), with the row's
`started_at` key inside the 30-minute freshness window. A live holder
(5-minute heartbeat refreshing) keeps both the task row and the lock; a
crashed holder is recovered exactly once. The sweep transaction then applies
the transitions without touching locks — lock release belongs to the settle,
pause, cancel and reclamation paths, never to an individual task's reset:

```sql
-- the NOT EXISTS exemption both sweeps carry
NOT EXISTS (
    SELECT 1 FROM system_signal s
    WHERE s.name = ANY(@lock_names::text[])
      AND s.value = 'true'
      AND (s.data->>'started_at')::timestamptz > NOW() - INTERVAL '30 minutes'
      AND s.data->>'batch_id' = task.batch_id
);
```

(`QuarantineStaleBlockingTasks` / `ResetStaleBlockingTasks` in
`sql/queries/lock.sql`; the freshness expression is the same boundary the
acquire queries use to steal, so "the sweep says stale" and "an acquire would
succeed" never disagree. Per-batch resets release the batch's locks by id in
the same transaction, which also covers a crashed child's run-held lock.
Manual NULL-token locks match neither arm — the window is the manual
holder's only recovery path.)

`attempts` increments **only** on reclamation resets — a normal handler
failure goes straight to `failed` (visible, manually retryable) without
burning budget.

Failed consume tasks get their files moved to the quarantine dir via
`QuarantineFailedFiles` (`consumption/consumer.go`), which classifies by
**task identity, never error text**: it takes the ids the reclamation just
quarantined (the sweeps' returned identities), plus a batch-scoped
attempts-exhausted query (`GetPermanentlyFailedConsumeTaskPayloads`) that
covers a run which died between its reset and its quarantine. An empty
batch id limits the work to the named ids — that is the global sweep's
cross-batch case, which the daemon runs immediately instead of waiting for
the batch's next entry. The file pass owns files only; successor state is
the repair net's business (below).

The successor repair net is one recursive query, run in the same shape at
every recovery point: the batch variant (`DiscardOrphanedWaitingSuccessors`)
runs inside `RunBatch` immediately before the settle
for **every** batch type, and the global variant
(`DiscardOrphanedWaitingSuccessorsGlobal`) runs inside
`service.Batch.ResetStaleProcessingTasks`, whose quarantined tasks have no
batch-level recovery point. Keep-set: a waiting successor whose parent is
`pending`, `processing` or `waiting` survives; one whose parent is `completed`,
`failed`, `cancelled` or `discarded` is discarded with
`COALESCE(parent.error, 'parent task did not complete')`. Recursion follows
the link so one pass clears a whole dead branch — the quarantine paths fail a
mid-chain task in place without cascading, and a one-level sweep would leave
its tail blocking the settle. `completed` keeps its discard outcome: with
activation inside the completion transaction, a waiting successor of a
completed parent is residue.

Both sweeps also **restore**: the reset half (tasks back to `pending`)
pairs with `RestoreDiscardedSuccessors` (global) or
`RestoreDiscardedSuccessorsByBatch` (per-batch) inside the same
transaction — a discarded successor of a live-again parent (seeded on
`pending`, or `waiting` further up the chain) flips back to `waiting`
(§8). Quarantined tasks (`failed`) are excluded from the restore by that
seed, and their waiting branches are discarded by the sweeps instead.

### Per-task panic recovery

Panics inside `Runner.Next` after the claim succeeds are caught by a deferred
`recover()` (`internal/task/execute.go`): the task is failed under the
execution guard via the guarded fail-write with `panic: <text>` recorded in
`task.error` — the real panic text, not the sweep's generic
`'Max retries exceeded'` — and the REQID is logged. The fail-write uses a
detached `context.Background()` with a 10s timeout so it lands even when the
panic coincides with shutdown, and the settle attempt that follows is made
by the same guards, so a panic of the batch's last active task still closes
the batch. The error
is returned to the pool, which logs it and keeps the worker running; a plain
error return never restarts the worker (only an uncaught panic in `runWorker`
does). The sweeps
above remain the backstop for hard crashes (kill -9, OOM) where no Go-level
recover can run: the recover handles panics promptly with attribution, the
sweeps handle process death after the cutoff.

---

## 11. Retry and resume

Three surfaces resolve to **one** operation, `task.ResumeBatch`
(`internal/task/batch.go`), with `includeFailed` selecting whether failed
tasks re-run:

- **`POST /api/v1/batches/{id}/resume?retry_failed=<bool>`** — the API
  recovery surface: `retry_failed=true` answers
  `{"resumed": true, "reset": <n>, "retried": <n>}` when the batch went back
  to `queued`, or `200 {"resumed": false, ..., "reason": ...}` when nothing
  can become active.
- **`kushim batch resume <batch-id>` / `kushim batch restart <batch-id>`** —
  the CLI's two spellings of the same operation: `resume` passes
  `includeFailed: false` (failed tasks stay failed), `restart` passes `true`.
- **`POST /wizard/config/retry`** — resume of the wizard's failed migration
  batches, plus a re-trigger of the failed or missing direct asset downloads.

`ResumeBatch` is the single guarded recovery: it re-runs from
`failed`/`completed`/`cancelled`/`paused` to `queued`, refuses a batch with a
live lease (`ErrBatchLocked`, database-clock check) or one still queued or
processing (`ErrBatchNotResumable`), resets the batch's processing tasks
(quarantine-at-max plus reset, restoring discarded successors — §10) inside the
same transaction, and when `includeFailed` is set re-runs the restore after
the retry so the retried tasks and their successors surface together with
the requeue. An operation that would leave no active work (a fully cancelled
batch, say) touches nothing and reports `Resumed = false`.

**Every endpoint maps one lifecycle sentinel the same way** (`batchLifecycleError`,
`internal/api/handlers/task.go`): missing batch → `404`, terminal / locked
by a live owner / not resumable → `409` with the sentinel's
message — so the same failure is never reported differently by endpoint.

The reset paths also restore: the batch reset
(`task.ResetProcessingTasksByBatch`, also offered transactionally through
`service.Batch.ResetProcessingTasksByBatch`) and the global
`ResetStaleProcessingTasks` each run their scoped restore inside the same
transaction as the reset, so a discarded successor is re-armed exactly when
its parent chain turns live again.

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
- Every batch claim carries an execution guard (§5), and the types with
  declared holds acquire through the token protocol **at the batch claim**:
  `task.ClaimBatch` locks the batch row and acquires the batch's holds in one
  transaction (`task.AcquireLocks`, backed by
  `AcquireLocks` in
  `sql/queries/lock.sql`): `LockRowsForUpdate` row-locks the
  `holds ∪ waits` rows in canonical name order, the waits must all be free,
  and each hold row is overwritten only if free or past the window. A conflict
  refuses the batch claim (`ErrLockBusy`) and the dispatcher or launcher moves
  on — the task queue simply stays deferred on the held lock. No execution
  ever holds one
  lock while waiting for another, so no deadlock is possible; two same-day
  maintenance runs serialize with both eventually completing.
- The release is the run's leave-the-lifecycle moment: the settle, a batch
  reset, pause and cancel release the batch's locks by id (`ReleaseLocksForBatch`,
  transactional with the batch write), and the execution's teardown releases
  its own lease. An individual task transition releases nothing — the holds
  belong to the run and are released when it ends.
- The drain set is derived, not listed: `types.TaskTypesWaitingOn(holds...)`
  is the in-flight work a holder must let finish — a `maintenance` holder
  drains `consume`/`enrich`/`thumbnail`/`thumbbackfill`, a `storage` holder
  drains `thumbnail` — and `database.WaitForTaskDrain` polls it to zero on a
  5s ticker before the body runs. `database.StartLockHeartbeat` (`TouchLocksForToken`) keeps every
  held lock fresh for the run; a holder that stops heartbeating becomes
  takeable after the 30-minute window, the last-resort recovery when no reaper
  is running (the daemon's sweeps recover batch-held locks promptly;
  the window is the manual holder's only mechanism).
- `task.RunBatch` owns the lock-holding lifecycle: it starts the heartbeat and
  the drain for a batch whose types declare holds (§7) and dispatches pure
  handlers from its worker sets. The backup handler and the four tag passes
  are work functions
  under it; the `migrate-db` and `migrate-storage` handlers drain nothing
  themselves (the run has drained), and the work happens after claim and
  before settle.

Locks live as long as the *execution*, not one task. A batch whose task types
declare holds gets them acquired **at the dispatcher's claim**, for the whole
run: the claim transaction takes `storage` under the execution token
(verifying the waits the holding types declare, so a thumbbackfill batch is
refused while `maintenance` is held), and the child keeps the holds fresh —
lock heartbeat, drain of the declared waiters —
while its worker set runs claims that verify waits but skip acquisition.
There is no self-claim path: the child only runs with the execution token a
launcher claimed for it. New `thumbnail` claims from ingest
batches defer until the backfill settles — per-document thumbnails never
write while the storage tree is being bulk-walked. The release happens when
the run ends; reclamation releases by batch id, and the stale sweep's
exemption covers the batch id (§10).

Direct CLI commands run under the manual holder path (`task.RunDirect`,
`internal/task/direct.go`): acquire the policy's locks —
`task.DirectLockPolicyFor` returns a type's declared direct policy (consume
and enrich hold `maintenance`; `thumbnails` holds `storage` and waits
`maintenance`), falling back to the queued set for the types without an
override — all-or-nothing with a NULL token, heartbeat, drain, run the body, release. The release is
scoped to the locks the run itself acquired: manual runs share the NULL token,
so releasing every name would drop a concurrent manual run's lock. Waiting is the default; `--no-wait`
exits non-zero instead of waiting for a lock or drain.
`kushim backup` works with polling enabled — it waits for the lock and drains
rather than refusing. `kushim restore` and `kushim migrate database` keep
their quiescence preconditions because they replace state: they refuse while
the maintenance lock is held or gated tasks are processing (plus polling and
daemon PID checks for restore), try-acquire with a NULL token, and never wait
or drain.

Scheduling: only the polling scan still skips while `IsLockHeld(maintenance)`; a
`storage`-only holder does not pause it. Released locks notify
`lock_released`, which wakes the daemon's dispatch tick alongside
`batch_queued`. Every recurring schedule lives in one declarative table
(`internal/ops/queue/scheduler.go`): a definition per schedule — name, task
type, enabled predicate, timing, and the producer that builds the run's task
set — evaluated every tick against the freshly reloaded config. Backup
contributes one schedule per configured mode (names `backup:<mode>`, after
the dedup-key vocabulary); mirror, thumbnail-backfill and the four tag passes
keep their earlier names, so scheduled batches already in the table keep
anchoring their cadence. Per schedule the loop runs the enabled predicate, the
active check (`CountActiveBatchesForSchedule` — any queued or processing
batch of that schedule name), the due check
(`GetLastTerminalBatchCreatedAtForSchedule` — the last terminal batch,
`completed`, `failed` or `cancelled`, anchors `NextBackupTime` over its
`created_at`, so a failed occurrence waits for the next occurrence instead of
retrying every tick), calls the producer, and enqueues through `EnqueueBatch`
with origin `schedule`. A zero-enqueued result creates nothing and leaves the
schedule due; overlap beyond the name-keyed active check remains the dedup
key's responsibility. Unmet preconditions — the mirror's rsync check — skip
the occurrence with a log line, a failure in one schedule never stops the
others, and no production enqueues by forking a CLI. A schedule with no
terminal batch yet waits for the next preferred time-of-day, anchored in the
scheduler's `firstDue` map once per daemon lifetime; the entry becomes
irrelevant once the first batch lands. The scheduler never checks resource
locks: enqueueing while `maintenance` is held just queues the batch, which
the dispatcher claims when the locks free.

The **mirror** task (`internal/backup/mirror_task.go`) participates in the
same gate: it is claimed with the locks held, drains in-flight work, and runs
`rsync -a --delete` over the storage tree (see `internal/backup/`). Because a
large rsync can exceed the 30-minute staleness window, the handler runs a
heartbeat that refreshes the lock row's heartbeat timestamp every 5 minutes
(`TouchLocksForToken`, guarded by the execution token) for the rsync
itself — the gate never goes stale mid-mirror. The heartbeat is started by the
runner *before*
`WaitForTaskDrain` returns, so the drain-wait itself is covered too; the
backup, tagreconcile, tagmeasure, tagidentity and tagaudit handlers get that
sequence — heartbeat, drain, body, settle — from the
runner (`internal/task/execute.go`). The queued mirror handler is the pure
`backup.RunMirrorAndRecord` over the tree; the direct `kushim mirror` command
runs under
the manual holder path instead — waiting for the lock and draining by
default, `--no-wait` to fail fast — and writes
the `.edub-mirror.json` diagnostics file into the
destination. The mirror schedule is one entry of the declarative table —
gated by `mirror.enabled`, due-ness anchored the same way as every other
schedule — and its enqueue carries the `backup.MirrorPrecondition` rsync
check, evaluated through the enqueue path for the scheduler and for
`kushim batch create mirror` alike.

The **tagmeasure** task (`internal/tags/tagmeasure.go`) is a
lock-holding task: a `tagmeasure` batch is claimed with the maintenance lock
held, and its `kushim tagmeasure --from-batch` child adopts the claim's lease,
keeps the lock fresh across the whole run, and runs the pass's long
repeatable-read transaction — the pass's report is the task result stored on
the task row. The pass opens by checking the
`tag_measure_changed` flag: with the flag clear and a completed run under the
current rule set it short-circuits to a zero-work report, writing nothing;
otherwise its opening transaction clears the flag and the sweep runs, re-arming
the flag if the sweep fails. A bare `kushim tagmeasure` runs the pass now
under the manual holder path, and `kushim batch create tagmeasure` enqueues
the same queued task (dedup key `tagmeasure`);
`enricher.tag_measure.enabled` gates only the schedule
that enqueues runs automatically.

The **tagidentity** task (`internal/tags/admission/tagidentity.go`) follows
the same shape: a `tagidentity` batch is claimed with the lock held, and the
`kushim tagidentity --from-batch` child adopts the lease and resolves pending
identity evidence through `admission.IdentityResolver`, whose report (pending/
resolved/folded/aliased/deferred/failed) becomes the task result stored on the
task row. The review surface's reevaluate action, `kushim batch create
tagidentity`, and the pass's scheduled run all create the same queued task. The queue dispatches it always, and
the schedule — a fixed daily 03:30 pass with no config
keys — fires only while `enricher.tag_adjudicator.enabled` is set, so pending
claims wait for the next run when the judge is off.

The **tagaudit** task (`internal/tags/admission/tagaudit.go`) follows the same
shape as `tagmeasure`: a `tagaudit` batch is claimed with the lock held, and
the `kushim tagaudit --from-batch` child runs the coverage-verification pass
(`admission.AuditRunner`), whose report (sweep, sampled/flagged counts,
coverage tallies by class, fills/flags/proposals, judge call counts, pending
coverage) becomes the task result stored on the task row.
Runs come from the schedule (disabled by default), `kushim batch
create tagaudit`, and the reevaluate endpoint;
`enricher.tag_audit.enabled` gates only automatic enqueuing.

The **tagreconcile** task (`internal/tags/reconcile/tagreconcile.go`) is a
lock-holding task too: a `tagreconcile` batch is claimed with the maintenance
lock held, and the `kushim tagreconcile --from-batch` child adopts the claim's
lease, builds the reconciler and runs one pass, storing its report as the task
result. It dedups on the fixed key
`tagreconcile`, so an active run blocks the next trigger, and the queue
dispatches it always — `enricher.tag_reconcile.enabled` gates only the schedule
that enqueues runs automatically, while `kushim batch create tagreconcile`
enqueues through the queue the same way.

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
pending-coverage derivation on the flag — the sweep itself stays batch-driven (a
document is audited at most once per sweep), and an unchanged vocabulary reuses
the durable pending-coverage rows already written. The identity pass reads no
flag: its work set is the pending-event count, already its first read, and a
signal gate would strand capped remainders in a quiet store. A pass's own
applies re-arm the flags through the table triggers (migration `00044`;
`00045` removed the identity flag), which is the record's honest reading of
"changed since it last ran".

The **thumbbackfill** task (`internal/documents/consumption/thumbnail.go`'s
per-document `ThumbnailTaskHandler`) holds its lock at batch level: the
dispatcher's claim acquires
`storage` for the whole run after verifying `maintenance` is free, and the
forked `kushim thumbnails --from-batch` child drains
in-flight thumbnail tasks before starting, runs the same 5-minute
heartbeat across the run, and keeps the hold fresh; its per-task claims verify the maintenance wait but
skip acquisition (§12, batch-held locks). `ThumbnailBackfill`
(`internal/service/thumbnail_backfill.go`) creates `thumbbackfill` tasks
(dedup key `thumbnail:doc:<uuid>`) in `thumbbackfill` batches.

Direct CLI holders (`kushim backup`, `kushim restore`, `kushim mirror`,
`kushim migrate`, and the four tag passes' bare commands) are NULL-token lock
rows: their release is scoped to
the locks their own policy acquired, so a concurrent manual run holding a
different lock keeps it, and a loaded task- or child-held lock (a non-NULL
token) can never be dropped.

A lock-holding task longer than 30 minutes keeps its locks alive via the
5-minute heartbeat, and the age sweep leaves it alone: lock-holding recovery
is gated on lock ownership (§10), so a live heartbeating holder is never reset
mid-run.

---

## 13. Configuration knobs

| Key (config) | Default | Meaning |
|---|---|---|
| `consumer.workers` | `1` | worker count for `consume` tasks inside an ingest batch run |
| `enricher.workers` | `1` | worker count for `enrich` tasks inside a batch run |
| `srv.max_concurrent_batches` | `4` | the dispatcher's per-process budget for batches that acquire no locks (the lease carries the dispatcher identity; manual runs and other hosts never charge it) |
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
  "someone else won" (idle, not error); for the guarded task transitions it
  means "already transitioned", and the guarded batch writes (settle, pause,
  cancel, requeue) make a refusal *observable* while keeping the state
  truthful. Only treat 0 rows as an error where the contract says so
  (e.g. the guarded lock acquire).
- **Task transitions are lease-guarded** — `CompleteTask`/`FailTask` (over
  `CompleteBlockingTask`/`FailBlockingTask`) write only while the task is
  still `processing` under the execution gang of the batch that claimed it, so a stale executor
  can never rewrite a task the sweep reset. Keep the runner's ordering
  (complete first on success, fail otherwise) intact.
- **Ownership of the batch's terminal writes belongs to `SettleBatch`** —
  handlers carry no batch-status writes; diverting a "quick" status write
  into a handler reintroduces the four-writers race the single writer
  closed. The one sanctioned exception is `FailUnstartedBatch`, the
  launch-failure terminal write the dispatcher reaches only after its bounded
  retries.
- **`attempts` only grows on reclamation resets** — never bump it on ordinary
  handler failures; the budget exists to bound crash loops, not retries.
- **Owner-scoped claiming is the lease's teeth** — a worker whose lease was
  stolen (heartbeat 0 rows) keeps running but claims nothing. Don't remove
  the `owner_id` gate "for simplicity".
- **The claim's pid-0 lease is the crash net** — the dispatcher's claim
  transaction inserts the lease (pid 0) before forking; if the child dies
  before adopting, reclamation finds the stale pid-0 row and requeues the
  batch. Removing the lease insert breaks that window. The child adopts the
  lease once (`Owner.Adopt` refuses an adopted, released or re-owned lease),
  so a replayed execution token cannot resurrect a live run.
- **The execution token is the guard, not a secret** — the launcher passes
  `--exec-token` to the child; it authorizes the child's claims and lifecycle
  writes as the lease owner. It is deliberately a CLI argument (visible in
  `ps`), not ambient environment state.
- **NOTIFY is a hint; the 30s timer is the contract.** The non-blocking send
  drops notifications when the daemon is busy — never "fix" that by making
  the channel blocking, or a full channel stalls the daemon.
- **`waiting` tasks are invisible to normal claims** (they're not `pending`)
  and must be activated or discarded. Activation happens only in the parent's
  completion transaction; the recovery sweeps only *discard* — recursively, so
  one pass clears a whole dead branch (§10). A `waiting` successor whose parent
  is `completed`, `failed`, `cancelled` or `discarded` is discarded with the
  parent's error text (`DiscardOrphanedWaitingSuccessors`,
  `...Global`), for every linked type.
- **The successor invariant is enforced at recovery points, not continuously**:
  "a successor is never `waiting` under a dead parent, never `discarded` under
  a live one" holds because completion activates and failure discards in the
  same transaction, and the sweeps
  (`RunBatch` pre-settle, `ResetStaleProcessingTasks`,
  `DiscardOrphanedWaitingSuccessorsGlobal`) mop up anything else (crashes,
  legacy data).
- **Resume atomicity is the recovery boundary** — the retried tasks and
  their restored successors commit inside the resume transaction, so a failed
  restore rolls the resume back; nothing is reported as resumed with
  successors still `discarded`.
- **Paused batches refuse to run** and pause is sticky
  — resume is an explicit API/CLI action after the billing issue is fixed.
- **The dispatcher's slot budget counts only no-holds batches** —
  `CountLiveNoHoldsBatches` charges leases on batches holding no lock; a
  maintenance batch without a free slot is claimed anyway, because its locks
  serialize it.

---

*Last verified against the tree: 2026-10-27. If code and doc disagree, code wins.*

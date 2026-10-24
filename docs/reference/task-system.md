# Worker Pool (`internal/task/pool.go`)

## `pool.go`

### Struct

- `Pool` — `logger`, `runner Nexter`, `workers int`, `interval time.Duration`, `taskType string`, `stopCh`, `stopOnce`, `wg`, `ctx`, `cancel`
  - **Methods**:
    - `NewPool(logger, runner, workers, interval, taskType) *Pool`
    - `Start(ctx)` — Starts worker goroutines with a cancellable context
    - `Stop(ctx)` — Signals stop, waits for workers with timeout

### Interface

- `Nexter` — `Next(ctx, taskType) error` (implemented by `task.Runner`)

---

# Task System (`internal/task/`)

## `types.go`

### Types

- `Task` — domain struct with `ID int64`, `TaskID string`, `TaskType types.TaskType`, `BatchID string`, `Payload json.RawMessage`

## `errors.go`

### Types

- `Error` — error wrapper carrying a `ReqID` string alongside the wrapped `Err error`. Implemented at the package level for one-directional import safety (neither `enrichment` nor `consumption` import `task`).
  - **Methods**:
    - `Error() string` — delegates to the wrapped error's message
    - `Unwrap() error` — returns the inner error, enabling `errors.Is`/`errors.As` traversal

### Usage

The `Runner.Next` method uses `errors.As(err, &tErr)` to extract `ReqID` from handler errors before logging. Layers that have a document ID in scope (consumer, enricher, handlers) wrap fatal returns with `&task.Error{ReqID: documentID, Err: ...}`. Unmarshal failures and other errors without a document ID are left unwrapped; `Next` then logs without an id.

## `handler.go`

### Interfaces

- `Handler` — `Handle(ctx, task.Task) (json.RawMessage, error)`

## `store.go`

### Struct

- `Store` — wraps `*database.Queries`
  - **Methods**:
    - `ClaimNextPending(ctx, taskType) (database.Task, uuid.UUID, error)` — claims one pending task of the type and returns the task row together with its execution guard. Every claim runs one transaction: the wait-gated selection (scoped by owner when the store carries an owner id, claimability-filtered by the batch's `queued`/`processing` state), the claim write with the guard (lease owner id for a leased execution, fresh UUID otherwise), the guarded batch row lock and `queued → processing` flip (zero rows rolls the whole claim back), and — for types with declared holds — the all-or-nothing acquisition (`ErrLockBusy` on any conflict — the task stays pending); `SetBatchHoldsAcquired(true)` switches claims to verify-waits-only, the consume child's mode after its batch-level acquisition
    - `SetOwnerID(id string)` — the lease owner written on claims and ownership rows (the consume child runs through it)
    - `SetBatchHoldsAcquired(acquired bool)` — must run after the batch-level acquisition and before the claims it affects
    - `CompleteTask(ctx, taskType, id, result, token) (int64, error)` — the guarded completion (over `CompleteBlockingTask`): writes only while the task is still `processing` under the claiming guard, returns rows affected, and releases the claim's task-scoped locks in the same transaction; a type whose hold is batch-level (batch-holds mode) skips the release, that hold belongs to the run
    - `FailTask(ctx, taskType, id, errMsg, token) (int64, error)` — the guarded failure (over `FailBlockingTask`), same guard and lock-release rule
    - `ActivateChild(ctx, id, payload, dedupKey) (Activation, error)` — makes a waiting child pending with the definition-derived dedup key in one transaction (over `SetEnrichTaskPending`); a key colliding with live work rolls back to the savepoint and discards the duplicate child instead of failing the parent (`DiscardedDuplicate`), and an empty key is refused — a keyless child would bypass the dedup protection
    - `GetTask(ctx, id) (database.Task, error)`
    - `GetTaskByTaskID(ctx, taskID) (database.Task, error)`
    - `Discard(ctx, id, errMsg) (int64, error)` — Wraps `DiscardEnrichTask`; returns rows affected; only matches `waiting` enrich tasks (0 rows = already discarded or activated, benign no-op)

## `registry.go`

### Struct

- `Registry` — maps task type strings to `Handler` implementations
  - **Methods**:
    - `Register(taskType, handler)` — registers a handler
    - `Get(taskType) (Handler, error)` — looks up a handler

## `execute.go`

### Struct

- `Runner` — owns `Store` and `Registry`; implements `Nexter`
  - **Methods**:
    - `NewRunner(store, registry, logger) *Runner`
    - `Next(ctx, taskType) error` — the claim-run-settle loop: claims under the execution guard, holds the locks a holding type declares (heartbeat over the guard, drains the derived wait set with `drain-wait` progress), checks for nil payload (guarded fail), dispatches the handler, completes with retry (guarded), then settles the batch through `SettleBatch` after every transition — the active-task guard defers it to the batch's last terminal transition, and a zero-rows transition settles only through guards so a taken-over execution never rewrites the winner's status; extracts `ReqID` from handler errors via `errors.AsType` for log correlation, and a `*Error` with `PauseBatch` fails the task, runs the guarded pause and skips the settle. A deferred `recover()` after the claim fails the in-flight task with `panic: <text>` (guarded fail write, detached 10s-timeout context) and returns the error to the pool, which logs it without restarting the worker
    - `holdLocks(ctx, taskType, task, guard) (stop func(), err error)` — private: no-op for a type without holds and in batch-holds mode; starts `StartLockHeartbeat` and drains `TaskTypesWaitingOn` before the handler runs
    - `failTask(ctx, task, exec, msg)` / `failAndSettle(ctx, task, exec, msg)` — private: the guarded fail write, with the settle expressed as fail-then-settle for callers that do not request a pause
    - `completeTaskWithRetry(ctx, task, exec, result) (int64, error)` — private: 3× exponential backoff on the guarded completion
    - `settle(ctx, task, exec)` — private: best-effort `SettleBatch`; a refused settle logs and keeps the batch's current status

## `enqueue.go` / `type_definition.go`

### Types

- `BatchSpec` — one enqueue's batch contract: `origin BatchOrigin`, `schedule string` (name, required with origin `schedule`). Built by `NewBatchSpec`, which validates the origin and rejects a schedule name outside origin `schedule`; `validate()` re-checks the invariant so the enqueue can reject a zero-value spec from a caller that skipped the constructor
- `TaskSpec` — one task row an enqueue creates: `Type`, `TaskID` (empty = enqueue generates), `ParentTaskID` (must refer to an earlier spec in the same enqueue), `Payload`, `Status` (empty = pending; only `pending`/`waiting` accepted)
- `EnqueueResult` — `BatchID` (empty when nothing was created), `TaskIDs`, `Skipped` (dedup rejections plus children skipped with a dedup-skipped parent)
- `PreconditionError` — reports a failed task-type precondition; the enqueue created nothing

### Struct

- `Enqueuer` — the single creation path for queued work; owns the DB client and the `TypeCatalog`
  - **Methods**:
    - `NewEnqueuer(client, catalog) *Enqueuer`
    - `EnqueueBatch(ctx, spec, tasks []TaskSpec) (EnqueueResult, error)` — one transaction inserts the batch (origin, schedule, `queued`) and its tasks; a violation of the dedup index skips that task, a collision on any other unique constraint (the task id) aborts, zero survivors create nothing, and the batch types must share one `Holds` set; preconditions run before any row is written
    - `EnqueueTask(ctx, spec, task)` — single-task convenience over `EnqueueBatch`
    - `DedupKey(taskType, payload) (string, error)` — the derivation a task of that type would carry, for boundary-side coverage checks

### Type definitions

- `TypeDefinition` — one task type's enqueue contract: `Type`, `Locks LockPolicy` (`Holds`/`WaitsFor` over the closed `types.LockName` set, populated from `types.LockDeclarationFor`), `DirectLocks *LockPolicy` (the direct CLI run's policy; nil uses the queued set), `DedupKey func(payload) string` (empty result = no dedup), `Preconditions []func(ctx) error`, `BuildTasks func(ctx, BuildInput) ([]TaskSpec, error)` — the typed task-set builder used by `kushim batch create` and the scheduler
- `TypeCatalog` — `Definition(taskType) (TypeDefinition, error)`; the concrete table lives in `internal/ops/container` (`Catalog`)
- `BuildInput` — interface (`TaskType() types.TaskType`); concrete inputs live with the catalog

## `crud.go`

### Types

- `TaskFilter` — `BatchID`, `Status`, `TaskType`, `Limit`, `Offset`
- `BatchFilter` — `Status`, `Limit`, `Offset`
- `BatchCounts` — `BatchID`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`
  - `Total() int64` — Sum of all statuses including Waiting and Discarded
- `ErrTaskNotFound` — `errors.New("task not found")`

### Functions

- `Get(ctx, queries, taskID) (database.Task, error)` — By UUID task_id
- `ListFiltered(ctx, queries, filter) ([]database.Task, error)` — By batch/status/type/pagination; handles all combinations of filters via sqlc-generated queries (`ListTasks`, `ListTasksByStatus`, `ListTasksByBatch`, `ListTasksByBatchAndStatus`, `ListTasksByType`, `ListTasksByBatchAndType`, `ListTasksByStatusAndType`, `ListTasksByBatchAndStatusAndType` and their `All` variants)
- `CountBatchStatuses(ctx, queries, batchID) BatchCounts` — Counts per status including `waiting` and `discarded`
- `ListBatchSummaries(ctx, queries, filter) ([]BatchCounts, error)` — Lists batch summaries with filtering; uses `ListDistinctBatchIDs` or `ListDistinctBatchIDsByStatus`

### Lifecycle ops (`batch.go`)

- `LeaseStaleSeconds int32 = 15` — the lease window every staleness query compares against, decided by the database clock
- `ErrBatchLocked` / `ErrBatchTerminal` / `ErrBatchNotResumable` / `ErrBatchNotQueued` / `ErrTaskNotFound` / `ErrTaskNotFailed` — the lifecycle sentinels the API surfaces map
- `Execution{BatchID, Guard, Leased}` — the authorization a caller carries for one run's writes: the lease owner for a leased execution, the task claim token for a pool execution
- `ClaimRequest{BatchID, ExecToken, Holds, Waits, DispatcherID}` — one batch claim: the execution token the forked child adopts, the lock sets the claim acquires and verifies, and the dispatcher owning the lease (zero for a manual run, which never charges a dispatcher's slot budget)
- `TaskIdentity{TaskID, TaskType}` / `ResetResult{Reset, Quarantined}` (`ConsumeTaskIDs()`) / `CancelResult{PendingCancelled, ProcessingCancelled, OwnerPID, OwnerAlive, Signalled}` / `ResumeResult{Resumed, Reset, FailedRetried}`
- `Owner` — `Acquire` (two-step: try insert with no dispatcher identity, then steal from the stale heartbeat or re-acquire idempotently; a takeover clears `dispatcher_id`), `Adopt` (one-time adoption of the dispatcher's pid-0 lease under the same owner token: records the child pid, refuses an adopted, released or re-owned lease), `Heartbeat` (rows-reported renewal), `Release` (pair-guarded delete)
- `ClaimBatch(ctx, client, claim) error` — the guarded batch claim in one transaction: row-lock the batch, require `queued`, insert the pid-0 lease under the execution token, acquire the holds all-or-nothing while verifying the waits the holding types declare, flip `queued → processing`; `ErrLockBusy` leaves nothing acquired, a non-queued batch reports `ErrBatchNotQueued`, a batch that already carries a lease `ErrBatchLocked`
- `ResetProcessingTasksByBatch(ctx, client, batchID, maxRetries) (ResetResult, error)` — the takeover variant: one transaction that releases the batch's locks, quarantines the tasks at the retry bound, resets the rest (attempts + 1) and restores discarded children; returns the reset count and the quarantined identities
- `ResetProcessingTasksForClaim(ctx, client, batchID, maxRetries) (ResetResult, error)` — the claim-path variant: the same reset without the lock release, so the holds the claim acquired survive entry repair
- `IsOrphaned(state, pending, processing)` / `IsAlive(pid)` — the owner-liveness predicates (`IsAlive` refuses `pid <= 0`, so `kill(0)` can never target the caller's group)
- `SettleBatch(ctx, client, logger, exec) (types.BatchStatus, error)` — the normal settlement writer: `failed` when any task failed, `completed` otherwise, computed inside the guarded write; refuses (returns the current status untouched) while a pending/processing/waiting task exists, while the status is not `processing`, and when a lease row exists that is not the executor's; the settle transaction releases the batch's locks; a terminal batch is idempotent. The one sanctioned failure-terminal exception is `FailUnstartedBatch`
- `FailUnstartedBatch(ctx, client, batchID, execToken) (applied, error)` — the launch-failure terminal write: under the execution token's lease fence it cancels the tasks that never ran, releases the batch's locks and writes `failed` in one transaction; `applied = false` when the lease no longer belongs to the token (the batch changed hands since the launch decision) and nothing is written
- `PauseBatch(ctx, client, logger, exec) error` — guarded `processing → paused` (lease owner for a leased execution, a live claim token of the batch for a pool execution), releasing the batch's locks and the caller's lease; a refused pause writes nothing
- `CancelBatch(ctx, client, logger, batchID) (CancelResult, error)` — signals a live owner (SIGTERM through `IsAlive`), cancels pending + waiting + processing, releases the batch's locks and lease, writes `cancelled`; refuses a missing (`ErrBatchNotFound`) or terminal (`ErrBatchTerminal`) batch
- `ResumeBatch(ctx, client, logger, batchID, maxRetries, includeFailed) (ResumeResult, error)` — the single recovery: from `failed|completed|cancelled|paused` to `queued`, resets processing tasks (quarantine-at-max + restore) in the same transaction and, when `includeFailed`, retries the failed tasks with the batch restore re-run after the retry; refuses a live lease (`ErrBatchLocked`), a queued or processing batch (`ErrBatchNotResumable`) and leaves a batch with no resurrectable work untouched (`Resumed = false`)
- `RetryTask(ctx, client, logger, taskID, maxRetries) error` — theCompatibility alias behind the retry surfaces: resolves the task (`ErrTaskNotFound`), refuses a non-failed one (`ErrTaskNotFailed`), then resumes its batch with failed tasks reset

---

### SQL queries added

- `RetryFailedTasksByBatch :execrows` — `UPDATE ... WHERE batch_id = ? AND status = 'failed'` — Resets failed batch tasks to pending (attempts preserved; the budget only moves on reclamation resets). Runs inside `ResumeBatch` followed, in the same transaction, by `RestoreDiscardedEnrichTasksByBatch`.
- `GetConfigTaskByDedupKey :one` — Returns the most recent config task matching `dedup_key`. Used by `ConfigHandler.configTaskCovered` (skip enqueue when a `pending`/`processing` task already exists) and by `tryAcquireConfigTaskSlot` for the migration enqueuers. Completed/failed/cancelled tasks do not block a fresh enqueue: `enqueueConfigTasks` inserts a new task row under a new batch (the partial unique index on `dedup_key` only covers `pending`/`processing`) instead of retrying the old task.
- `DeleteConfigTaskByDedupKey :execrows` — `DELETE FROM task WHERE task_type = 'config' AND dedup_key = ? AND status IN ('pending','failed')` — Atomic, status-guarded supersede of a stale config task (used by `enqueueDBMigration` so a task claimed between read and delete survives and wins).
- `DiscardEnrichTask :execrows` — `UPDATE ... WHERE id = ? AND status = 'waiting' AND task_type = 'enrich'` — Discards an enrich task by internal `id`; returns rows affected so callers can distinguish a real discard from a no-op. Wrapped by `Store.Discard`.
- `DiscardEnrichTaskByTaskID :execrows` — `UPDATE ... WHERE task_id = ? AND status = 'waiting' AND task_type = 'enrich'` — Discards an enrich task by UUID `task_id`. Kept for the test suite (`TestDiscardEnrichTaskByTaskID`); production discard paths use `Store.Discard` (handler) and the sweep queries (recovery points).
- `SetEnrichTaskPending :exec` — `UPDATE ... SET status='pending', payload=@payload, dedup_key=@dedup_key, error=NULL, completed_at=NULL WHERE id = ? AND status IN ('waiting','discarded') AND task_type IN ('enrich','thumbnail')` — Children are activated with the definition-derived dedup key atomically with the pending flip,图片均 so the dedup index covers activated children; the join-based restores work from the same link.
- `RestoreDiscardedEnrichTasks :execrows` / `RestoreDiscardedEnrichTasksByBatch :execrows` — join-based restore (`UPDATE ... FROM task AS c WHERE e.status='discarded' AND c.status='pending' AND e.task_id = c.payload->>'on_completed'`, batch variant scoped by `c.batch_id`): re-arms every discarded child whose consume is pending again. Runs inside the sweep transactions paired with the resets, and inside `ResumeBatch` a second time after the retry.
- `DiscardOrphanedWaitingEnriches :execrows` / `DiscardOrphanedWaitingEnrichesGlobal :execrows` — join-based sweep: discards every waiting enrich or thumbnail whose parent consume is no longer `pending`/`processing` (failed, cancelled, or completed without activating its children), copying the parent's error text (`COALESCE(c.error, 'parent consume task did not complete')`). Enforces "a child is never waiting while its consume is terminal" at recovery points. The batch variant runs unconditionally in `QuarantineFailedFiles` (scoped by the batch id); the global variant runs inside `service.Batch.ResetStaleProcessingTasks`.
- `GetConsumeTaskPayloadsByIDs :many` / `GetPermanentlyFailedConsumeTaskPayloads :many` — the quarantine's two sources: the payload rows of the identities a reclamation just failed (cross-batch, by id), and a batch's failed consume rows at or above the retry bound (the crash residue). Both feed `QuarantineFailedFiles`, which classifies by task identity and never reads error text.
- `CancelPendingTasksByBatch :execrows` — `UPDATE ... WHERE batch_id = ? AND status IN ('pending','waiting')` — the cancel operation takes pending TASKS and their waiting children together with the processing writes.

---

## `consume.go` (`internal/documents/consumption`)

### Struct

- `ConsumeTaskHandler` — `consumer *consumption.Consumer`, `store *task.Store`, `queries *database.Queries`, `logger *log.Logger`, `catalog task.TypeCatalog`
  - **Methods**:
    - `NewConsumeTaskHandler(consumer, store, queries, logger, catalog) *ConsumeTaskHandler` — the catalog is the seam the activation needs: the child dedup keys derive through the same `TypeDefinition.DedupKey` the enqueue applies
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals payload (`file_path`, `document_id` UUID, `on_completed` enrich task ID), calls `FileFromPath` + `consumer.Process`. On success, if `on_completed` is set and a document was created, activates the linked enrich task via `Store.ActivateChild` (payload updated with the document id, key derived through the catalog; a conflicting activation is reported as `DiscardedDuplicate` and completes the parent, a failed activation is logged for enrich and fatal for thumbnail). On **any** failure (payload unmarshal, empty `file_path`, `FileFromPath`, or `Process`), discards the linked enrich task via `withDiscardAttempt` — if the discard itself fails, the error is appended (`"; additionally failed to discard enrich task <id>: ..."`) so it lands in the task's `error` field.
    - `childDedupKey(taskType, payload) (string, error)` — Looks up the type definition and derives the child's dedup key; a missing definition or an empty derived key (missing document id) fails the activation instead of writing a keyless child that would bypass the dedup index.
    - `deactivateChildEnrich(ctx, parent, onCompleted, parentErr)` — Looks up the enrich task by UUID, validates `waiting_for` matches the parent, and sets it to `discarded` with the parent error via `Store.Discard`. Only matches `waiting` enrich tasks; 0 rows affected is a benign no-op logged at info level.
    - `withDiscardAttempt(ctx, t, onCompleted, err)` — private: attempts the enrich discard for a failing consume and merges a discard failure into the returned error.
    - `recoverOnCompleted(payload)` — private: lenient re-parse of `on_completed` from a payload that failed strict unmarshalling, so the paired enrich can still be discarded.

## `enrich.go` (`internal/enrichment`)

### Struct

- `EnrichTaskHandler` — `enricher *enrichment.Enricher`, `queries *database.Queries`
  - **Methods**:
    - `NewEnrichTaskHandler(enricher, queries) *EnrichTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals `{"document_id":"<uuid>"}`, calls `h.queries.GetDocument` (lookup by UUID), then `enricher.Enrich`; the returned result is the `{"stats": …, "counts": …}` envelope (analyzer token usage plus the per-document admission counts the batch settle bookend aggregates)

## `configtask` (`internal/ops/configtask/configtask.go`)

The config handlers live in their own package (`internal/ops/configtask/`) to keep the edub binary free of CGo dependencies — unlike the domain packages that host the other task handlers, which transitively import CGo-only `internal/cgo` through `internal/documents/consumption`.

### Constants

- `OpTessdata = "tessdata"`, `OpHugot = "hugot"` — the config payload operations (asset downloads)
- `DedupKeyTessdataPrefix = "config:tessdata:"`, `DedupKeyHugot = "config:hugot"` — the config dedup key vocabulary, shared with the task type catalog and the API task labels
- `DedupKeyMigrateDB = "migrate-db"`, `DedupKeyMigrateStorage = "migrate-storage"` — fixed dedup keys of the two migration task types (at most one migration task pending/processing per type)

### Structs

- `MigrateDBPayload` — `config_dir`, destination connection fields (`host`, `port`, `user`, `password`, `database`, `sslmode`). Carried by the `migrate-db` task so the handler can open both databases directly and persist the new values only after the copy succeeds. No storage fields — storage relocation belongs to the `migrate-storage` task.
- `MigrateStoragePayload` — `config_dir`, old/new storage dirs (`old_storage_dir`, `new_storage_dir`, `old_consumption_dir`, `new_consumption_dir`). Carried by the `migrate-storage` task; the move strategy (`copy`/`move`) is read from `storage.migration_mode` in the on-disk config at task runtime.

### Handlers

Each kind is its own task type with its own handler; the three track progress
on the task row (`task.NewProgressTracker`) and perform no lifecycle work —
the runner owns the claim, the batch flip, the locks and the settle under a
fresh per-task execution guard.

- `ConfigTaskHandler` — runs the asset downloads. `Handle` unmarshals `{"config_dir":"...", "op":"tessdata|hugot", "lang":...}`, loads config from disk, and dispatches to `config.DownloadTessdataLanguage` or `config.DownloadHugotModel`
- `MigrateDBTaskHandler` — unmarshals `MigrateDBPayload` and calls `MigrateDatabase`
- `MigrateStorageTaskHandler` — unmarshals `MigrateStoragePayload` and calls `MigrateStorage`; a no-op migration is reported as a `{"status":"no-op"}` result instead of an error

### Package functions

- `MigrateDatabase(ctx, logger, payload, report task.ProgressFunc) error` — loads config from disk, takes a best-effort gzipped pre-migration snapshot into `backup.path` (keeps the latest 5), dumps the current DB to a temp file (`database.DumpSchemaAndData`), connects to the destination (10s connect timeout), then: skips the copy when the destination already holds data (`document`/`task` row counts), refuses databases with tables but no goose history (`database.ValidateMigrationDestination`), restores the dump via `psql` (`database.RestoreDumpViaPSQL`, run per the current config's `database.runtime` — host binary or exec into the current container — connecting over TCP to the destination; `runtime: remote` refuses before dumping), and finally persists the new connection settings via `config.SaveMap` (also clearing `database.dsn` so the new fields win). Storage paths are not touched — that is the `migrate-storage` task's job. (The claim acquires the maintenance hold and the runner drains the wait set before the handler runs.)
- `MigrateStorage(ctx, logger, payload, report task.ProgressFunc) error` — loads config from disk (so it connects to the database the current config points at — after a preceding `migrate-db` task, the new database). The claim acquires the maintenance hold and verifies the `storage` wait (through the type's lock declarations; the runner drains the conflicting tasks first), and for each changed directory: rewrites pending consume-task payload paths (`database.RewriteTaskPayloadPaths`, before any file move), moves the storage subdirs (`processed/`, `originals/`, `errors/`, `orphaned/`, `trash/`, `thumbnails/`) and inbox files per `storage.migration_mode` (`copy`: copy-then-delete; `move`: `os.Rename` per entry with copy fallback), rewrites `document.storage_path`/`original_path` and `orphaned_file.file_path` (`database.RewriteStoragePaths`, only when `storage_dir` changed), and persists the new dirs via `config.SaveMap` only after the moves succeed

Dedup derivation lives in the task type catalog (`internal/ops/container`): config payloads derive `config:tessdata:<lang>` / `config:hugot`, the migrations carry the fixed per-type keys. The enqueue path in `ConfigHandler.enqueueConfigTasks` and the migration slot helpers share it via `Enqueuer.DedupKey`.

---

## Container Integration (`internal/ops/container/container.go`)

### CLI (`kushim`)

The `Container` registers all thirteen task types (`"consume"`, `"enrich"`, `"thumbnail"`, `"config"`, `"migrate-db"`, `"migrate-storage"`, `"backup"`, `"mirror"`, `"tagreconcile"`, `"thumbbackfill"`, `"tagmeasure"`, `"tagidentity"`, `"tagaudit"`)
and creates a `MatcherClient` connected to the Unix socket at `<config_dir>/kushim-hugot.sock`.
The `"backup"` type is handled by `BackupTaskHandler` which runs with the DB-backed maintenance lock already held (acquired at claim time, in the claim transaction), waits for in-flight tasks to drain, runs the backup, and lets the runner release the lock on completion/failure (token-guarded). The catalog derives its dedup key `backup:<mode>:<date>` from the payload's mode (default `full`).

The `"mirror"` type is handled by `MirrorTaskHandler` which runs with the same locks already held (claim-time acquisition) and delegates to `backup.RunMirrorAndRecord` (the runner holds the lock heartbeat and drains the wait set before the handler; the handler runs `rsync -a --delete --info=stats2 --timeout=600` and writes the `.edub-mirror.json` state file); the runner releases the lock on completion/failure. The catalog derives its dedup key `mirror:<UTC date>`.

The `"tagreconcile"` type is handled by `TagReconcileTaskHandler` which runs with the lock already held, drains in-flight tasks, and forks `kushim tagreconcile --run <batch-id>`: the child builds the reconciler on a runner with the `textreducer`, `contentanalyzer` and `tagmatcher` tools — the reducer feeds the provisional re-judge path (`Runner.ReduceContent` over the tag's first live document) — runs one pass, and prints the `tagreconcile-report: <json>` line the handler captures as the task result, while the child's exit code decides completion/failure. The catalog derives the fixed `tagreconcile` key, so any active run blocks further enqueues of the type. The `"thumbbackfill"` type is handled by the shared per-document `ThumbnailTaskHandler`: backfill batches are not claimed task-by-task — the forked consume child acquires `storage` for the whole run under the batch-owner token after verifying its declared waits, drains in-flight thumbnail tasks, and runs a dedicated 1-worker/2s pool whose claims verify their waits without acquiring; per-document `thumbnail` claims defer on both `maintenance` and `storage`, so per-document writes pause while the backfill runs.
The `"tagmeasure"` type is handled by `TagMeasureTaskHandler` which runs with the lock already held, drains in-flight tasks, and forks `kushim tagmeasure --run <batch-id>`: the child executes the pass (`internal/tags`) in one repeatable-read transaction, writes the evidence records, and prints a report line the handler captures as the task result, while the child's exit code decides completion/failure through the attempts budget. The pass opens by reading its `tag_measure_changed` flag and short-circuits to a zero-work report (no records) when the flag is clear and a completed run exists under the current rule set; otherwise it clears the flag and sweeps, re-arming it if the sweep fails. The catalog derives the fixed `tagmeasure` key, so any active run blocks further enqueues of the type. A manual `kushim tagmeasure` enqueues the same queued task through `curation.TagMeasure`, and the pool always claims — `enricher.tag_measure.enabled` gates only automatic enqueuing.
The `"tagidentity"` type is handled by `TagIdentityTaskHandler`, the same drain + heartbeat + fork shape: it forks `kushim tagidentity --run <batch-id>`, whose child resolves the pending identity evidence through `admission.IdentityResolver` and prints the `tagidentity-report: <json>` line (pending/resolved/folded/aliased/deferred/failed) the handler captures; the exit code decides completion/failure. The queue daemon runs its pool unconditionally; runs come from the review surface's `POST /api/v1/tags/identity/reevaluate`, a manual `kushim tagidentity`, and the pass's own daily 03:30 schedule, which fires only while `enricher.tag_adjudicator.enabled` is set. The catalog derives the fixed `tagidentity` key, and a run with the adjudicator disabled only counts the backlog.
The `"tagaudit"` type is handled by `TagAuditTaskHandler`, the same drain + heartbeat + fork shape: it forks `kushim tagaudit --run <batch-id>`, whose child verifies document coverage against the vocabulary (`admission.AuditRunner`), fills the confirmed gaps, records the coverage classes, and prints the `tagaudit-report: <json>` line (sweep, sampled/flagged counts, coverage tallies by class, fills/flags/proposals, judge call counts, pending coverage) the handler captures; the exit code decides completion/failure. The catalog derives the fixed `tagaudit` key. Runs come from the anchored schedule (disabled by default), a manual `kushim tagaudit`, and `POST /api/v1/tags/audit/reevaluate`; the pool always claims, so only the schedule obeys `enricher.tag_audit.enabled`; the run draws its sample from the sweep register, which stamps a document with the sweep it was audited in, so it is audited at most once per sweep and returns exactly one sweep later.
The task type catalog is built from `client.Queries` before the handler
registry so the consume handler and the enqueuer share one catalog; the tag
services and `Enricher` receive the client instead of a direct Hugot
reference:

```go
matcherClient := matcher.NewMatcherClient(c.SocketPath(), matcher.MaxMatchBodyBytes(cfg.Enricher.TagMatcher.ReduceTargetWords))
tagSvc := curation.NewTag(client, c.GetLogger(), matcherClient)
enricher := enrichment.NewEnricher(cfg, c.GetLogger(), client, tagSvc, matcherClient)
store := task.NewStore(client)
c.catalog = NewCatalog(client.Queries)
c.enqueuer = task.NewEnqueuer(client, c.catalog)
registry.Register(types.Task.Type.Consume, consumption.NewConsumeTaskHandler(consumer, store, client.Queries, c.GetLogger(), c.catalog))
registry.Register(types.Task.Type.Enrich, enrichment.NewEnrichTaskHandler(enricher, client.Queries, c.GetLogger()))
registry.Register(types.Task.Type.Config, configtask.NewConfigTaskHandler(client.Queries, c.GetLogger()))
registry.Register(types.Task.Type.MigrateDB, configtask.NewMigrateDBTaskHandler(client.Queries, c.GetLogger()))
registry.Register(types.Task.Type.MigrateStorage, configtask.NewMigrateStorageTaskHandler(client.Queries, c.GetLogger()))
registry.Register(types.Task.Type.Backup, backup.NewBackupTaskHandler(c.db, client.Queries, func() *config.Config { return c.GetConfig() }, c.GetLogger()))
registry.Register(types.Task.Type.Mirror, backup.NewMirrorTaskHandler(client.Queries, func() *config.Config { return c.GetConfig() }, c.GetLogger()))
```

The `BackupTaskHandler` receives a config getter closure used for the backup-root fallback (`backup.path`) and the config path; the backup `mode`/`path`/`keep` come from the task payload (the shared `backup.BackupPayload`), with the path validated against the configured backup roots. The `MirrorTaskHandler` receives the queries and a config getter; the mirror destination comes from the payload (falling back to `mirror.path`) and is validated via `backup.ValidateDestination`. The config pool is started alongside
consume/enrich pools when a CLI command runs.

### Server (`edub`)

The `Server` registers the three config-host task types (`config`, `migrate-db`, `migrate-storage`). Consume/enrich pools are **not started**
— the server enqueues tasks and the `kushim queue` daemon forks `kushim consume --batch <id>` subprocesses instead:

```go
registry.Register(types.Task.Type.Config, configtask.NewConfigTaskHandler(client.Queries, logger))
registry.Register(types.Task.Type.MigrateDB, configtask.NewMigrateDBTaskHandler(client.Queries, logger))
registry.Register(types.Task.Type.MigrateStorage, configtask.NewMigrateStorageTaskHandler(client.Queries, logger))
```

One pool per type (1 worker, 5s poll interval) is started on server boot and
stopped on shutdown.

---

## See Also

- [API](api.md) — Task/batch API handlers and response types
- [CLI](cli.md) — Task CLI commands (list, status, retry)
- [Database](database.md) — Task table schema, generated task queries
- [Pipeline](pipeline.md) — Consumer and Enricher invoked by task handlers
- [Config & Utils](config-and-utils.md) — Config setup functions used by ConfigTaskHandler

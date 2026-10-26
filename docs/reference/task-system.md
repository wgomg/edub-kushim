# Task System (`internal/task/`)

The package hosts the task domain and execution library used by every batch-mode
task CLI and the manual locking paths: the guarded lifecycle writes, the batch
lease, the runner's claim-run-settle loop, the shared batch-counting registry
and the `task` domain type.

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

- `Runner` — owns `Store` and `Registry`
  - **Methods**:
    - `NewRunner(store, registry, logger) *Runner`
    - `Next(ctx, taskType) error` — the claim-run-settle loop: claims under the batch lease (owner-scoped selection, waits verified), checks for nil payload (guarded fail), dispatches the handler, completes with retry (lease-guarded), then settles the batch through `SettleBatch` after every transition — the active-task guard defers it to the batch's last terminal transition, and a zero-rows transition settles only through guards so a taken-over execution never rewrites the winner's status; extracts `ReqID` from handler errors via `errors.AsType` for log correlation, and a `*Error` with `PauseBatch` fails the task, runs the guarded pause and skips the settle. A deferred `recover()` after the claim fails the in-flight task with `panic: <text>` (guarded fail write, detached 10s-timeout context) — the worker set's caller logs the error; the batch's locks, drain and heartbeat belong to `RunBatch` (§7)
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
    - `EnqueueBatch(ctx, spec, tasks []TaskSpec) (EnqueueResult, error)` — one transaction inserts the batch (origin, schedule, `queued`) and its tasks; a violation of the dedup index skips that task, a collision on any other unique constraint (the task id) aborts, zero survivors create nothing, and the batch types must share one `Holds` set; preconditions run before any row is written; a pending task whose type carries no dedup hook in the process's catalog is refused instead of being created without deduplication
    - `EnqueueTask(ctx, spec, task)` — single-task convenience over `EnqueueBatch`
    - `DedupKey(taskType, payload) (string, error)` — the derivation a task of that type would carry, for boundary-side coverage checks; a type without a hook in the catalog is an error, never an empty key

### Type definitions

- `TypeDefinition` — one task type's enqueue and dispatch contract: `Type`, `Locks LockPolicy` (`Holds`/`WaitsFor` over the closed `types.LockName` set, populated from `types.LockDeclarationFor`), `DirectLocks *LockPolicy` (the direct CLI run's policy; nil uses the queued set), `DedupKey DedupKeyFunc` (a hook the owning producer supplies at catalog construction; an empty derived key means no dedup), `Preconditions []func(ctx) error`, `CLI`/`BatchSelector`/`Hosts` (the batch-mode dispatch surface) and `Concurrency`
- `TypeCatalog` — `Definition(taskType) (TypeDefinition, error)`; the concrete table is `task.Catalog` (`catalog.go`), built per process boundary
- `DedupKeyFunc` — `func(json.RawMessage) string`, the dedup derivation one type carries

## `catalog.go`

### Struct

- `Catalog` — one process boundary's task type table: the package-level static policy (per type: identity, queued and direct locks, batch-mode `CLI`/`BatchSelector`, `Hosts`) merged with the boundary's values
  - **Methods**:
    - `NewCatalog(dedupHooks map[types.TaskType]DedupKeyFunc, concurrency map[types.TaskType]int) (*Catalog, error)` — applies the two per-type maps over the static table; either map may be nil or partial (it names the types this process enqueues), and a key that is not a declared task type fails at construction
    - `Definition(taskType) (TypeDefinition, error)` — the entry for a task type; an unknown type is an error

### Package functions

- `DirectLockPolicyFor(taskType) LockPolicy` — the lock policy a direct CLI run uses: the type's declared direct override when one exists (consume and enrich hold `maintenance`; `thumbnails` holds `storage` and waits `maintenance`), otherwise the queued lock set
- `ConstantDedupKey(key) DedupKeyFunc` — the hook the fixed-key types carry (the four tag passes and the two migrations), wrapping a producer-owned constant

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
- `Execution{BatchID, Guard, Leased}` — the authorization a caller carries for one run's writes: the batch lease owner; the reaper's guardless settle passes a NULL owner
- `ClaimRequest{BatchID, ExecToken, Holds, Waits, DispatcherID}` — one batch claim: the execution token the forked child adopts, the lock sets the claim acquires and verifies, and the dispatcher owning the lease (zero for a manual run, which never charges a dispatcher's slot budget)
- `TaskIdentity{TaskID, TaskType}` / `ResetResult{Reset, Quarantined}` (`ConsumeTaskIDs()`) / `CancelResult{PendingCancelled, ProcessingCancelled, OwnerPID, OwnerAlive, Signalled}` / `ResumeResult{Resumed, Reset, FailedRetried}`
- `Owner` — `Adopt` (one-time adoption of the dispatcher's pid-0 lease under the same owner token: records the child pid, refuses an adopted, released or re-owned lease), `Heartbeat` (rows-reported renewal), `Release` (pair-guarded delete)
- `ClaimBatch(ctx, client, claim) error` — the guarded batch claim in one transaction: row-lock the batch, require `queued`, insert the pid-0 lease under the execution token, acquire the holds all-or-nothing while verifying the waits the holding types declare, flip `queued → processing`; `ErrLockBusy` leaves nothing acquired, a non-queued batch reports `ErrBatchNotQueued`, a batch that already carries a lease `ErrBatchLocked`
- `ResetProcessingTasksByBatch(ctx, client, batchID, maxRetries) (ResetResult, error)` — the takeover variant: one transaction that releases the batch's locks, quarantines the tasks at the retry bound, resets the rest (attempts + 1) and restores discarded children; returns the reset count and the quarantined identities
- `ResetProcessingTasksForClaim(ctx, client, batchID, maxRetries) (ResetResult, error)` — the claim-path variant: the same reset without the lock release, so the holds the claim acquired survive entry repair
- `IsOrphaned(state, pending, processing)` / `IsAlive(pid)` — the owner-liveness predicates (`IsAlive` refuses `pid <= 0`, so `kill(0)` can never target the caller's group)
- `SettleBatch(ctx, client, logger, exec) (types.BatchStatus, error)` — the normal settlement writer: `failed` when any task failed, `completed` otherwise, computed inside the guarded write; refuses (returns the current status untouched) while a pending/processing/waiting task exists, while the status is not `processing`, and when a lease row exists that is not the executor's; the settle transaction releases the batch's locks; a terminal batch is idempotent. The one sanctioned failure-terminal exception is `FailUnstartedBatch`
- `FailUnstartedBatch(ctx, client, batchID, execToken) (applied, error)` — the launch-failure terminal write: under the execution token's lease fence it cancels the tasks that never ran, releases the batch's locks and writes `failed` in one transaction; `applied = false` when the lease no longer belongs to the token (the batch changed hands since the launch decision) and nothing is written
- `PauseBatch(ctx, client, logger, exec) error` — guarded `processing → paused` through the batch lease owner, releasing the batch's locks and the caller's lease; a refused pause writes nothing
- `CancelBatch(ctx, client, logger, batchID) (CancelResult, error)` — signals a live owner (SIGTERM through `IsAlive`), cancels pending + waiting + processing, releases the batch's locks and lease, writes `cancelled`; refuses a missing (`ErrBatchNotFound`) or terminal (`ErrBatchTerminal`) batch
- `ResumeBatch(ctx, client, logger, batchID, maxRetries, includeFailed) (ResumeResult, error)` — the single recovery: from `failed|completed|cancelled|paused` to `queued`, resets processing tasks (quarantine-at-max + restore) in the same transaction and, when `includeFailed`, retries the failed tasks with the batch restore re-run after the retry; refuses a live lease (`ErrBatchLocked`), a queued or processing batch (`ErrBatchNotResumable`) and leaves a batch with no resurrectable work untouched (`Resumed = false`)
- `RetryTask(ctx, client, logger, taskID, maxRetries) error` — theCompatibility alias behind the retry surfaces: resolves the task (`ErrTaskNotFound`), refuses a non-failed one (`ErrTaskNotFailed`), then resumes its batch with failed tasks reset

---

### SQL queries added

- `RetryFailedTasksByBatch :execrows` — `UPDATE ... WHERE batch_id = ? AND status = 'failed'` — Resets failed batch tasks to pending (attempts preserved; the budget only moves on reclamation resets). Runs inside `ResumeBatch` followed, in the same transaction, by `RestoreDiscardedEnrichTasksByBatch`.
- `GetConfigTaskByDedupKey :one` — Returns the most recent task matching `dedup_key`. Used by `tryAcquireConfigTaskSlot` for the migration enqueuers. Completed/failed/cancelled tasks do not block a fresh enqueue: the migration enqueue inserts a new task row under a new batch (the partial unique index on `dedup_key` only covers `pending`/`processing`) instead of retrying the old task.
- `DeleteConfigTaskByDedupKey :execrows` — `DELETE FROM task WHERE task_type = @task_type AND dedup_key = @dedup_key AND status IN ('pending','failed')` — Atomic, status-guarded supersede of a stale migration task with the caller's task type (used by `enqueueDBMigration` so a task claimed between read and delete survives and wins).
- `DiscardEnrichTask :execrows` — `UPDATE ... WHERE id = ? AND status = 'waiting' AND task_type = 'enrich'` — Discards an enrich task by internal `id`; returns rows affected so callers can distinguish a real discard from a no-op. Wrapped by `Store.Discard`.
- `DiscardEnrichTaskByTaskID :execrows` — `UPDATE ... WHERE task_id = ? AND status = 'waiting' AND task_type = 'enrich'` — Discards an enrich task by UUID `task_id`. Kept for the test suite (`TestDiscardEnrichTaskByTaskID`); production discard paths use `Store.Discard` (handler) and the sweep queries (recovery points).
- `SetEnrichTaskPending :exec` — `UPDATE ... SET status='pending', payload=@payload, dedup_key=@dedup_key, error=NULL, completed_at=NULL WHERE id = ? AND status IN ('waiting','discarded') AND task_type IN ('enrich','thumbnail')` — Children are activated with the definition-derived dedup key atomically with the pending flip, so the dedup index covers activated children; the join-based restores work from the same link.
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

The migration task handlers live in their own package (`internal/ops/configtask/`) — it hosts the handlers `kushim migrate --from-batch` and the wizard's setup-time dispatcher register, keeping those boundaries free of the domain packages' transitive CGo dependencies.

### Constants

- `DedupKeyMigrateDB = "migrate-db"`, `DedupKeyMigrateStorage = "migrate-storage"` — fixed dedup keys of the two migration task types (at most one migration task pending/processing per type)

### Structs

- `MigrateDBPayload` — `config_dir`, destination connection fields (`host`, `port`, `user`, `password`, `database`, `sslmode`). Carried by the `migrate-db` task so the handler can open both databases directly and persist the new values only after the copy succeeds. No storage fields — storage relocation belongs to the `migrate-storage` task.
- `MigrateStoragePayload` — `config_dir`, old/new storage dirs (`old_storage_dir`, `new_storage_dir`, `old_consumption_dir`, `new_consumption_dir`). Carried by the `migrate-storage` task; the move strategy (`copy`/`move`) is read from `storage.migration_mode` in the on-disk config at task runtime.

### Handlers

Each kind is its own task type with its own handler; the two track progress
on the task row (`task.NewProgressTracker`) and perform no lifecycle work —
the run owns the claim, the batch flip, the locks and the settle under the
execution's lease owner.

- `MigrateDBTaskHandler` — unmarshals `MigrateDBPayload` and calls `MigrateDatabase`
- `MigrateStorageTaskHandler` — unmarshals `MigrateStoragePayload` and calls `MigrateStorage`; a no-op migration is reported as a `{"status":"no-op"}` result instead of an error

### Package functions

- `MigrateDBSpec(MigrateDBPayload) (task.TaskSpec, error)` / `MigrateStorageSpec(MigrateStoragePayload) (task.TaskSpec, error)` — the two migration payloads marshalled into ready specs
- `StorageMigrationPayload(cfg, newStorageDir, newConsumptionDir) (MigrateStoragePayload, bool)` — the payload builder the direct command, the API config handler and `kushim batch create migrate-storage` share: each changed pair carries its old and new path, and an unchanged pair is omitted; false means a no-op
- `MigrateDatabase(ctx, logger, payload, report task.ProgressFunc) error` — loads config from disk, takes a best-effort gzipped pre-migration snapshot into `backup.path` (keeps the latest 5), dumps the current DB to a temp file (`database.DumpSchemaAndData`), connects to the destination (10s connect timeout), then: skips the copy when the destination already holds data (`document`/`task` row counts), refuses databases with tables but no goose history (`database.ValidateMigrationDestination`), restores the dump via `psql` (`database.RestoreDumpViaPSQL`, run per the current config's `database.runtime` — host binary or exec into the current container — connecting over TCP to the destination; `runtime: remote` refuses before dumping), and finally persists the new connection settings via `config.SaveMap` (also clearing `database.dsn` so the new fields win). Storage paths are not touched — that is the `migrate-storage` task's job. (The batch claim acquires the maintenance hold and the run drains the wait set before the handler runs.)
- `MigrateStorage(ctx, logger, payload, report task.ProgressFunc) error` — loads config from disk (so it connects to the database the current config points at — after a preceding `migrate-db` task, the new database). The batch claim acquires the maintenance hold and verifies the `storage` wait, and for each changed directory: rewrites pending consume-task payload paths (`database.RewriteTaskPayloadPaths`, before any file move), moves the storage subdirs (`processed/`, `originals/`, `errors/`, `orphaned/`, `trash/`, `thumbnails/`) and inbox files per `storage.migration_mode` (`copy`: copy-then-delete; `move`: `os.Rename` per entry with copy fallback), rewrites `document.storage_path`/`original_path` and `orphaned_file.file_path` (`database.RewriteStoragePaths`, only when `storage_dir` changed), and persists the new dirs via `config.SaveMap` only after the moves succeed

Tessdata and hugot downloads are not tasks: `config.DownloadTessdataLanguage` and `config.DownloadHugotModel` are called directly by the config update paths (the shared config handler, the setup CLI and enrichment auto-detection); they create no batch, task row, dedup key, lease or queue progress.

Dedup derivation lives with each type's producer package (the migration types carry the fixed per-type keys), and the process catalog receives them as hooks at construction; the migration and `batch create` boundaries share it via `Enqueuer.DedupKey`.

---

## Task wiring at the process boundaries

There is no shared task-wiring point: each process boundary constructs its own
task catalog (dedup hooks naming the types it enqueues), its enqueuers and,
per batch-mode task type, the store and handler registry the type's CLI
registers when it runs a claimed batch. The task package
contains none of this; producers own their task-set construction.

### CLI (`kushim`)

`Container` (`internal/ops/container`) is a lazy assembler for the shared
dependency services only — config, the database connection and its reconnect
protocol, the tag service, the search engine, the matcher client and the tag
tools runner. It builds no task catalog, enqueuer or handler registry. The
`consume`/`enrich`/`thumbnail` handlers and the ingest family's registry are
built in `internal/ops/consume` (`newConsumeHandler`/`newEnrichHandler`/
`newThumbnailHandler` over the command's own consumer, enricher and thumbnail
tools): the direct mode builds the handlers it runs, and the batch modes build
a store and the registry of the types their batch carries before calling
`consume.RunClaimedBatch`. Every other batch-mode CLI assembles the same way
at its own command boundary — `backup`/`mirror`/`migrate` and the four tag
passes register the single-type registries their batch carries — and each
passes `nil` quarantine hooks. Every enqueue call site builds its own enqueuer over a
catalog wired with exactly the hooks of the types it creates, and builds its
task sets through the owning producer:

- inbox scans: `consumption.IngestSpecsFromInbox` (`batch create consume`,
  `kushim batch create consume` paused-guard) and `consumption.ScanAndEnqueue`
  (polling) / the orphan restore (`service.Orphaned.Restore` via
  `consumption.IngestTaskSpecs`)
- re-enrichment: `enrichment.EnrichSpecs` (explicit ids) and
  `service.ReEnrich` (the `--all | --limit` selection)
- thumbnails: `service.ThumbnailBackfillSpecs` (the all-documents scan) and
  `service.ThumbnailBackfill.BackfillBatch`/`BackfillDocument` (selections)
- backups and mirrors: `backup.BackupSpecs` / `backup.MirrorSpecs`
- the tag passes: the four spec constructors
  (`curation.ReconcileSpecs`, `curation.MeasureSpecs`,
  `curation.IdentitySpecs`, `curation.AuditSpecs`), named by
  `kushim batch create`
- the config family: `configtask.MissingAssetSpecs`-class direct downloads
  aside, the two migration payloads (`configtask.MigrateDBSpec`/
  `configtask.MigrateStorageSpec` and `StorageMigrationPayload`), enqueued by
  the API config handler, the wizard and `kushim batch create migrate-db` /
  `migrate-storage`

### Queue daemon (`kushim queue`)

`queue.Run` builds the daemon catalog once — the enqueue hooks of the types
the daemon creates (polling's `consume`, the schedules' `backup`, `mirror`,
`thumbbackfill` and the four tag passes) — and hands it to
`dispatch.NewDispatcher` (fork-only: the dispatcher resolves each batch's
launch target and forks its task CLI); the scheduler and the polling tick construct
`task.NewEnqueuer(client, catalog)` over the current connection at each use,
so a reconnect carries. The daemon builds no handler registries — the task
CLIs it forks carry their own — and its scheduler enqueues
through the producers above with origin `schedule`.

### edub and the wizard

edub constructs its catalog at boot/`rebuild` and its own enqueuer over it,
wiring the hooks of everything its handlers can enqueue (`consume`, `enrich`,
`thumbbackfill`, `migrate-db`, `migrate-storage`, `tagidentity`, `tagaudit`),
and never claims a batch: the `kushim queue` daemon forks the batches' owning
task CLIs. The wizard constructs the same shape at bootstrap with the two
migration hooks, and additionally runs the setup-time dispatcher whose
in-process registration covers `migrate-db`/`migrate-storage` — it registers
both migration handlers over the bootstrap client and executes a claimed
migration batch itself while no daemon exists:

```go
catalog, _ := task.NewCatalog(map[types.TaskType]task.DedupKeyFunc{ ... }, nil)
enqueuer := task.NewEnqueuer(client, catalog)
dispatcher := dispatch.NewDispatcher(catalog, logger, types.HostWizard, budget, wake,
    &dispatch.InProcessRegistration{ ... Run: dispatch.InProcessRun(deps) })
```

---

## See Also

- [API](api.md) — Task/batch API handlers and response types
- [CLI](cli.md) — Task CLI commands (list, status, retry)
- [Database](database.md) — Task table schema, generated task queries
- [Pipeline](pipeline.md) — Consumer and Enricher invoked by task handlers
- [Config & Utils](config-and-utils.md) — Config setup functions used by ConfigTaskHandler

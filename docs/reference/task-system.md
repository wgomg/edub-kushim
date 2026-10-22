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

- `Task` — domain struct with `ID int64`, `TaskID string`, `TaskType string`, `Payload json.RawMessage`

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
    - `ClaimNextPending(ctx, taskType) (database.Task, error)`
    - `GetTask(ctx, id) (database.Task, error)`
    - `GetTaskByTaskID(ctx, taskID) (database.Task, error)`
    - `CompleteTask(ctx, id, result) (int64, error)` — returns rows affected; requires `status = 'processing'` (optimistic concurrency guard)
    - `FailTask(ctx, id, errMsg) error`
    - `SetPending(ctx, id, payload) error` — Wraps `SetEnrichTaskPending`; matches both `waiting` and `discarded` enrich tasks
    - `Discard(ctx, id, errMsg) (int64, error)` — Wraps `DiscardEnrichTask`; returns rows affected; only matches `waiting` enrich tasks (0 rows = already discarded or activated, benign no-op)

## `registry.go`

### Struct

- `Registry` — maps task type strings to `Handler` implementations
  - **Methods**:
    - `Register(taskType, handler)` — registers a handler
    - `Get(taskType) (Handler, error)` — looks up a handler

## `runner.go`

### Struct

- `Runner` — owns `Store` and `Registry`; implements `Nexter`
  - **Methods**:
    - `NewRunner(store, registry, logger) *Runner`
    - `Next(ctx, taskType) error` — generic poll loop: claim next pending task, check for nil payload (fail task if nil), get handler, execute, complete (with retry+FailTask fallback); extracts `ReqID` from handler errors via `errors.As` and passes it to the logger for document-level correlation (`REQID=<doc-id> task <uuid> failed: ...`). A deferred `recover()` after the claim fails the in-flight task with `panic: <text>` (detached 10s-timeout context) and returns the error to the pool, which logs it without restarting the worker
    - `completeTaskWithRetry(ctx, id, result) error` — private: 3× exponential backoff on `CompleteTask`, handles `rows == 0` (task already transitioned by stale-task sweep)

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
    - `EnqueueBatch(ctx, spec, tasks []TaskSpec) (EnqueueResult, error)` — one transaction inserts the batch (origin, schedule, `queued`) and its tasks; a dedup conflict skips that task, zero survivors create nothing, and the batch types must share one `Holds` set; preconditions run before any row is written
    - `EnqueueTask(ctx, spec, task)` — single-task convenience over `EnqueueBatch`
    - `DedupKey(taskType, payload) (string, error)` — the derivation a task of that type would carry, for boundary-side coverage checks

### Type definitions

- `TypeDefinition` — one task type's enqueue contract: `Type`, `Locks LockPolicy` (`Holds`/`WaitsFor` over the closed `types.LockName` set), `DedupKey func(payload) string` (empty result = no dedup), `Preconditions []func(ctx) error`, `BuildTasks func(ctx, BuildInput) ([]TaskSpec, error)` — the typed task-set builder used by `kushim batch create` and the scheduler
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
- `Retry(ctx, queries, logger, taskID) error` — Failed tasks only (409 otherwise). For consume tasks whose payload carries `on_completed`, also restores the paired `discarded` enrich to `waiting` via `SetEnrichTaskWaiting`; a failed restore is logged at error level and the retry still succeeds (activation accepts `discarded`).
- `RetryBatchFailed(ctx, queries, batchID string) (int64, error)` — Resets all failed tasks in a batch to pending; returns count of retried tasks. Uses `RetryFailedTasksByBatch` sqlc query with `WHERE batch_id = ? AND status = 'failed'`.
- `CountBatchStatuses(ctx, queries, batchID) BatchCounts` — Counts per status including `waiting` and `discarded`
- `ListBatchSummaries(ctx, queries, filter) ([]BatchCounts, error)` — Lists batch summaries with filtering; uses `ListDistinctBatchIDs` or `ListDistinctBatchIDsByStatus`

---

### SQL queries added

- `RetryFailedTasksByBatch :execrows` — `UPDATE ... WHERE batch_id = ? AND status = 'failed'` — Resets failed batch tasks to pending with `attempts = 0` (batched retry). `Batch.RetryFailed` runs it transactionally alongside `RestoreDiscardedEnrichTasksByBatch`.
- `GetConfigTaskByDedupKey :one` — Returns the most recent config task matching `dedup_key`. Used by `ConfigHandler.configTaskCovered` (skip enqueue when a `pending`/`processing` task already exists) and by `tryAcquireConfigTaskSlot` for the migration enqueuers. Completed/failed/cancelled tasks do not block a fresh enqueue: `enqueueConfigTasks` inserts a new task row under a new batch (the partial unique index on `dedup_key` only covers `pending`/`processing`) instead of retrying the old task.
- `DeleteConfigTaskByDedupKey :execrows` — `DELETE FROM task WHERE task_type = 'config' AND dedup_key = ? AND status IN ('pending','failed')` — Atomic, status-guarded supersede of a stale config task (used by `enqueueDBMigration` so a task claimed between read and delete survives and wins).
- `DiscardEnrichTask :execrows` — `UPDATE ... WHERE id = ? AND status = 'waiting' AND task_type = 'enrich'` — Discards an enrich task by internal `id`; returns rows affected so callers can distinguish a real discard from a no-op. Wrapped by `Store.Discard`.
- `DiscardEnrichTaskByTaskID :execrows` — `UPDATE ... WHERE task_id = ? AND status = 'waiting' AND task_type = 'enrich'` — Discards an enrich task by UUID `task_id`. Kept for the test suite (`TestDiscardEnrichTaskByTaskID`); production discard paths now use `Store.Discard` (handler) and the sweep queries (recovery points).
- `SetEnrichTaskWaiting :execrows` — `UPDATE ... SET status='waiting', error=NULL, completed_at=NULL WHERE task_id = ? AND status = 'discarded' AND task_type = 'enrich'` — Restores a discarded enrich when its consume is retried/reset. Targeted by UUID `task_id` (the consume payload's `on_completed`), race-free.
- `RestoreDiscardedEnrichTasks :execrows` / `RestoreDiscardedEnrichTasksByBatch :execrows` — join-based restore (`UPDATE ... FROM task AS c WHERE e.status='discarded' AND c.status='pending' AND e.task_id = c.payload->>'on_completed'`, batch variant scoped by `c.batch_id`): re-arms every discarded enrich whose consume is pending again. Run inside the same transaction as the retry/reset.
- `DiscardWaitingEnrichesOfFailedConsumes :execrows` / `DiscardWaitingEnrichesOfFailedConsumesGlobal :execrows` — join-based sweep: discards every waiting enrich whose consume is `failed` (batch-scoped or global), copying the parent's error text (`COALESCE(c.error, 'parent consume task failed')`). Enforces "enrich is never waiting while its consume is terminal-failed" at recovery points. Batch variant runs unconditionally in `QuarantineFailedFiles`; the global variant runs inside `Batch.ResetStaleProcessingTasks`.

---

## `consume.go` (`internal/documents/consumption`)

### Struct

- `ConsumeTaskHandler` — `consumer *consumption.Consumer`, `store *task.Store`, `queries *database.Queries`, `logger *log.Logger`
  - **Methods**:
    - `NewConsumeTaskHandler(consumer, store, queries, logger) *ConsumeTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals payload (`file_path`, `document_id` UUID, `on_completed` enrich task ID), calls `FileFromPath` + `consumer.Process`. On success, if `on_completed` is set and a document was created, activates the linked enrich task via `Store.SetPending`. On **any** failure (payload unmarshal, empty `file_path`, `FileFromPath`, or `Process`), discards the linked enrich task via `withDiscardAttempt` — if the discard itself fails, the error is appended (`"; additionally failed to discard enrich task <id>: ..."`) so it lands in the task's `error` field.
    - `activateChildEnrich(ctx, parent, onCompleted, documentID)` — Looks up the enrich task by UUID, validates `waiting_for` matches the parent, updates its payload with the document ID, and sets it to `pending`. Works on both `waiting` and `discarded` enrich tasks so a retried consume can reactivate a previously-discarded enrich.
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

Each kind is its own task type with its own handler; all three track progress on the task row (`task.NewProgressTracker`) and settle their batch (`MarkBatchProcessing` / `FinalizeBatchStatus`).

- `ConfigTaskHandler` — runs the asset downloads. `Handle` unmarshals `{"config_dir":"...", "op":"tessdata|hugot", "lang":...}`, loads config from disk, and dispatches to `config.DownloadTessdataLanguage` or `config.DownloadHugotModel`
- `MigrateDBTaskHandler` — unmarshals `MigrateDBPayload` and calls `MigrateDatabase`
- `MigrateStorageTaskHandler` — unmarshals `MigrateStoragePayload` and calls `MigrateStorage`; a no-op migration is reported as a `{"status":"no-op"}` result instead of an error

### Package functions

- `MigrateDatabase(ctx, logger, payload, report task.ProgressFunc) error` — takes the maintenance lock on the current DB with `database.TryHoldMaintenanceLock` (refusing when another process holds it; the helper also keeps the lock heartbeating and drains in-flight tasks via `database.WaitForTaskDrain`), takes a best-effort gzipped pre-migration snapshot into `backup.path` (keeps the latest 5), dumps the current DB to a temp file (`database.DumpSchemaAndData`), connects to the destination (10s connect timeout), then: skips the copy when the destination already holds data (`document`/`task` row counts), refuses databases with tables but no goose history (`database.ValidateMigrationDestination`), restores the dump via `psql` (`database.RestoreDumpViaPSQL`, run per the current config's `database.runtime` — host binary or exec into the current container — connecting over TCP to the destination; `runtime: remote` refuses before dumping), and finally persists the new connection settings via `config.SaveMap` (also clearing `database.dsn` so the new fields win). Storage paths are not touched — that is the `migrate-storage` task's job.
- `MigrateStorage(ctx, logger, payload, report task.ProgressFunc) error` — loads config from disk (so it connects to the database the current config points at — after a preceding `migrate-db` task, the new database). Takes the maintenance lock with `database.TryHoldMaintenanceLock` (refusing when held; heartbeat + drain included), and for each changed directory: rewrites pending consume-task payload paths (`database.RewriteTaskPayloadPaths`, before any file move), moves the storage subdirs (`processed/`, `originals/`, `errors/`, `orphaned/`, `trash/`, `thumbnails/`) and inbox files per `storage.migration_mode` (`copy`: copy-then-delete; `move`: `os.Rename` per entry with copy fallback), rewrites `document.storage_path`/`original_path` and `orphaned_file.file_path` (`database.RewriteStoragePaths`, only when `storage_dir` changed), and persists the new dirs via `config.SaveMap` only after the moves succeed

Dedup derivation lives in the task type catalog (`internal/ops/container`): config payloads derive `config:tessdata:<lang>` / `config:hugot`, the migrations carry the fixed per-type keys. The enqueue path in `ConfigHandler.enqueueConfigTasks` and the migration slot helpers share it via `Enqueuer.DedupKey`.

---

## Container Integration (`internal/ops/container/container.go`)

### CLI (`kushim`)

The `Container` registers all thirteen task types (`"consume"`, `"enrich"`, `"thumbnail"`, `"config"`, `"migrate-db"`, `"migrate-storage"`, `"backup"`, `"mirror"`, `"tagreconcile"`, `"thumbbackfill"`, `"tagmeasure"`, `"tagidentity"`, `"tagaudit"`)
and creates a `MatcherClient` connected to the Unix socket at `<config_dir>/kushim-hugot.sock`.
The `"backup"` type is handled by `BackupTaskHandler` which runs with the DB-backed maintenance lock already held (acquired at claim time, in the claim transaction), waits for in-flight tasks to drain, runs the backup, and lets the runner release the lock on completion/failure (token-guarded). The catalog derives its dedup key `backup:<mode>:<date>` from the payload's mode (default `full`).

The `"mirror"` type is handled by `MirrorTaskHandler` which runs with the same lock already held (claim-time acquisition), then delegates to `backup.RunLocked` (5-minute `TouchMaintenanceLockForTask` heartbeat started before the drain → drain → `rsync -a --delete --info=stats2 --timeout=600` → `.edub-mirror.json` state write); the runner releases the lock on completion/failure. The catalog derives its dedup key `mirror:<UTC date>`.

The `"tagreconcile"` type is handled by `TagReconcileTaskHandler` which runs with the lock already held, drains in-flight tasks, and forks `kushim tagreconcile --run <batch-id>`: the child builds the reconciler on a runner with the `textreducer`, `contentanalyzer` and `tagmatcher` tools — the reducer feeds the provisional re-judge path (`Runner.ReduceContent` over the tag's first live document) — runs one pass, and prints the `tagreconcile-report: <json>` line the handler captures as the task result, while the child's exit code decides completion/failure. The catalog derives the fixed `tagreconcile` key, so any active run blocks further enqueues of the type. The `"thumbbackfill"` type is handled by `ThumbnailBackfillTaskHandler`, a wrapper that adds the same drain-wait + heartbeat around the per-document `ThumbnailTaskHandler`; backfill tasks are created by `service.ThumbnailBackfill` (dedup key `thumbnail:doc:<uuid>`) and processed by a dedicated 1-worker/2s pool, while per-document `thumbnail` tasks remain lock-gated.
The `"tagmeasure"` type is handled by `TagMeasureTaskHandler` which runs with the lock already held, drains in-flight tasks, and forks `kushim tagmeasure --run <batch-id>`: the child executes the pass (`internal/tags`) in one repeatable-read transaction, writes the evidence records, and prints a report line the handler captures as the task result, while the child's exit code decides completion/failure through the attempts budget. The pass opens by reading its `tag_measure_changed` flag and short-circuits to a zero-work report (no records) when the flag is clear and a completed run exists under the current rule set; otherwise it clears the flag and sweeps, re-arming it if the sweep fails. The catalog derives the fixed `tagmeasure` key, so any active run blocks further enqueues of the type. A manual `kushim tagmeasure` enqueues the same queued task through `curation.TagMeasure`, and the pool always claims — `enricher.tag_measure.enabled` gates only automatic enqueuing.
The `"tagidentity"` type is handled by `TagIdentityTaskHandler`, the same drain + heartbeat + fork shape: it forks `kushim tagidentity --run <batch-id>`, whose child resolves the pending identity evidence through `admission.IdentityResolver` and prints the `tagidentity-report: <json>` line (pending/resolved/folded/aliased/deferred/failed) the handler captures; the exit code decides completion/failure. The queue daemon runs its pool unconditionally; runs come from the review surface's `POST /api/v1/tags/identity/reevaluate`, a manual `kushim tagidentity`, and the pass's own daily 03:30 schedule, which fires only while `enricher.tag_adjudicator.enabled` is set. The catalog derives the fixed `tagidentity` key, and a run with the adjudicator disabled only counts the backlog.
The `"tagaudit"` type is handled by `TagAuditTaskHandler`, the same drain + heartbeat + fork shape: it forks `kushim tagaudit --run <batch-id>`, whose child verifies document coverage against the vocabulary (`admission.AuditRunner`), fills the confirmed gaps, records the coverage classes, and prints the `tagaudit-report: <json>` line (sweep, sampled/flagged counts, coverage tallies by class, fills/flags/proposals, judge call counts, pending coverage) the handler captures; the exit code decides completion/failure. The catalog derives the fixed `tagaudit` key. Runs come from the anchored schedule (disabled by default), a manual `kushim tagaudit`, and `POST /api/v1/tags/audit/reevaluate`; the pool always claims, so only the schedule obeys `enricher.tag_audit.enabled`; the run draws its sample from the sweep register, which stamps a document with the sweep it was audited in, so it is audited at most once per sweep and returns exactly one sweep later.
The task type catalog and the enqueuer are built together with the runner; the tag services and `Enricher` receive the client instead of a direct Hugot reference:

```go
matcherClient := matcher.NewMatcherClient(c.SocketPath(), matcher.MaxMatchBodyBytes(cfg.Enricher.TagMatcher.ReduceTargetWords))
tagSvc := curation.NewTag(client, c.GetLogger(), matcherClient)
enricher := enrichment.NewEnricher(cfg, c.GetLogger(), client, tagSvc, matcherClient)
c.catalog = container.NewCatalog(client.Queries)
c.enqueuer = task.NewEnqueuer(client, c.catalog)
registry.Register(types.Task.Type.Consume, consumption.NewConsumeTaskHandler(consumer, store, client.Queries, c.GetLogger()))
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

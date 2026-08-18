# Pool (`internal/pool/`)

## `pool.go`

### Struct

- `Pool` — `logger`, `runner Runner`, `workers int`, `interval time.Duration`, `taskType string`, `stopCh`, `stopOnce`, `wg`, `ctx`, `cancel`
  - **Methods**:
    - `New(logger, runner, workers, interval, taskType) *Pool`
    - `Start(ctx)` — Starts worker goroutines with a cancellable context
    - `Stop(ctx)` — Signals stop, waits for workers with timeout

### Interface

- `Runner` — `Next(ctx, taskType string) error` (implemented by `task.Runner`)

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

The `Runner.Next` method uses `errors.As(err, &tErr)` to extract `ReqID` from handler errors before logging. Layers that have a document ID in scope (consumer, enricher, handlers) wrap fatal returns with `&task.Error{ReqID: documentID, Err: ...}`. Unmarshal failures and other errors without a document ID are left unwrapped — `Next` falls back to nil reqID, producing the same log output as before.

## `handler.go`

### Interfaces

- `Handler` — `Handle(ctx, task.Task) (json.RawMessage, error)`
- `Dedupable` — `DedupKey(payload json.RawMessage) string`

## `store.go`

### Struct

- `Store` — wraps `*database.Queries`
  - **Methods**:
    - `CreateTask(ctx, taskType, batchID, payload, taskID, status, dedupKey string) (string, error)`
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
    - `DedupKey(taskType, payload) string` — extracts a handler-specific dedup key when available

## `runner.go`

### Struct

- `Runner` — owns `Store` and `Registry`; implements `pool.Runner`
  - **Methods**:
    - `NewRunner(store, registry, logger) *Runner`
    - `Next(ctx, taskType) error` — generic poll loop: claim next pending task, check for nil payload (fail task if nil), get handler, execute, complete (with retry+FailTask fallback); extracts `ReqID` from handler errors via `errors.As` and passes it to the logger for document-level correlation (`REQID=<doc-id> task <uuid> failed: ...`)
    - `completeTaskWithRetry(ctx, id, result) error` — private: 3× exponential backoff on `CompleteTask`, handles `rows == 0` (task already transitioned by stale-task sweep)

## `dispatcher.go`

### Struct

- `Dispatcher` — thin coordinator that owns `Store` and `Registry`; exposes a concise `Enqueue` API
  - **Methods**:
    - `NewDispatcher(logger, store, registry) *Dispatcher`
    - `Enqueue(ctx, taskType, batchID, payload, taskID string, status ...string) (string, error)` — validates task type, computes dedup key via registry, delegates persistence to store. Supports custom taskID and initial status (e.g., `"waiting"`)

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
- `GetConfigTaskByDedupKey :one` — Returns the most recent config task matching `dedup_key`. Used by `ConfigHandler.enqueueConfigTasks` and `enqueueDBMigration` to detect duplicate or failed config tasks before inserting.
- `DeleteConfigTaskByDedupKey :execrows` — `DELETE FROM task WHERE task_type = 'config' AND dedup_key = ? AND status IN ('pending','failed')` — Atomic, status-guarded supersede of a stale config task (used by `enqueueDBMigration` so a task claimed between read and delete survives and wins).
- `DiscardEnrichTask :execrows` — `UPDATE ... WHERE id = ? AND status = 'waiting' AND task_type = 'enrich'` — Discards an enrich task by internal `id`; returns rows affected so callers can distinguish a real discard from a no-op. Wrapped by `Store.Discard`.
- `DiscardEnrichTaskByTaskID :execrows` — `UPDATE ... WHERE task_id = ? AND status = 'waiting' AND task_type = 'enrich'` — Discards an enrich task by UUID `task_id`. Kept for the test suite (`TestDiscardEnrichTaskByTaskID`); production discard paths now use `Store.Discard` (handler) and the sweep queries (recovery points).
- `SetEnrichTaskWaiting :execrows` — `UPDATE ... SET status='waiting', error=NULL, completed_at=NULL WHERE task_id = ? AND status = 'discarded' AND task_type = 'enrich'` — Restores a discarded enrich when its consume is retried/reset. Targeted by UUID `task_id` (the consume payload's `on_completed`), race-free.
- `RestoreDiscardedEnrichTasks :execrows` / `RestoreDiscardedEnrichTasksByBatch :execrows` — join-based restore (`UPDATE ... FROM task AS c WHERE e.status='discarded' AND c.status='pending' AND e.task_id = c.payload->>'on_completed'`, batch variant scoped by `c.batch_id`): re-arms every discarded enrich whose consume is pending again. Run inside the same transaction as the retry/reset.
- `DiscardWaitingEnrichesOfFailedConsumes :execrows` / `DiscardWaitingEnrichesOfFailedConsumesGlobal :execrows` — join-based sweep: discards every waiting enrich whose consume is `failed` (batch-scoped or global), copying the parent's error text (`COALESCE(c.error, 'parent consume task failed')`). Enforces "enrich is never waiting while its consume is terminal-failed" at recovery points. Batch variant runs unconditionally in `QuarantineFailedFiles`; the global variant runs inside `Batch.ResetStaleProcessingTasks`.

---

## `handlers/consume.go`

### Struct

- `ConsumeTaskHandler` — `consumer *consumption.Consumer`, `store *task.Store`, `logger *utils.Logger`
  - **Methods**:
    - `NewConsumeTaskHandler(consumer, store, logger) *ConsumeTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals payload (`file_path`, `document_id` UUID, `on_completed` enrich task ID), calls `FileFromPath` + `consumer.Process`. On success, if `on_completed` is set and a document was created, activates the linked enrich task via `Store.SetPending`. On **any** failure (payload unmarshal, empty `file_path`, `FileFromPath`, or `Process`), discards the linked enrich task via `withDiscardAttempt` — if the discard itself fails, the error is appended (`"; additionally failed to discard enrich task <id>: ..."`) so it lands in the task's `error` field.
    - `activateChildEnrich(ctx, parent, onCompleted, documentID)` — Looks up the enrich task by UUID, validates `waiting_for` matches the parent, updates its payload with the document ID, and sets it to `pending`. Works on both `waiting` and `discarded` enrich tasks so a retried consume can reactivate a previously-discarded enrich.
    - `deactivateChildEnrich(ctx, parent, onCompleted, parentErr)` — Looks up the enrich task by UUID, validates `waiting_for` matches the parent, and sets it to `discarded` with the parent error via `Store.Discard`. Only matches `waiting` enrich tasks; 0 rows affected is a benign no-op logged at info level.
    - `withDiscardAttempt(ctx, t, onCompleted, err)` — private: attempts the enrich discard for a failing consume and merges a discard failure into the returned error.
    - `recoverOnCompleted(payload)` — private: lenient re-parse of `on_completed` from a payload that failed strict unmarshalling, so the paired enrich can still be discarded.
    - `DedupKey(payload) string` — Returns file path from payload

## `handlers/enrich.go`

### Struct

- `EnrichTaskHandler` — `enricher *enrichment.Enricher`, `queries *database.Queries`
  - **Methods**:
    - `NewEnrichTaskHandler(enricher, queries) *EnrichTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals `{"document_id":"<uuid>"}`, calls `h.queries.GetDocument` (lookup by UUID), then `enricher.Enrich`
    - `DedupKey(payload) string` — Returns `"enrich:doc:<uuid>"` or empty string

## `configtask` (`internal/configtask/configtask.go`)

The `ConfigTaskHandler` lives in its own package (`internal/configtask/`) to keep the edub binary free of CGo dependencies — unlike `internal/task/handlers/`, which transitively imports CGo-only `internal/tools/adapters` through `internal/consumption`.

### Constants

- `TaskTypeConfig = "config"` — Task type string for config-related async work
- `DedupKeyMigrateDB = "config:migrate-db"` — Dedup key for the `migrate-db` op (at most one migration task pending/processing/failed)
- `DedupKeyMigrateStorage = "config:migrate-storage"` — Dedup key for the `migrate-storage` op (at most one storage migration task pending/processing/failed)

### Structs

- `MigrateDBPayload` — `op`, `config_dir`, destination connection fields (`host`, `port`, `user`, `password`, `database`, `sslmode`). Carried by the `migrate-db` task so the handler can open both databases directly and persist the new values only after the copy succeeds. No storage fields — storage relocation belongs to the `migrate-storage` task.
- `MigrateStoragePayload` — `op`, `config_dir`, old/new storage dirs (`old_storage_dir`, `new_storage_dir`, `old_consumption_dir`, `new_consumption_dir`). Carried by the `migrate-storage` task; the move strategy (`copy`/`move`) is read from `storage.migration_mode` in the on-disk config at task runtime.

### Struct

- `ConfigTaskHandler` — `logger *utils.Logger`
  - **Methods**:
    - `NewConfigTaskHandler(logger) *ConfigTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals `{"config_dir":"...", "op":"tessdata|hugot|migrate-db|migrate-storage", ...}`, loads config from disk, dispatches to `config.DownloadTessdataLanguage`, `config.DownloadHugotModel`, `handleMigrateDB`, or `handleMigrateStorage`
    - `handleMigrateDB(ctx, t)` — Acquires the backup lock on the current DB, waits for in-flight tasks to drain (`database.WaitForTaskDrain`), takes a best-effort gzipped pre-migration snapshot into `backup.path` (keeps the latest 5), dumps the current DB to a temp file (`database.DumpSchemaAndData`), connects to the destination (10s connect timeout), then: skips the copy when the destination already holds data (`document`/`task` row counts), refuses databases with tables but no goose history (`database.ValidateMigrationDestination`), restores the dump statement-by-statement (`database.ExecuteDumpFile`, atomic via the dump's own BEGIN/COMMIT), and finally persists the new connection settings via `config.SaveMap` (also clearing `database.dsn` so the new fields win). Storage paths are not touched — that is the `migrate-storage` task's job.
    - `handleMigrateStorage(ctx, t)` — Loads config from disk (so it connects to the database the current config points at — after a preceding `migrate-db` task, the new database). Acquires the backup lock, drains in-flight tasks, and for each changed directory: rewrites pending consume-task payload paths (`database.RewriteTaskPayloadPaths`, before any file move), moves the storage subdirs (`processed/`, `originals/`, `errors/`, `orphaned/`, `trash/`, `thumbnails/`) and inbox files per `storage.migration_mode` (`copy`: copy-then-delete; `move`: `os.Rename` per entry with copy fallback), rewrites `document.storage_path`/`original_path` and `orphaned_file.file_path` (`database.RewriteStoragePaths`, only when `storage_dir` changed), and persists the new dirs via `config.SaveMap` only after the moves succeed
    - `DedupKey(payload) string` — Returns `"config:tessdata:<lang>"` for tessdata ops, `"config:hugot"` for hugot ops, `"config:migrate-db"` for migrate-db ops, `"config:migrate-storage"` for migrate-storage ops. Used by the idempotent enqueue path in `ConfigHandler.enqueueConfigTasks` to avoid duplicate pending/processing config tasks.

---

## Container Integration (`internal/commands/container.go`)

### CLI (`kushim`)

The `Container` registers all five task types (`"consume"`, `"enrich"`, `"thumbnail"`, `"config"`, `"backup"`)
and creates a `MatcherClient` connected to the Unix socket at `<config_dir>/kushim-hugot.sock`.
The `"backup"` type is handled by `BackupTaskHandler` which acquires the DB-backed backup lock via `AcquireBackupLock`, waits for in-flight tasks to drain, runs the backup, and releases the lock. It implements `DedupKey` returning `backup:<mode>:<date>` (mode parsed from the task payload, default `full`).
The `TagService` and `Enricher` receive the client instead of a direct Hugot reference:

```go
matcherClient := tagmatch.NewMatcherClient(c.socketPath(), tagmatch.MaxMatchBodyBytes(cfg.Enricher.TagMatcher.ReduceTargetWords))
tagSvc, err := service.NewTag(client.Queries, c.logger, matcherClient)
enricher, err := enrichment.NewEnricher(cfg, c.logger, client.Queries, services, matcherClient)
registry.Register("consume", taskhandlers.NewConsumeTaskHandler(consumer, store, c.logger))
registry.Register("enrich", taskhandlers.NewEnrichTaskHandler(enricher, client.Queries, c.logger))
registry.Register("config", configtask.NewConfigTaskHandler(c.logger))
registry.Register("backup", taskhandlers.NewBackupTaskHandler(c.db, client.Queries, func() *config.Config { return c.cfg.Load() }, c.logger))
```

The `BackupTaskHandler` receives a config getter closure used for the backup-root fallback (`backup.path`) and the config path; the backup `mode`/`path`/`keep` come from the task payload, with the path validated against the configured backup roots. The config pool is started alongside
consume/enrich pools when a CLI command runs.

### Server (`edub`)

The `Server` only registers the `"config"` task type. Consume/enrich pools are **not started**
— the server enqueues tasks and the `kushim queue` daemon forks `kushim consume --batch <id>` subprocesses instead:

```go
registry.Register("config", configtask.NewConfigTaskHandler(logger))
```

Only the config pool (1 worker, 5s poll interval) is started on server boot and
stopped on shutdown.

---

## See Also

- [API](api.md) — Task/batch API handlers and response types
- [CLI](cli.md) — Task CLI commands (list, status, retry)
- [Database](database.md) — Task table schema, generated task queries
- [Pipeline](pipeline.md) — Consumer and Enricher invoked by task handlers
- [Config & Utils](config-and-utils.md) — Config setup functions used by ConfigTaskHandler

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
    - `CompleteTask(ctx, id, result) error`
    - `FailTask(ctx, id, errMsg) error`
    - `SetPending(ctx, id, payload) error`
    - `Discard(ctx, id, errMsg) error`

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
    - `Next(ctx, taskType) error` — generic poll loop: claim next pending task, get handler, execute, complete/fail

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
- `Retry(ctx, queries, taskID) error` — Failed tasks only
- `RetryBatchFailed(ctx, queries, batchID string) (int64, error)` — Resets all failed tasks in a batch to pending; returns count of retried tasks. Uses `RetryFailedTasksByBatch` sqlc query with `WHERE batch_id = ? AND status = 'failed'`.
- `CountBatchStatuses(ctx, queries, batchID) BatchCounts` — Counts per status including `waiting` and `discarded`
- `ListBatchSummaries(ctx, queries, filter) ([]BatchCounts, error)` — Lists batch summaries with filtering; uses `ListDistinctBatchIDs` or `ListDistinctBatchIDsByStatus`

---

### SQL queries added

- `RetryFailedTasksByBatch :execrows` — `UPDATE ... WHERE batch_id = ? AND status = 'failed'` — Resets failed batch tasks to pending (batched retry).
- `GetConfigTaskByDedupKey :one` — Returns the most recent config task matching `dedup_key`. Used by `ConfigHandler.enqueueConfigTasks` to detect duplicate or failed config tasks before inserting.

---

## `handlers/consume.go`

### Struct

- `ConsumeTaskHandler` — `consumer *consumption.Consumer`, `store *task.Store`, `logger *utils.Logger`
  - **Methods**:
    - `NewConsumeTaskHandler(consumer, store, logger) *ConsumeTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals payload (`file_path`, `document_id` UUID, `on_completed` enrich task ID), calls `FileFromPath` + `consumer.Process`. On success, if `on_completed` is set and a document was created, activates the linked enrich task via `Store.SetPending`. On failure, if `on_completed` is set, discards the linked enrich task via `Store.Discard`.
    - `activateChildEnrich(ctx, parent, onCompleted, documentID)` — Looks up the enrich task by UUID, validates `waiting_for` matches the parent, updates its payload with the document ID, and sets it to `pending`.
    - `deactivateChildEnrich(ctx, parent, onCompleted, parentErr)` — Looks up the enrich task by UUID, validates `waiting_for` matches the parent, and sets it to `discarded` with the parent error.
    - `DedupKey(payload) string` — Returns file path from payload

## `handlers/enrich.go`

### Struct

- `EnrichTaskHandler` — `enricher *enrichment.Enricher`
  - **Methods**:
    - `NewEnrichTaskHandler(enricher) *EnrichTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals `{"document_id":"<uuid>"}`, calls `enricher.GetDb()` + `GetDocument` (lookup by UUID), then `enricher.Enrich`
    - `DedupKey(payload) string` — Returns `"enrich:doc:<uuid>"` or empty string

## `handlers/config.go`

### Constants

- `TaskTypeConfig = "config"` — Task type string for config-related async work

### Struct

- `ConfigTaskHandler` — `logger *utils.Logger`
  - **Methods**:
    - `NewConfigTaskHandler(logger) *ConfigTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals `{"config_dir":"...", "op":"tessdata|hugot", "lang":"..."}`, loads config from disk, dispatches to `config.DownloadTessdataLanguage` or `config.DownloadHugotModel`
    - `DedupKey(payload) string` — Returns `"config:tessdata:<lang>"` for tessdata ops, `"config:hugot"` for hugot ops. Used by the idempotent enqueue path in `ConfigHandler.enqueueConfigTasks` to avoid duplicate pending/processing config tasks.

---

## Container Integration (`internal/commands/container.go`)

The `Container` (used by CLI commands) and `Server` (used by `edub`) both register
the `"config"` task type alongside `"consume"` and `"enrich"`:

```go
registry.Register("config", taskhandlers.NewConfigTaskHandler(logger))
```

Both create a dedicated pool for config tasks with 1 worker and a 5-second poll
interval. The config pool is started with the other pools on server start and
stopped on shutdown.

---

## See Also

- [API](api.md) — Task/batch API handlers and response types
- [CLI](cli.md) — Task CLI commands (list, status, retry)
- [Database](database.md) — Task table schema, generated task queries
- [Pipeline](pipeline.md) — Consumer and Enricher invoked by task handlers
- [Config & Utils](config-and-utils.md) — Config setup functions used by ConfigTaskHandler

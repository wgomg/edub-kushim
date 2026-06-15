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
- `CountBatchStatuses(ctx, queries, batchID) BatchCounts` — Counts per status including `waiting` and `discarded`
- `ListBatchSummaries(ctx, queries, filter) ([]BatchCounts, error)` — Lists batch summaries with filtering; uses `ListDistinctBatchIDs` or `ListDistinctBatchIDsByStatus`

---

## `handlers/consume.go`

### Struct

- `ConsumeTaskHandler` — `consumer *consumption.Consumer`, `store *task.Store`, `logger *utils.Logger`
  - **Methods**:
    - `NewConsumeTaskHandler(consumer, store, logger) *ConsumeTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals payload (`file_path`, `document_id` UUID, `on_completed` enrich task ID), calls `FileFromPath` + `consumer.Process`. On success, if `on_completed` is set and a document was created, activates the linked enrich task via `Store.SetPending`.
    - `DedupKey(payload) string` — Returns file path from payload

## `handlers/enrich.go`

### Struct

- `EnrichTaskHandler` — `enricher *enrichment.Enricher`
  - **Methods**:
    - `NewEnrichTaskHandler(enricher) *EnrichTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals `{"document_id":"<uuid>"}`, calls `enricher.GetDb()` + `GetDocument` (lookup by UUID), then `enricher.Enrich`
    - `DedupKey(payload) string` — Returns `"enrich:doc:<uuid>"` or empty string

---

## See Also

- [API](api.md) — Task/batch API handlers and response types
- [CLI](cli.md) — Task CLI commands (list, status, retry)
- [Database](database.md) — Task table schema, generated task queries
- [Pipeline](pipeline.md) — Consumer and Enricher invoked by task handlers

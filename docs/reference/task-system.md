# Pool (`internal/pool/`)

## `pool.go`

### Struct

- `Pool` — `logger`, `runner Runner`, `workers int`, `interval time.Duration`, `taskType string`, `stopCh`, `stopOnce`, `wg`, `ctx`, `cancel`
  - **Methods**:
    - `New(logger, runner, workers, interval, taskType) *Pool`
    - `Start(ctx)` — Starts worker goroutines with a cancellable context
    - `Stop(ctx)` — Signals stop, waits for workers with timeout

### Interface

- `Runner` — `Next(ctx, taskType string) error` (implemented by `task.Dispatcher`)

---

# Task System (`internal/task/`)

## `handler.go`

### Interfaces

- `Handler` — `Handle(ctx, database.Task) (json.RawMessage, error)`
- `Dedupable` — `DedupKey(payload json.RawMessage) string`

---

## `dispatcher.go`

### Struct

- `Dispatcher`
  - **Fields**: `consumer *consumption.Consumer`, `enricher *enrichment.Enricher`, `logger *utils.Logger`, `queries *database.Queries`
  - **Methods**:
    - `NewDispatcher(cfg, logger, db, embeddingCache *cache.Cache) (*Dispatcher, error)` — Creates consumer and enricher with cache
    - `Enqueue(ctx, taskType, batchID, payload, taskID string, status ...string) (string, error)` — Validates task type, computes dedup key, inserts row. Supports custom taskID and initial status (e.g., `"waiting"`)
    - `Next(ctx, taskType string) error` — Implements `pool.Runner`: fetch pending task of type via `GetNextPendingTaskOfType` → claim → handle → result. For `"consume"` tasks, extracts `document_db_id` (int64) and `document_id` (UUID) from the result, activates the waiting enrich task (matched via `on_completed`/`waiting_for` pointers) by calling `SetEnrichTaskPending` **before** `CompleteTask`, so the poll loop never sees a gap where the batch looks finished.
    - `setEnrichTaskPending(ctx, id, payload) error` — Updates enrich task status to pending and injects `document_id`

### Functions

- `Dispatcher.getHandler(taskType string) (Handler, error)` — Type switch; supports `"consume"` → `handlers.ConsumeTaskHandler`, `"enrich"` → `handlers.EnrichTaskHandler`

---

## `crud.go`

### Types

- `TaskFilter` — `BatchID`, `Status`, `Limit`, `Offset`
- `BatchFilter` — `Status`, `Limit`, `Offset`
- `BatchCounts` — `BatchID`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`
  - `Total() int64` — Sum of all statuses including Waiting
- `ErrTaskNotFound` — `errors.New("task not found")`

### Functions

- `Get(ctx, queries, taskID) (database.Task, error)` — By UUID task_id
- `ListFiltered(ctx, queries, filter) ([]database.Task, error)` — By batch/status/pagination; handles all combinations of filters
- `Retry(ctx, queries, taskID) error` — Failed tasks only
- `CountBatchStatuses(ctx, queries, batchID) BatchCounts` — Counts per status including `waiting`
- `ListBatchSummaries(ctx, queries, filter) ([]BatchCounts, error)` — Lists batch summaries with filtering; uses `ListDistinctBatchIDs` or `ListDistinctBatchIDsByStatus`

---

## `handlers/consume.go`

### Struct

- `ConsumeTaskHandler` — `consumer *consumption.Consumer`
  - **Methods**:
    - `NewConsumeTaskHandler(consumer) *ConsumeTaskHandler`
    - `Handle(ctx, t) (json.RawMessage, error)` — Unmarshals payload (with `file_path` and `document_id` UUID), calls `FileFromPath` + `consumer.Process`, returns `{"document_db_id":N,"storage_path":"...","document_id":"<uuid>"}`
    - `DedupKey(payload) string` — Returns file path from payload

---

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

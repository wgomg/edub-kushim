# API Layer (`internal/api/`)

## `server.go`

### Struct

- `Server`
  - **Fields**: `httpServer *http.Server`, `logger *utils.Logger`, `addr string`, `pools struct { consume *pool.Pool; enrich *pool.Pool }`
  - **Methods**:
    - `NewServer(cfg config.Config, logger *utils.Logger, db *sql.DB) *Server` — Builds tag cache, creates dispatcher, starts pools
    - `Start() error` — Starts both pools, then HTTP server
    - `Shutdown(ctx context.Context) error` — Stops HTTP server, then both pools
    - `Addr() string`

### Functions

- `registerRoutes(mux *http.ServeMux, logger, queries, engine, dispatcher, cfg)` — Registers all API routes including document file, task/batch routes; uses Go 1.22+ pattern routing (`"GET /api/v1/documents/{id}"`)
- `registerStaticRoutes(mux *http.ServeMux)` — Registers `"GET /{path...}"` handler; tries to serve the requested file from the embedded FS, falls back to `index.html` for client-side SPA routes if the file doesn't exist
- `chainMiddleware(logger *utils.Logger, h http.Handler) http.Handler` — Composes request + parambag middleware
- `requestMiddleware(logger *utils.Logger, next http.Handler) http.Handler` — Adds reqid to context, logs requests
- `parambagMiddleware(next http.Handler) http.Handler` — Injects ParamBag into request context

---

## `handlers/consume.go`

### Struct

- `ConsumeHandler`
  - **Fields**: `cfg *config.Config`, `logger *utils.Logger`, `dispatcher *task.Dispatcher`
  - **Methods**:
    - `NewConsumeHandler(cfg *config.Config, logger *utils.Logger, dispatcher *task.Dispatcher) *ConsumeHandler`
    - `Consume(w, r)` — Scans inbox, enqueues one pair of (consume + enrich) tasks per file via `dispatcher.Enqueue`. Each consume task payload includes a `document_id` UUID for log correlation, plus `file_path`, `file_index`, and `on_completed` pointing to the enrich task ID. Enrich tasks start as `"waiting"` status with `waiting_for` pointer. Returns `202` with `batch_id`, `total_files`, `enqueued`, and `_links.tasks`. Returns `200` JSON `{batch_id:null, total_files:0, message:"no files found"}` when inbox is empty.

---

## `handlers/document.go`

### Struct

- `DocumentHandler`
  - **Fields**: `queries *database.Queries`, `logger *utils.Logger`, `engine *search.Engine`
  - **Methods**:
    - `NewDocumentHandler(queries, logger, engine) *DocumentHandler`
    - `ListDocuments(w, r)` — Supports `sort_by` and `sort_order` query params; response includes `Language`, `DocumentTypeID`
    - `GetDocument(w, r)` — Returns full document with tags (`TagResponse`), people (`PersonResponse`), doc type name
    - `GetDocumentFile(w, r)` — Serves raw PDF via `http.ServeFile`; rejects non-PDF content
    - `SearchDocuments(w, r)` — Returns `FTSDocumentResponse` with enhanced fields

---

## `handlers/health.go`

### Struct

`HealthResponse` — `Status string`, `Version string`, `Time string`

### Function

- `HealthHandler(w, r, logger)` — Writes `{"status":"healthy","version":"0.1.0","time":"..."}`

---

## `handlers/task.go`

### Struct

- `TaskHandler`
  - **Fields**: `queries *database.Queries`, `logger *utils.Logger`
  - **Methods**:
    - `NewTaskHandler(queries, logger) *TaskHandler`
    - `ListTasks(w, r)` — Filters by `batch`, `status`, `limit`, `offset`; includes batch summary when `batch` is set
    - `GetTask(w, r)` — Single task by UUID
    - `ListBatches(w, r)` — Lists all batch summaries with filtering (`status`, `limit`, `offset`)
    - `GetBatchSummary(w, r)` — Counts per status for a single batch (via `{id}`)
    - `GlobalSummary(w, r)` — Global totals: number of batches, total files, per-status counts (including `waiting`), total file size in GB
    - **Helpers**: `buildBatchSummary(ctx, queries, batchID) BatchSummaryResponse`, `taskToResponse(t) TaskResponse`

---

## `types/document.go`

### Structs

- `TagResponse` — `ID int64`, `Name string`
- `PersonResponse` — `ID`, `Name`, `PersonTypeID`, `PersonTypeName`, `PersonTypeDescription`
- `DocumentResponse`
  - **Fields**: `DocumentID string` (UUID), `Title`, `MD5Checksum`, `SHA512Checksum`, `MimeType`, `FileSize`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `CreatedAt`, `ModifiedAt`
- `FTSDocumentResponse`
  - **Fields**: Same as `DocumentResponse` (including `DocumentTypeName`, `Tags`, `People`) plus `Rank float64`, `Snippet string`, `TextContent string`

---

## `types/task.go`

### Structs

- `TaskResponse` — `TaskID`, `BatchID`, `FileName`, `Status`, `DocumentID *int64`, `Error *string`, `CreatedAt`, `StartedAt *string`, `CompletedAt *string`
- `BatchSummaryResponse` — `BatchID`, `Total`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`
- `ListBatchesResponse` — `Batches []BatchSummaryResponse`
- `ListTasksResponse` — `BatchID`, `Summary *BatchSummaryResponse`, `Tasks []TaskResponse`
- `GlobalSummaryResponse` — `TotalBatches`, `TotalFiles`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `TotalSizeGB`

---

## See Also

- [Task System](task-system.md) — Dispatcher and task lifecycle used by the consume handler
- [Pipeline](pipeline.md) — Consumption and enrichment engines triggered via API
- [Database](database.md) — Document and task queries used by handlers
- [Frontend](frontend.md) — SvelteKit SPA that consumes these API endpoints

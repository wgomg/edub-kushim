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
    - `ListDocuments(w, r)` — Supports `sort_by` and `sort_order` query params; response includes tags (`TagResponse`), people (`PersonResponse`), content stats (`PageCount`, `WordCount`, `CharCount`), `Language`, `DocumentTypeID`
    - `GetDocument(w, r)` — Returns full document with tags (`TagResponse`), people (`PersonResponse`), doc type name
    - `GetDocumentFile(w, r)` — Serves raw PDF via `http.ServeFile`; rejects non-PDF content
    - `SearchDocuments(w, r)` — `GET /api/v1/documents/search` — Returns `FTSDocumentResponse` array with enhanced fields
    - `SearchDocumentsStructured(w, r)` — `POST /api/v1/documents/search` — Accepts `search.Filter` JSON body, calls `engine.SearchStructured(ctx, filter)`, returns `SearchResponse` with `results` array and `total` count. Enriches each result with tags and people from DB to avoid N+1.

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
    - `RetryTask(w, r)` — `POST /api/v1/tasks/{id}/retry` — Resets a failed task to pending. Returns `204 No Content`. Returns 404 if task not found, 409 if task is not failed.
    - `ListBatches(w, r)` — Lists all batch summaries with filtering (`status`, `limit`, `offset`)
    - `GetBatchSummary(w, r)` — Counts per status for a single batch (via `{id}`)
    - `RetryBatch(w, r)` — `POST /api/v1/batches/{id}/retry` — Resets all failed tasks in a batch to pending. Returns `200 {"retried": <n>}`. Idempotent (0 retried is valid success).
    - `GlobalSummary(w, r)` — Global totals: number of batches, total files, per-status counts (including `waiting` and `discarded`), total file size in GB
    - **Helpers**: `buildBatchSummary(ctx, queries, batchID) BatchSummaryResponse`, `taskToResponse(t) TaskResponse`

---

## `types/document.go`

### Structs

- `TagResponse` — `ID int64`, `Name string`
- `PersonResponse` — `ID`, `Name`, `NameNative` (original non-Latin script, if any), `PersonTypeID`, `PersonTypeName`, `PersonTypeDescription`
- `DocumentResponse`
  - **Fields**: `ID string` (UUID, JSON `"id"`), `Title`, `MD5Checksum`, `SHA512Checksum`, `MimeType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `CreatedAt`, `ModifiedAt`
- `FTSDocumentResponse`
  - **Fields**: `ID string` (UUID, replaces the old int64 `id`), `Title`, `MD5Checksum`, `SHA512Checksum`, `MimeType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `Rank float64`, `Snippet string`, `TextContent string`

---

## `types/task.go`

### Structs

- `TaskResponse` — `TaskID`, `BatchID`, `TaskType`, `FileName`, `PayloadDocID`, `Status`, `DocumentID *int64`, `Error *string`, `CreatedAt`, `StartedAt *string`, `CompletedAt *string`
- `BatchSummaryResponse` — `BatchID`, `Total`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`
- `ListBatchesResponse` — `Batches []BatchSummaryResponse`
- `ListTasksResponse` — `BatchID`, `Summary *BatchSummaryResponse`, `Tasks []TaskResponse`
- `GlobalSummaryResponse` — `TotalBatches`, `TotalFiles`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`, `TotalSizeGB`

---

---

## `handlers/autocomplete.go`

### Struct

- `AutocompleteHandler`
  - **Fields**: `queries *database.Queries`, `logger *utils.Logger`
  - **Methods**:
    - `NewAutocompleteHandler(queries, logger) *AutocompleteHandler`
    - `ListTags(w, r)` — `GET /api/v1/tags?q=<prefix>&limit=20` — Searches tag names by prefix (uses `SearchTagsByName` sqlc query). Falls back to `ListAllTags` when `q` is empty.
    - `ListPeople(w, r)` — `GET /api/v1/people?q=<prefix>&limit=20` — Searches people names by prefix (uses `SearchPeopleByName` sqlc query). Falls back to `ListAllPeople` when `q` is empty.
    - `ListPeopleTypes(w, r)` — `GET /api/v1/people-types` — Lists all person types from DB
    - `ListDocumentTypes(w, r)` — `GET /api/v1/document-types` — Lists all document types from DB

---

## `handlers/config.go`

### Struct

- `ConfigHandler`
  - **Fields**: `cfg *config.Config`, `queries *database.Queries`, `logger *utils.Logger`, `dispatcher *task.Dispatcher`, `OnBootstrap func(configDir string) (*config.Config, *database.Queries, *task.Dispatcher, error)`
  - **Methods**:
    - `NewConfigHandler(cfg, queries, logger, dispatcher) *ConfigHandler`
    - `SetBootstrap(cfg, queries, dispatcher)` — Sets handler state from bootstrap results. Used by the wizard's auto-resume path to populate the handler after detecting an existing config on startup.
    - `GetConfig(w, r)` — `GET /wizard/config` — Returns user-configurable settings as `ConfigResponse` (app, server, consumer, enricher sections plus available_engines; app includes boolean `initialized`; enricher includes LLM provider tokens). Returns defaults from `DefaultConfig("")` when no config is loaded (wizard not yet bootstrapped), so the frontend always receives a complete config shape.
    - `PutConfig(w, r)` — `PUT /wizard/config` — Two-phase: if `config_dir` is present and no config exists, bootstraps config directory, DB, and skeleton YAML. Otherwise writes config via `SaveMap`, reloads, and enqueues config tasks for missing downloads (tessdata, hugot). Returns `200` or `201` with pending task count and a `missing_tools` array of hard-blocking tool-availability issues.
    - `ConfigStatus(w, r)` — `GET /wizard/config/status` — Returns `ConfigStatusResponse` with `configured` flag, `pending_tasks` count, `failed_tasks` (array of `{task_id, op, lang, error}`), `errors`, plus `tools` (full `[]ExternalTool` availability list) and `missing_tools` (hard-blocking subset).
    - `RetryFailedConfig(w, r)` — `POST /wizard/config/retry` — Retries all failed config tasks. Returns `200 {"retried": <n>}`.

---

## `types/config.go`

### Structs

- `AppConfigResponse` — `Initialized bool` (true when config_dir has been bootstrapped)
- `ServerConfigResponse` — `Host string`, `Port int`
- `ConfigResponse` — `App AppConfigResponse`, `Server ServerConfigResponse`, `Consumer ConsumerConfigResponse`, `Enricher EnricherConfigResponse`, `AvailableEngines map[string][]EngineEntry`
- `ConsumerConfigResponse` — `DeleteOriginal bool`, `Workers int`, `TextExtractor TextExtractorResponse`, `PdfOptimizer PdfOptimizerResponse`, `OCR OCRResponse`
- `TextExtractorResponse` — `Engine string`, `Timeout int`
- `PdfOptimizerResponse` — `Engine string`, `Fallback string`, `Timeout int`
- `OCRResponse` — `Engine string`, `Languages []string`, `DataDir string`, `Timeout int`
- `EnricherConfigResponse` — `Workers int`, `TextReducer TextReducerResponse`, `ContentAnalyzer ContentAnalyzerResponse`, `TagMatcher TagMatcherResponse`
- `TextReducerResponse` — `Engine string`, `Timeout int`, `TargetWords int`
- `ContentAnalyzerResponse` — `Engine string`, `Timeout int`, `Llm LlmProvidersResponse`
- `LlmProvidersResponse` — `OpenAI LlmProviderResponse`, `Anthropic LlmProviderResponse`, `DeepSeek LlmProviderResponse`, `Ollama LlmProviderResponse`
- `LlmProviderResponse` — `BaseURL string`, `Model string`, `Token string`
- `TagMatcherResponse` — `Engine string`, `Timeout int`, `ReduceTargetWords int`, `ChunkSize int`, `Hugot HugotResponse`
- `HugotResponse` — `Model string`, `Backend string`
- `FailedTaskSummary` — `TaskID string`, `Op string`, `Lang string` (omitempty), `Error string`
- `ConfigStatusResponse` — `Configured bool`, `PendingTasks int`, `FailedTasks []FailedTaskSummary` (omitempty), `Errors []string`, `Tools []config.ExternalTool` (full availability list), `MissingTools []config.ExternalTool` (hard-blocking subset)

### Functions

- `ConfigResponseFrom(cfg *config.Config) ConfigResponse` — Maps internal config to the API response, excluding internal/computed fields (model paths, similarity thresholds, etc.). Includes LLM provider tokens, server host/port, and produces the `initialized` boolean in the `app` section.

---

## `handlers/saved_search.go`

### Struct

- `SavedSearchHandler`
  - **Fields**: `queries *database.Queries`, `logger *utils.Logger`
  - **Methods**:
    - `NewSavedSearchHandler(queries, logger) *SavedSearchHandler`
    - `Create(w, r)` — `POST /api/v1/saved-searches` — Accepts `CreateSavedSearchRequest` JSON, inserts into `saved_search` table, returns `{"id": <new_id>}` with `201 Created`
    - `List(w, r)` — `GET /api/v1/saved-searches` — Returns array of `SavedSearchResponse` ordered by `created_at DESC`
    - `Delete(w, r)` — `DELETE /api/v1/saved-searches/{id}` — Parses ID from path, deletes record, returns `204 No Content`

---

## `types/autocomplete.go`

### Structs

- `PersonRefResponse` — `ID int64`, `Name string`
- `DocumentTypeRefResponse` — `ID int64`, `Name string`, `Description string`
- `PeopleTypeRefResponse` — `ID int64`, `Name string`, `Description string`

---

## `types/saved_search.go`

### Structs

- `CreateSavedSearchRequest` — `Name string`, `Filter json.RawMessage`
- `SavedSearchResponse` — `ID int64`, `Name string`, `Filter json.RawMessage`, `CreatedAt string`

---

## `types/document.go` (additions)

### Structs

- `SearchResponse`
  - **Fields**: `Results []FTSDocumentResponse`, `Total int64`
  - Used by `SearchDocumentsStructured` to return both results and total count

---

## Route Registration (`server.go`)

All registered routes:

```go
mux.HandleFunc("GET /health", ...)

mux.HandleFunc("GET /api/v1/documents", docHandler.ListDocuments)
mux.HandleFunc("GET /api/v1/documents/{id}", docHandler.GetDocument)
mux.HandleFunc("GET /api/v1/documents/{id}/file", docHandler.GetDocumentFile)
mux.HandleFunc("GET /api/v1/documents/search", docHandler.SearchDocuments)
mux.HandleFunc("POST /api/v1/documents/search", docHandler.SearchDocumentsStructured)

mux.HandleFunc("GET /api/v1/tags", autocompleteHandler.ListTags)
mux.HandleFunc("GET /api/v1/people", autocompleteHandler.ListPeople)
mux.HandleFunc("GET /api/v1/people-types", autocompleteHandler.ListPeopleTypes)
mux.HandleFunc("GET /api/v1/document-types", autocompleteHandler.ListDocumentTypes)

mux.HandleFunc("POST /api/v1/consume", consumeHandler.Consume)

mux.HandleFunc("GET /wizard/config", configHandler.GetConfig)
mux.HandleFunc("PUT /wizard/config", configHandler.PutConfig)
mux.HandleFunc("GET /wizard/config/status", configHandler.ConfigStatus)
mux.HandleFunc("POST /wizard/config/retry", configHandler.RetryFailedConfig)

mux.HandleFunc("GET /api/v1/tasks", taskHandler.ListTasks)
mux.HandleFunc("GET /api/v1/tasks/{id}", taskHandler.GetTask)
mux.HandleFunc("POST /api/v1/tasks/{id}/retry", taskHandler.RetryTask)
mux.HandleFunc("GET /api/v1/batches", taskHandler.ListBatches)
mux.HandleFunc("GET /api/v1/batches/{id}", taskHandler.GetBatchSummary)
mux.HandleFunc("POST /api/v1/batches/{id}/retry", taskHandler.RetryBatch)
mux.HandleFunc("GET /api/v1/summary", taskHandler.GlobalSummary)

mux.HandleFunc("GET /api/v1/saved-searches", savedSearchHandler.List)
mux.HandleFunc("POST /api/v1/saved-searches", savedSearchHandler.Create)
mux.HandleFunc("DELETE /api/v1/saved-searches/{id}", savedSearchHandler.Delete)
```

---

## See Also

- [Search](search.md) — Search engine architecture and structured search
- [Task System](task-system.md) — Dispatcher and task lifecycle used by the consume handler
- [Pipeline](pipeline.md) — Consumption and enrichment engines triggered via API
- [Database](database.md) — Document and task queries used by handlers
- [Frontend](frontend.md) — SvelteKit SPA that consumes these API endpoints
- [Config & Utils](config-and-utils.md) — Config setup functions and response types

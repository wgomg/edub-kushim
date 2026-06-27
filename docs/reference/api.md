# API Layer (`internal/api/`)

## `server.go`

### Struct

- `Server`
  - **Fields**: `httpServer *http.Server`, `logger *utils.Logger`, `addr string`, `matcherClient *tagmatch.MatcherClient`, `services *types.CrudServices`, `pools struct { config *pool.Pool }`
  - **Methods**:
    - `NewServer(cfg config.Config, logger *utils.Logger, db *sql.DB) *Server` — Creates a `MatcherClient` connected to `kushim-hugot.sock` in the config dir, builds `CrudServices` with `Tag` (wired through `MatcherClient`), `People`, `PeopleType`, `DocumentType` services, creates dispatcher with only the `"config"` task type registered, creates a `Semaphore` for batch concurrency, registers routes
    - `Start() error` — Probes matcher health (startup warning if unreachable), starts config pool, then HTTP server
    - `Shutdown(ctx context.Context) error` — Shuts down HTTP server, config pool, then `services.Close()`
    - `Addr() string`

### Functions

- `probeMatcher()` — Calls `matcherClient.Health()` with 2s timeout. Logs warning and continues if matcher is unreachable; tag CRUD returns `503` and enrich falls back to LLM-only tags.
- `registerRoutes(mux *http.ServeMux, logger, queries, engine, dispatcher, cfg, services, semaphore, workStore)` — Registers all API routes; passes `services` to `NewDocumentHandler`, `NewTagHandler`, `NewPeopleHandler`, and `NewDocumentTypeHandler`. Uses Go 1.22+ pattern routing (`"GET /api/v1/documents/{id}"`).
- `registerStaticRoutes(mux *http.ServeMux)` — Registers `"GET /{path...}"` handler; tries to serve the requested file from the embedded FS, falls back to `index.html` for client-side SPA routes if the file doesn't exist
- `chainMiddleware(logger *utils.Logger, h http.Handler) http.Handler` — Composes request + parambag middleware
- `requestMiddleware(logger *utils.Logger, next http.Handler) http.Handler` — Adds reqid to context, logs requests
- `parambagMiddleware(next http.Handler) http.Handler` — Injects ParamBag into request context

---

## `handlers/consume.go`

### Struct

- `ConsumeHandler`
  - **Fields**: `cfg *config.Config`, `logger *utils.Logger`, `workStore *task.Store`, `queries *database.Queries`, `semaphore *pool.Semaphore`
  - **Methods**:
    - `NewConsumeHandler(cfg, logger, workStore, queries, semaphore) *ConsumeHandler`
    - `Consume(w, r)` — Acquires semaphore slot, scans inbox using `utils.ListFilePaths`, creates a batch, enqueues one pair of (consume + enrich) tasks per file via `workStore.CreateTask`, then **forks a `kushim consume --batch <id>` child process** to handle actual processing. Returns `202` with `batch_id`, `total_files`, `enqueued`, and `_links.tasks`. Returns `429 Too Many Requests` when semaphore slots are exhausted (`server.max_concurrent_batches`). Returns `200` JSON `{batch_id:null, total_files:0, message:"no files found"}` when inbox is empty.
    - `Upload(w, r)` — Acquires semaphore slot, accepts multipart upload (`files` field, repeatable), streams bytes to temp files in the inbox, validates MIME type against `consumer.supported_files`, enqueues tasks, then **forks a `kushim consume --batch <id>` child process**. Returns `202` with `batch_id`, `accepted`, `rejected`, and `_links.tasks`. Returns `413` when body exceeds `server.max_upload_size`. Returns `422` when no supported files are found or required tools are missing.
    - `forkWorker(batchID) error` — Finds the `kushim` binary (PATH or sibling of `edub`), starts it detached with `--batch <id>`, and releases the semaphore slot when the child exits.

---

## `handlers/document.go`

### Struct

- `DocumentHandler`
  - **Fields**: `queries *database.Queries`, `logger *utils.Logger`, `engine *search.Engine`, `services *itypes.CrudServices`
  - **Methods**:
    - `NewDocumentHandler(queries, logger, engine, services) *DocumentHandler`
    - `ListDocuments(w, r)` — Supports `sort_by` and `sort_order` query params; response includes tags (`TagResponse`), people (`PersonResponse`), content stats (`PageCount`, `WordCount`, `CharCount`), `Language`, `DocumentTypeID`
    - `GetDocument(w, r)` — Returns full document with tags (`TagResponse`), people (`PersonResponse`), doc type name
    - `GetDocumentFile(w, r)` — Serves raw PDF via `http.ServeFile`; rejects non-PDF content
    - `SearchDocuments(w, r)` — `GET /api/v1/documents/search` — Returns `FTSDocumentResponse` array with enhanced fields
    - `SearchDocumentsStructured(w, r)` — `POST /api/v1/documents/search` — Accepts `search.Filter` JSON body, calls `engine.SearchStructured(ctx, filter)`, returns `SearchResponse` with `results` array and `total` count. Enriches each result with tags and people from DB to avoid N+1.
    - `UpdateDocument(w, r)` — `PUT /api/v1/documents/{id}` — Accepts `DocumentUpdateRequest` JSON (title, document_type_id, language, text_content). Validates title non-empty, document type exists (via `GetDocumentType`), defaults language to `"und"` when empty. Preserves existing `text_content` when nil. Returns `204 No Content`.
    - `DeleteDocument(w, r)` — `DELETE /api/v1/documents/{id}` — Fetches document to get file paths, calls `DeleteDocument` (single DELETE with cascade + FTS trigger), then best-effort `os.Remove` on original and storage paths. Returns `204 No Content`.
    - `AddDocumentTag(w, r)` — `POST /api/v1/documents/{id}/tags` — Accepts `{tag_id}`, validates document and tag exist via `services.Tag.Get` (maps `KindNotFound` → 404 via `writeServiceError`), calls `AddDocumentTag` (INSERT OR IGNORE). Returns `204 No Content`.
    - `RemoveDocumentTag(w, r)` — `DELETE /api/v1/documents/{id}/tags` — Accepts `{tag_id}`, validates document exists, calls `RemoveDocumentTag`. Returns `204 No Content`.
    - `AddDocumentPeople(w, r)` — `POST /api/v1/documents/{id}/people` — Accepts `{people_id, people_type_id}`, validates document, person, and people type exist, calls `AddDocumentPeople` (INSERT OR IGNORE). Returns `204 No Content`.
    - `RemoveDocumentPeople(w, r)` — `DELETE /api/v1/documents/{id}/people` — Accepts `{people_id, people_type_id}`, validates document exists, calls `RemoveDocumentPeople` (now filters by all three PK columns: document_id, people_id, people_type_id). Returns `204 No Content`.

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
  - **Fields**: `queries *database.Queries`, `logger *utils.Logger`, `cfg *config.Config`
  - **Methods**:
    - `NewTaskHandler(queries, logger, cfg) *TaskHandler`
    - `ListTasks(w, r)` — Filters by `batch`, `status`, `limit`, `offset`; includes batch summary when `batch` is set
    - `GetTask(w, r)` — Single task by UUID
    - `RetryTask(w, r)` — `POST /api/v1/tasks/{id}/retry` — Resets a failed task to pending. Returns `204 No Content`. Returns 404 if task not found, 409 if task is not failed.
    - `ListBatches(w, r)` — Lists all batch summaries with filtering (`status`, `limit`, `offset`)
    - `GetBatchSummary(w, r)` — Counts per status for a single batch (via `{id}`)
    - `RetryBatch(w, r)` — `POST /api/v1/batches/{id}/retry` — Resets all failed tasks in a batch to pending. Returns `200 {"retried": <n>}`. Idempotent (0 retried is valid success).
    - `ResumeBatch(w, r)` — `POST /api/v1/batches/{id}/resume` (formerly `AdoptBatch`). Checks batch ownership via `BatchOwnerState` (returns 409 if locked by a live owner), then **forks `kushim consume --batch <id> --force`** to resume processing. Returns `202 {"resumed": true}`.
    - `CancelBatch(w, r)` — `POST /api/v1/batches/{id}/cancel` — Cancels pending tasks, sends `SIGTERM` to the batch owner process if alive, and releases the batch owner. Returns `200` with `cancelled_pending`, `cancelled_processing`, and `signal_sent` booleans.
    - `GetDashboard(w, r)` — Returns `recent_batches` (top 20) + `activity` (top 30 chronological events from documents, tasks, batches) + `analytics` (language/document-type/tag distributions, missing counts) + `processing_health` (task success rate, avg duration, active/orphaned batches, missing tools count). Activity includes: `event_type`, `title`, `timestamp`, `link`. Processing health queries use a 7-day window and reuses `config.MissingExternalToolErrors` to detect missing tools at request time.
    - `GlobalSummary(w, r)` — Global totals: number of batches, total files, per-status counts (including `waiting` and `discarded`), total file size in GB, MIME type breakdown, daily storage trend, average file size, total pages, total words
    - **Helpers**: `buildBatchSummary(ctx, queries, batchID) BatchSummaryResponse`, `buildDocumentAnalytics(ctx, reqID) *DocumentAnalytics`, `buildProcessingHealth(ctx, reqID) *ProcessingHealth`, `taskToResponse(t) TaskResponse`

---

## `types/document.go`

### Structs

- `TagResponse` — `ID int64`, `Name string`
- `PersonResponse` — `ID`, `Name`, `NameNative` (original non-Latin script, if any), `PersonTypeID`, `PersonTypeName`, `PersonTypeDescription`
- `DocumentResponse`
  - **Fields**: `ID string` (UUID, JSON `"id"`), `Title`, `MD5Checksum`, `SHA512Checksum`, `MimeType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `CreatedAt`, `ModifiedAt`
- `FTSDocumentResponse`
  - **Fields**: `ID string` (UUID, replaces the old int64 `id`), `Title`, `MD5Checksum`, `SHA512Checksum`, `MimeType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `Rank float64`, `Snippet string`, `TextContent string`

### Request Structs (`types/document.go`)

- `DocumentUpdateRequest` — `Title string`, `DocumentTypeID int64`, `Language string`, `TextContent *string` (omitempty)
- `AddDocumentTagRequest` — `TagID int64`
- `RemoveDocumentTagRequest` — `TagID int64`
- `AddDocumentPeopleRequest` — `PeopleID int64`, `PeopleTypeID int64`
- `RemoveDocumentPeopleRequest` — `PeopleID int64`, `PeopleTypeID int64`

---

## `types/task.go`

### Structs

- `TaskResponse` — `TaskID`, `BatchID`, `TaskType`, `FileName`, `PayloadDocID`, `Status`, `DocumentID *int64`, `Error *string`, `CreatedAt`, `StartedAt *string`, `CompletedAt *string`
- `BatchSummaryResponse` — `BatchID`, `Total`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`
- `ListBatchesResponse` — `Batches []BatchSummaryResponse`
- `ListTasksResponse` — `BatchID`, `Summary *BatchSummaryResponse`, `Tasks []TaskResponse`
- `MimeTypeStat` — `MimeType`, `Count`, `TotalBytes`
- `StorageTrendPoint` — `Date`, `DailyCount`, `DailyBytes`, `CumulativeBytes`
- `ActivityEvent` — `EventType`, `Title`, `Timestamp`, `Link`
- `DistributionItem` — `Label string`, `Count int64`
- `DocumentAnalytics` — `LanguageDistribution []DistributionItem`, `DocumentTypeDistribution []DistributionItem`, `TagFrequency []DistributionItem`, `MissingLanguageCount int64`, `MissingTypeCount int64`, `MissingTagsCount int64`
- `ProcessingHealth` — `SuccessRate float64`, `CompletedLast7d int64`, `FailedLast7d int64`, `AvgDurationMs int64`, `ActiveBatches int64`, `OrphanedBatches int64`, `MissingTools int64`
- `DashboardResponse` — `RecentBatches []BatchOverviewItem`, `Activity []ActivityEvent`, `Analytics *DocumentAnalytics`, `ProcessingHealth *ProcessingHealth`
- `GlobalSummaryResponse` — `TotalBatches`, `TotalFiles`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`, `TotalSizeGB`, `MimeTypeBreakdown []MimeTypeStat`, `StorageTrend []StorageTrendPoint`, `AvgFileSizeBytes`, `TotalPages`, `TotalWords`

---

---

## `handlers/tag.go`

### Struct

- `TagHandler`
  - **Fields**: `services *itypes.CrudServices`, `logger *utils.Logger`
  - **Methods**:
    - `NewTagHandler(services, logger) *TagHandler`
    - `List(w, r)` — `GET /api/v1/tags?q=<prefix>&limit=50&offset=0` — With `q`: searches by prefix via `services.Tag.Search`. Without `q`: lists paginated via `services.Tag.List`. Returns bare JSON array of `{id,name}`.
    - `Create(w, r)` — `POST /api/v1/tags` — Accepts `{name}`. Calls `services.Tag.Create(ctx, []string{name})`. Maps status: `Created` → 201 with `{id,name}`, `Conflict` → 409 with existing `{id,name}`, `Invalid` → 400. Returns `503` with `{"error":"matcher unavailable — tag store is offline"}` when the external matcher process is unreachable.
    - `Update(w, r)` — `PUT /api/v1/tags/{id}` — Accepts `{name}`. Calls `services.Tag.Update(ctx, []UpdatePair{{ID: id, Name: name}})`. Maps status: `Updated`/`Noop` → 200 with `{id,name}`, `Conflict` → 409, `NotFound` → 404, `Invalid` → 400. Returns `503` when matcher is unreachable.
    - `Delete(w, r)` — `DELETE /api/v1/tags/{id}` — Calls `services.Tag.Delete(ctx, []int64{id})`. Maps status: `Deleted` → 204, `NotFound` → 404. Returns `503` when matcher is unreachable.

---

## `handlers/people.go`

### Structs

- `PeopleHandler`
  - **Fields**: `services *itypes.CrudServices`, `logger *utils.Logger`
  - **Methods**:
    - `NewPeopleHandler(services, logger) *PeopleHandler`
    - `List(w, r)` — `GET /api/v1/people?q=<prefix>&limit=50&offset=0` — With `q`: searches by prefix. Without `q`: lists paginated. Returns `PersonResponse[]` (with `name_native` if present).
    - `Create(w, r)` — `POST /api/v1/people` — Accepts `{name, name_native}`. `Created` → 201, `Conflict` → 409 with existing `{id,name,name_native}`, `Invalid` → 400.
    - `Update(w, r)` — `PUT /api/v1/people/{id}` — Accepts `{name, name_native}`. `Updated`/`Noop` → 200, `Conflict` → 409, `NotFound` → 404, `Invalid` → 400.
    - `Delete(w, r)` — `DELETE /api/v1/people/{id}` — `Deleted` → 204 (CASCADE removes `document_people` rows), `NotFound` → 404.
    - `ListPeopleTypes(w, r)` — `GET /api/v1/people-types?q=<prefix>&limit=50&offset=0` — With `q`: searches by prefix. Without `q`: lists paginated.
    - `CreatePeopleType(w, r)` — `POST /api/v1/people-types` — Accepts `{name, description}`. `Created` → 201, `Conflict` → 409, `Invalid` → 400.
    - `UpdatePeopleType(w, r)` — `PUT /api/v1/people-types/{id}` — Accepts `{name, description}`. `Updated`/`Noop` → 200, `Conflict` → 409, `NotFound` → 404, `Invalid` → 400.
    - `DeletePeopleType(w, r)` — `DELETE /api/v1/people-types/{id}` — `Deleted` → 204, `DeleteConflict` → 409 `{"error":"in use"}`, `NotFound` → 404.

## `handlers/document_type.go`

### Struct

- `DocumentTypeHandler`
  - **Fields**: `services *itypes.CrudServices`, `logger *utils.Logger`
  - **Methods**:
    - `NewDocumentTypeHandler(services, logger) *DocumentTypeHandler`
    - `List(w, r)` — `GET /api/v1/document-types?q=<prefix>&limit=50&offset=0` — With `q`: searches by prefix. Without `q`: lists paginated.
    - `Create(w, r)` — `POST /api/v1/document-types` — Accepts `{name, description}`. `Created` → 201, `Conflict` → 409, `Invalid` → 400.
    - `Update(w, r)` — `PUT /api/v1/document-types/{id}` — Accepts `{name, description}`. `Updated`/`Noop` → 200, `Conflict` → 409, `NotFound` → 404, `Invalid` → 400.
    - `Delete(w, r)` — `DELETE /api/v1/document-types/{id}` — `Deleted` → 204, `DeleteConflict` → 409 `{"error":"in use"}`, `NotFound` → 404.

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

## `types/tag.go`

### Structs

- `CreateTagRequest` — `Name string`
- `UpdateTagRequest` — `Name string`
- `TagResponse` — `ID int64`, `Name string` (reused from `document.go`)

## `internal/types.go` (package `types`, import path `github.com/wgomg/edub-kushim/internal`)

### Structs

- `CrudServices` — `Tag *service.Tag`, `People *service.People`, `PeopleType *service.PeopleType`, `DocumentType *service.DocumentType`
  - `Close()` — Uses reflection to iterate struct fields; calls `Close()` on every field implementing `io.Closer`. Automatically picks up new services added as fields (none of the new services implement `io.Closer`).

## `types/people.go`

### Structs

- `CreatePersonRequest` — `Name string`, `NameNative string`
- `UpdatePersonRequest` — `Name string`, `NameNative string`
- `CreatePeopleTypeRequest` — `Name string`, `Description string`
- `UpdatePeopleTypeRequest` — `Name string`, `Description string`
- `PeopleTypeResponse` — `ID int64`, `Name string`, `Description string`

## `types/document_type.go`

### Structs

- `CreateDocumentTypeRequest` — `Name string`, `Description string`
- `UpdateDocumentTypeRequest` — `Name string`, `Description string`
- `DocumentTypeResponse` — `ID int64`, `Name string`, `Description string`

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
mux.HandleFunc("PUT /api/v1/documents/{id}", docHandler.UpdateDocument)
mux.HandleFunc("DELETE /api/v1/documents/{id}", docHandler.DeleteDocument)
mux.HandleFunc("POST /api/v1/documents/{id}/tags", docHandler.AddDocumentTag)
mux.HandleFunc("DELETE /api/v1/documents/{id}/tags", docHandler.RemoveDocumentTag)
mux.HandleFunc("POST /api/v1/documents/{id}/people", docHandler.AddDocumentPeople)
mux.HandleFunc("DELETE /api/v1/documents/{id}/people", docHandler.RemoveDocumentPeople)

mux.HandleFunc("GET /api/v1/tags", tagHandler.List)
mux.HandleFunc("POST /api/v1/tags", tagHandler.Create)
mux.HandleFunc("PUT /api/v1/tags/{id}", tagHandler.Update)
mux.HandleFunc("DELETE /api/v1/tags/{id}", tagHandler.Delete)

mux.HandleFunc("GET /api/v1/people", peopleHandler.List)
mux.HandleFunc("POST /api/v1/people", peopleHandler.Create)
mux.HandleFunc("PUT /api/v1/people/{id}", peopleHandler.Update)
mux.HandleFunc("DELETE /api/v1/people/{id}", peopleHandler.Delete)
mux.HandleFunc("GET /api/v1/people-types", peopleHandler.ListPeopleTypes)
mux.HandleFunc("POST /api/v1/people-types", peopleHandler.CreatePeopleType)
mux.HandleFunc("PUT /api/v1/people-types/{id}", peopleHandler.UpdatePeopleType)
mux.HandleFunc("DELETE /api/v1/people-types/{id}", peopleHandler.DeletePeopleType)

mux.HandleFunc("GET /api/v1/document-types", docTypeHandler.List)
mux.HandleFunc("POST /api/v1/document-types", docTypeHandler.Create)
mux.HandleFunc("PUT /api/v1/document-types/{id}", docTypeHandler.Update)
mux.HandleFunc("DELETE /api/v1/document-types/{id}", docTypeHandler.Delete)

mux.HandleFunc("POST /api/v1/consume", consumeHandler.Consume)
mux.HandleFunc("POST /api/v1/consume/upload", consumeHandler.Upload)

mux.HandleFunc("GET /wizard/config", configHandler.GetConfig)
mux.HandleFunc("PUT /wizard/config", configHandler.PutConfig)
mux.HandleFunc("GET /wizard/config/status", configHandler.ConfigStatus)
mux.HandleFunc("POST /wizard/config/retry", configHandler.RetryFailedConfig)

mux.HandleFunc("GET /api/v1/tasks", taskHandler.ListTasks)
mux.HandleFunc("GET /api/v1/tasks/{id}", taskHandler.GetTask)
mux.HandleFunc("POST /api/v1/tasks/{id}/retry", taskHandler.RetryTask)
mux.HandleFunc("GET /api/v1/dashboard", taskHandler.GetDashboard)
mux.HandleFunc("GET /api/v1/batches", taskHandler.ListBatches)
mux.HandleFunc("GET /api/v1/batches/{id}", taskHandler.GetBatchSummary)
mux.HandleFunc("POST /api/v1/batches/{id}/retry", taskHandler.RetryBatch)
mux.HandleFunc("POST /api/v1/batches/{id}/resume", taskHandler.ResumeBatch)
mux.HandleFunc("POST /api/v1/batches/{id}/cancel", taskHandler.CancelBatch)
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

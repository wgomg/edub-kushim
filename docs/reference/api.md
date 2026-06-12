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
  - **Fields**: `ID string` (UUID, JSON `"id"`), `Title`, `MD5Checksum`, `SHA512Checksum`, `MimeType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `CreatedAt`, `ModifiedAt`
- `FTSDocumentResponse`
  - **Fields**: `ID string` (UUID, replaces the old int64 `id`), `Title`, `MD5Checksum`, `SHA512Checksum`, `MimeType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `Rank float64`, `Snippet string`, `TextContent string`

---

## `types/task.go`

### Structs

- `TaskResponse` — `TaskID`, `BatchID`, `TaskType`, `FileName`, `PayloadDocID`, `Status`, `DocumentID *int64`, `Error *string`, `CreatedAt`, `StartedAt *string`, `CompletedAt *string`
- `BatchSummaryResponse` — `BatchID`, `Total`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`
- `ListBatchesResponse` — `Batches []BatchSummaryResponse`
- `ListTasksResponse` — `BatchID`, `Summary *BatchSummaryResponse`, `Tasks []TaskResponse`
- `GlobalSummaryResponse` — `TotalBatches`, `TotalFiles`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `TotalSizeGB`

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

New routes added:

```go
mux.HandleFunc("POST /api/v1/documents/search", docHandler.SearchDocumentsStructured)

mux.HandleFunc("GET /api/v1/tags", autocompleteHandler.ListTags)
mux.HandleFunc("GET /api/v1/people", autocompleteHandler.ListPeople)
mux.HandleFunc("GET /api/v1/people-types", autocompleteHandler.ListPeopleTypes)
mux.HandleFunc("GET /api/v1/document-types", autocompleteHandler.ListDocumentTypes)

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

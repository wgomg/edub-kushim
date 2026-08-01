# API Layer (`internal/api/`)

## `server.go`

### Struct

- `Server`
  - **Fields**: `httpServer *http.Server`, `logger *utils.Logger`, `addr string`, `cfg atomic.Pointer[config.Config]`, `matcherClient *tagmatch.MatcherClient`, `services *types.CrudServices`, `configWatcher *config.Watcher`, `pools struct { config *pool.Pool }`
  - **Methods**:
    - `NewServer(cfg config.Config, logger *utils.Logger, db *sql.DB) *Server` — Creates a `MatcherClient` connected to `kushim-hugot.sock` in the config dir, builds `CrudServices` with `Batch`, `Tag` (wired through `MatcherClient`), `People`, `PeopleType`, `DocumentType`, `User`, `Orphaned`, `ErroredFiles`, `ReEnrich` services, creates dispatcher with only the `"config"` task type registered. Generates a random `SessionSecret` if none is configured (with a warning log). Registers routes and middleware chain (request → auth → parambag).
    - `Start() error` — Probes matcher health (startup warning if unreachable), starts config pool, starts config file watcher (5s interval), then HTTP server
    - `Shutdown(ctx context.Context) error` — Stops config watcher, shuts down HTTP server, config pool, then `services.Close()`
    - `Addr() string`

### Functions

- `probeMatcher()` — Calls `matcherClient.Health()` with 2s timeout. Logs warning and continues if matcher is unreachable; tag CRUD returns `503` and enrich falls back to LLM-only tags.
- `registerRoutes(logger, client, dispatcher, getConfig, onConfigSet, services, workStore) *http.ServeMux` — Creates and returns a `*http.ServeMux` with all API routes registered; internally creates the `search.Engine` from `client`. Uses Go 1.22+ pattern routing (`"GET /api/v1/documents/{id}"`). Auth routes (`POST /api/v1/auth/login`, `POST /api/v1/auth/logout`) are registered before all other routes so they are public (bypassed by `AuthMiddleware`). Orphaned file routes (`/api/v1/orphaned/...`) are registered via `OrphanedHandler` after the document routes. Errored file routes (`/api/v1/errored/...`) are registered via `ErroredHandler` after the orphaned block.
- `registerStaticRoutes(mux *http.ServeMux)` — Registers `"GET /{path...}"` handler; tries to serve the requested file from the embedded FS, falls back to `index.html` for client-side SPA routes if the file doesn't exist
- `chainMiddleware(logger *utils.Logger, getSecret func() string, getAuthEnabled func() bool, validateAPIKey func(ctx context.Context, rawKey string) (*database.User, error), getUserByID func(ctx context.Context, id int64) (*database.User, error), h http.Handler) http.Handler` — Composes request + auth + parambag middleware. The auth middleware skips public paths (`/health`, `GET /wizard/bootstrap`, `/api/v1/auth/*`, non-API paths) and validates Bearer tokens (from `Authorization` header or `edub_token` cookie) on all other routes. Only `GET /wizard/bootstrap` is public among `/wizard/*` paths — the rest require authentication (admin role via `RequireRole`). Tokens with `ek_` prefix are validated via `validateAPIKey` closure (hashes + DB lookup); other tokens are validated as JWT via `auth.ValidateToken`.
- `AuthMiddleware(next http.Handler, getSecret func() string, getAuthEnabled func() bool, validateAPIKey func(ctx context.Context, rawKey string) (*database.User, error), getUserByID func(ctx context.Context, id int64) (*database.User, error)) http.Handler` — Extracts `Authorization: Bearer <token>` header. If the header is absent, falls back to the `edub_token` cookie. If token starts with `ek_`, validates via `validateAPIKey` (hashes key, DB lookup) and injects `userID`, `username`, `role` (from DB), and `authSource="apikey"` into context. Otherwise, validates JWT via `auth.ValidateToken`, then re-fetches the user from DB via `getUserByID` to get the current `role` (not trusting the JWT claim for role). Injects `userID`, `username`, `role`, and `authSource="session"`. Returns 401 JSON for missing/invalid/expired tokens or deleted users, 500 JSON for internal errors. Bypasses auth for public paths (`/health`, `GET /wizard/bootstrap`, `/api/v1/auth/*`, non-API paths). When `auth_enabled` is false, the middleware injects an `admin` identity into context and passes every request through.
- `RequireRole(allowed ...auth.Role) func(http.Handler) http.Handler` — **(`permission.go`)** Middleware factory that returns a handler requiring the request context to contain one of the allowed roles via `auth.RoleKey`. Returns 403 JSON `{"error":"forbidden"}` if role is missing or not in the allowed set. Used to wrap every API route in `registerRoutes`.
- `requestMiddleware(logger *utils.Logger, next http.Handler) http.Handler` — Adds reqid to context, logs requests
- `parambagMiddleware(next http.Handler) http.Handler` — Injects ParamBag into request context

---

## `handlers/consume.go`

### Struct

- `ConsumeHandler`
  - **Fields**: `getConfig func() *config.Config`, `logger *utils.Logger`, `workStore *task.Store`, `queries *database.Queries`, `services *itypes.CrudServices`
  - **Methods**:
    - `NewConsumeHandler(getConfig, logger, workStore, queries, services) *ConsumeHandler`
    - `Consume(w, r)` — Scans inbox using `utils.ListFilePaths`, creates a batch with `status='queued'`, enqueues one pair of (consume + enrich) tasks per file via `workStore.CreateTask`. The `kushim queue` daemon picks up the queued batch for actual processing. Returns `202` with `batch_id`, `total_files`, `enqueued`, and `_links.tasks`. Returns `200` JSON `{batch_id:null, total_files:0, message:"no files found"}` when inbox is empty.
    - `Upload(w, r)` — Accepts multipart upload (`files` field, repeatable), streams bytes to temp files in the inbox, validates MIME type against `consumer.supported_files`, creates a batch with `status='queued'`, enqueues tasks. The `kushim queue` daemon picks up the batch. Returns `202` with `batch_id`, `accepted`, `rejected`, and `_links.tasks`. Returns `413` when body exceeds `server.max_upload_size`. Returns `422` when no supported files are found or required tools are missing.

---

## `handlers/document.go`

### Struct

- `DocumentHandler`
  - **Fields**: `client *database.Client`, `logger *utils.Logger`, `engine *search.Engine`, `services *itypes.CrudServices`, `getConfig func() *config.Config`
  - **Methods**:
    - `NewDocumentHandler(queries, logger, engine, services) *DocumentHandler`
    - `ListDocuments(w, r)` — Supports `sort_by` and `sort_order` query params; response includes tags (`TagResponse`), people (`PersonResponse`), content stats (`PageCount`, `WordCount`, `CharCount`), `Language`, `DocumentTypeID`
    - `GetDocument(w, r)` — Returns full document with tags (`TagResponse`), people (`PersonResponse`), doc type name
    - `GetDocumentFile(w, r)` — Serves the document's processed file via `http.ServeFile` with `Content-Disposition` set to `inline` (or `attachment` when `?download=true`); the filename is sanitized via `sanitizeFilename`
    - `SearchDocuments(w, r)` — `GET /api/v1/documents/search` — Returns `FTSDocumentResponse` array with enhanced fields. The `Snippet` field is HTML-escaped through `sanitizeSnippetHTML` which allows only `<b>`/`</b>` highlighting tags through to prevent XSS.
    - `SearchDocumentsStructured(w, r)` — `POST /api/v1/documents/search` — Accepts `search.Filter` JSON body, calls `engine.SearchStructured(ctx, filter)`, returns `SearchResponse` with `results` array and `total` count. Enriches each result with tags and people from DB to avoid N+1. The `Snippet` field is sanitized identically to `SearchDocuments`.
    - `UpdateDocument(w, r)` — `PUT /api/v1/documents/{id}` — Accepts `DocumentUpdateRequest` JSON (title, document_type_id, language, text_content). Validates title non-empty, document type exists (via `GetDocumentType`), defaults language to `"und"` when empty. Preserves existing `text_content` when nil. Returns `204 No Content`.
    - `DeleteDocument(w, r)` — `DELETE /api/v1/documents/{id}` — Soft-deletes the document: calls `services.Trash.SoftDelete`, which moves files to `<storage>/trash/<document_id>/` (missing files tolerated) and sets `deleted_at`. Returns `204 No Content`; `404` when the document is missing or already in the trash. File-move failures return 500 and leave the document active.
    - `AddDocumentTag(w, r)` — `POST /api/v1/documents/{id}/tags` — Accepts `{tag_id}`, validates document and tag exist via `services.Tag.Get` (maps `KindNotFound` → 404 via `writeServiceError`), calls `AddDocumentTag` (INSERT OR IGNORE). Returns `204 No Content`.
    - `RemoveDocumentTag(w, r)` — `DELETE /api/v1/documents/{id}/tags` — Accepts `{tag_id}`, validates document exists, calls `RemoveDocumentTag`. Returns `204 No Content`.
    - `AddDocumentPeople(w, r)` — `POST /api/v1/documents/{id}/people` — Accepts `{people_id, people_type_id}`, validates document, person, and people type exist, calls `AddDocumentPeople` (INSERT OR IGNORE). Returns `204 No Content`.
    - `RemoveDocumentPeople(w, r)` — `DELETE /api/v1/documents/{id}/people` — Accepts `{people_id, people_type_id}`, validates document exists, calls `RemoveDocumentPeople` (now filters by all three PK columns: document_id, people_id, people_type_id). Returns `204 No Content`.
    - `ReEnrich(w, r)` — `POST /api/v1/documents/{id}/reenrich` — Validates document ID is non-empty, calls `services.ReEnrich.ReEnrich(ctx, documentID)`. Returns `202 Accepted` with `{batch_id, _links: {tasks: ...}}`. Maps document-not-found and dedup-conflict errors via `writeServiceError` (404 / 409).
    - `DownloadDocuments(w, r)` — `POST /api/v1/documents/download` — Accepts `{document_ids: [...]}` JSON (or form-encoded). Streams a ZIP archive with each document's processed file. Validates count against `max_download_files` and total size against `max_download_size_mb`. Returns `200` with `Content-Type: application/zip`, `400` with validation errors.
    - `BatchDeleteDocuments(w, r)` — `POST /api/v1/documents/batch-delete` — Accepts `{document_ids: [...]}` JSON. Soft-deletes each document independently via `services.Trash.SoftDelete`; returns partial failure info (`deleted`/`failed`, `404` for missing/already-trashed IDs). Count validated against `max_batch_delete`.
    - `BatchAssignTags(w, r)` — `POST /api/v1/documents/batch-tags` — Accepts `{document_ids, tag_ids, mode}`. Supports `add` (append) and `replace` (transactional clear+add) modes. Validates all tag IDs exist before modifying any document.
    - `FilterLanguages(w, r)` — `GET /api/v1/filter-languages` — Returns distinct language codes from the document corpus as a JSON string array.
    - `SupportedMimeTypes(w, r)` — `GET /api/v1/supported-mime-types` — Returns the compiled-in supported MIME types as a JSON array of `MimeInfo` objects (mime_type, extension, label).

---

## `handlers/trash.go`

### Struct

- `TrashHandler`
  - **Fields**: `logger *utils.Logger`, `trashSvc *service.TrashService`, `getConfig func() *config.Config`
  - **Methods**:
    - `NewTrashHandler(logger, trashSvc, getConfig) *TrashHandler`
    - `ListTrash(w, r)` — `GET /api/v1/trash` — Paginated trashed documents (`limit` default 50 max 100, `offset`), ordered by `deleted_at` DESC. Returns `TrashListResponse` with `documents`, `total` (via `CountTrash`), `limit`, `offset`.
    - `GetTrashDocument(w, r)` — `GET /api/v1/trash/{id}` — Full metadata for one trashed document (checksums, counts, language, type, timestamps — no filesystem paths). `404` if not in trash.
    - `RestoreDocument(w, r)` — `POST /api/v1/trash/{id}/restore` — Moves files back to `<storage>/originals|processed/` and clears `deleted_at`. `204`; `404` if not in trash; missing files are skipped (partial restore).
    - `PermanentlyDeleteDocument(w, r)` — `DELETE /api/v1/trash/{id}` — Deletes the DB row (junction tables cascade) first, then removes the document's trash dir best-effort. `204`; `404` if not in trash.
    - `PurgeExpired(w, r)` — `POST /api/v1/trash/purge` — Deletes all trashed rows older than `storage.trash.retention_days`, then sweeps orphaned trash dirs. Returns `{"purged": N}`.
    - `BatchPermanentlyDelete(w, r)` — `POST /api/v1/trash/batch-delete` — Per-ID `PermanentlyDelete` with `deleted`/`failed` result; count validated against `max_batch_delete`.
    - `BatchRestore(w, r)` — `POST /api/v1/trash/batch-restore` — Per-ID `RestoreDocument` with `restored`/`failed` result; count validated against `max_batch_delete`.

---

## `handlers/auth.go`

### Struct

- `AuthHandler`
  - **Fields**: `userService *service.User`, `getConfig func() *config.Config`, `logger *utils.Logger`
  - **Methods**:
    - `NewAuthHandler(userService, getConfig, logger) *AuthHandler`
    - `Login(w, r)` — `POST /api/v1/auth/login` — Accepts `{"username", "password"}`. Calls `userService.Authenticate()` (bcrypt compare + DB lookup). On success: generates a 24h JWT via `auth.GenerateToken()` passing `user.Role`, returns `{"token": "...", "user": {"id", "username", "role", "created_at"}}` with 200. Also sets an `edub_token` HttpOnly cookie (`SameSite=Lax`, `Secure` in production, `Max-Age=86400`) so that browser navigations (iframe, `<a href>`) are authenticated without requiring the `Authorization` header. On invalid credentials: returns 401 with generic `"invalid username or password"`. On empty username/password: returns 401 (same generic message to avoid user enumeration). On malformed body: returns 400.
    - `Logout(w, r)` — `POST /api/v1/auth/logout` — Returns 204 No Content. Clears the `edub_token` HttpOnly cookie (`Max-Age=0`) to prevent stale cookie authentication. Client-side localStorage token is also discarded by the frontend.
    - `MeHandler(w, r)` — `GET /api/v1/me` — Reads `auth.UserIDKey` from request context, calls `userService.Get()` to fetch the current user profile, returns `UserResponse` including `role`. Returns 401 if no user ID in context, 404 if user was deleted.

### Internal helpers

- `loginRequest` — `Username string`, `Password string`
- `loginResponse` — `Token string`, `User types.UserResponse`
- `writeUnauthorized(w)` — Writes 401 JSON `{"error": "invalid username or password"}`

---

## `auth_middleware.go`

See `AuthMiddleware` under `server.go` → Functions.

---

## `handlers/health.go`

### Struct

`HealthResponse` — `Status string`, `Version string`, `Time string`

### Function

- `HealthHandler(w, r, logger)` — Writes `{"status":"healthy","version":"2.8.0","time":"..."}`

---

## `handlers/task.go`

### Struct

- `TaskHandler`
  - **Fields**: `queries *database.Queries`, `logger *utils.Logger`, `getConfig func() *config.Config`
  - **Methods**:
    - `NewTaskHandler(queries, logger, getConfig) *TaskHandler`
    - `ListTasks(w, r)` — Filters by `batch`, `status`, `limit`, `offset`; includes batch summary when `batch` is set
    - `GetTask(w, r)` — Single task by UUID
    - `RetryTask(w, r)` — `POST /api/v1/tasks/{id}/retry` — Resets a failed task to pending. Returns `204 No Content`. Returns 404 if task not found, 409 if task is not failed.
    - `ListBatches(w, r)` — Lists all batch summaries with filtering (`status`, `limit`, `offset`)
    - `GetBatchSummary(w, r)` — Counts per status for a single batch (via `{id}`)
    - `RetryBatch(w, r)` — `POST /api/v1/batches/{id}/retry` — Resets all failed tasks in a batch to pending. Returns `200 {"retried": <n>}`. Idempotent (0 retried is valid success).
    - `ResumeBatch(w, r)` — `POST /api/v1/batches/{id}/resume`. Checks batch ownership via `BatchOwnerState` (returns 409 if locked by a live owner), then resets `processing`→`pending` tasks and sets batch status to `queued`. The queue daemon picks it up. Returns `202 {"resumed": true}`.
    - `CancelBatch(w, r)` — `POST /api/v1/batches/{id}/cancel` — Cancels pending tasks, sends `SIGTERM` to the batch owner process if alive, and releases the batch owner. Returns `200` with `cancelled_pending`, `cancelled_processing`, and `signal_sent` booleans.
    - `GetDashboard(w, r)` — Returns `recent_batches` (top 20) + `activity` (top 30 chronological events from documents, tasks, batches) + `analytics` (language/document-type/tag distributions, missing counts) + `processing_health` (task success rate, avg duration, active/orphaned batches, missing tools count) + storage panel fields (`total_batches`, `total_files`, per-status counts, `total_size_gb`, `original_type_breakdown`, `storage_trend`, `avg_file_size_bytes`, `total_pages`, `total_words`). Activity includes: `event_type`, `title`, `timestamp`, `link`. Processing health queries use a 7-day window and reuses `config.MissingExternalToolErrors` to detect missing tools at request time.

    - **Helpers**: `buildBatchSummary(ctx, queries, batchID) BatchSummaryResponse`, `buildDocumentAnalytics(ctx, reqID) *DocumentAnalytics`, `buildProcessingHealth(ctx, reqID) *ProcessingHealth`, `taskToResponse(t) TaskResponse`

---

## `types/document.go`

### Structs

- `TagResponse` — `ID int64`, `Name string`, `DocumentCount int64`
- `PersonResponse` — `ID`, `Name`, `NameNative` (original non-Latin script, if any), `PersonTypeID`, `PersonTypeName`, `PersonTypeDescription`, `DocumentCount int64`
- `DocumentResponse`
  - **Fields**: `ID string` (UUID, JSON `"id"`), `Title`, `MD5Checksum`, `SHA512Checksum`, `OriginalType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `CreatedAt`, `ModifiedAt`
- `FTSDocumentResponse`
  - **Fields**: `ID string` (UUID, replaces the old int64 `id`), `Title`, `MD5Checksum`, `SHA512Checksum`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `Rank float64`, `Snippet string` (HTML-escaped, `<b>` highlighting preserved), `TextContent string`

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
- `BatchSummaryResponse` — `BatchID`, `Status` (queued/processing/completed/failed/cancelled), `Total`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`
- `BatchOverviewItem` — `BatchID`, `Status`, `Source`, `CreatedAt`, `Total`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`, `OwnerState`, `Orphaned`, `DurationMs *int64`
- `BatchCounts` — `Total`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`
- `ListBatchesResponse` — `Batches []BatchSummaryResponse`
- `ListTasksResponse` — `BatchID`, `Summary *BatchSummaryResponse`, `Tasks []TaskResponse`
- `OriginalTypeStat` — `OriginalType`, `Count`, `TotalBytes`
- `StorageTrendPoint` — `Date`, `DailyCount`, `DailyBytes`, `CumulativeBytes`
- `ActivityEvent` — `EventType`, `Title`, `Timestamp`, `Link`
- `DistributionItem` — `Label string`, `Count int64`
- `DocumentAnalytics` — `LanguageDistribution []DistributionItem`, `DocumentTypeDistribution []DistributionItem`, `TagFrequency []DistributionItem`, `MissingLanguageCount int64`, `MissingTypeCount int64`, `MissingTagsCount int64`
- `ProcessingHealth` — `SuccessRate float64`, `CompletedLast7d int64`, `FailedLast7d int64`, `AvgDurationMs int64`, `ActiveBatches int64`, `OrphanedBatches int64`, `MissingTools int64`
- `DashboardResponse` — `RecentBatches []BatchOverviewItem`, `Activity []ActivityEvent`, `Analytics *DocumentAnalytics`, `ProcessingHealth *ProcessingHealth`, `TotalBatches`, `TotalFiles`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`, `TotalSizeGB`, `OriginalTypeBreakdown []OriginalTypeStat`, `StorageTrend []StorageTrendPoint`, `AvgFileSizeBytes`, `TotalPages`, `TotalWords`

---

---

## `handlers/tag.go`

### Struct

- `TagHandler`
  - **Fields**: `services *itypes.CrudServices`, `logger *utils.Logger`
  - **Methods**:
    - `NewTagHandler(services, logger) *TagHandler`
    - `List(w, r)` — `GET /api/v1/tags?q=<prefix>&limit=50&offset=0` — With `q`: searches by prefix via `services.Tag.SearchByNameWithDocumentCount`. Without `q`: lists paginated via `services.Tag.ListWithDocumentCount`. Returns `{results, total}` with `document_count` per tag.
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
    - `List(w, r)` — `GET /api/v1/people?q=<prefix>&limit=50&offset=0` — With `q`: searches by prefix via `SearchByNameWithDocumentCount`. Without `q`: lists paginated via `ListWithDocumentCount`. Returns `PersonListResponse` envelope (`{results: PersonResponse[], total: int64}`) with `document_count` per person.
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
    - `List(w, r)` — `GET /api/v1/document-types?q=<prefix>&limit=50&offset=0` — With `q`: searches by prefix via `SearchByNameWithDocumentCount`. Without `q`: lists all via `ListAllWithDocumentCount`. Returns `DocumentTypeResponse[]` with `document_count` per type.
    - `Create(w, r)` — `POST /api/v1/document-types` — Accepts `{name, description}`. `Created` → 201, `Conflict` → 409, `Invalid` → 400.
    - `Update(w, r)` — `PUT /api/v1/document-types/{id}` — Accepts `{name, description}`. `Updated`/`Noop` → 200, `Conflict` → 409, `NotFound` → 404, `Invalid` → 400.
    - `Delete(w, r)` — `DELETE /api/v1/document-types/{id}` — `Deleted` → 204, `DeleteConflict` → 409 `{"error":"in use"}`, `NotFound` → 404.

---

## `handlers/user.go`

### Struct

- `UserHandler`
  - **Fields**: `services *itypes.CrudServices`, `logger *utils.Logger`
  - **Methods**:
    - `NewUserHandler(services, logger) *UserHandler`
    - `List(w, r)` — `GET /api/v1/users?limit=50&offset=0` — Lists paginated users. Returns `UserListResponse` with `users` array and `total` count. Excludes `password_hash` and `api_key_hash`.
    - `Get(w, r)` — `GET /api/v1/users/{id}` — Returns single `UserResponse`. `KindNotFound` → 404.
    - `Create(w, r)` — `POST /api/v1/users` — Accepts `CreateUserRequest` JSON (`username`, `password`, `role` optional, defaults to `"viewer"`). Validates username non-empty. Password validation is delegated to the service layer (`ValidatePassword`): minimum 12 characters, maximum 128 characters, must contain at least one uppercase letter, lowercase letter, digit, and special character. Returns `400` via `writeServiceError` on validation failure. Bcrypt hashes password on creation. Passes `req.Role` to `service.User.Create()`. `KindConflict` → 409 `{"error":"username already exists"}`. Returns `201` with `UserResponse`.
    - `Update(w, r)` — `PUT /api/v1/users/{id}` — Accepts `UpdateUserRequest` JSON (`username`, `password` optional, `role` optional). Validates username non-empty. Same service-layer password validation when password is provided; empty password skips validation (keep current password). Bcrypts only when password provided, writes username + password_hash in a single `UPDATE`. If `role` is non-empty and different from current value, updates role via the same service call (`service.User.Update` now handles both credential and role changes). `KindNotFound` → 404, `KindConflict` → 409. Returns `200` with `UserResponse`.
    - `Delete(w, r)` — `DELETE /api/v1/users/{id}` — Pre-checks existence via `Get`, then deletes. `KindNotFound` → 404. Returns `204 No Content`.

---

## `handlers/api_key.go`

### Struct

- `APIKeyHandler`
  - **Fields**: `userService *service.User`, `logger *utils.Logger`
  - **Methods**:
    - `NewAPIKeyHandler(userService, logger) *APIKeyHandler`
    - `GenerateKey(w, r)` — `POST /api/v1/users/{id}/api-key` — Generates a new `ek_`-prefixed API key. Hash (SHA-256), prefix (`ek_` + 9 hex chars), and timestamp are stored in DB. Returns `201` with `{api_key, prefix, message}`. Returns `403` Forbidden for mismatched caller ID. `KindNotFound` → 404.
    - `RevokeKey(w, r)` — `DELETE /api/v1/users/{id}/api-key` — Sets `api_key_hash`, `api_key_prefix`, `api_key_created_at` to NULL. Returns `204 No Content`. Returns `403` Forbidden for mismatched caller ID. `KindNotFound` → 404.
    - `RotateKey(w, r)` — `PUT /api/v1/users/{id}/api-key` — Overwrites existing key with a new one (same as Generate). Returns `200` with `{api_key, prefix, message}`. Returns `403` Forbidden for mismatched caller ID. `KindNotFound` → 404.
    - `GetKeyStatus(w, r)` — `GET /api/v1/users/{id}/api-key` — Returns `200` with `{has_api_key, api_key_prefix, api_key_created_at}`. Does not return the raw key. Returns `403` Forbidden for mismatched caller ID. `KindNotFound` → 404.
    - `MeGenerateKey(w, r)` — `POST /api/v1/me/api-key` — Self-service variant of `GenerateKey`. Reads caller ID from context instead of path param. Returns `201`. Returns `401` if no caller ID (unauthenticated).
    - `MeRevokeKey(w, r)` — `DELETE /api/v1/me/api-key` — Self-service variant of `RevokeKey`. Reads caller ID from context. Returns `204`.
    - `MeRotateKey(w, r)` — `PUT /api/v1/me/api-key` — Self-service variant of `RotateKey`. Reads caller ID from context. Returns `200`.
    - `MeGetKeyStatus(w, r)` — `GET /api/v1/me/api-key` — Self-service variant of `GetKeyStatus`. Reads caller ID from context. Returns `200`.

---

## Config Handler (`handlers/config.go`)

### Struct

- `ConfigHandler`
  - **Fields**: `getConfig func() *config.Config`, `onConfigSet func(*config.Config)`, `queries *database.Queries`, `logger *utils.Logger`, `dispatcher *task.Dispatcher`, `OnBootstrap func(configDir string) (*config.Config, *database.Queries, *task.Dispatcher, error)`
  - **Methods**:
    - `NewConfigHandler(getConfig, onConfigSet, queries, logger, dispatcher) *ConfigHandler` — `getConfig` and `onConfigSet` are closures wrapping a shared `atomic.Pointer[config.Config]`. The handler never owns its own pointer; config flows via these closures, allowing the file watcher to atomically update the shared state without handler coordination.
    - `SetServices(client, dispatcher)` — Sets `client.Queries`, creates CrudServices with `Batch` and `User` services, sets dispatcher on an already-initialized handler. Used by the wizard's auto-resume path after `onConfigSet` stores the bootstrapped config.
    - `Bootstrap(w, r)` — `GET /wizard/bootstrap` — Public endpoint (no auth required). Returns only non-sensitive fields: `auth_enabled` (bool) and `missing_tools` (array of `ExternalTool`). The SPA calls this before rendering the login screen to determine whether auth is enabled and whether any external tools are missing. This is the only `/wizard/*` route reachable without authentication — all other wizard routes are admin-protected.
    - `GetConfig(w, r)` — `GET /wizard/config` — Returns user-configurable settings as `ConfigResponse` (app, server, consumer, enricher sections plus `available_engines` and `available_file_types`; app includes boolean `initialized`; enricher includes LLM provider tokens). Returns defaults from `DefaultConfig("")` when no config is loaded (wizard not yet bootstrapped), so the frontend always receives a complete config shape. Requires admin role.
    - `PutConfig(w, r)` — `PUT /wizard/config` — Two-phase: if `config_dir` is present and no config exists, bootstraps config directory, DB, and skeleton YAML. Otherwise writes config via `SaveMap`, reloads, and enqueues config tasks for missing downloads (tessdata, hugot). Returns `200` or `201` with pending task count and a `missing_tools` array of hard-blocking tool-availability issues. Requires admin role.
    - `ConfigStatus(w, r)` — `GET /wizard/config/status` — Returns `ConfigStatusResponse` with `configured` flag, `pending_tasks` count, `failed_tasks` (array of `{task_id, op, lang, error}`), `errors`, plus `tools` (full `[]ExternalTool` availability list) and `missing_tools` (hard-blocking subset). Requires admin role.
    - `RetryFailedConfig(w, r)` — `POST /wizard/config/retry` — Retries all failed config tasks. Returns `200 {"retried": <n>}`. Requires admin role.
    - `CreateAdminUser(w, r)` — `POST /wizard/admin-user` — Creates initial admin user. Accepts `CreateUserRequest` JSON body, validates username (non-empty, stripped), calls `User.Create`. Returns `201` with `UserResponse` on success, `409` with `{"error":"username already exists"}` on duplicate, `400` on validation error.

---

## `types/config.go`

### Structs

- `AppConfigResponse` — `Initialized bool` (true when config_dir has been bootstrapped)
- `ServerConfigResponse` — `Host string`, `Port int`
- `StorageConfigResponse` — `ConsumptionDir string` (inbox path), `StorageDir string` (processed document path)
- `DatabaseConfigResponse` — `Path string` (not used with PostgreSQL)
- `ConfigResponse` — `App AppConfigResponse`, `Server ServerConfigResponse`, `Storage StorageConfigResponse`, `Database DatabaseConfigResponse`, `Consumer ConsumerConfigResponse`, `Enricher EnricherConfigResponse`, `Backup BackupConfigResponse`, `AvailableEngines map[string][]EngineEntry`, `AvailableFileTypes []AvailableFileType`
- `AvailableFileType` — `MimeType string`, `Label string`, `Extensions []string` (canonical + aliases, e.g. TIFF → `.tiff`, `.tif`), `Required bool` (PDF only) — drives the supported-file-types checkboxes in both UIs
- `ConsumerConfigResponse` — `SupportedFiles []string`, `Workers int`, `MaxFilesPerBatch int`, `Converter DocxOdtConverterResponse`, `TextExtractor TextExtractorResponse`, `PdfOptimizer PdfOptimizerResponse`, `OCR OCRResponse`, `Polling PollingConfigResponse`, `Reclaim ReclaimConfigResponse`
- `DocxOdtConverterResponse` — `Enabled bool`, `Binary string`, `Timeout int`
- `TextExtractorResponse` — `Engine string`, `Timeout int`
- `PdfOptimizerResponse` — `Engine string`, `Fallback string`, `Timeout int`
- `OCRResponse` — `Engine string`, `Languages []string`, `DataDir string`, `Timeout int`
- `EnricherConfigResponse` — `Workers int`, `TextReducer TextReducerResponse`, `ContentAnalyzer ContentAnalyzerResponse`, `TagMatcher TagMatcherResponse`
- `TextReducerResponse` — `Engine string`, `Timeout int`, `TargetWords int`
- `ContentAnalyzerResponse` — `Enabled bool`, `Timeout int`, `Llm LlmConfigResponse`, `PromptTemplate string`
- `LlmConfigResponse` — `Adapter string`, `Provider string`, `Model string`, `Token string`, `Reasoning bool`, `ReasoningEffort string`, `Temperature float64`
- `LlmModelEntry` — `ID string`, `Capabilities` (SupportsReasoning, ReasoningEfforts, MaxInputTokens, MaxOutputTokens, SupportsTemperature, SupportsResponseSchema)
- `LlmModelsResponse` — `Adapters map[string][]string`, `Providers map[string][]LlmModelEntry`
- `DocTypeRefinementResponse` — `Enabled bool`, `HeadWords int`, `TailWords int`
- `TagMatcherResponse` — `Engine string`, `Timeout int`, `ReduceTargetWords int`, `ChunkSize int`, `Hugot HugotResponse`
- `HugotResponse` — `Model string`, `Backend string`
- `FailedTaskSummary` — `TaskID string`, `Op string`, `Lang string` (omitempty), `Error string`
- `BootstrapResponse` — `AuthEnabled bool`, `MissingTools []config.ExternalTool` — Returned by `GET /wizard/bootstrap`. Contains only non-sensitive fields so the SPA can render the login screen without exposing LLM tokens or DB connection details.
- `ConfigStatusResponse` — `Configured bool`, `PendingTasks int`, `FailedTasks []FailedTaskSummary` (omitempty), `Errors []string`, `Tools []config.ExternalTool` (full availability list), `MissingTools []config.ExternalTool` (hard-blocking subset)

### Functions

- `ConfigResponseFrom(cfg *config.Config) ConfigResponse` — Maps internal config to the API response, excluding internal/computed fields (model paths, similarity thresholds, etc.). Includes LLM adapter/provider/model config (flat structure), server host/port, `consumer.supported_files` + `consumer.converter`, and produces the `initialized` boolean in the `app` section plus the `available_file_types` list (derived from `internal/mime` via `mime.ExtensionsFor`).

---

## `handlers/orphaned.go`

### Struct

- `OrphanedHandler`
  - **Fields**: `svc *service.Orphaned`, `logger *utils.Logger`
  - **Methods**:
    - `NewOrphanedHandler(svc, logger) *OrphanedHandler`
    - `ListOrphaned(w, r)` — `GET /api/v1/orphaned` — Lists all pending orphaned file records as JSON array. Returns `200`.
    - `ScanOrphaned(w, r)` — `POST /api/v1/orphaned/scan` — Walks `originals/` and `processed/` dirs, quarantines files without matching DB records. Returns `200 {"quarantined": <n>}`.
    - `DeleteOrphaned(w, r)` — `DELETE /api/v1/orphaned/{id}` — Removes file from quarantine + soft-deletes DB record. Returns `204`.
    - `RestoreOrphaned(w, r)` — `POST /api/v1/orphaned/{id}/restore` — Copies uuid-named files to inbox, calculates MD5, creates matched consume+enrich task pair with linked `on_completed`/`waiting_for` cross-references and dedup key, then creates batch record with `status='queued'` (queue daemon picks it up). Returns `202`. Rejects dbid-named files and existing UUID collisions.
    - `MoveToInbox(w, r)` — `POST /api/v1/orphaned/{id}/move-to-inbox` — Copies any orphan to inbox without creating a consume task (the normal inbox scan will pick it up). Returns `202`.
    - `DeleteAllOrphaned(w, r)` — `POST /api/v1/orphaned/delete-all` — Removes all pending files + bulk marks deleted. Returns `200 {"deleted": <n>}`.
    - `MoveAllToInbox(w, r)` — `POST /api/v1/orphaned/move-to-inbox-all` — Moves all pending orphans to inbox. Returns `200 {"moved": <n>}`.

---

## `handlers/errored.go`

### Struct

- `ErroredHandler`
  - **Fields**: `svc *service.ErroredFiles`, `logger *utils.Logger`
  - **Methods**:
    - `NewErroredHandler(svc, logger) *ErroredHandler`
    - `ListErrored(w, r)` — `GET /api/v1/errored` — Lists all errored files from `<storageDir>/errors/` and `<storageDir>/errors/duplicated/` as JSON array with `name`, `subdir`, `size`, `original_type`, `modified_at`. Returns `200`.
    - `DownloadErrored(w, r)` — `GET /api/v1/errored/download?subdir=...&file=...` — Serves an errored file as attachment. Validates path via `GetPath` to prevent traversal. Returns `200` with file, `400` on missing params, `404` if file not found.
    - `DeleteErrored(w, r)` — `DELETE /api/v1/errored?subdir=...&file=...` — Deletes a single errored file from disk. Returns `204`. Returns `400` on missing params.
    - `DeleteAllErrored(w, r)` — `POST /api/v1/errored/delete-all` — Deletes all errored files from both dirs. Returns `200 {"deleted": <n>}`.

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

## `handlers/logs.go`

### Struct

- `LogsHandler`
  - **Fields**: `getConfig func() *config.Config`, `logger *utils.Logger`
  - **Methods**:
    - `NewLogsHandler(getConfig, logger) *LogsHandler`
    - `ListLogs(w, r)` — `GET /api/v1/logs/{name}` — Reads log files from `{configDir}/logs/{name}.log`. Whitelist restricts `name` to `kushim`, `edub`, `hugot`, `queue` (path traversal prevention). Returns `{"lines": [...]}` JSON. Supports `?lines=N` query param (default 500, floor 100, ceiling 5000). Multi-line entries (e.g. LLM prompt dumps) are merged by timestamp prefix detection so they appear as a single entry. On files > 2 MiB, seeks to end and reads tail chunk. Returns 404 for invalid name or missing file, 500 on read errors.

---

## `types/tag.go`

### Structs

- `CreateTagRequest` — `Name string`
- `UpdateTagRequest` — `Name string`
- `TagResponse` — `ID int64`, `Name string` (reused from `document.go`)

## `internal/types.go` (package `types`, import path `github.com/wgomg/edub-kushim/internal`)

### Structs

- `CrudServices` — `Batch *service.Batch`, `Tag *service.Tag`, `People *service.People`, `PeopleType *service.PeopleType`, `DocumentType *service.DocumentType`, `User *service.User`, `Orphaned *service.Orphaned`, `ErroredFiles *service.ErroredFiles`, `ReEnrich *service.ReEnrich`
  - `Close()` — Uses reflection to iterate struct fields; calls `Close()` on every field implementing `io.Closer`. Automatically picks up new services added as fields. `Orphaned` and `ErroredFiles` do not implement `io.Closer` and are skipped silently.

## `types/user.go`

### Structs

- `CreateUserRequest` — `Username string`, `Password string`, `Role string` (omitempty — defaults to `"viewer"`)
- `UpdateUserRequest` — `Username string`, `Password string` (omitempty — leave blank to keep current password), `Role string` (omitempty — leave blank to keep current role)
- `UserResponse` — `ID int64`, `Username string`, `Role string`, `CreatedAt string` (RFC 3339), `HasAPIKey bool`, `APIKeyPrefix *string` (omitempty), `APIKeyCreatedAt *string` (omitempty). Excludes `password_hash` and `api_key_hash`.
- `UserListResponse` — `Users []UserResponse`, `Total int64`
- `CreateAPIKeyResponse` — `APIKey string` (raw key, returned only at creation), `Prefix string` (display-safe prefix `ek_` + first 9 hex chars), `Message string`
- `APIKeyStatusResponse` — `HasAPIKey bool`, `APIKeyPrefix *string` (omitempty), `APIKeyCreatedAt *string` (omitempty)

## `types/people.go`

### Structs

- `CreatePersonRequest` — `Name string`, `NameNative string`
- `UpdatePersonRequest` — `Name string`, `NameNative string`
- `PersonResponse` — `ID int64`, `Name string`, `NameNative string`, `PersonTypeID int64`, `PersonTypeName string`, `PersonTypeDescription string`, `DocumentCount int64`
- `PersonListResponse` — `Results []PersonResponse`, `Total int64`
- `CreatePeopleTypeRequest` — `Name string`, `Description string`
- `UpdatePeopleTypeRequest` — `Name string`, `Description string`
- `PeopleTypeResponse` — `ID int64`, `Name string`, `Description string`

## `types/document_type.go`

### Structs

- `CreateDocumentTypeRequest` — `Name string`, `Description string`
- `UpdateDocumentTypeRequest` — `Name string`, `Description string`
- `DocumentTypeResponse` — `ID int64`, `Name string`, `Description string`, `DocumentCount int64`

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

All registered routes, with their role requirements:

```go
// Public routes (bypassed by AuthMiddleware):
mux.HandleFunc("GET /health", ...)
mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
mux.HandleFunc("POST /api/v1/auth/logout", authHandler.Logout)
mux.HandleFunc("GET /wizard/bootstrap", configHandler.Bootstrap)  // only non-sensitive fields

// LLM model discovery (viewer + public):
mux.HandleFunc("GET /api/v1/llm/models", llmHandler.ListModels)  // viewer role

// viewer (read-only):
viewer := []auth.Role{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin}
mux.Handle("GET /api/v1/documents", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/documents/{id}", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/documents/{id}/file", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/documents/search", RequireRole(viewer...)(...))
mux.Handle("POST /api/v1/documents/search", RequireRole(viewer...)(...))
mux.Handle("POST /api/v1/documents/download", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/filter-languages", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/supported-mime-types", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/tags", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/people", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/people-types", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/document-types", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/tasks", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/tasks/{id}", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/dashboard", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/batches", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/batches/{id}", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/saved-searches", RequireRole(viewer...)(...))

// Self-service (viewer — any authenticated user):
mux.Handle("GET /api/v1/me", RequireRole(viewer...)(...))
mux.Handle("POST /api/v1/me/api-key", RequireRole(viewer...)(...))
mux.Handle("DELETE /api/v1/me/api-key", RequireRole(viewer...)(...))
mux.Handle("PUT /api/v1/me/api-key", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/me/api-key", RequireRole(viewer...)(...))

// editor (all viewer + mutations):
editor := []auth.Role{auth.RoleEditor, auth.RoleAdmin}
mux.Handle("PUT /api/v1/documents/{id}", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/documents/{id}", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/documents/{id}/tags", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/documents/{id}/tags", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/documents/{id}/people", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/documents/{id}/people", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/documents/{id}/reenrich", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/documents/batch-delete", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/documents/batch-tags", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/tags", RequireRole(editor...)(...))
mux.Handle("PUT /api/v1/tags/{id}", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/tags/{id}", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/people", RequireRole(editor...)(...))
mux.Handle("PUT /api/v1/people/{id}", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/people/{id}", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/people-types", RequireRole(editor...)(...))
mux.Handle("PUT /api/v1/people-types/{id}", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/people-types/{id}", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/document-types", RequireRole(editor...)(...))
mux.Handle("PUT /api/v1/document-types/{id}", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/document-types/{id}", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/consume", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/consume/upload", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/tasks/{id}/retry", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/batches/{id}/resume", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/batches/{id}/cancel", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/batches/{id}/retry", RequireRole(editor...)(...))
mux.Handle("GET /api/v1/orphaned", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/orphaned/scan", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/orphaned/{id}", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/orphaned/{id}/restore", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/orphaned/{id}/move-to-inbox", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/orphaned/delete-all", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/orphaned/move-to-inbox-all", RequireRole(editor...)(...))
mux.Handle("GET /api/v1/errored", RequireRole(editor...)(...))
mux.Handle("GET /api/v1/errored/download", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/errored", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/errored/delete-all", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/saved-searches", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/saved-searches/{id}", RequireRole(editor...)(...))

// Trash (viewer reads, editor mutations):
mux.Handle("GET /api/v1/trash", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/trash/{id}", RequireRole(viewer...)(...))
mux.Handle("POST /api/v1/trash/{id}/restore", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/trash/{id}", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/trash/batch-delete", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/trash/batch-restore", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/trash/purge", RequireRole(editor...)(...))

// admin (all editor + user management + wizard config + logs):
admin := []auth.Role{auth.RoleAdmin}
mux.Handle("GET /api/v1/users", RequireRole(admin...)(...))
mux.Handle("GET /api/v1/users/{id}", RequireRole(admin...)(...))
mux.Handle("POST /api/v1/users", RequireRole(admin...)(...))
mux.Handle("PUT /api/v1/users/{id}", RequireRole(admin...)(...))
mux.Handle("DELETE /api/v1/users/{id}", RequireRole(admin...)(...))
mux.Handle("POST /api/v1/users/{id}/api-key", RequireRole(admin...)(...))
mux.Handle("DELETE /api/v1/users/{id}/api-key", RequireRole(admin...)(...))
mux.Handle("PUT /api/v1/users/{id}/api-key", RequireRole(admin...)(...))
mux.Handle("GET /api/v1/users/{id}/api-key", RequireRole(admin...)(...))
mux.Handle("GET /api/v1/logs/{name}", RequireRole(admin...)(...))

// Wizard config (admin only):
mux.Handle("GET /wizard/config", RequireRole(admin...)(configHandler.GetConfig))
mux.Handle("PUT /wizard/config", RequireRole(admin...)(configHandler.PutConfig))
mux.Handle("GET /wizard/config/status", RequireRole(admin...)(configHandler.ConfigStatus))
mux.Handle("POST /wizard/config/retry", RequireRole(admin...)(configHandler.RetryFailedConfig))
```

---

## See Also

- [Search](search.md) — Search engine architecture and structured search
- [Task System](task-system.md) — Dispatcher and task lifecycle used by the consume handler
- [Pipeline](pipeline.md) — Consumption and enrichment engines triggered via API
- [Database](database.md) — Document and task queries used by handlers
- [Frontend](frontend.md) — SvelteKit SPA that consumes these API endpoints
- [Config & Utils](config-and-utils.md) — Config setup functions and response types

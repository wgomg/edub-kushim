# API Layer (`internal/api/`)

## `server.go`

### Struct

- `Server`
  - **Fields**: `httpServer *http.Server`, `logger *log.Logger`, `addr string`, `cfg atomic.Pointer[config.Config]`, `matcherClient *matcher.MatcherClient`, `services *service.CrudServices`, `tag *curation.Tag`, `tagIdentity *curation.TagIdentity`, `tagAudit *curation.TagAudit`, `configWatcher *config.Watcher`, `db *sql.DB`, `client *database.Client`
  - **Methods**:
    - `NewServer(cfg config.Config, logger *log.Logger, db *sql.DB) *Server` — Creates a `MatcherClient` connected to `kushim-hugot.sock` in the config dir, builds `CrudServices` with `Batch`, `People`, `PeopleType`, `DocumentType`, `User`, `Orphaned`, `ErroredFiles`, `ReEnrich`, `Trash` services plus the curation tag services (`Tag` wired through `MatcherClient`, `TagIdentity`, `TagAudit`), creates the task type catalog and enqueuer with the dedup hooks of the types its handlers enqueue (`consume`, `enrich`, `thumbbackfill`, `migrate-db`, `migrate-storage`, `tagidentity`, `tagaudit`). Generates a random `SessionSecret` if none is configured (with a warning log). Registers routes and middleware chain (request → auth → parambag). edub never claims a batch — the `kushim queue` daemon forks the batches' owning task CLIs.
    - `Start() error` — Probes matcher health (startup warning if unreachable), starts config file watcher (5s interval), then HTTP server
    - `Shutdown(ctx context.Context) error` — Stops config watcher, shuts down the HTTP server, closes the tag service (embedder), then `services.Close()`
    - `Addr() string`

### Functions

- `probeMatcher()` — Calls `matcherClient.Health()` with 2s timeout. Logs warning and continues if matcher is unreachable; tag CRUD is unaffected (embedding-store errors are logged and swallowed) and enrich falls back to LLM-only tags.
- `registerRoutes(logger, client, enqueuer, getConfig, onConfigSet, services, tag, tagIdentity, tagAudit) *http.ServeMux` — Creates and returns a `*http.ServeMux` with all API routes registered; internally creates the `search.Engine` from `client`. Uses Go 1.22+ pattern routing (`"GET /api/v1/documents/{id}"`). Auth routes (`POST /api/v1/auth/login`, `POST /api/v1/auth/logout`) are registered before all other routes so they are public (bypassed by `AuthMiddleware`). Orphaned file routes (`/api/v1/orphaned/...`) are registered via `OrphanedHandler` after the document routes. Errored file routes (`/api/v1/errored/...`) are registered via `ErroredHandler` after the orphaned block. The read-only `GET /api/v1/tags/reconciliation/report` is registered viewer-gated and served by `TaskHandler.GetTagReconcileReport`; the read-only `GET /api/v1/tags/siblings` is registered viewer-gated and served by `TagHandler.ListSiblings`; the provisional-tag queue routes — `GET /api/v1/tags/provisional` (viewer-gated), `POST /api/v1/tags/{id}/promote` and `POST /api/v1/tags/{id}/demote` (editor-gated) — are served by `TagHandler`.
- Static serving goes through the shared `handlers.RegisterStaticRoutes(mux, fsys)` (`internal/api/handlers/static.go`), called here with `static.WebFS()` and by the wizard with `WebFS()`; it registers the `"GET /{path...}"` handler, serves the requested file from the given FS, and falls back to `index.html` for client-side SPA routes if the file doesn't exist
- The middleware chain is composed inline at its single call site in `rebuild`: `requestMiddleware(logger, AuthMiddleware(parambagMiddleware(mux), getSecret, getAuthEnabled, validateAPIKey, getUserByID))`. The auth middleware skips public paths (`/health`, `GET /wizard/bootstrap`, `/api/v1/auth/*`, non-API paths) and validates Bearer tokens (from `Authorization` header or `edub_token` cookie) on all other routes. Only `GET /wizard/bootstrap` is public among `/wizard/*` paths — the rest require authentication (admin role via `RequireRole`). Tokens with `ek_` prefix are validated via `validateAPIKey` closure (hashes + DB lookup); other tokens are validated as JWT via `auth.ValidateToken`.
- `AuthMiddleware(next http.Handler, getSecret func() string, getAuthEnabled func() bool, validateAPIKey func(ctx context.Context, rawKey string) (*database.User, error), getUserByID func(ctx context.Context, id int64) (*database.User, error)) http.Handler` — Extracts `Authorization: Bearer <token>` header. If the header is absent, falls back to the `edub_token` cookie. If token starts with `ek_`, validates via `validateAPIKey` (hashes key, DB lookup) and injects `userID`, `username`, `role` (from DB), and `authSource="apikey"` into context. Otherwise, validates JWT via `auth.ValidateToken`, then re-fetches the user from DB via `getUserByID` to get the current `role` (not trusting the JWT claim for role). Injects `userID`, `username`, `role`, and `authSource="session"`. Returns 401 JSON for missing/invalid/expired tokens or deleted users, 500 JSON for internal errors. Bypasses auth for public paths (`/health`, `GET /wizard/bootstrap`, `/api/v1/auth/*`, non-API paths). When `auth_enabled` is false, the middleware injects an `admin` identity into context and passes every request through.
- `RequireRole(allowed ...auth.Role) func(http.Handler) http.Handler` — **(`permission.go`)** Middleware factory that returns a handler requiring the request context to contain one of the allowed roles via `auth.RoleKey`. Returns 403 JSON `{"error":"forbidden"}` if role is missing or not in the allowed set. Used to wrap every API route in `registerRoutes`.
- `requestMiddleware(logger *log.Logger, next http.Handler) http.Handler` — Stores the request ID in the context with `log.WithReqID` and logs each request; handlers read it back with `log.ReqIDFrom`
- `parambagMiddleware(next http.Handler) http.Handler` — Injects ParamBag into request context

---

## `handlers/consume.go`

### Struct

- `ConsumeHandler`
  - **Fields**: `getConfig func() *config.Config`, `logger *log.Logger`, `workStore *task.Store`, `queries *database.Queries`, `services *service.CrudServices`
  - **Methods**:
    - `NewConsumeHandler(getConfig, logger, workStore, queries, services) *ConsumeHandler`
    - `Consume(w, r)` — Scans inbox using `storage.ListFilePaths`, creates a batch with `status='queued'`, enqueues one pair of (consume + enrich) tasks per file via `workStore.CreateTask`. The `kushim queue` daemon picks up the queued batch for actual processing. Returns `202` with `batch_id`, `total_files`, `enqueued`, and `_links.tasks`. Returns `200` JSON `{batch_id:null, total_files:0, message:"no files found"}` when inbox is empty.
    - `Upload(w, r)` — Accepts multipart upload (`files` field, repeatable), streams bytes to temp files in the inbox, validates MIME type against `consumer.supported_files`, creates a batch with `status='queued'`, enqueues tasks. The `kushim queue` daemon picks up the batch. Returns `202` with `batch_id`, `accepted`, `rejected`, and `_links.tasks`. Returns `413` when body exceeds `server.max_upload_size`. Returns `422` when no supported files are found or required tools are missing.

---

## `handlers/document.go`

### Struct

- `DocumentHandler`
  - **Fields**: `client *database.Client`, `logger *log.Logger`, `engine *search.Engine`, `services *service.CrudServices`, `tag *curation.Tag`, `getConfig func() *config.Config`
  - **Methods**:
    - `NewDocumentHandler(client, logger, engine, services, tag, getConfig) *DocumentHandler`
    - `ListDocuments(w, r)` — Supports `sort_by` and `sort_order` query params; response includes tags (`TagResponse`), people (`PersonResponse`), content stats (`PageCount`, `WordCount`, `CharCount`), `Language`, `DocumentTypeID`
    - `GetDocument(w, r)` — Returns full document with tags (`TagResponse`), people (`PersonResponse`), doc type name
    - `GetDocumentFile(w, r)` — Serves the document's processed file via `http.ServeFile` with `Content-Disposition` set to `inline` (or `attachment` when `?download=true`); the filename is sanitized via `sanitizeFilename`
    - `SearchDocuments(w, r)` — `GET /api/v1/documents/search` — Returns `FTSDocumentResponse` array with enhanced fields. The `Snippet` field is HTML-escaped through `sanitizeSnippetHTML` which allows only `<b>`/`</b>` highlighting tags through to prevent XSS.
    - `SearchDocumentsStructured(w, r)` — `POST /api/v1/documents/search` — Accepts `search.Filter` JSON body, calls `engine.SearchStructured(ctx, filter)`, returns `SearchResponse` with `results` array and `total` count. Enriches each result with tags and people from DB to avoid N+1. The `Snippet` field is sanitized identically to `SearchDocuments`.
    - `UpdateDocument(w, r)` — `PUT /api/v1/documents/{id}` — Accepts `DocumentUpdateRequest` JSON (title, document_type_id, language, text_content). Validates title non-empty, document type exists (via `GetDocumentType`), defaults language to `"und"` when empty. Preserves existing `text_content` when nil. Returns `204 No Content`.
    - `DeleteDocument(w, r)` — `DELETE /api/v1/documents/{id}` — Soft-deletes the document: calls `services.Trash.SoftDelete`, which moves files to `<storage>/trash/<document_id>/` (missing files tolerated) and sets `deleted_at`. Returns `204 No Content`; `404` when the document is missing or already in the trash. File-move failures return 500 and leave the document active.
    - `AddDocumentTag(w, r)` — `POST /api/v1/documents/{id}/tags` — Accepts `{tag_id}`, validates document and tag exist via `tag.Get` (maps `KindNotFound` → 404 via `writeServiceError`), calls `AddDocumentTag` (INSERT OR IGNORE). Returns `204 No Content`.
    - `RemoveDocumentTag(w, r)` — `DELETE /api/v1/documents/{id}/tags` — Accepts `{tag_id}`, validates document exists, calls `RemoveDocumentTag`. Returns `204 No Content`.
    - `AddDocumentPeople(w, r)` — `POST /api/v1/documents/{id}/people` — Accepts `{people_id, people_type_id}`, validates document, person, and people type exist, calls `AddDocumentPeople` (INSERT OR IGNORE). Returns `204 No Content`.
    - `RemoveDocumentPeople(w, r)` — `DELETE /api/v1/documents/{id}/people` — Accepts `{people_id, people_type_id}`, validates document exists, calls `RemoveDocumentPeople` (now filters by all three PK columns: document_id, people_id, people_type_id). Returns `204 No Content`.
    - `ReEnrich(w, r)` — `POST /api/v1/documents/{id}/reenrich` — Validates document ID is non-empty, calls `services.ReEnrich.ReEnrich(ctx, documentID)`. Returns `202 Accepted` with `{batch_id, _links: {tasks: ...}}`. Maps document-not-found and dedup-conflict errors via `writeServiceError` (404 / 409).
    - `DownloadDocuments(w, r)` — `POST /api/v1/documents/download` — Accepts `{document_ids: [...]}` JSON (or form-encoded). Streams a ZIP archive with each document's processed file. Validates count against `max_download_files` and total size against `max_download_size_mb`. Returns `200` with `Content-Type: application/zip`, `400` with validation errors.
    - `BatchDeleteDocuments(w, r)` — `POST /api/v1/documents/batch-delete` — Accepts `{document_ids: [...]}` JSON. Soft-deletes each document independently via `services.Trash.SoftDelete`; returns partial failure info (`deleted`/`failed`, `404` for missing/already-trashed IDs). Count validated against `max_batch_delete`.
    - `BatchAssignTags(w, r)` — `POST /api/v1/documents/batch-tags` — Accepts `{document_ids, tag_ids, mode}`. Supports `add` (append) and `replace` (transactional clear+add) modes. Validates all tag IDs exist before modifying any document.
    - `BatchSetDocumentType(w, r)` — `POST /api/v1/documents/batch-type` — Accepts `{document_ids, document_type_id}`. Single atomic `UPDATE ... WHERE document_id = ANY($2::text[]) AND deleted_at IS NULL` via `SetDocumentsDocumentType` (one round trip, `modified_at` bumped). Validates the type exists before touching documents (404 when missing); non-existent/soft-deleted IDs reported in `failed[]` as `"not found"`; 400 only when nothing was updated. Count validated against `max_batch_delete`.
    - `FilterLanguages(w, r)` — `GET /api/v1/filter-languages` — Returns distinct language codes from the document corpus as a JSON string array.
    - `SupportedMimeTypes(w, r)` — `GET /api/v1/supported-mime-types` — Returns the compiled-in supported MIME types as a JSON array of `MimeInfo` objects (mime_type, extension, label).

---

## `handlers/trash.go`

### Struct

- `TrashHandler`
  - **Fields**: `logger *log.Logger`, `trashSvc *service.Trash`, `getConfig func() *config.Config`
  - **Methods**:
    - `NewTrashHandler(logger, trashSvc, getConfig) *TrashHandler`
    - `ListTrash(w, r)` — `GET /api/v1/trash` — Paginated trashed documents (`limit` default 50 max 100, `offset`), ordered by `deleted_at` DESC. Returns `TrashListResponse` with `documents`, `total` (via `CountTrash`), `limit`, `offset`.
    - `GetTrashDocument(w, r)` — `GET /api/v1/trash/{id}` — Full metadata for one trashed document (checksums, counts, language, type, timestamps — no filesystem paths). `404` if not in trash.
    - `RestoreDocument(w, r)` — `POST /api/v1/trash/{id}/restore` — Moves files back to `<storage>/originals|processed/` and clears `deleted_at`. `204`; `404` if not in trash; missing files are skipped (partial restore).
    - `PermanentlyDeleteDocument(w, r)` — `DELETE /api/v1/trash/{id}` — Deletes the DB row (junction tables cascade) first, then removes the document's trash dir best-effort. `204`; `404` if not in trash.
    - `PurgeExpired(w, r)` — `POST /api/v1/trash/purge` — Deletes all trashed rows older than `storage.trash.retention_days`, sweeps orphaned trash dirs when rows were deleted, and always sweeps orphaned thumbnails in `storage/thumbnails/`. Returns `{"purged": N}`.
    - `BatchPermanentlyDelete(w, r)` — `POST /api/v1/trash/batch-delete` — Per-ID `PermanentlyDelete` with `deleted`/`failed` result; count validated against `max_batch_delete`.
    - `BatchRestore(w, r)` — `POST /api/v1/trash/batch-restore` — Per-ID `RestoreDocument` with `restored`/`failed` result; count validated against `max_batch_delete`.

---

## `handlers/auth.go`

### Struct

- `AuthHandler`
  - **Fields**: `userService *service.User`, `getConfig func() *config.Config`, `logger *log.Logger`
  - **Methods**:
    - `NewAuthHandler(userService, getConfig, logger) *AuthHandler`
    - `Login(w, r)` — `POST /api/v1/auth/login` — Accepts `{"username", "password"}`. Calls `userService.Authenticate()` (bcrypt compare + DB lookup). On success: generates a 24h JWT via `auth.GenerateToken()` passing `user.Role`, returns `{"token": "...", "user": {"id", "username", "role", "created_at"}}` with 200. Also sets an `edub_token` HttpOnly cookie (`SameSite=Lax`, `Secure` in production, `Max-Age=86400`) so that browser navigations (PDF viewer fetch, `<a href>`) are authenticated without requiring the `Authorization` header. On invalid credentials: returns 401 with generic `"invalid username or password"`. On empty username/password: returns 401 (same generic message to avoid user enumeration). On malformed body: returns 400.
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

- `HealthHandler(w, r, logger)` — Writes `{"status":"healthy","version":"2.9.0","time":"..."}`

---

## `handlers/task.go`

### Struct

- `TaskHandler`
  - **Fields**: `services *service.CrudServices`, `client *database.Client`, `queries *database.Queries`, `logger *log.Logger`, `getConfig func() *config.Config`
  - **Methods**:
    - `NewTaskHandler(services, client, logger, getConfig) *TaskHandler`
    - `ListTasks(w, r)` — Filters by `batch`, `status`, `limit`, `offset`; includes the batch summary ([BatchResponse](#batchresponse)) when `batch` is set — served by `service.Batch.GetOverview`, an unknown batch answers `404` via `writeServiceError`. `status=active` is a special value returning `pending`+`processing`+`waiting` (processing-first) via `ListActiveTasks`
    - `GetTask(w, r)` — Single task by UUID (uses `taskDetailResponse`: includes `result` for `tagreconcile` tasks)
    - `GetTagReconcileReport(w, r)` — `GET /api/v1/tags/reconciliation/report` — Returns the latest completed `tagreconcile` task's report (`task_id`, `completed_at`, `result` = persisted `Report` JSON) via `GetLastCompletedTagReconcileTaskWithResult`. `404` when no completed run exists. Viewer-gated, Postgres-only (no matcher dependency).
    - `ListBatches(w, r)` — One page of [BatchResponse](#batchresponse) items via `service.Batch.ListOverviews`, newest first; `status` filters batch status
    - `GetBatchSummary(w, r)` — One batch's [BatchResponse](#batchresponse) row (via `{id}`); an unknown batch answers `404` via `writeServiceError`
    - `ResumeBatch(w, r)` — `POST /api/v1/batches/{id}/resume?retry_failed=<bool>` — the single guarded recovery (`task.ResumeBatch`; the flag is the retry intent, default `false`): refuses a live owner or a still-queued/processing batch (`409`, one shared `batchLifecycleError` mapping for the resume and cancel surfaces), resets `processing`→`pending` in a transaction with the requeue — children of retried consumes restore in the same transaction when retried with the flag — and requeues the batch for the daemon. Returns `202 {"resumed": true, "reset": <n>, "retried": <m>}` (`retried` counts the failed tasks reset when `retry_failed=true`), or `200 {"resumed": false, ..., "reason": ...}` when nothing can become active.
    - `CancelBatch(w, r)` — `POST /api/v1/batches/{id}/cancel` — the single cancel operation (`task.CancelBatch`): signals a live owner (SIGTERM) when its lease row names a live process, cancels pending + waiting + processing tasks, and releases the batch's locks and lease. Returns `200` with `cancelled_pending`, `cancelled_processing`, and `signal_sent`; a missing batch → `404`, a terminal batch → `409` (terminal states are immutable).
    - `GetDashboard(w, r)` — Returns `recent_batches` (top 10 [BatchResponse](#batchresponse) items) + `running_tasks` (object with `count` = uncapped active-task total and `tasks` = top 25 active tasks: `pending`/`processing`/`waiting`, processing-first, via `ListActiveTasks`; the count is derived from the per-status counts) + `analytics` (language/document-type/tag distributions, missing counts) + `processing_health` (task success rate, avg duration, active/orphaned batches, missing tools count) + storage panel fields (`total_batches`, `total_files`, `inbox_files` (files in the consumption dir matching `consumer.supported_files`), per-status counts, `originals_size_bytes` (sum of `file_size`), `processed_size_bytes` (sum of `processed_size`, sentinel `-1` excluded), `original_type_breakdown`, `storage_trend`, `avg_file_size_bytes`, `total_pages`, `total_words`). Active tasks carry a derived `label` (`TaskType: file name` for consume/enrich/thumbnail, task-type/payload based otherwise; the backup mode defaults to `full`). Processing health reads the batch-table counts (`CountBatches`, `CountActiveBatches`, `CountOrphaned`) and reuses `config.MissingExternalToolErrors` to detect missing tools at request time.

    - **Helpers**: `batchItemFromOverview(ov *service.BatchOverview) BatchItem`, `buildDocumentAnalytics(ctx, reqID) *DocumentAnalytics`, `buildProcessingHealth(ctx) *ProcessingHealth`, `taskLabel(taskType, payload) string`, `taskToResponse(t) TaskResponse` (list shape — no `result`), `taskDetailResponse(t) TaskResponse` (includes `result` for `tagreconcile` tasks), `buildTaskResponse(t, includeResult)`

---

## DTOs (`handlers/document.go`)

### Structs

- `TagResponse` — `ID int64`, `Name string`, `DocumentCount int64`, `Promoted bool`, `Seeded bool` (the vocabulary list fills `DocumentCount`, `Promoted` and `Seeded`; the document surfaces fill `Promoted` and `Seeded` only)
- `PersonResponse` — `ID`, `Name`, `NameNative` (original non-Latin script, if any), `PersonTypeID`, `PersonTypeName`, `PersonTypeDescription`, `DocumentCount int64`
- `DocumentResponse`
  - **Fields**: `ID string` (UUID, JSON `"id"`), `Title`, `MD5Checksum`, `SHA512Checksum`, `OriginalType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `CreatedAt`, `ModifiedAt`
- `FTSDocumentResponse`
  - **Fields**: `ID string` (UUID, replaces the old int64 `id`), `Title`, `MD5Checksum`, `SHA512Checksum`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `DocumentTypeID *int64`, `DocumentTypeName *string`, `Tags []TagResponse`, `People []PersonResponse`, `Rank float64`, `Snippet string` (HTML-escaped, `<b>` highlighting preserved), `TextContent string`

### Request Structs (`handlers/document.go`)

- `DocumentUpdateRequest` — `Title string`, `DocumentTypeID int64`, `Language string`, `TextContent *string` (omitempty)
- `AddDocumentTagRequest` — `TagID int64`
- `RemoveDocumentTagRequest` — `TagID int64`
- `AddDocumentPeopleRequest` — `PeopleID int64`, `PeopleTypeID int64`
- `RemoveDocumentPeopleRequest` — `PeopleID int64`, `PeopleTypeID int64`

---

## DTOs (`handlers/task.go`)

### Structs

- `TaskResponse` — `TaskID`, `BatchID`, `TaskType`, `FileName`, `PayloadDocID`, `Status`, `Error *string`, `Label` (`TaskType: file name` for consume/enrich/thumbnail, task-type/payload derived otherwise — the backup mode defaults to `full`), `Progress *TaskProgressResponse`, `Result json.RawMessage` (present only for `tagreconcile` tasks on the detail endpoint — the persisted reconciliation `Report`), `CreatedAt`, `StartedAt *string`, `CompletedAt *string`
- `TagReconcileReportResponse` — `TaskID string`, `CompletedAt string`, `Result json.RawMessage` (the persisted reconciliation `Report` of the latest completed run)
- `BatchItem` — `BatchID`, `Status`, `Origin`, `Schedule` (omitempty), `CreatedAt` (RFC 3339, omitempty), embedded `BatchCounts`, `OwnerPID int64` (omitempty), `DurationMs *int64` (omitempty); one row shared by the batch list, the single-batch endpoint, the task list's summary and the dashboard
- `BatchCounts` — `Total`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`, `OwnerState`, `Orphaned`
- `ListBatchesResponse` — `Batches []BatchItem`
- `ListTasksResponse` — `BatchID`, `Summary *BatchItem`, `Tasks []TaskResponse`
- `OriginalTypeStat` — `OriginalType`, `Count`, `TotalBytes`
- `StorageTrendPoint` — `Date`, `DailyCount`, `DailyBytes`, `CumulativeBytes`
- `DistributionItem` — `Label string`, `Count int64`
- `DocumentAnalytics` — `LanguageDistribution []DistributionItem`, `DocumentTypeDistribution []DistributionItem`, `TagFrequency []DistributionItem`, `MissingLanguageCount int64`, `MissingTypeCount int64`, `MissingTagsCount int64`
- `ProcessingHealth` — `SuccessRate float64`, `CompletedLast7d int64`, `FailedLast7d int64`, `AvgDurationMs int64`, `ActiveBatches int64`, `OrphanedBatches int64`, `MissingTools int64`
- `DashboardResponse` — `RecentBatches []BatchItem`, `RunningTasks RunningTasksResponse`, `Analytics *DocumentAnalytics`, `ProcessingHealth *ProcessingHealth`, `TotalBatches`, `TotalFiles`, `InboxFiles`, `Waiting`, `Pending`, `Processing`, `Completed`, `Failed`, `Cancelled`, `Discarded`, `OriginalsSizeBytes`, `ProcessedSizeBytes`, `OriginalTypeBreakdown []OriginalTypeStat`, `StorageTrend []StorageTrendPoint`, `AvgFileSizeBytes`, `TotalPages`, `TotalWords`
- `RunningTasksResponse` — `Count int64` (uncapped total of active tasks), `Tasks []TaskResponse` (capped at 25)

---

---

## `handlers/tag.go`

### Struct

- `TagHandler`
  - **Fields**: `tag *curation.Tag`, `identity *curation.TagIdentity`, `logger *log.Logger`
  - **Methods**:
    - `NewTagHandler(tag, identity, logger) *TagHandler`
    - `List(w, r)` — `GET /api/v1/tags?q=<prefix>&limit=50&offset=0` — With `q`: searches by prefix via `tag.SearchByNameWithDocumentCount`. Without `q`: lists paginated via `tag.ListWithDocumentCount`. Returns `{results, total}` with `document_count`, `promoted` and `seeded` per tag.
    - `Create(w, r)` — `POST /api/v1/tags` — Accepts `{name}`. Calls `tag.Create(ctx, []string{name})`. The service normalizes the name through `textnorm.NormalizeTag` (lowercase, accent-folded, keeps `+ # .`, digits, and letters of any Unicode script); the response `name` is the canonical form and conflicts are checked against canonical names. Maps status: `Created` → 201 with `{id,name}`, `Conflict` → 409 with existing `{id,name}`, `Invalid` → 400 (empty after normalization).
    - `Update(w, r)` — `PUT /api/v1/tags/{id}` — Accepts `{name}`. Calls `tag.Update(ctx, []UpdatePair{{ID: id, Name: name}})`. The name is normalized the same way; `Noop` (200) is returned when the canonical form equals the stored name. Maps status: `Updated`/`Noop` → 200 with `{id,name}`, `Conflict` → 409, `NotFound` → 404, `Invalid` → 400.
    - `Delete(w, r)` — `DELETE /api/v1/tags/{id}` — Calls `tag.Delete(ctx, []int64{id})`. Maps status: `Deleted` → 204, `NotFound` → 404.
    - `ListDeleted(w, r)` — `GET /api/v1/tags/deleted?q=<prefix>&limit=50&offset=0` — Calls `tag.ListDeleted(ctx, prefix, limit, offset)` (`ListDeletedTags` + `CountDeletedTags`). Returns `200` with `{results: [{name, kind, deleted_at}], total}`; `kind` is `blocked`. Editor/admin only.
    - `RestoreDeleted(w, r)` — `DELETE /api/v1/tags/deleted/{name}` — Calls `tag.RestoreDeleted(ctx, name)`: reads the blocklist row, re-creates the tag (a `Conflict` — tag already exists — is tolerated, and the embedding store is updated by `Tag.Create`), then deletes the blocklist row only on success so a failed restore keeps the name blocked. Unknown name → `404` via `errs.KindOf`; matcher unavailable on the create path → 503. Returns `200` with `{name, kind}`. Editor/admin only.
    - `ListProvisional(w, r)` — `GET /api/v1/tags/provisional?limit=50&offset=0&criteria=&verdict=` — `limit` via `ParamBag` (default 50, clamped 1–100), `offset` (default 0, clamped 0–100000; negative values clamp to 0); `parseProvisionalFilter` reads `criteria` (`pb.GetStrings`, each entry a `key:state` pair over the six rubric keys with states `pass`/`fail`/`absent` mapped to the SQL text `true`/`false`/`absent`) and `verdict` (`''`/`none`/`admit`/`defer`/`reject`), answering `400` (`Invalid criteria filter`/`Invalid verdict filter`) for unknown keys, states, or values. Calls `tag.ListProvisional` (`ListProvisionalTagsWithDocumentCount` — `promoted_at IS NULL` with at least one assignment, newest first, the lateral latest-verdict join and the same optional filters as the count, `document_count` counting live documents only) and `tag.CountProvisional` (filtered total), plus `tag.CountPendingIdentity` for the queue-level pending count, then one batched `tag.LatestQualityEventsForNames` lookup over the page's names at `analyzer.TagQualityVersion`, one `PendingIdentityCounts` lookup, one `IdentityHeldNames` lookup, one `CompositionHeldNames` lookup, and one `LatestCompositionEventsForNames` lookup over the same names; each row's `criteria` JSON unmarshals into `TagQualityCriteriaView` (a failed unmarshal leaves `criteria` nil). Returns `200` with `{results: [{id, name, document_count, created_at, verdict, pending_identity, identity_hold?, composition_hold?, composition_reason?, demoted_at?, demoted_reason?}], total, pending_identity_total}`; `total` is the filtered count, `verdict` is `null` for names with no event row, `pending_identity` counts unresolved canonical-identity claims referencing the name on either side, `identity_hold` carries the live store anchor of a refused fold (omitted when the name is not held), `composition_hold` carries the reason the latest composition verdict under the current split criteria version withholds promotion (omitted when there is no hold), `composition_reason` carries the reason on the most recent decomposition event whatever its criteria version (omitted when the name has none), `demoted_at`/`demoted_reason` carry the revocation record of a value that was demoted rather than born provisional (omitted otherwise), and `pending_identity_total` is the queue-wide unresolved-claim count. Viewer-gated.
    - `PromoteTag(w, r)` — `POST /api/v1/tags/{id}/promote` — Parses the path id (`400` on a malformed id) and calls `tag.Promote(ctx, id, "human-approve")`, the same service method the reconciliation sweep uses. Maps status: `PromotePromoted` → `204`, `PromoteAlreadyPromoted` → `409`, `PromoteNotFound` → `404`. The service re-reads the tag, promotes it under `promoted_at IS NULL`, adds the name to the embedding store, and logs `tagquality[v6]: promote %q (reason=human-approve)`. Editor/admin only.
    - `DemoteTag(w, r)` — `POST /api/v1/tags/{id}/demote` — Parses the path id (`400` on a malformed id) and calls `tag.Demote(ctx, id, "operator")`, the same service method the reconciliation sweep uses. Maps status: `DemoteDemoted` → `204`, `DemoteSeeded` → `409` (`Seeded values cannot be demoted`), `DemoteNotPromoted` → `409` (`Tag is not promoted`), `DemoteNotFound` → `404`. The service re-reads the tag, clears promotion under `promoted_at IS NOT NULL AND NOT seeded`, records `demoted_at`/`demoted_reason`, and removes the name from the embedding store (retried once, best-effort), leaving the row and its assignments in place; it logs `tagquality[v6]: demote %q (reason=operator rule_set=decision-v7)`. Editor/admin only.
    - `ReevaluateIdentity(w, r)` — `POST /api/v1/tags/identity/reevaluate` — Calls `identity.Enqueue(ctx)`: while a `tagidentity` batch is queued or processing it returns `202` with `{already_queued: true}` and an empty `batch_id`; otherwise it creates one task (dedup key `tagidentity`) plus a queued `tagidentity` batch and returns `202` with `{batch_id, already_queued: false}`. The run resolves pending identity evidence under `enricher.tag_reconcile.max_rejudge`; the queue's identity pool executes it unconditionally, and the pass also runs on its own daily 03:30 schedule while `enricher.tag_adjudicator.enabled` is set. Editor/admin only.
    - `ReevaluateAudit(w, r)` — `POST /api/v1/tags/audit/reevaluate` — Calls `audit.Enqueue(ctx)`: while a `tagaudit` batch is queued or processing (or a unique violation on the active-dedup index loses the race) it returns `202` with `{already_queued: true}` and an empty `batch_id`; otherwise it creates one task (dedup key `tagaudit`) plus a queued `tagaudit` batch and returns `202` with `{batch_id, already_queued: false}`. The run draws its sample from the sweep register and verifies coverage; it executes whenever the daemon dispatches it, independent of the schedule (`enricher.tag_audit.enabled` gates only automatic enqueuing). Editor/admin only.
    - `ListAuditFlags(w, r)` — `GET /api/v1/tags/audit/flags?limit=50&offset=0` — `limit` via `ParamBag` (default 50, clamped 1–100), `offset` (default 0, clamped 0–100000). Calls `audit.ListFlags(ctx, limit, offset)` (`ListOpenTagCoverageFlags` + `CountOpenTagCoverageFlags`): the open coverage flags are the latest event of each (document, dimension) pair whose action is `flag` or `proposal` and whose review outcome is still unset, newest first, joined to the document title. Returns `200` with `{results: [{id, document_id, document_title, tag_name, class, source, provisional, created_at}], total}`. Viewer and above.
    - `ApplyAuditFlag(w, r)` — `POST /api/v1/tags/audit/flags/apply` — Accepts `{document_id, tag_name}`. Calls `audit.ApplyFlag(ctx, document_id, tag_name)`: attaches the canonical tag to the document (`document_tag.source='audit'`) and records the `applied` review outcome, closing the flag. Unknown tag → `404`; malformed body → `400`. Returns `204`. Editor/admin only.
    - `DismissAuditFlag(w, r)` — `POST /api/v1/tags/audit/flags/dismiss` — Accepts `{document_id, tag_name}`. Calls `audit.DismissFlag(ctx, document_id, tag_name)`: records the `not_applies` review outcome, closing the flag; the outcome is authoritative, so the next audit run does not re-flag the pair while the document text is unchanged. Malformed body → `400`. Returns `204`. Editor/admin only.
    - `CreateAuditValue(w, r)` — `POST /api/v1/tags/audit/flags/create-value` — Accepts `{tag_name}` (no document: the value is a vocabulary addition). Normalizes the name and calls `tag.CreateAndUnblock(ctx, []string{name})`, registering the name provisionally with pending identity evidence through the normal creation path; the fill lands on a later audit pass once the value exists. Empty name → `400`; matcher unavailable → `503`. Returns `204`. Editor/admin only.
    - `ListSiblings(w, r)` — `GET /api/v1/tags/siblings?limit=50&offset=0` — `limit` via `ParamBag` (default 50, clamped 1–100) and `offset` (default 0, clamped 0–100000). Calls `tag.ListSiblings`, which reads only the latest measurement run's overlap records through `Store.SiblingWorklist` and derives the containment worklist under the current cuts, the review band first, then pages in-process. Returns `200` with `{run_id, snapshot_at, absent, results: [{a, b, containment, mass_a, mass_b, gray}], total}`: `a` is the broader tag and `b` the contained one, `containment` is the fraction of `b`'s live documents `a` also holds, the masses are live document counts, `gray` marks the review band, `absent` reports that no measurement run exists yet, and `total` counts every pair in the run. Read-only: containment drives no merge, hold, or demotion. Viewer-gated.

---

## `handlers/people.go`

### Structs

- `PeopleHandler`
  - **Fields**: `services *service.CrudServices`, `logger *log.Logger`
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
  - **Fields**: `services *service.CrudServices`, `logger *log.Logger`
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
  - **Fields**: `services *service.CrudServices`, `logger *log.Logger`
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
  - **Fields**: `userService *service.User`, `logger *log.Logger`
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
  - **Fields**: `getConfig func() *config.Config`, `onConfigSet func(*config.Config)`, `queries *database.Queries`, `logger *log.Logger`, `enqueuer *task.Enqueuer`, `OnBootstrap func(configDir string) (*config.Config, *database.Client, *task.Enqueuer, error)`
  - **Methods**:
    - `NewConfigHandler(getConfig, onConfigSet, client *database.Client, logger *log.Logger, enqueuer *task.Enqueuer, services *service.CrudServices) *ConfigHandler` — `getConfig` and `onConfigSet` are closures wrapping a shared `atomic.Pointer[config.Config]`. The handler never owns its own pointer; config flows via these closures, allowing the file watcher to atomically update the shared state without handler coordination. A nil client leaves the queries unset, so the wizard can serve its pre-bootstrap routes.
    - `SetServices(client, enqueuer)` — Sets `client.Queries`, creates CrudServices with `Batch` and `User` services, sets enqueuer on an already-initialized handler. Used by the wizard's auto-resume path after `onConfigSet` stores the bootstrapped config.
    - `Bootstrap(w, r)` — `GET /wizard/bootstrap` — Public endpoint (no auth required). Returns only non-sensitive fields: `auth_enabled` (bool) and `missing_tools` (array of `ExternalTool`). The SPA calls this before rendering the login screen to determine whether auth is enabled and whether any external tools are missing. This is the only `/wizard/*` route reachable without authentication — all other wizard routes are admin-protected.
    - `GetConfig(w, r)` — `GET /wizard/config` — Returns user-configurable settings as `ConfigResponse` (app, server, consumer, enricher sections plus `available_engines` and `available_file_types`; app includes boolean `initialized`; enricher includes LLM provider tokens). Returns defaults from `DefaultConfig("")` when no config is loaded (wizard not yet bootstrapped), so the frontend always receives a complete config shape. Requires admin role.
    - `PutConfig(w, r)` — `PUT /wizard/config` — Three-phase: if `config_dir` is present and no config exists, bootstraps config directory, DB, and skeleton YAML. Otherwise it computes `dbChanged` (`dbParamsFromBody` → `config.DatabaseConnectionChanged`, DSN-aware) and `storageChanged` (`storage.storage_dir`/`storage.consumption_dir` differ, `~`-expanded). When neither changed it writes config via `SaveMap`, reloads, and starts direct config-update downloads (`config.DownloadTessdataLanguage`/`DownloadHugotModel`, one goroutine per missing asset; an asset already downloading is skipped), returning `200`/`201` with a `pending_tasks` count covering the downloads and a `missing_tools` array. When at least one changed, it probes the new directories for writability, persists everything except the deferred keys (`database.*`, `storage.storage_dir`, `storage.consumption_dir`) immediately, enqueues `migrate-db` (if `dbChanged`) then `migrate-storage` (if `storageChanged`) — both migrations hold the maintenance lock while they run — starts the asset downloads over the saved immediate config, and returns `202 {"pending_tasks": <n>, "message": "migration(s) queued — settings apply once the migration completes"}`. A migration task in flight answers `409`; a stale pending/failed task is superseded atomically via `DeleteConfigTaskByDedupKey` (extracted into `tryAcquireConfigTaskSlot`). Requires admin role.
    - `ConfigStatus(w, r)` — `GET /wizard/config/status` — Returns `ConfigStatusResponse` with the `configured` flag, `pending_tasks` (the queue's migration tasks plus the live missing-asset count), `failed_tasks` (failed migration tasks plus direct-download failures, each `{op, lang: <tessdata language or empty>, error}`), `errors`, plus `tools` (full `[]ExternalTool` availability list) and `missing_tools` (hard-blocking subset). The wizard's completion gate polls this every 3 seconds until `pending_tasks` is 0. Requires admin role.
    - `RetryFailedConfig(w, r)` — `POST /wizard/config/retry` — Resumes the batch of every failed migration task (one `ResumeBatch` per distinct batch, failed tasks reset) and re-triggers the direct asset downloads for missing or failed assets (idempotent — both helpers skip when the asset is present). Returns `200 {"retried": <n>, "downloads_started": <m>}`; a live owner or a still-running batch skips that batch and continues with the rest. Requires admin role.
    - `CreateAdminUser(w, r)` — `POST /wizard/admin-user` — Creates initial admin user. Accepts `CreateUserRequest` JSON body, validates username (non-empty, stripped), calls `User.Create`. Returns `201` with `UserResponse` on success, `409` with `{"error":"username already exists"}` on duplicate, `400` on validation error.

---

## DTOs (`handlers/config.go`)

### Structs

- `AppConfigResponse` — `Initialized bool` (true when config_dir has been bootstrapped)
- `ServerConfigResponse` — `Host string`, `Port int`
- `StorageConfigResponse` — `ConsumptionDir string` (inbox path), `StorageDir string` (processed document path)
- `DatabaseConfigResponse` — `Path string` (not used with PostgreSQL)
- `ConfigResponse` — `App AppConfigResponse`, `Server ServerConfigResponse`, `Storage StorageConfigResponse`, `Database DatabaseConfigResponse`, `Consumer ConsumerConfigResponse`, `Enricher EnricherConfigResponse`, `Backup BackupConfigResponse`, `Mirror MirrorConfigResponse`, `AvailableEngines map[string][]EngineEntry`, `AvailableFileTypes []AvailableFileType`
- `MirrorConfigResponse` — `Enabled bool`, `Path string` (local path or `[user@]host:path` remote target), `Interval float64` (days), `Time string` (HH:MM)
- `AvailableFileType` — `MimeType string`, `Label string`, `Extensions []string` (canonical + aliases, e.g. TIFF → `.tiff`, `.tif`), `Required bool` (PDF only) — drives the supported-file-types checkboxes in both UIs
- `ConsumerConfigResponse` — `SupportedFiles []string`, `Workers int`, `MaxFilesPerBatch int`, `Converter DocxOdtConverterResponse`, `TextExtractor TextExtractorResponse`, `PdfOptimizer PdfOptimizerResponse`, `OCR OCRResponse`, `Thumbnail ThumbnailConfigResponse`, `Polling PollingConfigResponse`, `Reclaim ReclaimConfigResponse`
- `ThumbnailConfigResponse` — `Enabled bool`, `Engine string`, `DPI int`, `MaxWidth int`, `Quality int`, `Timeout int`, `Workers int`
- `DocxOdtConverterResponse` — `Enabled bool`, `Binary string`, `Timeout int`
- `TextExtractorResponse` — `Engine string`, `Timeout int`
- `PdfOptimizerResponse` — `Engine string`, `Fallback string`, `Timeout int`
- `OCRResponse` — `Engine string`, `Languages []string`, `DataDir string`, `Timeout int`
- `EnricherConfigResponse` — `Workers int`, `TextReducer TextReducerResponse`, `ContentAnalyzer ContentAnalyzerResponse`, `TagMatcher TagMatcherResponse`, `TagAdjudicator TagAdjudicatorConfigResponse`, `TagQuality TagQualityConfigResponse`, `TagReconcile TagReconcileConfigResponse`, `TagMeasure TagMeasureConfigResponse`, `TagAudit TagAuditConfigResponse`
- `TextReducerResponse` — `Engine string`, `Timeout int`, `TargetWords int`
- `ContentAnalyzerResponse` — `Enabled bool`, `Timeout int`, `MinTags int`, `Llm LlmConfigResponse`, `Fallbacks []FallbackConfigResponse` (omitempty — absent when no fallbacks are configured), `PromptTemplate string`
- `FallbackConfigResponse` — `Enabled bool`, `Llm LlmConfigResponse`
- `LlmConfigResponse` — `Adapter string`, `Provider string`, `Model string`, `Token string`, `Reasoning bool`, `Temperature float64`, `RequestDelay float64`
- `LlmModelEntry` — `ID string`, `Capabilities` (CanDisableReasoning, ReasoningEfforts, MaxInputTokens, MaxOutputTokens, SupportsTemperature, SupportsResponseSchema)
- `LlmModelsResponse` — `Adapters map[string][]string`, `Providers map[string][]LlmModelEntry`
- `DocTypeRefinementResponse` — `Enabled bool`, `HeadWords int`, `TailWords int`
- `TagMatcherResponse` — `Engine string`, `Timeout int`, `ReduceTargetWords int`, `ChunkSize int`, `TopN int`, `Hugot HugotResponse`
- `HugotResponse` — `Model string`, `Backend string`
- `TagAdjudicatorConfigResponse` — `Enabled bool`, `Timeout int`, `Llm TagAdjudicatorLlmResponse`
- `TagQualityConfigResponse` — `Enabled bool`, `Timeout int`, `Llm TagAdjudicatorLlmResponse` (the decision thresholds are internal constants, absent from the response by design)
- `TagAdjudicatorLlmResponse` — `Adapter string`, `Provider string`, `Model string`, `Token string` (omitempty), `RequestDelay float64`
- `TagReconcileConfigResponse` — `Enabled bool`, `ReportOnly bool`, `Interval float64`, `Time string`, `MaxRejudge int`, `MaxRetag int`, `DeadTagGraceDays int`, `ProvisionalGraceDays int`
- `TagMeasureConfigResponse` — `Enabled bool`, `Interval float64`, `Time string`
- `TagAuditConfigResponse` — `Enabled bool`, `Interval float64`, `Time string`, `SampleSize int`
- `FailedTaskSummary` — `TaskID string`, `Op string`, `Lang string` (omitempty), `Error string`
- `BootstrapResponse` — `AuthEnabled bool`, `MissingTools []config.ExternalTool` — Returned by `GET /wizard/bootstrap`. Contains only non-sensitive fields so the SPA can render the login screen without exposing LLM tokens or DB connection details.
- `ConfigStatusResponse` — `Configured bool`, `PendingTasks int`, `FailedTasks []FailedTaskSummary` (omitempty), `Errors []string`, `Tools []config.ExternalTool` (full availability list), `MissingTools []config.ExternalTool` (hard-blocking subset)

### Functions

- `ConfigResponseFrom(cfg *config.Config) ConfigResponse` — Maps internal config to the API response, excluding internal/computed fields (model paths, similarity thresholds, etc.). Includes LLM adapter/provider/model config (flat structure), server host/port, `consumer.supported_files` + `consumer.converter`, and produces the `initialized` boolean in the `app` section plus the `available_file_types` list (derived from `internal/documents/mime` via `mime.ExtensionsFor`).

---

## `handlers/orphaned.go`

### Struct

- `OrphanedHandler`
  - **Fields**: `svc *service.Orphaned`, `logger *log.Logger`
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
  - **Fields**: `svc *service.ErroredFiles`, `logger *log.Logger`
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
  - **Fields**: `queries *database.Queries`, `logger *log.Logger`
  - **Methods**:
    - `NewSavedSearchHandler(queries, logger) *SavedSearchHandler`
    - `Create(w, r)` — `POST /api/v1/saved-searches` — Accepts `CreateSavedSearchRequest` JSON, inserts into `saved_search` table, returns `{"id": <new_id>}` with `201 Created`
    - `List(w, r)` — `GET /api/v1/saved-searches` — Returns array of `SavedSearchResponse` ordered by `created_at DESC`
    - `Delete(w, r)` — `DELETE /api/v1/saved-searches/{id}` — Parses ID from path, deletes record, returns `204 No Content`

---

## `handlers/logs.go`

### Struct

- `LogsHandler`
  - **Fields**: `getConfig func() *config.Config`, `logger *log.Logger`
  - **Methods**:
    - `NewLogsHandler(getConfig, logger) *LogsHandler`
    - `ListLogs(w, r)` — `GET /api/v1/logs/{name}` — Reads log files from `{configDir}/logs/{name}.log`. Whitelist restricts `name` to `kushim`, `edub`, `hugot`, `queue` (path traversal prevention). Returns `{"lines": [...]}` JSON. Supports `?lines=N` query param (default 500, floor 100, ceiling 5000). Multi-line entries (e.g. LLM prompt dumps) are merged by timestamp prefix detection so they appear as a single entry. On files > 2 MiB, seeks to end and reads tail chunk. Lines and merged entries longer than 1 MiB are truncated with a `... [N bytes truncated]` marker instead of failing the request. Returns 404 for invalid name or missing file, 500 on read errors.

---

## DTOs (`handlers/tag.go`)

### Structs

- `CreateTagRequest` — `Name string`
- `UpdateTagRequest` — `Name string`
- `TagResponse` — `ID int64`, `Name string`, `Promoted bool`, `Seeded bool` (reused from `document.go`)
- `TagQualityCriteriaView` — `SubjectMatter *bool`, `Dimension *bool`, `NotPersonPlaceWork *bool`, `Atomic *bool`, `Defer *bool` (JSON snake_case; `nil` means the answer had no signal in the stored payload)
- `TagQualityVerdictInfo` — `Pass bool`, `Outcome string` (JSON `outcome`, omitted when empty — the recorded `reject`/`defer`/`admit` disposition, absent on rows predating the outcome column), `Criteria *TagQualityCriteriaView`, `Model string` (JSON `model`, omitted when empty — cache-applied rows carry no model), `VerdictAt time.Time`
- `ProvisionalTagResponse` — `ID int64`, `Name string`, `DocumentCount int64` (live, non-trashed documents only), `CreatedAt time.Time` (the list query coalesces NULL `created_at` to the current time), `Verdict *TagQualityVerdictInfo` (`null` when the name has no `tag_quality_event` row at the current policy version), `PendingIdentity int64` (unresolved identity claims referencing the tag on either side), `DemotedAt *time.Time` / `DemotedReason string` (present for a value whose standing was revoked rather than one born provisional)
- `ProvisionalTagListResponse` — `Results []ProvisionalTagResponse`, `Total int64` (provisional tags matching the active filters, independent of the page), `PendingIdentityTotal int64` (unresolved identity claims across the whole queue — the "Reevaluate identity" affordance)

## `CrudServices` (`internal/service/crud.go`)

### Structs

- `CrudServices` — `Batch *service.Batch`, `People *service.People`, `PeopleType *service.PeopleType`, `DocumentType *service.DocumentType`, `User *service.User`, `Orphaned *service.Orphaned`, `ErroredFiles *service.ErroredFiles`, `ReEnrich *service.ReEnrich`, `Trash *service.Trash`. The tag services (`Tag`, `TagIdentity`) live in `internal/tags/curation` and are held by the server and the handlers that use them.
  - `Close() error` — Closes the members that own resources by name (today only `Trash`, whose `Close` stops the background purge) and returns the joined close failures. Nil-safe for compositions that omit `Trash`, such as the container's. A member that starts owning resources is added to this explicit list.

## DTOs (`handlers/user.go`)

### Structs

- `CreateUserRequest` — `Username string`, `Password string`, `Role string` (omitempty — defaults to `"viewer"`)
- `UpdateUserRequest` — `Username string`, `Password string` (omitempty — leave blank to keep current password), `Role string` (omitempty — leave blank to keep current role)
- `UserResponse` — `ID int64`, `Username string`, `Role string`, `CreatedAt string` (RFC 3339), `HasAPIKey bool`, `APIKeyPrefix *string` (omitempty), `APIKeyCreatedAt *string` (omitempty). Excludes `password_hash` and `api_key_hash`.
- `UserListResponse` — `Users []UserResponse`, `Total int64`
- `CreateAPIKeyResponse` — `APIKey string` (raw key, returned only at creation), `Prefix string` (display-safe prefix `ek_` + first 9 hex chars), `Message string`
- `APIKeyStatusResponse` — `HasAPIKey bool`, `APIKeyPrefix *string` (omitempty), `APIKeyCreatedAt *string` (omitempty)

## DTOs (`handlers/people.go`)

### Structs

- `CreatePersonRequest` — `Name string`, `NameNative string`
- `UpdatePersonRequest` — `Name string`, `NameNative string`
- `PersonResponse` — `ID int64`, `Name string`, `NameNative string`, `PersonTypeID int64`, `PersonTypeName string`, `PersonTypeDescription string`, `DocumentCount int64`
- `PersonListResponse` — `Results []PersonResponse`, `Total int64`
- `CreatePeopleTypeRequest` — `Name string`, `Description string`
- `UpdatePeopleTypeRequest` — `Name string`, `Description string`
- `PeopleTypeResponse` — `ID int64`, `Name string`, `Description string`

## DTOs (`handlers/document_type.go`)

### Structs

- `CreateDocumentTypeRequest` — `Name string`, `Description string`
- `UpdateDocumentTypeRequest` — `Name string`, `Description string`
- `DocumentTypeResponse` — `ID int64`, `Name string`, `Description string`, `DocumentCount int64`

---

## DTOs (`handlers/saved_search.go`)

### Structs

- `CreateSavedSearchRequest` — `Name string`, `Filter json.RawMessage`
- `SavedSearchResponse` — `ID int64`, `Name string`, `Filter json.RawMessage`, `CreatedAt string`

---

## DTOs (`handlers/document.go`, additions)

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

// LLM model catalog (reads viewer; catalog mutations and reset admin):
mux.Handle("GET /api/v1/llm/models", RequireRole(viewer...)(llmHandler.ListModels))
mux.Handle("GET /api/v1/llm/catalog", RequireRole(viewer...)(llmHandler.GetCatalog))
mux.Handle("POST /api/v1/llm/providers", RequireRole(admin...)(llmHandler.AddProvider))
mux.Handle("PUT /api/v1/llm/providers/{name}", RequireRole(admin...)(llmHandler.SetProvider))
mux.Handle("DELETE /api/v1/llm/providers/{name}", RequireRole(admin...)(llmHandler.RemoveProvider))
mux.Handle("POST /api/v1/llm/models", RequireRole(admin...)(llmHandler.AddModel))
mux.Handle("PUT /api/v1/llm/models/{provider}/{model}", RequireRole(admin...)(llmHandler.SetModel))
mux.Handle("DELETE /api/v1/llm/models/{provider}/{model}", RequireRole(admin...)(llmHandler.RemoveModel))
mux.Handle("POST /api/v1/llm/reset", RequireRole(admin...)(llmHandler.ResetCatalogs))

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
mux.Handle("GET /api/v1/tags/provisional", RequireRole(viewer...)(...))
mux.Handle("GET /api/v1/tags/siblings", RequireRole(viewer...)(...))
mux.Handle("POST /api/v1/tags/identity/reevaluate", RequireRole(editor...)(...))
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
mux.Handle("POST /api/v1/documents/batch-type", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/tags", RequireRole(editor...)(...))
mux.Handle("PUT /api/v1/tags/{id}", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/tags/{id}", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/tags/{id}/promote", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/tags/{id}/demote", RequireRole(editor...)(...))
mux.Handle("GET /api/v1/tags/deleted", RequireRole(editor...)(...))
mux.Handle("DELETE /api/v1/tags/deleted/{name}", RequireRole(editor...)(...))
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
mux.Handle("POST /api/v1/batches/{id}/resume", RequireRole(editor...)(...))
mux.Handle("POST /api/v1/batches/{id}/cancel", RequireRole(editor...)(...))
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
- [Task System](task-system.md) — Enqueuer and task lifecycle used by the consume handler
- [Pipeline](pipeline.md) — Consumption and enrichment engines triggered via API
- [Database](database.md) — Document and task queries used by handlers
- [Frontend](frontend.md) — SvelteKit SPA that consumes these API endpoints
- [Config & Utils](config-and-utils.md) — Config setup functions and response types

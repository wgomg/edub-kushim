# Development Roadmap

> **Development focus**: LLM-based automatic classification (tags, title, people, document type), followed by the web UI layer.

## ✅ Complete

> **Legend:** ✓ = verified working · ◐ = exists but has gaps · ✗ = not yet implemented

### Core Infrastructure

- HTTP server with middleware (request ID, parameter parsing via `ParamBag`)
- Database layer with sqlc-generated type-safe queries (SQLite, WAL mode)
- Configuration system with YAML support, default values, and validation
- Structured logging with request correlation, file logging, numeric level filtering
- CLI framework with dependency injection container (lazy DB, cache, dispatcher, pools)
- Per-tool timeout via `ToolConfig` struct + `runWithTimeout` generic wrapper
- Configurable worker pool size (`consumer.workers`, `enricher.workers`, default 1 each)
- Database-backed batch ownership with heartbeats (`batch_owner` table, stale lease detection, `--force` override, server/CLI isolation via per-process owner UUID)
- Matcher as external process: Hugot embedding model runs as `kushim hugot` over Unix domain socket RPC
- Worker forking: `kushim queue` daemon forks `kushim consume --batch` for processing; `edub` only enqueues tasks (`CGO_ENABLED=0`)
- Matcher RPC protocol: encode, match, consolidate, add-to-store, remove-from-store operations over HTTP/Unix socket
- Queue-driven batch processing: API creates `queued` batches; `kushim queue` consumes them with configurable concurrency (`server.max_concurrent_batches`)
- `internal/configtask/` — ConfigTaskHandler (downloads tessdata/Hugot model in background, registered as "config" task type)
- `internal/utils/files.go` — `ListFilePaths` scans inbox directories with MIME detection (replaced `internal/fileresolver/`)
- Config handler `AdoptBatch` renamed to `ResumeBatch`, now enqueues for queue daemon
- **User accounts**: CRUD with bcrypt password hashing (`golang.org/x/crypto/bcrypt`), user list page in settings UI (tab bar: Configuration / Users), 5 API endpoints, 13 handler tests

### Document Pipeline

- UUID-based `document_id` column added to document table for external identification and log correlation
- Document consumption pipeline: scan inbox → extract text → OCR fallback → optimize → store
- Text extraction via MuPDF CGo wrapper (default), gopdf (pure Go), or pdftotext (external tool)
- OCR via gosseract (Tesseract + MuPDF at 200 DPI) or ocrmypdf (external tool)
- Searchable PDF generation with invisible text layer (PDF text rendering mode 3)
- PDF optimization via MuPDF (CGo wrapper) or Ghostscript (external tool)
- Database reset capability (`kushim setup --reset-database`) — drops all tables and re-runs schema + seeders
- MD5 → SHA512 two-step duplicate detection
- Dual storage: originals preserved alongside processed/OCR'd versions
- Date-based storage organization (`year/month/day/hour/documentID.ext`)
- Database transaction with rollback on file-operation failure
- Deferred cleanup of temporary files
- Embedded Tesseract language data (`eng.traineddata`)
- OCR subprocess isolation: gosseract adapter forks `kushim internal-ocr` to prevent heartbeat starvation during image-only PDF processing
- Custom MuPDF 1.27.2 CGo wrapper (document open/close, page rendering, text extraction, `pdf_clean_file`)

### Enrichment Pipeline

- Async enrichment pipeline: consume → enqueue enrich → text reduction → tag matching → LLM classification → tag consolidation
- LLM providers: OpenAI, Anthropic, DeepSeek, Ollama (via `ContentAnalyzer` interface)
- Text reduction via TextRank extractive summarization (TF-IDF, weighted PageRank, diversity penalty)
- Semantic tag matching via Hugot (Go or ONNX backend), cosine similarity, chunked encoding
- Dual text reduction: separate `target_words` for LLM and `reduce_target_words` for tag matching
- Post-LLM tag consolidation via `Consolidate` (fixes casing, hyphenation, synonym mismatches)
- Tag embedding cache (`BuildTagCache`) — pre-computed tag embeddings at startup
- **New tag cache update**: newly created tags during enrichment are immediately encoded and added to the embedding cache
- **Classification persistence**: enrichment writes to `document_tag`, `document_people`, `document.document_type_id` including auto-creating new tags/people as needed
- Seeded tag vocabulary (110+ Dewey Decimal tags via `sql/seed-tags.sql`)
- Seeded document types (`sql/seed-document-types.sql`)
- Seeded people types (`sql/seed-people-types.sql`)
- Token usage stats and prompt logging

### Search & Retrieval

- SQLite FTS5 full-text search with `unicode61` tokenizer
- Manual FTS5 query layer (`fts5.go`) — sqlc doesn't support FTS5 syntax
- Query sanitization layer (`search.Engine`) — wraps user input as phrase literals
- BM25 relevance ranking, snippet highlighting
- Automatic FTS index sync via SQLite triggers (INSERT, UPDATE, DELETE)
- `RebuildDocumentFTS` for disaster recovery
- **Structured search** (`POST /api/v1/documents/search`) — dynamic SQL query builder with filters for tags, people, document type, language, MIME type, date range, file size
- **Search engine** (`internal/search/search.go`) — `Engine.SearchStructured()` returning results + total count
- **Database query builder** (`internal/database/structured_search.go`) — dynamic `WHERE` clause composition with proper parameterization, batch tag/people fetching
- **Autocomplete endpoints** — prefix search for tags (`SearchTagsByName`), people (`SearchPeopleByName`), document types, person types
- **Saved searches** — `saved_search` table, CRUD API, frontend save/load/delete
- **Frontend query parser** (`searchFilter.js`) — tokenizes `field:value` syntax into structured filter state

### Task System

- Generic `task.Dispatcher` + `pool.Pool` with `task.Handler` / `Dedupable` interfaces
- Task lifecycle: `waiting` → `pending` → `processing` → `completed` / `failed` / `cancelled` / `discarded`
- Linked task pairs: enrich tasks start `waiting` until their consume task completes
- Batch cancellation: cancels pending tasks, sends SIGTERM, marks in-flight as cancelled
- Retry support for failed tasks
- Dedup prevention via unique partial index on active tasks (`task_type`, `dedup_key`)

### API Endpoints

| Endpoint                               | Description                                                 |
| -------------------------------------- | ----------------------------------------------------------- |
| `GET /health`                          | Health check                                                |
| `GET /api/v1/documents`                | List documents (sortable, paginated)                        |
| `GET /api/v1/documents/{id}`           | Get document with tags, people                              |
| `GET /api/v1/documents/{id}/file`      | Download PDF file                                           |
| `GET /api/v1/documents/search`         | FTS5 search with snippets                                   |
| `POST /api/v1/documents/search`        | Structured search (tags, people, dates, size)               |
| `PUT /api/v1/documents/{id}`           | Update document metadata (title, type, language)            |
| `DELETE /api/v1/documents/{id}`        | Delete document + files                                     |
| `POST /api/v1/documents/{id}/tags`     | Add tag assignment to document                              |
| `DELETE /api/v1/documents/{id}/tags`   | Remove tag assignment from document                         |
| `POST /api/v1/documents/{id}/people`   | Add person assignment to document                           |
| `DELETE /api/v1/documents/{id}/people` | Remove person assignment from document                      |
| `POST /api/v1/documents/batch-delete`  | Batch delete documents (with partial failure reporting)     |
| `POST /api/v1/documents/batch-tags`    | Batch assign tags (add or replace mode, with transaction)   |
| `GET /api/v1/tags`                     | List/autocomplete tag names (`?q=`, `limit`, `offset`)      |
| `POST /api/v1/tags`                    | Create tag                                                  |
| `PUT /api/v1/tags/{id}`                | Update tag name                                             |
| `DELETE /api/v1/tags/{id}`             | Delete tag                                                  |
| `GET /api/v1/people`                   | List/autocomplete people names (`?q=`, `limit`, `offset`)   |
| `POST /api/v1/people`                  | Create person                                               |
| `PUT /api/v1/people/{id}`              | Update person name, native name                             |
| `DELETE /api/v1/people/{id}`           | Delete person (CASCADE to document_people)                  |
| `GET /api/v1/people-types`             | List/autocomplete person types (`?q=`, `limit`, `offset`)   |
| `POST /api/v1/people-types`            | Create person type                                          |
| `PUT /api/v1/people-types/{id}`        | Update person type name, description                        |
| `DELETE /api/v1/people-types/{id}`     | Delete person type (409 if referenced)                      |
| `GET /api/v1/document-types`           | List/autocomplete document types (`?q=`, `limit`, `offset`) |
| `POST /api/v1/document-types`          | Create document type with description                       |
| `PUT /api/v1/document-types/{id}`      | Update document type name, description                      |
| `DELETE /api/v1/document-types/{id}`   | Delete document type (409 if referenced)                    |
| `GET /api/v1/saved-searches`           | List saved searches                                         |
| `POST /api/v1/saved-searches`          | Create saved search                                         |
| `DELETE /api/v1/saved-searches/{id}`   | Delete saved search                                         |
| `GET /api/v1/users`                    | List users (paginated, excludes password_hash/api_key_hash)      |
| `GET /api/v1/users/{id}`               | Get single user                                             |
| `POST /api/v1/users`                   | Create user (username + bcrypt password, min 12 chars)      |
| `PUT /api/v1/users/{id}`               | Update username + optional password                         |
| `DELETE /api/v1/users/{id}`            | Delete user                                                 |
| `POST /api/v1/users/{id}/api-key`      | Generate API key (returns raw key once, 201)                |
| `DELETE /api/v1/users/{id}/api-key`     | Revoke API key (204)                                        |
| `PUT /api/v1/users/{id}/api-key`        | Rotate API key (overwrites, returns new key, 200)           |
| `GET /api/v1/users/{id}/api-key`        | Get API key status (has_api_key, prefix, created_at)        |
| `POST /api/v1/auth/login`              | Authenticate user, return JWT token + user profile          |
| `POST /api/v1/auth/logout`             | No-op (client-side token discard)                           |

| `POST /api/v1/consume` | Scan inbox, enqueue files, create queued batch |
| `POST /api/v1/consume/upload` | Upload files via multipart, create queued batch |
| `GET /api/v1/tasks` | List tasks (batch, status filters) |
| `GET /api/v1/tasks/{id}` | Get single task |
| `GET /api/v1/batches` | List batch summaries |
| `GET /api/v1/batches/{id}` | Get single batch summary |
| `POST /api/v1/batches/{id}/resume` | Resume batch (enqueues for queue daemon) |
| `POST /api/v1/batches/{id}/cancel` | Cancel batch (SIGTERM worker, mark tasks cancelled) |
| `GET /api/v1/dashboard` | Dashboard data: recent batches + activity timeline + document analytics (language/type/tag distributions, missing counts) + storage panel (totals, MIME breakdown, storage trend, pages, words) |
| `GET /api/v1/errored` | List errored files from disk (`errors/` and `errors/duplicated/`) |
| `GET /api/v1/errored/download` | Download single errored file (with path traversal guard) |
| `DELETE /api/v1/errored` | Delete single errored file |
| `POST /api/v1/errored/delete-all` | Delete all errored files |
| `GET /api/v1/logs/{name}` | Read log file: whitelisted names (kushim, edub, hugot, queue), `?lines=N` param (100–5000), tail-read for >2 MiB, multi-line entry merge via timestamp prefix detection |

### CLI Commands

| Command                       | Description                                            |
| ----------------------------- | ------------------------------------------------------ |
| `kushim setup`                | Generate config, download models, init DB              |
| `kushim consume`              | Scan inbox, enqueue, direct-fallback if queue empty    |
| `kushim consume --batch <id>` | Resume existing batch                                  |
| `kushim consume --batch <id>` | Resume existing batch                                  |
| `kushim consume --force`      | Override stale PID lock                                |
| `kushim consume cancel <id>`  | Cancel running batch                                   |
| `kushim search`               | FTS5 search with ANSI highlighting                     |
| `kushim task list`            | List tasks with filters                                |
| `kushim task status <id>`     | Show task details                                      |
| `kushim task retry <id>`      | Reset failed task to pending                           |
| `kushim version`              | Print version                                          |
| `kushim hugot`                | Start matcher RPC server over Unix socket              |
| `kushim backup`               | Create a backup of database, config, and storage files |
| `kushim restore`              | Restore from a backup archive                          |
| `edub`                        | Start API server                                       |
| `edub version`                | Print server version                                   |

### Web UI

- SvelteKit SPA with embedded Go binary (`//go:embed`)
- CSS custom properties design system (clay/gold/lapis/parchment palette)
- Reusable `DataTable` component (sortable columns, paginated, configurable page sizes, total count)
- API client module (`src/lib/api.js`) — documents, tasks, batches, summary, health, autocomplete, saved searches
- Hot-reload dev server (`npm run dev`), production build via `make web-build`
- **Structured search UI**: `SearchBar.svelte` with chip display and autocomplete suggestions
- **Filter panel**: `FilterPanel.svelte` — collapsible panel with tags, people, document type, language, MIME type, date range, file size filters
- **Filter state management**: shared reactive store (`filterStore.js`) + query parser (`searchFilter.js`)
- **Saved searches**: save/load/delete search configurations via API

#### Web UI — Page Status

| Route                 | Status | Notes                                                                                                                                                                                                                                                                                  |
| --------------------- | ------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/` (dashboard)       | ✓      | Health + summary stats + recent docs + batch overview panel + activity timeline                                                                                                                                                                                                        |
| `/documents`          | ✓      | Structured search bar + filter panel + saved searches + sortable DataTable                                                                                                                                                                                                             |
| `/documents/[id]`     | ✓      | PDF preview + editable metadata sidebar (title, type, language, tags, people) + delete                                                                                                                                                                                                 |
| `/settings`           | ✓      | Full configuration form: server host/port, OCR, consumer, text extractor, PDF optimizer, enricher, content analyzer (LLM) with tokens, tag matcher, text reducer, backup (enabled, interval, time, keep, path), Users tab (DataTable + create/edit/delete modals), logout confirmation |
| `/login`              | ✓      | Centered card form with username/password fields, auth-live error display, loading spinner, redirects to dashboard on success                                                                                                                                                          |
| `/tags`               | ✓      | List, filter, create, edit, delete tags                                                                                                                                                                                                                                                |
| `/people`             | ✓      | Two tabs: People (name + native name) and Person Types (name + description)                                                                                                                                                                                                            |
| `/document-types`     | ✓      | List, create, edit, delete document types                                                                                                                                                                                                                                              |
| `/tasks`              | ✓      | Batch list + task drill-down                                                                                                                                                                                                                                                           |
| `/tasks/[id]`         | ✓      | Task detail with status badge, batch/doc links, timestamps, error display, retry action                                                                                                                                                                                                |
| `/documents/orphaned` | ✓      | Two tabs: Orphaned (scan, list, delete, restore, move-to-inbox) and Errored (list, download, delete, delete-all from disk)                                                                                                                                                             |
| `/logs`               | ✓      | Tabbed log viewer (Kushim, Edub, Hugot, Queue), lines control, auto-refresh, monospace color-coded rendering with expandable long lines, multi-line entry merge, jump-to-bottom                                                                                                        |

### Quality

- ✓ Database integration tests (17 tests) — document/tag/people CRUD, task lifecycle, enrich flow, batch ownership, FTS-adjacent operations, saved searches
- ✓ Search engine tests (7 tests) — FTS5 search, structured search, pagination, query sanitization
- ✓ Task system tests (14 tests) — Store, dispatcher, runner, pool lifecycle, dedup key handling
- ✓ API handler tests (62 tests) — health, document CRUD, tag/people CRUD, user CRUD, task endpoints, saved searches, concurrent operations, auth login/logout, token claims, errored file list/download/delete/delete-all, logs viewer (invalid name, file not found, success, lines clamping, large file tail, empty file), API key generate/revoke/rotate/status
- ✓ Auth package tests (6 tests) — session secret generation, JWT generation/validation, wrong secret, expired token, malformed token
- ✓ Auth middleware tests (12 tests) — public path bypass, missing/invalid/valid token, wrong secret, missing bearer prefix, empty header, disabled flag passes all paths, valid API key, invalid API key, wrong prefix falls through, auth disabled bypasses, internal error returns 500
- ✓ Consumption pipeline tests (11 tests) — full consume flow with mock runner, file I/O, duplicate detection, error paths
- ✓ `internal/testutil` package — assertion helpers, PDF fixtures, mock embedder
- ✓ `ErroredFiles` service tests (11 tests) — list from both dirs, path resolution and traversal blocking, delete and delete-all with empty/missing dirs
- ✓ `ErroredHandler` handler tests (8 tests) — list empty/with-data, download missing params, delete success/not-found/missing-params, delete-all
- ✓ `Makefile` test targets (`make test`, `make test-verbose`)
- ✗ No tests for CLI commands (kushim consume, search, task, setup)

### Build & Deployment

- Two binaries: **kushim** (CLI) + **edub** (API server)
- `edub` built with `CGO_ENABLED=0` (pure Go, no C dependencies)
- `kushim` retains static CGo linking for MuPDF, Tesseract, Leptonica, libpng
- Pre-built `libtokenizers.a` for Hugot Go backend
- Containerized builds for glibc and musl (`make build-glibc`, `make build-musl`)
- Deployment image (`make build-tools-image`)
- **Docker Compose quick-start** (`docker compose up`) — multi-stage build from source, zero host-side toolchain required
- Go build tags: `XLA`, `ORT` for Hugot ONNX support
- `make web-build` target for UI embedding

---

## 🟢 Roadmap (Priority Order)

> **Core differentiator achieved**: Automatic classification via LLM providers (tags, title, people,
> document type) with semantic tag matching

| #   | Feature                                                | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| --- | ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | **Configuration/setup wizard in web UI**               | ✓ In-app configuration page (LLM providers, Tesseract languages, storage paths, model downloads)                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| 2   | **Continue interrupted tasks**                         | ✓ Manual retry for failed tasks (API + web UI). Wizard auto-resumes on restart, re-submission does not duplicate tasks, failed config downloads surfaced with retry action.                                                                                                                                                                                                                                                                                                                                                              |
| 3   | **Docker Compose quick-start**                         | ✓ Single `docker compose up` command runs the full stack — multi-stage Dockerfile builds everything from source, entrypoint handles first-boot setup and conditional apt installs                                                                                                                                                                                                                                                                                                                                                        |
| 4   | **Document detail — tags/people/type**                 | ✓ Display tags, people, and document type in the sidebar                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| 5   | **Document metadata editing**                          | ✓ Detail page sidebar — edit tags, people, type, title (override LLM)                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| 6   | **Document update endpoint**                           | ✓ `PUT /api/v1/documents/{id}` — update title, document_type_id, text_content                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| 7   | **Document delete endpoint**                           | ✓ `DELETE /api/v1/documents/{id}` — remove document + files                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| 8   | **Document tag/people assignment**                     | ✓ `POST/DELETE /api/v1/documents/{id}/tags` and `/people` — junction management                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| 9   | **Tags CRUD API**                                      | ✓ `GET/POST/PUT/DELETE /api/v1/tags` with `TagService` batch CRUD, auto-encoding, cache sync                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| 10  | **People CRUD API**                                    | ✓ `GET/POST/PUT/DELETE /api/v1/people` + `/api/v1/people-types` — `PeopleService` + `PeopleTypeService` batch CRUD                                                                                                                                                                                                                                                                                                                                                                                                                       |
| 11  | **Document Types CRUD API**                            | ✓ `GET/POST/PUT/DELETE /api/v1/document-types` — `DocumentTypeService` batch CRUD                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| 12  | **Tag management page**                                | ✓ List, create, edit, delete tags with filter input + conflict detection                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| 13  | **People management page**                             | ✓ Two-tab route: People (name, native name, cascade delete) and Person Types (name, description, 409 handling)                                                                                                                                                                                                                                                                                                                                                                                                                           |
| 14  | **Document type management page**                      | ✓ List, create, edit, delete document types with 409 conflict handling                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 15  | **Document upload/consume flow**                       | ✓ Wire Upload button to `POST /api/v1/consume/upload`, show progress feedback via modal                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| 16  | **Single task detail page**                            | ✓ New route `/tasks/{taskID}` — status badge, batch/doc links, timestamps, error display, retry action                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 17  | **Integration test**                                   | ✓ End-to-end consumption test (mock runner), DB CRUD, search, task system, API handlers — covering database queries, search engine, pool, enrichment flow                                                                                                                                                                                                                                                                                                                                                                                |
| 18  | **Test coverage**                                      | ✓ Database queries, API handlers, search engine, task system (store/runner/dispatcher/pool), consumption pipeline, storage layer, orphaned service, errored file service and handler. ✗ Adapters, CLI commands (existing gap)                                                                                                                                                                                                                                                                                                            |
| 19  | **Document download**                                  | ✓ Download individual documents from the document list table. Batch download with configurable limits (max file count and/or total accumulated size).                                                                                                                                                                                                                                                                                                                                                                                    |
| 20  | **Bulk operations**                                    | ✓ Batch delete, batch tag assignment                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| 21  | **Batch cancel API endpoint**                          | ✓ `POST /api/v1/batches/{id}/cancel` — expose `kushim consume cancel` through the API                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| 22  | **Dashboard: storage panel**                           | ✓ Total size, MIME type breakdown (count + size per type), cumulative storage trend by day/week, average file size, total pages, total words                                                                                                                                                                                                                                                                                                                                                                                             |
| 23  | **Dashboard: batch overview panel**                    | ✓ Recent N batches with per-batch task summary, active/orphaned state, batch source, creation time, duration when complete                                                                                                                                                                                                                                                                                                                                                                                                               |
| 24  | **Dashboard: activity timeline**                       | ✓ Chronological feed: document uploaded, task completed, task failed, batch created — merged from document.created_at, task.completed_at, batch.created_at                                                                                                                                                                                                                                                                                                                                                                               |
| 25  | **Dashboard: document analytics panel**                | ✓ Language distribution, document type distribution, tag frequency (top N), documents without tags/type/language counts                                                                                                                                                                                                                                                                                                                                                                                                                  |
| 26  | **Dashboard: processing health panel**                 | ✓ Task success rate (completed vs failed, last 7 days), avg task duration, active batch count, orphaned batch count, missing tools count                                                                                                                                                                                                                                                                                                                                                                                                 |
| 27  | **Dashboard: summary API**                             | ✓ Single `GET /api/v1/dashboard` endpoint returning all panel data — built from GROUP BY/JOIN queries on existing schema, no migrations needed                                                                                                                                                                                                                                                                                                                                                                                           |
| 28  | **User accounts**                                      | ✓ User CRUD, bcrypt password hashing, user list page in settings UI                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 29  | **Login and session management**                       | ✓ Login/logout endpoints, JWT session tokens, auth middleware, protected API routes, login page, auth guard in root layout                                                                                                                                                                                                                                                                                                                                                                                                               |
| 30  | **Orphaned file management**                           | ✓ Detect, quarantine, list, delete, restore, and re-ingest orphaned files via API, CLI (`kushim storage orphans`), and WebUI. Startup + post-batch auto-scan. SQLite-backed tracking table with soft-delete.                                                                                                                                                                                                                                                                                                                             |
| 31  | **Batch service**                                      | ✓ Create `internal/service/batch.go` — `BatchSummary`, `BatchOverview`, `CancelResult` domain types. All batch operations (GetSummary, ListSummaries, ListOverviews, CountOrphaned, RetryFailed, Create, BeginCancel/CompleteCancel, CountDistinct, ActiveIDs, HasPendingWork, IsLockedByLiveOwner) moved from handlers/helpers to the service. 12 service-level tests verify owner-state derivation, orphaned flag, cancel flow. Dead code (`CountBatchStatuses`, `ListBatchSummaries`, `RetryBatchFailed`, `BatchOwnerState`) removed. |
| 32  | **Batch queue schema and queries**                     | ✓ Migration adds `status` column to batch table with backfill, 10 new SQL queries (CountQueuedBatches, GetNextQueuedBatch, RequeueBatch, SetBatchProcessing/Completed/Failed/Cancelled, CountLiveBatches, ListStaleBatchOwners), sqlc regeneration, service wrappers, `CompleteCancel` sets `cancelled`. API response types include `Status`.                                                                                                                                                                                            |
| 33  | **`kushim queue` daemon**                              | ✓ New daemon CLI command (sibling to `kushim hugot`). Runs two loops: (a) queue consumption — `GetNextQueuedBatch`, fork `kushim consume --batch <id>` when `CountLiveBatches < MaxConcurrentBatches`; (b) stale reclamation — `ListStaleBatchOwners` (>15s heartbeat with active tasks), reset processing→pending, requeue, delete stale owner. `--bg` to daemonize. PID file, signal handling, zombie reaping, orphaned-batch recovery via pre-fork owner row.                                                                         |
| 34  | **Move inbox polling to `kushim queue`**               | ✓ Remove `PollingScheduler` from edub (`internal/scheduler/`). Embed inbox scanning loop in `kushim queue` as a third loop. Before enqueueing: check `CountQueuedBatches + CountLiveBatches < MaxConcurrentBatches`. Respects existing `consumer.polling` config (interval, windows, enabled). New `config.Reload` function enables live config re-read on each poll tick. `consumption.ScanAndEnqueue` shared function supports future handler/CLI unification.                                                                         |
| 35  | **API endpoints enqueue, no fork**                     | ✓ `POST /api/v1/consume` and `POST /api/v1/consume/upload` create batch with `status='queued'`, return 202 with batch ID. Remove `forkWorker()` and all semaphore acquire/release from `ConsumeHandler`. Remove `semaphore` field from handler and constructor. Remove semaphore creation and `registerRoutes` parameter from `server.go`. Inbox scanning and dedup unchanged.                                                                                                                                                           |
| 36  | **API batch resume enqueues**                          | ✓ `POST /api/v1/batches/{id}/resume` sets batch status to `queued`, resets processing→pending tasks. Queue daemon picks it up instead of direct forking.                                                                                                                                                                                                                                                                                                                                                                                 |
| 37  | **Delete `pool.Semaphore`**                            | ✓ Remove `internal/pool/semaphore.go`. No remaining call sites after features 34–36. `MaxConcurrentBatches` config stays for queue daemon (reads via `kushim queue` from shared config).                                                                                                                                                                                                                                                                                                                                                 |
| 38  | **CLI `kushim consume` enqueues with direct-fallback** | ✓ Scans inbox, creates batch with `status='queued'`. If queue was empty before enqueueing, falls back to direct processing (current behavior). If queue was non-empty, prints "Batch <id> queued — start `kushim queue` to process". Remove `--bg` flag. `--batch <id>` unchanged for recovery.                                                                                                                                                                                                                                          |
| 39  | **Upgrade MuPDF to 1.28.0**                            | ✓ Rebuild kushim against MuPDF 1.28.0 (which includes Bug 708689 fix + broader `fz_var` initialization), rerun the crash-test on all 4 known files. If the crash disappears, the upstream fix is confirmed — no filing needed. See [report](.kilo/reports/2026-07-02-mupdf-uaf-pdfclean-crash-loop.md).                                                                                                                                                                                                                                  |
| 40  | **Isolate `PdfCleanFile` in a subprocess**             | ✓ Add a hidden subcommand (e.g. `kushim internal-mupdf-clean`) that calls `PdfCleanFile` and exits. Have `mupdf.go`'s `Optimize()` shell out to it via `exec.CommandContext`, so a SIGSEGV kills only the subprocess and returns a normal Go error — making the Ghostscript fallback (`runner.go:205-227`) reachable for these files. See [report](.kilo/reports/2026-07-02-mupdf-uaf-pdfclean-crash-loop.md).                                                                                                                           |
| 41  | **Cap retry count on batch task reset**                | ✓ `ResetProcessingTasksByBatch` now quarantines tasks to `failed` after `max_retries` consecutive reclamation cycles. Per-task `attempts` counter with configurable `consumer.reclaim.max_retries` (default 3). Counter resets on successful completion or explicit user retry. See [report](.kilo/reports/2026-07-02-mupdf-uaf-pdfclean-crash-loop.md).                                                                                                                                                                                 |
| 42  | **Signal orphaned children in stale reclamation**      | ✓ `reclaimStaleBatches` signals (SIGTERM) the previous owner's PID before resetting tasks, matching `consumeCancelHandler`'s existing pattern. `ListStaleBatchOwners` query extended to return `owner_id` and `pid`. This bounds resource pileup for any future cause of staleness. See [report](.kilo/reports/2026-07-02-mupdf-uaf-pdfclean-crash-loop.md).                                                                                                                                                                             |
| 43  | **Orphan Restore creates proper batch**                | ✓ `Restore()` creates batch record with `status='queued'`, creates matched consume+enrich task pair (fixes missing enrich task bug). No direct forking — queue daemon picks it up. `MoveToInbox` path unchanged.                                                                                                                                                                                                                                                                                                                         |
| 44  | **Auto-detect OCR languages from LLM**                 | ✓ LLM-detected document language from enrichment is cross-checked against configured OCR languages. Missing languages are added to config automatically, improving OCR accuracy without manual intervention. See [report](.kilo/reports/auto-detect-ocr-languages.md).                                                                                                                                                                                                                                                                   |
| 45  | **Setup creates initial admin user**                   | ✓ `kushim setup` (CLI + wizard) prompts for admin username + password before completion, creates user via `service.User.Create()`. Session secret auto-generated and persisted during setup.                                                                                                                                                                                                                                                                                                                                             |
| 46  | **API keys**                                           | ✓ Key generation/revocation/rotation, Bearer token validation middleware, per-user key management. SHA-256 hashed storage with `ek_` prefix, `AuthSourceKey` context value for audit logging. See [plan](/.kilo/plans/1783222883958-api-keys.md) |
| 47  | **Roles and permissions**                              | RBAC schema with admin/editor/viewer roles, permission enforcement middleware, role assignment                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| 48  | **Document notes/comments**                            | User-added notes and annotations on documents                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| 49  | **Pre-built binaries**                                 | Release binaries for major platforms (Linux amd64/arm64)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| 50  | **MySQL / MariaDB database backend**                   | Additional database backend support                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 51  | **User preferences**                                   | Theme, pagination defaults, notification settings                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| 52  | **Email ingestion**                                    | IMAP inbox scanning                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 53  | **Document relationships**                             | Parent/child, cross-references between documents                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| 54  | **Contributing guide & issue templates**               | `CONTRIBUTING.md`, issue/PR templates, and GitHub community setup to encourage contributions                                                                                                                                                                                                                                                                                                                                                                                                                                             |

---

## 🔵 Icebox

Low-priority items worth revisiting when nothing more impactful demands attention.

| #   | Feature                                     | Description                                                                                                                                                                                               |
| --- | ------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | **Double tokenization in chunked encoding** | `Encode()` and `encodeChunked()` redundantly tokenize the same text; per-chunk decode→re-encode round-trip adds unnecessary overhead. See [report](research/double-tokenization-chunked-encoding.md).     |
| 2   | **External search engine**                  | ZincSearch or Meilisearch integration for large-scale search                                                                                                                                              |
| 3   | **Metrics and monitoring**                  | Prometheus endpoints, structured metrics                                                                                                                                                                  |
| 4   | **Post-classification notification**        | Optional webhook or websocket event when a classification batch completes. Deferred: lacks auth, multi-tenancy, and notification infrastructure. A long-polling wait endpoint is a preferable first step. |

---

## See Also

- [Architecture & Design](architecture.md) — Core design principles and pipeline narrative
- [Code Reference](reference/overview.md) — Detailed implementation reference per package
- [Testing Reference](reference/tests.md) — Test infrastructure, patterns, and how to run

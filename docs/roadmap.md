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
- Worker forking: `edub` enqueues tasks and forks `kushim consume --batch` for processing; `edub` is pure Go (`CGO_ENABLED=0`)
- Matcher RPC protocol: encode, match, consolidate, add-to-store, remove-from-store operations over HTTP/Unix socket
- Semaphore-based batch concurrency limiting (`server.max_concurrent_batches`, default 2)
- `internal/configtask/` — ConfigTaskHandler (downloads tessdata/Hugot model in background, registered as "config" task type)
- `internal/utils/files.go` — `ListFilePaths` scans inbox directories with MIME detection (replaced `internal/fileresolver/`)
- Config handler `AdoptBatch` renamed to `ResumeBatch`, now forks kushim worker
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
- Date-based storage organization (`year/month/day/documentID.ext`)
- Database transaction with rollback on file-operation failure
- Deferred cleanup of temporary files
- Embedded Tesseract language data (`eng.traineddata`)
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

| Endpoint                               | Description                                                                                                                                                                                     |
| -------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /health`                          | Health check                                                                                                                                                                                    |
| `GET /api/v1/documents`                | List documents (sortable, paginated)                                                                                                                                                            |
| `GET /api/v1/documents/{id}`           | Get document with tags, people                                                                                                                                                                  |
| `GET /api/v1/documents/{id}/file`      | Download PDF file                                                                                                                                                                               |
| `GET /api/v1/documents/search`         | FTS5 search with snippets                                                                                                                                                                       |
| `POST /api/v1/documents/search`        | Structured search (tags, people, dates, size)                                                                                                                                                   |
| `PUT /api/v1/documents/{id}`           | Update document metadata (title, type, language)                                                                                                                                                |
| `DELETE /api/v1/documents/{id}`        | Delete document + files                                                                                                                                                                         |
| `POST /api/v1/documents/{id}/tags`     | Add tag assignment to document                                                                                                                                                                  |
| `DELETE /api/v1/documents/{id}/tags`   | Remove tag assignment from document                                                                                                                                                             |
| `POST /api/v1/documents/{id}/people`   | Add person assignment to document                                                                                                                                                               |
| `DELETE /api/v1/documents/{id}/people` | Remove person assignment from document                                                                                                                                                          |
| `POST /api/v1/documents/batch-delete`  | Batch delete documents (with partial failure reporting)                                                                                                                                         |
| `POST /api/v1/documents/batch-tags`    | Batch assign tags (add or replace mode, with transaction)                                                                                                                                       |
| `GET /api/v1/tags`                     | List/autocomplete tag names (`?q=`, `limit`, `offset`)                                                                                                                                          |
| `POST /api/v1/tags`                    | Create tag                                                                                                                                                                                      |
| `PUT /api/v1/tags/{id}`                | Update tag name                                                                                                                                                                                 |
| `DELETE /api/v1/tags/{id}`             | Delete tag                                                                                                                                                                                      |
| `GET /api/v1/people`                   | List/autocomplete people names (`?q=`, `limit`, `offset`)                                                                                                                                       |
| `POST /api/v1/people`                  | Create person                                                                                                                                                                                   |
| `PUT /api/v1/people/{id}`              | Update person name, native name                                                                                                                                                                 |
| `DELETE /api/v1/people/{id}`           | Delete person (CASCADE to document_people)                                                                                                                                                      |
| `GET /api/v1/people-types`             | List/autocomplete person types (`?q=`, `limit`, `offset`)                                                                                                                                       |
| `POST /api/v1/people-types`            | Create person type                                                                                                                                                                              |
| `PUT /api/v1/people-types/{id}`        | Update person type name, description                                                                                                                                                            |
| `DELETE /api/v1/people-types/{id}`     | Delete person type (409 if referenced)                                                                                                                                                          |
| `GET /api/v1/document-types`           | List/autocomplete document types (`?q=`, `limit`, `offset`)                                                                                                                                     |
| `POST /api/v1/document-types`          | Create document type with description                                                                                                                                                           |
| `PUT /api/v1/document-types/{id}`      | Update document type name, description                                                                                                                                                          |
| `DELETE /api/v1/document-types/{id}`   | Delete document type (409 if referenced)                                                                                                                                                        |
| `GET /api/v1/saved-searches`           | List saved searches                                                                                                                                                                             |
| `POST /api/v1/saved-searches`          | Create saved search                                                                                                                                                                             |
| `DELETE /api/v1/saved-searches/{id}`   | Delete saved search                                                                                                                                                                             |
| `GET /api/v1/users`                    | List users (paginated, excludes password_hash/api_key)           |
| `GET /api/v1/users/{id}`              | Get single user                                                  |
| `POST /api/v1/users`                   | Create user (username + bcrypt password, min 8 chars)            |
| `PUT /api/v1/users/{id}`              | Update username + optional password                              |
| `DELETE /api/v1/users/{id}`           | Delete user                                                      |
| `POST /api/v1/consume`                 | Enqueue inbox files, fork processing worker                     |
| `POST /api/v1/consume/upload`          | Upload files via multipart, fork processing worker                                                                                                                                              |
| `GET /api/v1/tasks`                    | List tasks (batch, status filters)                                                                                                                                                              |
| `GET /api/v1/tasks/{id}`               | Get single task                                                                                                                                                                                 |
| `GET /api/v1/batches`                  | List batch summaries                                                                                                                                                                            |
| `GET /api/v1/batches/{id}`             | Get single batch summary                                                                                                                                                                        |
| `POST /api/v1/batches/{id}/resume`     | Resume batch (forks kushim worker)                                                                                                                                                              |
| `POST /api/v1/batches/{id}/cancel`     | Cancel batch (SIGTERM worker, mark tasks cancelled)                                                                                                                                             |
| `GET /api/v1/dashboard`                | Dashboard data: recent batches + activity timeline + document analytics (language/type/tag distributions, missing counts) + storage panel (totals, MIME breakdown, storage trend, pages, words) |

### CLI Commands

| Command                       | Description                                       |
| ----------------------------- | ------------------------------------------------- |
| `kushim setup`                | Generate config, download models, init DB         |
| `kushim consume`              | Scan inbox, process files (foreground by default) |
| `kushim consume --bg`         | Spawn detached child process                      |
| `kushim consume --batch <id>` | Resume existing batch                             |
| `kushim consume --force`      | Override stale PID lock                           |
| `kushim consume cancel <id>`  | Cancel running batch                              |
| `kushim search`               | FTS5 search with ANSI highlighting                |
| `kushim task list`            | List tasks with filters                           |
| `kushim task status <id>`     | Show task details                                 |
| `kushim task retry <id>`      | Reset failed task to pending                      |
| `kushim version`              | Print version                                     |
| `kushim hugot`                | Start matcher RPC server over Unix socket         |
| `edub`                        | Start API server                                  |
| `edub version`                | Print server version                              |

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

| Route             | Status | Notes                                                                                                                                                            |
| ----------------- | ------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/` (dashboard)   | ✓      | Health + summary stats + recent docs + batch overview panel + activity timeline                                                                                  |
| `/documents`      | ✓      | Structured search bar + filter panel + saved searches + sortable DataTable                                                                                       |
| `/documents/[id]` | ✓      | PDF preview + editable metadata sidebar (title, type, language, tags, people) + delete                                                                           |
| `/settings`       | ✓      | Full configuration form: server host/port, OCR, consumer, text extractor, PDF optimizer, enricher, content analyzer (LLM) with tokens, tag matcher, text reducer |
| `/tags`           | ✓      | List, filter, create, edit, delete tags                                                                                                                          |
| `/people`         | ✓      | Two tabs: People (name + native name) and Person Types (name + description)                                                                                      |
| `/document-types` | ✓      | List, create, edit, delete document types                                                                                                                        |
| `/tasks`          | ✓      | Batch list + task drill-down                                                                                                                                     |
| `/tasks/[id]`     | ✓      | Task detail with status badge, batch/doc links, timestamps, error display, retry action                                                                          |

### Quality

- ✓ Database integration tests (17 tests) — document/tag/people CRUD, task lifecycle, enrich flow, batch ownership, FTS-adjacent operations, saved searches
- ✓ Search engine tests (7 tests) — FTS5 search, structured search, pagination, query sanitization
- ✓ Task system tests (14 tests) — Store, dispatcher, runner, pool lifecycle, dedup key handling
- ✓ API handler tests (16 tests) — health, document CRUD, tag/people CRUD, user CRUD, task endpoints, saved searches, concurrent operations
- ✓ Consumption pipeline tests (11 tests) — full consume flow with mock runner, file I/O, duplicate detection, error paths
- ✓ `internal/testutil` package — assertion helpers, PDF fixtures, mock embedder
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

| #   | Feature                                  | Description                                                                                                                                                                       |
| --- | ---------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | **Configuration/setup wizard in web UI** | ✓ In-app configuration page (LLM providers, Tesseract languages, storage paths, model downloads)                                                                                  |
| 2   | **Continue interrupted tasks**           | ✓ Manual retry for failed tasks (API + web UI). Wizard auto-resumes on restart, re-submission does not duplicate tasks, failed config downloads surfaced with retry action.       |
| 3   | **Docker Compose quick-start**           | ✓ Single `docker compose up` command runs the full stack — multi-stage Dockerfile builds everything from source, entrypoint handles first-boot setup and conditional apt installs |
| 4   | **Document detail — tags/people/type**   | ✓ Display tags, people, and document type in the sidebar                                                                                                                          |
| 5   | **Document metadata editing**            | ✓ Detail page sidebar — edit tags, people, type, title (override LLM)                                                                                                             |
| 6   | **Document update endpoint**             | ✓ `PUT /api/v1/documents/{id}` — update title, document_type_id, text_content                                                                                                     |
| 7   | **Document delete endpoint**             | ✓ `DELETE /api/v1/documents/{id}` — remove document + files                                                                                                                       |
| 8   | **Document tag/people assignment**       | ✓ `POST/DELETE /api/v1/documents/{id}/tags` and `/people` — junction management                                                                                                   |
| 9   | **Tags CRUD API**                        | ✓ `GET/POST/PUT/DELETE /api/v1/tags` with `TagService` batch CRUD, auto-encoding, cache sync                                                                                      |
| 10  | **People CRUD API**                      | ✓ `GET/POST/PUT/DELETE /api/v1/people` + `/api/v1/people-types` — `PeopleService` + `PeopleTypeService` batch CRUD                                                                |
| 11  | **Document Types CRUD API**              | ✓ `GET/POST/PUT/DELETE /api/v1/document-types` — `DocumentTypeService` batch CRUD                                                                                                 |
| 12  | **Tag management page**                  | ✓ List, create, edit, delete tags with filter input + conflict detection                                                                                                          |
| 13  | **People management page**               | ✓ Two-tab route: People (name, native name, cascade delete) and Person Types (name, description, 409 handling)                                                                    |
| 14  | **Document type management page**        | ✓ List, create, edit, delete document types with 409 conflict handling                                                                                                            |
| 15  | **Document upload/consume flow**         | ✓ Wire Upload button to `POST /api/v1/consume/upload`, show progress feedback via modal                                                                                           |
| 16  | **Single task detail page**              | ✓ New route `/tasks/{taskID}` — status badge, batch/doc links, timestamps, error display, retry action                                                                            |
| 17  | **Integration test**                     | ✓ End-to-end consumption test (mock runner), DB CRUD, search, task system, API handlers — covering database queries, search engine, pool, enrichment flow                         |
| 18  | **Test coverage**                        | ✓ Database queries, API handlers, search engine, task system (store/runner/dispatcher/pool), consumption pipeline. ✗ Adapters, CLI commands (existing gap)                        |
| 19  | **Document download**                    | ✓ Download individual documents from the document list table. Batch download with configurable limits (max file count and/or total accumulated size).                             |
| 20  | **Bulk operations**                      | ✓ Batch delete, batch tag assignment                                                                                                                                              |
| 21  | **Batch cancel API endpoint**            | ✓ `POST /api/v1/batches/{id}/cancel` — expose `kushim consume cancel` through the API                                                                                             |
| 22  | **Dashboard: storage panel**             | ✓ Total size, MIME type breakdown (count + size per type), cumulative storage trend by day/week, average file size, total pages, total words                                      |
| 23  | **Dashboard: batch overview panel**      | ✓ Recent N batches with per-batch task summary, active/orphaned state, batch source, creation time, duration when complete                                                        |
| 24  | **Dashboard: activity timeline**         | ✓ Chronological feed: document uploaded, task completed, task failed, batch created — merged from document.created_at, task.completed_at, batch.created_at                        |
| 25  | **Dashboard: document analytics panel**  | ✓ Language distribution, document type distribution, tag frequency (top N), documents without tags/type/language counts                                                           |
| 26  | **Dashboard: processing health panel**   | ✓ Task success rate (completed vs failed, last 7 days), avg task duration, active batch count, orphaned batch count, missing tools count                                          |
| 27  | **Dashboard: summary API**               | ✓ Single `GET /api/v1/dashboard` endpoint returning all panel data — built from GROUP BY/JOIN queries on existing schema, no migrations needed                                    |
| 28  | **User accounts**                        | ✓ User CRUD, bcrypt password hashing, user list page in settings UI                                                                                                               |
| 29  | **Login and session management**         | Login/logout endpoints, session tokens, auth middleware, protected API routes, login/register page                                                                                |
| 30  | **API keys**                             | Key generation/revocation/rotation, Bearer token validation middleware, per-user key management                                                                                   |
| 31  | **Roles and permissions**                | RBAC schema with admin/editor/viewer roles, permission enforcement middleware, role assignment                                                                                    |
| 32  | **Document notes/comments**              | User-added notes and annotations on documents                                                                                                                                     |
| 33  | **Pre-built binaries**                   | Release binaries for major platforms (Linux amd64/arm64)                                                                                                                          |
| 34  | **MySQL / MariaDB database backend**     | Additional database backend support                                                                                                                                               |
| 35  | **User preferences**                     | Theme, pagination defaults, notification settings                                                                                                                                 |
| 36  | **Email ingestion**                      | IMAP inbox scanning                                                                                                                                                               |
| 37  | **Document relationships**               | Parent/child, cross-references between documents                                                                                                                                  |
| 38  | **Contributing guide & issue templates** | `CONTRIBUTING.md`, issue/PR templates, and GitHub community setup to encourage contributions                                                                                      |

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

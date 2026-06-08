# Development Roadmap

> **Development focus**: LLM-based automatic classification (tags, title, people, document type)
> as the core differentiator against paperless-ngx, followed by the web UI layer.

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
- PID file system for batch locking (Acquire/Release, IsAlive, stale lock override with `--force`)

### Document Pipeline

- Document consumption pipeline: scan inbox → extract text → OCR fallback → optimize → store
- Text extraction via MuPDF CGo wrapper (default), gopdf (pure Go), or pdftotext (external tool)
- OCR via gosseract (Tesseract + MuPDF at 200 DPI) or ocrmypdf (external tool)
- Searchable PDF generation with invisible text layer (PDF text rendering mode 3)
- PDF optimization via MuPDF (CGo wrapper) or Ghostscript (external tool)
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
- Post-LLM tag consolidation via `MatchEach` (fixes casing, hyphenation, synonym mismatches)
- Tag embedding cache (`BuildTagCache`) — pre-computed tag embeddings at startup
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

### Task System

- Generic `task.Dispatcher` + `pool.Pool` with `task.Handler` / `Dedupable` interfaces
- Task lifecycle: `waiting` → `pending` → `processing` → `completed` / `failed` / `cancelled`
- Linked task pairs: enrich tasks start `waiting` until their consume task completes
- Batch cancellation: cancels pending tasks, sends SIGTERM, marks in-flight as cancelled
- Retry support for failed tasks
- Dedup prevention via unique partial index on active tasks (`task_type`, `dedup_key`)

### API Endpoints

| Endpoint                          | Description                          |
| --------------------------------- | ------------------------------------ |
| `GET /health`                     | Health check                         |
| `GET /api/v1/documents`           | List documents (sortable, paginated) |
| `GET /api/v1/documents/{id}`      | Get document with tags, people       |
| `GET /api/v1/documents/{id}/file` | Download PDF file                    |
| `GET /api/v1/documents/search`    | FTS5 search with snippets            |
| `POST /api/v1/consume`            | Enqueue inbox files                  |
| `GET /api/v1/tasks`               | List tasks (batch, status filters)   |
| `GET /api/v1/tasks/{id}`          | Get single task                      |
| `GET /api/v1/batches`             | List batch summaries                 |
| `GET /api/v1/batches/{id}`        | Get single batch summary             |
| `GET /api/v1/summary`             | Global totals across all batches     |

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
| `edub`                        | Start API server                                  |
| `edub version`                | Print server version                              |

### Web UI

- SvelteKit SPA with embedded Go binary (`//go:embed`)
- CSS custom properties design system (clay/gold/lapis/parchment palette)
- Reusable `DataTable` component (sortable columns, paginated, configurable page sizes)
- API client module (`src/lib/api.js`) — documents, tasks, batches, summary, health
- Hot-reload dev server (`npm run dev`), production build via `make web-build`

#### Web UI — Page Status

| Route             | Status | Notes                                                             |
| ----------------- | ------ | ----------------------------------------------------------------- |
| `/` (dashboard)   | ✓      | Health + summary stats + recent docs                              |
| `/documents`      | ✓      | DataTable with sort/paginate                                      |
| `/documents/[id]` | ◐      | PDF preview + metadata sidebar; no tags/people/type displayed yet |
| `/tags`           | ✗      | Placeholder: "Tag management will go here."                       |
| `/tasks`          | ✓      | Batch list + task drill-down                                      |
| `/tasks/[id]`     | ✗      | Route does not exist                                              |

### Quality

- ✗ **No test files exist** in the internal packages (the only `*_test.go` in the repo belongs to a third-party C library)
- FTS, search engine, enrichment, consumption, handlers, CLI, pool — none have automated tests

### Build & Deployment

- Two binaries: **kushim** (CLI) + **edub** (API server)
- Static CGo linking for MuPDF, Tesseract, Leptonica, libpng
- Pre-built `libtokenizers.a` for Hugot Go backend
- Containerized builds for glibc and musl (`make build-glibc`, `make build-musl`)
- Deployment image (`make build-tools-image`)
- Go build tags: `XLA`, `ORT` for Hugot ONNX support
- `make web-build` target for UI embedding

---

## 🟢 Roadmap (Priority Order)

> **Core differentiator achieved**: Automatic classification via LLM providers (tags, title, people,
> document type) with semantic tag matching — the feature that distinguishes this project
> from paperless-ngx.

### 🔴 Tier 2 — API Foundation (Remaining)

| #   | Feature                            | Description                                                                                                                                     |
| --- | ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | **Tags API**                       | `GET/POST/PUT/DELETE /api/v1/tags` — CRUD handlers (sqlc queries exist)                                                                         |
| 2   | **People API**                     | `GET/POST/PUT/DELETE /api/v1/people` — CRUD handlers for people + people_types                                                                  |
| 3   | **Document Types API**             | `GET/POST/PUT/DELETE /api/v1/document_types` — CRUD handlers (sqlc queries exist)                                                               |
| 4   | **Document update endpoint**       | `PUT /api/v1/documents/{id}` — update title, document_type_id, text_content                                                                     |
| 5   | **Document delete endpoint**       | `DELETE /api/v1/documents/{id}` — remove document + files                                                                                       |
| 6   | **Document tag/people assignment** | `POST/DELETE /api/v1/documents/{id}/tags` and `/people` — junction management                                                                   |
| 7   | **Batch cancel API endpoint**      | `POST /api/v1/batches/{id}/cancel` — expose `kushim consume cancel` through the API                                                             |
| 8   | **Test coverage**                  | Add automated tests for: adapters, CLI commands, database queries, API handlers, search engine, worker pool, enrichment pipeline, and utilities |

### 🟢 Tier 3 — Web UI (Manual Management & UX)

| #   | Feature                                | Description                                                               |
| --- | -------------------------------------- | ------------------------------------------------------------------------- |
| 9   | **Tag management page**                | Replace placeholder: list, create, edit, delete tags                      |
| 10  | **People management page**             | New route: list, create, edit, delete people and their types              |
| 11  | **Document type management page**      | New route: list, create, edit, delete document types                      |
| 12  | **Document detail — tags/people/type** | Display tags, people, and document type in the sidebar                    |
| 13  | **Document metadata editing**          | Detail page sidebar — edit tags, people, type, title (override LLM)       |
| 14  | **Functional search**                  | Wire header search input to `/api/v1/documents/search`, show results      |
| 15  | **Document upload/consume flow**       | Wire Upload button to `POST /api/v1/consume`, show progress feedback      |
| 16  | **Single task detail page**            | New route `/tasks/{taskID}` — status, file, timestamps, error information |
| 17  | **Tag-based filtering**                | Click tag → filter document list; date range, type, people filters        |
| 18  | **Bulk operations**                    | Batch delete, batch tag assignment, batch download                        |
| 19  | **Dashboard enhancements**             | Storage usage trend, recent batch status, activity timeline               |
| 20  | **Post-classification notification**   | Optional webhook or websocket event when a classification batch completes |

---

## 🔵 Planned (Post-MVP)

- Authentication and user management (login, API keys, roles)
- ZincSearch or Meilisearch integration for large-scale search
- User preferences (theme, pagination defaults, notification settings)
- Document notes/comments and annotations
- Document relationships (parent/child, cross-references)
- Email ingestion (IMAP inbox scanning)
- Metrics and monitoring (prometheus endpoints, structured metrics)
- MySQL / MariaDB database backend support
- Pre-built binaries and package manager distribution

---

## See Also

- [Architecture & Design](architecture.md) — Core design principles and pipeline narrative
- [Code Reference](reference/overview.md) — Detailed implementation reference per package

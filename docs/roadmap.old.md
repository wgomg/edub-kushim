# Development Roadmap

> **Development focus**: LLM-based automatic classification (tags, title, author, document type)
> as the core differentiator against paperless-ngx, followed by the web UI layer.

## ✅ Complete

- HTTP Server with middleware (request ID, parameter parsing)
- Database layer with sqlc-generated type-safe queries
- Configuration system with YAML support
- Structured logging with request correlation, file logging support
- Document API (list, get by ID, get file, search, sortable)
- CLI framework with dependency injection
- Adapter pattern for OCR, text extraction, PDF optimization, content analysis, text reduction, and tag matching
- Document consumption pipeline (scan → extract → OCR → store)
- Text extraction via MuPDF CGo wrapper (default), gopdf (pure Go), or pdftotext (external tool)
- OCR via gosseract (Tesseract + MuPDF CGo wrapper, default) or ocrmypdf (external tool)
- PDF optimization via MuPDF (CGo wrapper, default) or Ghostscript (external tool)
- Content analysis via LLM providers (OpenAI, Anthropic, DeepSeek, Ollama)
- Text reduction via TextRank extractive summarization
- Semantic tag matching via Hugot (Go or ORT backend) with cosine similarity ranking
- Custom MuPDF CGo wrapper (`mupdf_wrapper.go`) — document open/close, page rendering, text extraction, pdf_clean_file
- Embedded Tesseract language data (`eng.traineddata`)
- Searchable PDF generation with invisible text layer (text rendering mode 3)
- Deferred cleanup of temporary files
- Full-text search (FTS5) with manual query layer
- CLI search command with pagination, ANSI highlighting, and index rebuild
- Query sanitization layer (search.Engine) wrapping FTS5 for safe user input
- FTS search API endpoint (`GET /api/v1/documents/search`)
- Consume API endpoint (`POST /api/v1/consume`)
- Document file download endpoint (`GET /api/v1/documents/{id}/file`)
- **edub** binary: standalone API server with graceful shutdown, embedded SPA frontend
- Async task system: generic `task.Dispatcher` + `pool.Pool` — handlers for
  task types registered via `task.Handler` interface.
- Batch cancellation (`kushim consume cancel <batch-id>`) — cancels pending tasks,
  sends SIGTERM to the running process, marks in‑flight tasks as cancelled.
  PID file lock prevents duplicate batch runs. `--force` overrides stale locks.
- Async enrichment pipeline: consume → enqueue enrich → text reduction → tag matching → LLM classification → tag consolidation
- Tag embedding cache (`cache.BuildTagCache`) — pre-computed tag embeddings at startup
- Seeded tag vocabulary (`sql/seed-tags.sql`) — 110+ Dewey Decimal tags
- Per-tool timeout configuration via `ToolConfig` struct + `runWithTimeout` generic wrapper
- Configurable worker pool size (`consumer.workers`, `enricher.workers`, default 1 each)
- Numeric log levels (`LevelSilent`/`LevelFatal`/`LevelError`/`LevelInfo`/`LevelDebug`) with hierarchical filtering via numeric comparison
- Complete test suite for all adapters, CLI commands, database queries, API handlers,
  search engine, worker pool, enrichment pipeline, and utilities
- FTS trigger integration testing (INSERT, UPDATE, DELETE)
- Performance testing for large collections (30K documents, sub-3s FTS search, sub-10ms list/task queries)

## 🟢 Roadmap (Priority Order)

> **Core differentiator achieved**: Automatic classification via LLM providers (tags, title, author,
> document type) with semantic tag matching — the feature that distinguishes this project
> from paperless-ngx.

### 🔴 Tier 2 — API Foundation (Remaining)

| #   | Feature                               | Description                                                                          |
| --- | ------------------------------------- | ------------------------------------------------------------------------------------ |
| 1   | **Tags API**                          | `GET/POST/PUT/DELETE /api/v1/tags` — CRUD handlers (sqlc queries exist)              |
| 2   | **Authors API**                       | `GET/POST/PUT/DELETE /api/v1/authors` — CRUD handlers (sqlc queries exist)           |
| 3   | **Document Types API**                | `GET/POST/PUT/DELETE /api/v1/document_types` — CRUD handlers (sqlc queries exist)    |
| 4   | **Document update endpoint**          | `PUT /api/v1/documents/{id}` — update title, document_type_id, text_content          |
| 5   | **Document delete endpoint**          | `DELETE /api/v1/documents/{id}` — remove document + files                            |
| 6   | **Document metadata in responses**    | Extend `DocumentResponse` with `tags`, `authors`, `document_type_name`               |
| 7   | **Document tag/author assignment**    | `POST/DELETE /api/v1/documents/{id}/tags` and `/authors` — junction management       |
| 8   | **Classification result persistence** | LLM output written to `document_tag`, `document_author`, `document.document_type_id` |

### 🟢 Tier 3 — Web UI (Manual Management & UX)

| #   | Feature                              | Description                                                               |
| --- | ------------------------------------ | ------------------------------------------------------------------------- |
| 9   | **Tag management page**              | List, create, edit, delete tags (replaces placeholder)                    |
| 10  | **Author management page**           | List, create, edit, delete authors                                        |
| 11  | **Document type management page**    | List, create, edit, delete document types                                 |
| 12  | **Document metadata editing**        | Detail page sidebar — edit tags, author, type, title (override LLM)       |
| 13  | **Functional search**                | Connect header search input to FTS API, display results with snippets     |
| 14  | **Document upload/consume flow**     | Connect upload button to `POST /api/v1/consume`, show progress feedback   |
| 15  | **Tag-based filtering**              | Click tag → filter document list; date range, type, author filters        |
| 16  | **Bulk operations**                  | Batch delete, batch tag assignment, batch download                        |
| 17  | **Single task detail page**          | `/tasks/{taskID}` — status, file, timestamps, error                       |
| 18  | **Dashboard enhancements**           | Storage usage trend, recent batch status, activity timeline               |
| 19  | **Batch resume/cancel from UI**      | Expose `kushim consume cancel` via API + UI button                        |
| 20  | **Post-classification notification** | Optional webhook or websocket event when a classification batch completes |

## 🔵 Planned (Post-MVP)

- Authentication and user management
- ZincSearch integration
- User preferences (theme, pagination defaults)
- Document notes/comments

## See Also

- [Architecture & Design](architecture.md) — Core design principles and pipeline narrative
- [Code Reference](reference/overview.md) — Detailed implementation reference per package

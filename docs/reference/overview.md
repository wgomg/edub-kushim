# Project Overview

## Project Structure

```
internal/
├── api/                    # HTTP handlers, middleware, types
│   ├── handlers/
│   │   ├── config.go       # Config handler: GET/PUT /wizard/config, GET /wizard/config/status
│   │   ├── consume.go     # Consume handler (async, creates paired consume+enrich tasks, returns batch ID)
│   │   ├── document.go    # Document API handlers (list, get, search, structured search, get file) — returns tags, people, language, doc type
│   │   ├── document_type.go # Document type CRUD handlers (list, create, update, delete)
│   │   ├── health.go      # Health check handler
│   │   ├── people.go      # People + people-type CRUD handlers (list, create, update, delete)
│   │   ├── saved_search.go # Saved search CRUD handlers (create, list, delete)
│   │   ├── tag.go         # Tag CRUD handlers (list, create, update, delete)
│   │   ├── task.go        # Task API handlers (list, get, batch summary, global summary with waiting status)
│   │   ├── orphaned.go     # Orphaned file management handlers (list, scan, delete, restore, move-to-inbox)
│   │   └── errored.go      # Errored file management handlers (list, download, delete, delete-all)
│   ├── server.go          # HTTP server setup, middleware, route registration, static SPA (Go 1.22+ patterns)
│   └── types/
│       ├── config.go          # Config response types (ConfigResponse, ConfigStatusResponse, engine responses)
│       ├── document.go        # API response types (with tags, people, language, doc type, SearchResponse)
│       ├── document_type.go   # Document type CRUD request/response types
│       ├── people.go          # People + people-type CRUD request/response types
│       ├── saved_search.go    # Saved search request/response types
│       ├── tag.go             # Tag request/response types
│       └── task.go            # Task/batch/global summary response types (with waiting status)
├── cache/                 # Embedding cache system
│   ├── cache.go           # Generic thread-safe store (Set, Get)
│   ├── bootstrap.go       # BuildTagCache — pre-compute tag embeddings at startup
│   └── embedding_store.go # EmbeddingStore (map[string][]float32 with thread-safe ops)
├── commands/              # CLI command framework
│   ├── commands.go        # Command definitions and runner
│   ├── consume.go         # Document consumption command (--batch, cancel)
│   ├── container.go       # Dependency injection container (DB, pools, cache, dispatcher); includes config pool
│   ├── flags.go           # CLI flag parser (shared by commands)
│   ├── search.go          # Search command (CLI)
│   ├── serve_matching.go  # Matcher RPC server over Unix socket (encode, match, consolidate, store ops)
│   ├── storage.go         # Storage commands: `kushim storage orphans` (list, scan, delete, restore, move-to-inbox)
│   ├── setup.go           # Setup command — launches web wizard by default, --cli for terminal mode
│   └── task.go            # Task commands (list, status, retry)
├── configtask/            # Config task handler
│   └── configtask.go      # ConfigTaskHandler — downloads tessdata/Hugot model in background ("config" task type)
├── enrichment/            # Enrichment engine (LLM pipeline)
│   └── enricher.go        # Enricher: dual text reduction → tag matching → LLM → consolidation → people/tag/doc type with romanization + normalization
├── service/               # Merged domain service package (replaces people/tags/documenttypes)
│   ├── status.go          # Shared CRUD enums (CreateStatus, UpdateStatus, DeleteStatus)
│   ├── result.go          # Generic result types (CreateResult[T], UpdateResult[T], DeleteResult)
│   ├── people.go          # People service (NewPeople, batch CRUD)
│   ├── peopletype.go      # PeopleType service (NewPeopleType, batch CRUD)
│   ├── tag.go             # Tag service (NewTag, batch CRUD, embedder integration)
│   ├── documenttype.go    # DocumentType service (NewDocumentType, batch CRUD)
│   ├── batch.go           # Batch service (NewBatch, GetSummary, ListSummaries, ListOverviews, Create, BeginCancel/CompleteCancel, CountOrphaned, RetryFailed, HasPendingWork, IsLockedByLiveOwner)
│   ├── orphaned.go        # Orphaned file service (scan, list, delete, restore, move-to-inbox)
│   └── errored.go        # Errored file service (list on disk, get path, delete, delete-all)
├── pool/                  # Generic worker pool
│   └── pool.go            # Pool struct, Start(ctx), Stop(ctx), worker loop
├── task/                  # Generic task system
│   ├── batch.go           # Batch ownership — Owner.Acquire, Release, Heartbeat, BatchOwnerState
│   ├── crud.go            # Task CRUD (Get, ListFiltered, Retry, CountBatchStatuses, ListBatchSummaries)
│   ├── dispatcher.go      # Task dispatcher (Enqueue with custom taskID/status)
│   ├── handler.go         # Handler + Dedupable interfaces
│   ├── heartbeat.go       # Heartbeat goroutine — periodic Owner.Heartbeat every 5s
│   └── handlers/
│       ├── consume.go     # ConsumeTaskHandler (uses FileFromPath)
│       ├── enrich.go      # EnrichTaskHandler (fetches document, calls Enricher.Enrich)
│       └── backup.go      # BackupTaskHandler (SQL dump, tar.gz, retention)
├── search/                # Full-text search engine
│   └── search.go          # Engine, Result (with Language, DocumentTypeID, checksums), Filter (structured search), sanitizeQuery, SearchStructured (returns results + total count)
├── backup/                # Backup & restore
│   ├── backup.go          # Create backup (SQL dump, tar.gz, manifest, retention)
│   ├── restore.go         # Validate, extract, and replace files from backup archive
│   └── scheduler.go       # Backup scheduling (NextRunTime, ShouldSchedule, state persistence)
├── config/                # Configuration parsing
│   ├── config.go          # Configuration structs and loading (ConsolidationSimilarity, default thresholds, engine identifier constants, AvailableEngines map)
│   └── setup.go           # Bootstrap config, SaveMap, tessdata/Hugot model download helpers
├── consumption/           # Document processing engine
│   ├── consumer.go        # Main consumer (extract → OCR → optimize → store), duplicate detection (MD5+SHA512)
│   └── storage.go         # File operations, checksums, FileFromPath, MIME detection via mimetype lib
├── database/              # Database layer (sqlc-generated + manual)
│   ├── connection.go      # DB connection (PostgreSQL via pgx, 25 max conn)
│   ├── schema.go          # InitializeSchema — goose migrations + seeders (tags, doc-types, people-types)
│   ├── models.go          # Generated data models (Document now has PageCount, WordCount, CharCount, Language; added SavedSearch)
│   ├── db.go              # Database interface (Queries, WithTx)
│   ├── document_sort.go   # ListDocumentsWithSort (whitelisted sort columns)
│   ├── structured_search.go # Dynamic SQL query builder for structured search (SearchDocumentsStructured, CountDocumentsStructured)
│   ├── document.sql.go    # Generated document queries (with WordCount, CharCount, Language)
│   ├── document_tag.sql.go # Generated document_tag junction queries
│   ├── document_people.sql.go # Generated document_people junction queries
│   ├── document_type.sql.go # Generated document type queries (includes UpdateDocumentType)
│   ├── tag.sql.go         # Generated tag queries (includes SearchTagsByName)
│   ├── people.sql.go      # Generated people queries (CreatePeople with name_native, ListAllPeople, SearchPeopleByName, UpdatePeopleNative, GetPeopleByName)
│   ├── people_type.sql.go # Generated people type queries
│   ├── task.sql.go        # Generated task queries (includes SetEnrichTaskPending, DiscardEnrichTask)
│   ├── user.sql.go        # Generated user queries
│   ├── saved_search.sql.go # Generated saved search queries (CreateSavedSearch, ListSavedSearches, DeleteSavedSearch)
│   ├── structured_search.go # Dynamic search query builder with tsvector support
│   └── sql/               # Embedded SQL assets
│       ├── schema/        # Seed SQL files (seed-tags.sql, seed-document-types.sql, seed-people-types.sql)
│       │   └── migrations/ # goose versioned migrations (00001_baseline.sql, ...) — also read by sqlc
│       └── queries/       # SQL queries for sqlc
│           ├── document.sql
│           ├── document_tag.sql
│           ├── document_people.sql
│           ├── document_type.sql
│           ├── saved_search.sql
│           ├── tag.sql
│           ├── people.sql
│           ├── people_type.sql
│           ├── task.sql
│           ├── user.sql
│           └── orphaned.sql
├── storage/              # Filesystem operations for orphaned file management
│   └── orphaned.go        # Walk originals/processed, quarantine, remove, copy to inbox
├── static/                # Embedded web UI (main app)
│   └── fs.go              # Embedded SvelteKit build (build/ directory via //go:embed)
├── tagmatch/              # Tag matcher RPC client
│   └── client.go          # MatcherClient — HTTP client over Unix socket to external matcher process (flattened from rpc/)
├── version/               # Application version
│   └── version.go         # const Version = "0.1.0"
├── wizard/                # Setup wizard HTTP server
│   ├── server.go          # Standalone HTTP server on :8420, serves wizard SPA + config API
│   └── fs.go              # Embedded SvelteKit wizard build (static/ via //go:embed)
├── utils/                 # Utilities
│   ├── config.go          # ConfigDir() — returns ~/.config/edub-kushim
│   ├── files.go           # ListFilePaths — scans inbox directories with MIME detection (replaces fileresolver)
│   ├── logger.go          # Structured logging (file logging support, numeric level filtering)
│   ├── metrics.go         # Memory metrics (HeapInUse, RSS, NumGC), HumanDuration, FormatMemDelta
│   ├── parambag.go        # HTTP parameter parsing (query params, path values)
│   └── text.go            # CountWords, EstimateTokensFromWords, CleanUp, Truncate, CleanCodeBlock, ContainsNonLatin, NormalizeForDB, StripTags, StripTagsPtr
├── adapters/
│   │   ├── mupdf_wrapper.go    # MuPDF CGo wrapper (6 C helpers + Go API)
│   │   ├── contentanalyzer/    # LLM classification providers
│   │   │   ├── adapter.go          # ContentAnalyzer interface + factory (NewContentAnalyzer); PeopleResult with NameRomanized
│   │   │   ├── shared.go           # BuildPrompt, system message, prompt asks for name_romanized on non-Latin names
│   │   │   ├── llm_openai.go       # OpenAI-compatible API (OpenAI, OpenRouter)
│   │   │   ├── llm_anthropic.go    # Anthropic Messages API
│   │   │   ├── llm_deepseek.go     # DeepSeek Chat API
│   │   │   └── llm_ollama.go       # Local Ollama API
│   │   ├── ocr/
│   │   │   ├── adapter.go      # OCR interface and factory
│   │   │   ├── gosseract.go    # gosseract OCR with subprocess fork
│   │   │   ├── standalone.go   # RunStandalone OCR pipeline (subprocess entry)
│   │   │   ├── ocrmypdf.go     # ocrmypdf external tool
│   │   │   ├── tesseract_link.go  # CGo linker flags for static Tesseract
│   │   │   ├── tessdata.go     # Embedded traineddata + download
│   │   │   ├── font_embed.go   # LiberationSans TTF embedding
│   │   │   └── kushim_font.ttf # Embedded font file
│   │   ├── tagmatcher/            # Semantic tag matching (embeddings)
│   │   │   ├── adapter.go         # Matcher, Embedder, EmbeddingStore interfaces
│   │   │   └── hugot.go           # Hugot session (Go or ORT backend), cosine similarity, chunked encoding
│   │   ├── textextractor/
│   │   │   ├── adapter.go      # Text extractor interface
│   │   │   ├── mupdf.go         # MuPDF text extractor (default, CGo, streams)
│   │   │   ├── gopdf.go         # gopdf text extractor (pure Go)
│   │   │   └── pdftotext.go    # pdftotext external tool
│   │   ├── textreducer/           # Text summarization
│   │   │   ├── adapter.go         # TextReducer interface + factory
│   │   │   └── textrank.go        # TextRank: chunking, TF-IDF, weighted PageRank, diversity penalty
│   │   └── pdfoptimizer/
│   │       ├── adapter.go      # PDF optimizer interface
│   │       ├── mupdf.go         # MuPDF optimizer (default, CGo wrapper)
│   │       └── ghostscript.go  # Ghostscript external tool
│   └── runner.go          # Unified tool runner (all adapters), runWithTimeout generic wrapper
sql/                       # NOTE: Moved under internal/database/sql/
├── queries/               # SQL queries for sqlc
└── schema/                # Seed data
    ├── seed-tags.sql
    ├── seed-document-types.sql
    └── seed-people-types.sql

cmd/
├── kushim/               # CLI entry point
│   └── main.go
└── edub/                 # API server entry point
    └── main.go

web/                      # SvelteKit SPA frontend (main UI)
├── src/
│   ├── app.html          # HTML shell
│   ├── lib/
│   │   ├── api.js        # API client (fetch helper) — documents, tasks, autocomplete, saved searches, config
│   │   ├── index.js
│   │   ├── assets/
│   │   │   └── favicon.svg
│   │   ├── stores/
│   │   │   ├── filterStore.js   # Reactive filter state store (setPartial, reset, fromQueryString)
│   │   │   └── searchFilter.js  # Query parser, tokenizer, size/date formatting utilities
│   │   └── components/
│   │       ├── DataTable.svelte   # Reusable data table (sortable, paginated, total count, refreshKey)
│   │       ├── FilterPanel.svelte # Collapsible advanced filter panel (tags, people, dates, size, etc.)
│   │       └── SearchBar.svelte   # Rich search input with chips, autocomplete, keyboard navigation
│   └── routes/
│       ├── +layout.svelte        # App layout shell — includes Settings sidebar link
│       ├── +page.svelte          # Home/dashboard page
│       ├── layout.css            # Global styles (CSS custom properties)
│       ├── documents/
│       │   ├── +page.svelte      # Document list with search bar, filters, saved searches, DataTable
│       │   └── [id]/
│       │       └── +page.svelte  # Single document detail page
│       ├── documents/orphaned/
│       │   └── +page.svelte      # Orphaned file management page
│       ├── settings/
│       │   └── +page.svelte      # Settings page — OCR, consumer, enricher config via /wizard/config API
│       ├── tags/
│       │   └── +page.svelte      # Tag management page
│       └── tasks/
│           └── +page.svelte      # Task/batch monitoring page
├── static/
│   └── robots.txt
├── package.json
├── svelte.config.js
├── vite.config.js
├── eslint.config.js
└── .npmrc

web-wizard/               # SvelteKit SPA setup wizard (embedded in kushim binary)
├── src/
│   ├── app.html          # HTML shell
│   ├── app.css           # Tailwind styles (clay/gold/lapis/parchment palette)
│   ├── lib/
│   │   └── api.js        # API client for /wizard/config endpoints
│   └── routes/
│       ├── +layout.svelte  # Wizard layout shell
│       └── +page.svelte   # Six-step setup wizard flow
├── package.json
├── svelte.config.js
├── vite.config.js
├── eslint.config.js
├── .prettierrc
├── .npmrc
└── .gitignore
```

## Architecture Overview

This system is a document management pipeline with three main stages:

1. **Consumption** — Scans an inbox directory, extracts text (via MuPDF/gopdf/pdftotext), optionally OCRs (via Tesseract), optimizes PDFs, stores files with checksums
2. **Enrichment** — Applies LLM-based classification (title, doc type, tags, people, language) plus semantic tag matching via embeddings
3. **Search & API** — Full-text search via PostgreSQL tsvector, REST API with SvelteKit SPA frontend

Processing is async via a task queue with batch tracking and a polling CLI mode.

### Process Architecture

The system uses two linked binaries with **worker forking**:

- **`edub`** (API server, `CGO_ENABLED=0`) — Pure Go binary handling HTTP requests. When consume/upload is called, it enqueues tasks with `status='queued'`; the `kushim queue` daemon picks up the batch and forks a worker. Also runs a config pool for background download tasks.
- **`kushim`** (CLI, CGo) — Handles all document processing (consumption, enrichment). Also hosts the **matcher RPC server** via `kushim hugot`.
- **Matcher process** — A standalone `kushim hugot` process provides semantic tag matching (Hugot embeddings) over a Unix domain socket (`kushim-hugot.sock`). Both `edub` and `kushim` workers communicate with it via `internal/tagmatch.MatcherClient`.

## See Also

- [API Layer](api.md) — HTTP handlers, routes, response types
- [CLI Commands](cli.md) — Command definitions, container, flags
- [Pipeline](pipeline.md) — Consumption and enrichment engines
- [Task System](task-system.md) — Pool, dispatcher, CRUD, task handlers
- [Database](database.md) — Schema, queries, tsvector
- [Search](search.md) — Search engine, structured search, autocomplete
- [Tools](tools.md) — Adapter framework for all processing tools
- [Config & Utils](config-and-utils.md) — Configuration, cache, logging, utilities
- [Frontend](frontend.md) — SvelteKit UI and build system

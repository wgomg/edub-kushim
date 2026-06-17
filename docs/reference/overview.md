# Project Overview

## Project Structure

```
internal/
├── api/                    # HTTP handlers, middleware, types
│   ├── handlers/
│   │   ├── autocomplete.go # Autocomplete handlers (tags, people, person types, doc types)
│   │   ├── config.go       # Config handler: GET/PUT /wizard/config, GET /wizard/config/status
│   │   ├── consume.go     # Consume handler (async, creates paired consume+enrich tasks, returns batch ID)
│   │   ├── document.go    # Document API handlers (list, get, search, structured search, get file) — returns tags, people, language, doc type
│   │   ├── health.go      # Health check handler
│   │   └── saved_search.go # Saved search CRUD handlers (create, list, delete)
│   │   └── task.go        # Task API handlers (list, get, batch summary, global summary with waiting status)
│   ├── server.go          # HTTP server setup, middleware, route registration, static SPA (Go 1.22+ patterns)
│   └── types/
│       ├── autocomplete.go    # Autocomplete response types (PersonRef, DocumentTypeRef, PeopleTypeRef)
│       ├── config.go          # Config response types (ConfigResponse, ConfigStatusResponse, engine responses)
│       ├── document.go        # API response types (with tags, people, language, doc type, SearchResponse)
│       └── saved_search.go    # Saved search request/response types
│       └── task.go            # Task/batch/global summary response types (with waiting status)
├── cache/                 # Embedding cache system
│   ├── cache.go           # Generic thread-safe store (Set, Get)
│   ├── bootstrap.go       # BuildTagCache — pre-compute tag embeddings at startup
│   └── embedding_store.go # EmbeddingStore (map[string][]float32 with thread-safe ops)
├── commands/              # CLI command framework
│   ├── commands.go        # Command definitions and runner
│   ├── consume.go         # Document consumption command (--bg, --batch, cancel)
│   ├── container.go       # Dependency injection container (DB, pools, cache, dispatcher); includes config pool
│   ├── flags.go           # CLI flag parser (shared by commands)
│   ├── search.go          # Search command (CLI)
│   ├── setup.go           # Setup command — launches web wizard by default, --cli for terminal mode
│   └── task.go            # Task commands (list, status, retry)
├── enrichment/            # Enrichment engine (LLM pipeline)
│   ├── enricher.go        # Enricher: dual text reduction → tag matching → LLM → consolidation → people/tag/doc type with romanization + normalization
├── pidfile/               # PID file locking for batch consumption
│   └── pidfile.go         # Lock, Acquire/Release, IsAlive, Read
├── pool/                  # Generic worker pool
│   └── pool.go            # Pool struct, Start(ctx), Stop(ctx), worker loop
├── task/                  # Generic task system
│   ├── crud.go            # Task CRUD (Get, ListFiltered, Retry, CountBatchStatuses with Waiting, ListBatchSummaries)
│   ├── dispatcher.go      # Task dispatcher (Enqueue with custom taskID/status, Next uses GetNextPendingTaskOfType)
│   ├── handler.go         # Handler + Dedupable interfaces
│   └── handlers/
│       ├── config.go      # ConfigTaskHandler — downloads tessdata/Hugot model in background ("config" task type)
│       ├── consume.go     # ConsumeTaskHandler (uses FileFromPath)
│       └── enrich.go      # EnrichTaskHandler (fetches document, calls Enricher.Enrich)
├── search/                # Full-text search engine
│   └── search.go          # Engine, Result (with Language, DocumentTypeID, checksums), Filter (structured search), sanitizeQuery, SearchStructured (returns results + total count)
├── config/                # Configuration parsing
│   ├── config.go          # Configuration structs and loading (ConsolidationSimilarity, default thresholds, engine identifier constants, AvailableEngines map)
│   └── setup.go           # Bootstrap config, SaveMap, tessdata/Hugot model download helpers
├── consumption/           # Document processing engine
│   ├── consumer.go        # Main consumer (extract → OCR → optimize → store), duplicate detection (MD5+SHA512)
│   └── storage.go         # File operations, checksums, FileFromPath, MIME detection via mimetype lib
├── database/              # Database layer (sqlc-generated + manual)
│   ├── connection.go      # DB connection (SQLite WAL mode, 1 max conn)
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
│   ├── fts5.go            # Manual FTS5 query implementation (SearchDocumentsFTS, etc.)
│   └── sql/               # Embedded SQL assets
│       ├── schema/        # Seed SQL files (seed-tags.sql, seed-document-types.sql, seed-people-types.sql)
│       │   └── migrations/ # goose versioned migrations (00001_baseline.sql, ...) — also read by sqlc
│       └── queries/       # SQL queries for sqlc
├── static/                # Embedded web UI (main app)
│   └── fs.go              # Embedded SvelteKit build (build/ directory via //go:embed)
├── version/               # Application version
│   └── version.go         # const Version = "0.1.0"
├── wizard/                # Setup wizard HTTP server
│   ├── server.go          # Standalone HTTP server on :8420, serves wizard SPA + config API
│   └── fs.go              # Embedded SvelteKit wizard build (static/ via //go:embed)
├── utils/                 # Utilities
│   ├── config.go          # ConfigDir() — returns ~/.config/edub-kushim
│   ├── logger.go          # Structured logging (file logging support, numeric level filtering)
│   ├── metrics.go         # Memory metrics (HeapInUse, RSS, NumGC), HumanDuration, FormatMemDelta
│   ├── parambag.go        # HTTP parameter parsing (query params, path values)
│   └── text.go            # CountWords, EstimateTokensFromWords, CleanUp, Truncate, CleanCodeBlock, ContainsNonLatin, NormalizeName
├── pidfile/               # PID file locking for batch consumption
│   └── pidfile.go         # Lock, Acquire/Release, IsAlive, Read with cross-process safety
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
│   │   │   ├── gosseract.go    # gosseract OCR (Tesseract + MuPDF)
│   │   │   ├── ocrmypdf.go     # ocrmypdf external tool
│   │   │   ├── tesseract_link.go  # CGo linker flags for static Tesseract
│   │   │   ├── tessdata.go     # Embedded traineddata + download
│   │   │   ├── font_embed.go   # LiberationSans TTF embedding
│   │   │   └── kushim_font.ttf # Embedded font file
│   │   ├── tagmatcher/            # Semantic tag matching (embeddings)
│   │   │   ├── adapter.go         # TagMatcher interface + factory
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
│       └── +page.svelte   # Four-step setup wizard flow
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
3. **Search & API** — Full-text search via SQLite FTS5, REST API with SvelteKit SPA frontend

Processing is async via a task queue with batch tracking, worker pools, and a polling CLI mode.

## See Also

- [API Layer](api.md) — HTTP handlers, routes, response types
- [CLI Commands](cli.md) — Command definitions, container, flags
- [Pipeline](pipeline.md) — Consumption and enrichment engines
- [Task System](task-system.md) — Pool, dispatcher, CRUD, task handlers
- [Database](database.md) — Schema, queries, FTS5
- [Search](search.md) — Search engine, structured search, autocomplete
- [Tools](tools.md) — Adapter framework for all processing tools
- [Config & Utils](config-and-utils.md) — Configuration, cache, logging, utilities
- [Frontend](frontend.md) — SvelteKit UI and build system

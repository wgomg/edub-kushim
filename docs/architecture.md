# Architecture & Design

## Core Design Principles

- **Headless First**: API-driven with CLI interface; web UI as optional layer
- **Tool Agnostic**: Adapter pattern; OCR, text extraction, PDF optimization, content analysis,
  text reduction, and semantic tag matching all switchable between built‑in adapters and
  external tools/providers
- **SQLite First**: Development-friendly with migration path to production databases
  (sqlc-generated queries are portable; database-specific features like partial
  indexes use compatible patterns — see MySQL/MariaDB notes in the code reference)
- **Fallback Processing**: Text extraction → OCR → text extraction pattern
- **Date-based Organization**: Temporal storage structure for scalability
- **Transaction Safety**: Coordinated database and file operations with rollback
- **Process Isolation**: The Hugot embedding model runs as a **separate process** (`kushim hugot`), communicating over a Unix domain socket. The API server (`edub`) is pure Go (`CGO_ENABLED=0`) — it enqueues tasks and the `kushim queue` daemon forks workers for document processing; `edub` communicates with the matcher via RPC.
- **Two Binaries**: **kushim** (CLI document processing + matcher server) and **edub** (REST API server). Static C dependencies (Tesseract, Leptonica, MuPDF) linked only into **kushim**; **edub** is compiled with `CGO_ENABLED=0`.

See [Roadmap](roadmap.md) for the implementation status and upcoming priorities.

---

## Process Architecture

The system runs four cooperating processes:

### 1. Queue Daemon (`kushim queue`)

A long-lived daemon that manages batch processing and inbox polling. Runs three concurrent loops: (a) **stale reclamation** — reclaims stale batch owners (>15s heartbeat with active tasks) by resetting processing→pending, re-queuing, and removing the stale owner (gated by `consumer.reclaim.enabled`, default `true`); (b) **queue consumption** — picks the oldest queued batch and forks `kushim consume --batch <id> --force` if under the configured concurrency limit (`server.max_concurrent_batches`); (c) **inbox polling** — periodically scans the consumption directory, deduplicates by MD5, creates `queued` batches, and lets the consumer loop pick them up. When `backup.enabled` is `true`, a **backup pool** (1 worker, 60s interval) picks up scheduled backup tasks. The main ticker and polling loop both use `backupMu.TryRLock()` to skip all operations while a backup is running, preventing concurrent data access. Re-reads config from disk on each poll tick so `consumer.polling` changes take effect without restart. Replaces the former `PollingScheduler`. Uses a PID file for single-instance enforcement and logs to `<config_dir>/logs/queue.log`. Can be daemonized via `--bg`.

### 2. Matcher Server (`kushim hugot`)

A standalone HTTP server over a Unix domain socket (`kushim-hugot.sock` in the config directory). Hosts the Hugot embedding model, manages the tag embedding store, and exposes RPC endpoints:

| Endpoint                         | Method | Purpose                      |
| -------------------------------- | ------ | ---------------------------- |
| `POST /rpc/v1/encode`            | POST   | Encode text to embeddings    |
| `POST /rpc/v1/match`             | POST   | Match text against tags      |
| `POST /rpc/v1/consolidate`       | POST   | Consolidate tag names        |
| `POST /rpc/v1/add-to-store`      | POST   | Add names to embedding store |
| `POST /rpc/v1/remove-from-store` | POST   | Remove names from store      |
| `GET /health`                    | GET    | Health check                 |

This process requires CGo (Tesseract/Hugot libraries) and should be started before the API server.

### 3. API Server (`edub`)

Pure Go binary (`CGO_ENABLED=0`) that handles HTTP requests. It:

- Enqueues consume/enrich tasks when `POST /api/v1/consume` is called, creating batches with `status='queued'`
- The `kushim queue` daemon picks up queued batches and forks `kushim consume --batch <id>` for processing
- Runs a config pool for background download tasks (tessdata, Hugot model)
- Probes the matcher socket on startup; tag CRUD returns 503 if matcher is unreachable
- Does not manage inbox polling — the queue daemon (`kushim queue`) handles scanning as a goroutine loop

### 4. CLI / Worker (`kushim`)

The CGo binary that performs actual document processing:

- `kushim consume` — scans inbox, enqueues tasks, direct-fallback if queue empty
- `kushim consume --batch <id>` — resumes a previously enqueued batch (used by `kushim queue` and `edub`'s API resume handler)
- `kushim queue` — starts the batch queue daemon for background consumption and inbox polling (replaces the former `PollingScheduler`)
- `kushim hugot` — starts the matcher RPC server
- `kushim setup` — setup wizard

### Communication Flow

```
  HTTP Request
       │
       ▼
┌──────────────┐     enqueue tasks      ┌──────────────┐
│    edub      │ ──────────────────────▶ │   SQLite DB  │
│ (CGO_ENABLED │                         │  (status='   │
│     =0)      │                         │   queued')   │
└──────────────┘                         └──────┬───────┘
       │                                        │
       │                                        │ kushim queue
       │                                        │ picks up batch
       ▼                                        ▼
┌──────────────┐                     ┌──────────────┐
│   kushim     │ ◀───── polls ────── │  task queue  │
│ (CGo binary) │   ┌──────────────── │              │
│              │   │  kushim queue   └──────────────┘
│  Matcher RPC │   │  daemon forks
└──────────────┘   └────────────────▶┐
       │                             │
       ▼                             │
┌──────────────┐                     │
│   Hugot /    │                     │
│  Embeddings  │◀────────────────────┘
└──────────────┘
│              │
│  Matcher RPC │◀──── Unix socket ───┐
└──────────────┘                     │
       │                            │
       ▼                            │
┌──────────────┐                    │
│   Hugot /    │                    │
│  Embeddings  │◀───────────────────┘
└──────────────┘
```

The matcher is optional — if it's not running, `edub` logs a warning and tag CRUD returns 503. The `kushim` CLI (used for document processing) can start its own matcher when run directly (via the consume command which has direct Hugot access), or communicate with the external matcher via the same RPC interface.

---

## Processing Pipeline

### 1. File Discovery

Scans `consumption_dir` for supported extensions, detects MIME type, computes MD5 and
SHA512 checksums in a single pass.

### 2. Duplicate Detection

MD5 checksum lookup → SHA512 verification. Skips processing on exact match.

### 3. Text Extraction

Two‑stage strategy (configurable via adapter pattern):

**Primary** (default: `mupdf`): Extracts embedded text from PDF via MuPDF's
structured text device (page‑by‑page streaming, no Go heap pressure). If text
is found above a minimum density ratio (`minTextDensityRatio = 0.001`), the
PDF is optimized (MuPDF or Ghostscript) and stored. If optimization fails,
the original file is used as the processed copy — ingestion continues.

**Fallback** (default: `gosseract`): OCRs image‑only pages via Tesseract + MuPDF
and produces a searchable PDF. Text is re-extracted from the OCR'd PDF.

The external‑tool adapters (`pdftotext` for text extraction, `ocrmypdf` for OCR,
Ghostscript for PDF optimization) are available as alternatives. Configure via
`consumer.textextractor`, `consumer.ocr`, and `consumer.pdfoptimizer`.

When active, the gosseract adapter renders each page at 200 DPI via MuPDF,
OCRs from PNG, and builds a searchable PDF with `go‑pdf/fpdf` using
**text rendering mode 3** (`3 Tr`) for invisible‑but‑selectable text.

### 4. Database Integration

Document record created via `CreateDocument` with a generated `document_id` UUID,
auto-increment ID obtained from `LastInsertId()`, date‑based storage paths
generated, paths updated via `UpdateDocumentPaths`. All wrapped in a database
transaction with rollback on file‑operation failure. The UUID serves as the
stable external identifier for the API, while the auto-increment ID is used
for internal storage paths.

### 5. File Movement

Three‑way branch:

- **OCR case**: Move OCR temp file → processed storage, copy original → original storage.
- **Optimized text case**: Copy original → original storage, move optimized file → processed storage.
- **Unoptimized text case** (optimization failed or skipped): Copy original → both processed and original storage.

### 6. FTS Indexing

Automatic via SQLite triggers:

- `document_ai` — INSERT into `document_fts`
- `document_au` — UPDATE FTS index
- `document_ad` — DELETE from FTS index

Uses `unicode61` tokenizer for multi‑language support without language‑specific stemming.

### 7. Async Enrichment (Post-Consume)

After a `consume` task completes, its handler activates the waiting `enrich`
task by updating its payload with the document ID and setting its status to
`pending` (via `Store.SetPending`). If the consume task fails, the handler
discards the waiting enrich task by setting it to `discarded` with the
parent error message (via `Store.Discard`). **On retry** — when the failed
consume is retried (via `kushim task retry` or the API) and subsequently
succeeds — `activateChildEnrich` re-activates the discarded enrich task to
`pending`, clearing the previous error and completion timestamp. The enrichment pipeline:

1. **Text Reduction** (optional, configurable threshold) — if the document exceeds
   `enricher.textreducer.target_words`, TextRank extracts the most salient content.
2. **Semantic Tag Pre-filtering** — document content is matched against all existing tag
   names via the `Matcher` interface. In the `kushim` CLI, this uses direct Hugot calls
   and the local embedding store. In the `edub` API server, the `MatcherClient` forwards
   the request over a Unix socket to the external matcher process. Falls back to all tags
   on failure.
3. **LLM Classification** — the reduced content, along with available document types
   and tag suggestions, is sent to the configured LLM provider. Returns structured
   JSON: title, type, tags, people (with types like author, sender), language.
   For non-Latin names (Korean, Arabic, Cyrillic, etc.), the LLM is prompted to
   provide a `name_romanized` field alongside the original name.
4. **Post-LLM Tag Consolidation** — LLM output labels are re-matched against canonical
   tag embeddings via `Consolidate` (delegated to the matcher interface), fixing casing,
   hyphenation, and synonym mismatches.
5. **New Tag Store Update** — any new tags created during enrichment are batch-created
   via `services.Tag.Create(ctx, analysis.Tags)`. The service delegates store management
   to the matcher via `AddToStore`, which encodes new names and adds them to the shared
   embedding store. This makes them available for matching against subsequent documents
   without additional encode. When using the RPC client, this forwards the request to the
   matcher process over the Unix socket.
6. **Result Logging** — token usage stats and prompt text are logged.

### People Deduplication

When storing people from LLM results, the enricher:

1. **Romanizes** — if the LLM provided a `name_romanized` for a non-Latin name,
   that becomes the canonical form. Falls back to AnyAscii transliteration.
2. **Normalizes** — NFKC normalization, lowercase, dots/commas/apostrophes/quotes
   removed, dash variants collapsed to spaces, whitespace trimmed. This makes
   `"Itamar Ben-Gvir"` match `"Itamar Ben Gvir"` and `"O'Brien"` match `"Obrien"`.
3. **Exact-match lookup** — looks up the normalized name against existing people;
   creates a new entry only when no match is found.
4. **Stores native script** — when the original name contains non-Latin characters,
   it is stored in the `name_native` column for display in the UI.

This pipeline is non-blocking: documents are stored and searchable immediately via
FTS5, with enrichment arriving asynchronously.

---

## Setup Wizard

The project provides two setup modes:

### 1. Standalone Web Wizard (default)

When `kushim setup` is invoked without the `--cli` flag, a standalone HTTP server starts
on `0.0.0.0:8420` serving an embedded SvelteKit SPA (`web-wizard/`). The wizard is a
five-step guided flow:

1. **Config directory** — user specifies where `config.yaml`, database, and models are stored
2. **Consumer settings** — OCR engine, languages, timeout; text extractor engine/timeout; PDF optimizer
   engine/fallback/timeout; server port; consumer workers.
   **Inline warnings** show when a selected external tool (`ocrmypdf`, `gs`, `pdftotext`) is not found
   in `PATH`, along with companion status (tesseract, unpaper, pngquant for ocrmypdf) and tesseract
   language-pack guidance.
3. **Enricher settings** — content analyzer engine/timeout + LLM provider config (Base URL, model, token
   with show/hide); tag matcher engine/timeout, reduce-target-words, chunk size, Hugot model/backend;
   text reducer engine/timeout/target-words; enricher workers
4. **Progress** — background tasks download tessdata language files and the Hugot ONNX model;
   the UI polls `GET /wizard/config/status` every 3 seconds
5. **Completion** — all downloads are finished; the server is ready to run.
   If any required external tools are missing, a notice lists what to install
   (engine binaries, required companions like tesseract/unpaper, and the curl
   prerequisite). The wizard does not block completion — tools can be installed
   later.

The wizard server uses a stripped-down version of the full server stack: it bootstraps
the config directory (via `config.Bootstrap`), initializes the SQLite schema, creates
a dispatcher with only the `"config"` task type registered, and runs a single-worker
pool for background downloads. It serves the wizard UI directly via `//go:embed` on
`internal/wizard/static/`.

### 2. Terminal Setup (`kushim setup --cli`)

The original terminal-based flow: accepts `--languages`, path flags, and engine selection
flags, downloads models synchronously, prints progress to stdout. Suitable for headless
environments or CI.

### In-App Settings Page

The main web UI (`web/`) includes a **Settings** page at `/settings` backed by the same
`/wizard/config` API endpoints. It provides a single-page form covering all configurable
fields: server host/port, max upload/download sizes, max download files; OCR engine, timeout, data directory, languages;
consumer workers, max files per batch; polling scheduler on/off toggle and interval; text extractor engine/timeout;
PDF optimizer engine/fallback/timeout;
enricher workers; content analyzer engine/timeout + LLM provider Base URL, model, token;
tag matcher engine/timeout, reduce-target-words, chunk size, Hugot model/backend;
text reducer engine/timeout/target-words;
backup enabled/disabled, interval, preferred time, keep count, output path.
Changes trigger background downloads for any
missing tessdata or Hugot model files. **Inline tool-status warnings** appear beneath
each engine selector when the selected external tool is missing, and a persistent
amber banner at the top of the app shows whenever required tools are not installed.

### Config API

Three endpoints serve both the wizard and the settings page:

| Endpoint                    | Purpose                                                                                                                                       |
| --------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /wizard/config`        | Returns current config as `ConfigResponse` (user-facing subset)                                                                               |
| `PUT /wizard/config`        | Accepts `config_dir` for bootstrap, or settings map for update                                                                                |
| `GET /wizard/config/status` | Returns `ConfigStatusResponse` — `configured`, `pending_tasks`, `tools` (full tool-availability list), `missing_tools` (hard-blocking subset) |

The `PUT` handler has two phases:

- **Bootstrap phase**: when `config_dir` is present and no config exists, it calls
  `config.Bootstrap()` to create directories, write skeleton config, and initialize the DB.
- **Update phase**: otherwise, it writes the provided key-value pairs via `config.SaveMap`,
  reloads the config, and enqueues `"config"` tasks for any missing downloads.
  The response includes a `missing_tools` array of any hard-blocking tool-availability issues
  (missing engine binaries, required companions, or the curl prerequisite).

### Config Task Handler

A new task type `"config"` handles background downloads:

| Operation  | Payload                                               |
| ---------- | ----------------------------------------------------- |
| `tessdata` | `{"config_dir":"...", "op":"tessdata", "lang":"eng"}` |
| `hugot`    | `{"config_dir":"...", "op":"hugot"}`                  |

The handler loads the config from disk, then delegates to `config.DownloadTessdataLanguage`
or `config.DownloadHugotModel`. This keeps the API response fast and moves the actual
download work to the worker pool, with retry support via the task system.

### Engine Identifiers

Engine names (e.g. `"mupdf"`, `"gosseract"`, `"llmopenai"`) are now defined as
exported package-level vars in `internal/config/config.go`:

| Group             | Constants                                                                                                        |
| ----------------- | ---------------------------------------------------------------------------------------------------------------- |
| `ContentAnalyzer` | `OpenAI` (`"llmopenai"`), `Anthropic` (`"llmanthropic"`), `DeepSeek` (`"llmdeepseek"`), `Ollama` (`"llmollama"`) |
| `OCR`             | `Gosseract` (`"gosseract"`), `OcrMyPdf` (`"ocrmypdf"`)                                                           |
| `PdfOptimizer`    | `MuPDF` (`"mupdf"`), `GS` (`"gs"`)                                                                               |
| `TextExtractor`   | `MuPDF` (`"mupdf"`), `GoPdf` (`"gopdf"`), `PdfToText` (`"pdftotext"`)                                            |
| `TextReducer`     | `TextRank` (`"textrank"`)                                                                                        |
| `TagMatcher`      | `Hugot` (`"hugot"`)                                                                                              |

A corresponding `AvailableEngines` map provides structured engine listings for the frontend
UI (engine selector dropdowns). All adapter factories (`NewOCR`, `NewTextExtractor`, etc.)
and `Name()` methods use these constants instead of string literals.

---

### Design Note: Context Cancellation in Adapters

Each adapter (CGo and external‑tool) checks for context cancellation at page boundaries.
Additionally, the `Runner` layer wraps every adapter call in `runWithTimeout`, a generic
helper that runs the function in a goroutine and returns `ctx.Err()` if the context
expires first. This ensures timeout detection works for any adapter regardless of
whether it checks `ctx.Done()` internally — eliminating the need for per-adapter
cancellation wiring.

---

## Storage Organization

```
storage/
├── originals/                    # Original files (copied)
│   └── 2024/03/19/15/<uuid>.pdf
├── errors/                       # Failed processing (auto‑quarantined)
│   ├── duplicated/               # Duplicate files
│   │   └── <uuid>-report.pdf
│   └── <uuid>-corrupt.pdf
└── processed/                    # Processed files (OCR'd or optimized)
    └── 2024/03/19/15/<uuid>.pdf
```

Date‑based (`year/month/day/hour/documentID.ext`) under `processed/` avoids "too many files in one directory"
at scale. Dual storage preserves originals alongside processed versions. Files that fail processing
are moved to `errors/` (or `errors/duplicated/` for exact duplicates) with a UUID prefix to prevent
name collisions. Inbox files are always deleted after successful processing.

---

## Search & Retrieval

The search system provides two tiers of document retrieval, both backed by SQLite FTS5 and a dynamic SQL query builder:

### Tier 1: Simple FTS5 Search (`GET /api/v1/documents/search?q=...`)

Quick keyword search for users who just need to find documents by content. The query is sanitized via phrase wrapping and passed to the `document_fts` virtual table. Results are ranked by BM25 relevance and include highlighted `<b>` snippets. No metadata filtering — search terms match against `title` and `text_content` columns.

### Tier 2: Structured Search (`POST /api/v1/documents/search`)

Combines full-text search with arbitrary metadata filters in a single request. The API accepts a JSON `Filter` struct and returns both paginated results and a `total` count for UI pagination. This avoids the common REST anti-pattern of requiring multiple API round-trips (one for filtering, one for counting).

The structured search flows through three layers:

1. **`search.Engine.SearchStructured()`** (`internal/search/search.go`) — Accepts the high-level `Filter` struct (tags, people, document type, language, MIME type, date ranges, file size). Translates it into the database-layer `SearchFilter` struct and calls count + query in sequence.

2. **`queryBuilder`** (`internal/database/structured_search.go`) — Dynamic SQL composition helper that builds `WHERE` clauses with proper positional parameterization (prevents SQL injection). Key patterns:
   - **Tags**: Subquery via `document_tag JOIN tag WHERE tag.name IN (?,?,...)`
   - **People**: Two subqueries — one for person name, one for person type — joined on `document_people`
   - **Document type / language / MIME**: Simple `d.col = ?` equality (skipped when empty)
   - **Date ranges**: `d.col >= ? AND d.col <= ?` with optional from/to
   - **File size**: `d.file_size >= ? AND d.file_size <= ?`
   - **Sorting**: Whitelisted column names (`title`, `mime_type`, `file_size`, `created_at`). When a FTS5 query is present, defaults to BM25 `rank` instead.
   - **Limit/Offset**: Standard `LIMIT ? OFFSET ?` pagination

3. **FTS5 virtual table** (`internal/database/fts5.go`) — Uses `unicode61` tokenizer with no language-specific stemming for multilingual support. Index kept in sync via SQLite triggers (`document_ai`, `document_au`, `document_ad`).

### Result Enrichment

After the database returns rows, the handler batch-fetches tags and people for all result documents in two queries (avoiding N+1), then maps them back to each result.

### Autocomplete System

Four API endpoints support progressive filtering in the UI via prefix-scanned B-tree lookups (no full-text index needed):

| Endpoint                     | Source table    | SQL pattern                       |
| ---------------------------- | --------------- | --------------------------------- |
| `GET /api/v1/tags?q=fin`     | `tag`           | `WHERE name LIKE 'fin%' LIMIT 20` |
| `GET /api/v1/people?q=john`  | `people`        | Same pattern                      |
| `GET /api/v1/people-types`   | `people_type`   | Full list, no filter              |
| `GET /api/v1/document-types` | `document_type` | Full list, no filter              |

### Saved Searches

Users persist search configurations via a simple CRUD API (`saved_search` table). The `filter_json` column stores the raw `Filter` struct as JSON. The frontend restores full filter state (SearchBar chips + FilterPanel) from a single click.

### Frontend Search State

Three layers manage search state on the client:

- **`searchFilter.js`** — Pure utility tokenizer. Parses `field:value` syntax (e.g. `tag:finance size:>1MB`), normalizes date ranges and file sizes, serializes filter state to query strings.
- **`filterStore.js`** — Svelte writable store holding the current `Filter`. Exposes `setPartial()` for incremental updates, `reset()` for clearing. A derived `queryString` store syncs to URL search params.
- **`SearchBar.svelte`** / **`FilterPanel.svelte`** — UI components consuming the store. The search bar shows active filters as color-coded chips with autocomplete on `:` keystrokes. The filter panel provides structured form controls per dimension.

The `field:value` syntax in the search bar supports:

| Prefix     | Example                          | SQL effect                   |
| ---------- | -------------------------------- | ---------------------------- |
| `tag:`     | `tag:finance`                    | Tag subquery filter          |
| `author:`  | `author:"Jane Smith"`            | People subquery, type=author |
| `type:`    | `type:invoice`                   | Document type equality       |
| `lang:`    | `lang:eng`                       | Language equality            |
| `size:`    | `size:>1MB`                      | File size range              |
| `created:` | `created:2024-01-01..2024-06-30` | Date range filter            |

### Why Not a Dedicated Search Engine?

FTS5 is "good enough" for the expected document volume (thousands to low tens of thousands). It provides zero operational overhead, transactionally consistent indexing, and no external processes to manage. The `search.Engine` abstraction provides a clear upgrade path to Meilisearch, ZincSearch, or Elasticsearch should the need arise — the API contract and `Filter` struct remain unchanged.

---

## See Also

- [Roadmap](roadmap.md) — Implementation status and priority queue
- [Database Reference](reference/database.md) — Data models, schema, FTS5 setup, triggers, indexes
- [Tools Reference](reference/tools.md) — Adapter interfaces for enrichment pipeline
- [Pipeline Reference](reference/pipeline.md) — Consumption and enrichment engine details
- [Task System Reference](reference/task-system.md) — Dispatcher and pool internals
- [Search Reference](reference/search.md) — Search engine, structured search, autocomplete, query syntax
- [Testing Reference](reference/tests.md) — Test infrastructure, patterns, and how to run

## Key Design Decisions

| Decision                          | Why                                                                                                                                                                                                                                                                                                                                |
| --------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| PNG for OCR, JPEG for PDF         | Two encodes for two consumers. PNG avoids libjpeg version conflicts between Leptonica and MuPDF. JPEG for fpdf is pure Go.                                                                                                                                                                                                         |
| Text rendering mode 3 (`3 Tr`)    | PDF standard for invisible‑but‑selectable text. Works in all viewers. Set via `SetTextRenderingMode(3)` before `Text()` calls. (Used by gosseract adapter.)                                                                                                                                                                        |
| Embedded CID font                 | LiberationSans registered via `AddUTF8FontFromBytes`. CID Type0 font with Identity-H encoding + ToUnicode CMap makes every language selectable and extractable, even when the font lacks glyphs for that script.                                                                                                                   |
| Static C libraries                | Tesseract, Leptonica, and MuPDF linked statically. Single binary, no runtime deps. See `build-leptonica-tesseract.md`.                                                                                                                                                                                                             |
| fpdf over MuPDF PDF creation      | fpdf is pure Go; MuPDF's PDF writing API requires additional CGo. (Only relevant for gosseract adapter.)                                                                                                                                                                                                                           |
| TextRank over LLM summarization   | Extractive summarization via graph-based ranking is deterministic, zero-cost, privacy-preserving. Reduces token count before LLM call — cost saving and faster.                                                                                                                                                                    |
| Hugot with ORT backend (default)  | ONNX Runtime for GPU-class CPU inference with BGE-M3. Auto-downloads `libonnxruntime.so`. ORT CPU memory arena and memory-pattern pre-allocation are disabled by default (internal `CpuMemArena`/`MemPattern` flags) to keep idle RSS at ~2.2–2.5 GB instead of ~4–5 GB. Go backend available as alternative with no runtime deps. |
| `runWithTimeout` wrapper          | Ensures timeout detection for every adapter call, regardless of whether the adapter checks `ctx.Done()` internally. Eliminates the need for per-adapter cancellation wiring.                                                                                                                                                       |
| Seeded tag vocabulary (Dewey)     | 110+ tags organized by Dewey Decimal Classification. Provides a sensible default taxonomy for LLM classification without requiring user setup.                                                                                                                                                                                     |
| External matcher process          | Hugot embedding model runs as a separate process (`kushim hugot`) over a Unix socket. The API server (`edub`) is pure Go with no CGo dependencies.                                                                                                                                                                                 |
| Forked processing workers         | The `kushim queue` daemon forks `kushim consume --batch` as child processes. Clean process isolation, no in-process heartbeat/ownership management needed. `edub` only enqueues tasks — it never forks directly.                                                                                                                                             |
| `CGO_ENABLED=0` for `edub`        | `edub` is compiled without CGo, making it a lightweight, statically linked binary. No runtime dependency on C libraries. `kushim` retains all CGo for Tesseract/Leptonica/MuPDF/Hugot.                                                                                                                                             |
| Queue-driven batch processing     | Both API consume endpoints create batches with `status='queued'` and return immediately. The `kushim queue` daemon polls for queued batches, enforces its own concurrency limit (`server.max_concurrent_batches`), and forks workers. No in-process semaphore needed in `edub`.                                                     |

---

## Known Limitations

- **FTS5**: No CJK word segmentation; text duplicated in `document` and `document_fts` tables
- **External dependencies**: `pdftotext`, `ghostscript`, and `ocrmypdf` required at runtime (only when using external-tool adapters; Go-native adapters have no runtime deps). The ocrmypdf adapter additionally requires `tesseract` and `unpaper` as companions, with `pngquant` recommended for image optimization. `curl` is required for any download (tessdata, Hugot model). Pre-flight checks at the consume entry point surface these requirements with actionable install hints.
- **Build‑time**: Requires `gcc`, `gcc-c++`, `make`, `autotools` for Leptonica/Tesseract/MuPDF compilation. Additionally `libtokenizers.a` downloaded as a pre-built binary for Hugot. The web UI requires Node.js 24 (see `.nvmrc` — run `nvm use` to activate). Use `docker compose up` to avoid installing any host-side toolchain — the multi-stage Dockerfile handles all build dependencies inside a container.
- **MuPDF**: Compiled from source (1.28.0) via `make build-deps` (or inside the Docker build)
- **Malformed PDFs**: MuPDF's `pdf_clean_file` may fail on PDFs with invalid patterns
  or bogus font metrics. When this happens, the original file is used as the processed
  copy — ingestion is not blocked. Set `pdfoptimizer.fallback: 'gs'` to fall
  back to Ghostscript for these files.
- **Hugot ORT backend**: ONNX Runtime downloaded at runtime on first use — requires internet access. The Go backend has no runtime deps. ORT's CPU memory arena and memory pattern pre-allocation are disabled by default (`HugotConfig.CpuMemArena=false`, `MemPattern=false`) to cap idle RSS at ~2.2–2.5 GB rather than retaining peak-inference buffers (~4–5 GB). This adds ~10–20% per-inference latency from buffer re-allocation, which is dwarfed by text extraction, OCR, and LLM API latency in the enrichment pipeline. Toggle to `true` in `DefaultConfig` to restore ORT defaults if performance is unacceptable.
- **Matcher as external process**: The tag matcher runs as a separate process (`kushim hugot`). If it's not running, tag CRUD operations return `503 Service Unavailable`, and enrichment falls back to LLM-only tags (no semantic tag matching). The matcher must be started before `edub` for full functionality.
- **Queue daemon forks kushim**: The `kushim queue` daemon finds the `kushim` binary in PATH or as a sibling of the `edub` binary. If `kushim` is not found, batch processing fails. The API consume endpoints no longer fork directly — they create `queued` batches for the daemon to pick up.

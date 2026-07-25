# Architecture & Design

## Core Design Principles

- **Headless First**: API-driven with CLI interface; web UI as optional layer
- **Tool Agnostic**: Adapter pattern; OCR, text extraction, PDF optimization, content analysis,
  text reduction, and semantic tag matching all switchable between built‑in adapters and
  external tools/providers
- **PostgreSQL Target**: SQL and generated Go code target PostgreSQL. SQL files use `$1, $2` placeholders and PostgreSQL DDL (`GENERATED ALWAYS AS IDENTITY`, `JSONB`, `TIMESTAMPTZ`). sqlc engine is `postgresql`. The `kushim setup` wizard, `Bootstrap`, and `NewTestDB` all use `NewPostgresDB`.
- **Fallback Processing**: Text extraction → OCR → text extraction pattern
- **Date-based Organization**: Temporal storage structure for scalability
- **Transaction Safety**: Coordinated database and file operations with rollback
- **Process Isolation**: The Hugot embedding model runs as a **separate process** (`kushim hugot`), communicating over a Unix domain socket. The API server (`edub`) is pure Go (`CGO_ENABLED=0`) — it enqueues tasks and the `kushim queue` daemon forks workers for document processing; `edub` communicates with the matcher via RPC.
- **Two Binaries**: **kushim** (CLI document processing + matcher server) and **edub** (REST API server). Static C dependencies (Tesseract, Leptonica, MuPDF) linked only into **kushim**; **edub** is compiled with `CGO_ENABLED=0`.

See [Roadmap](roadmap.md) for the implementation status and upcoming priorities.

---

## Process Architecture

The system runs four cooperating processes. All three long-running services
(`kushim hugot`, `kushim queue`, `edub`) are grouped under an
`edub-kushim.target` systemd target via `PartOf=`, allowing a single
`systemctl enable --now edub-kushim.target` to manage all of them. The hugot
service uses `Type=notify` + `NotifyAccess=main` and sends `READY=1` via
sd_notify after the model is loaded, tag cache is built, and the Unix socket
is listening, so `After=` dependents actually gate on readiness.

### 1. Queue Daemon (`kushim queue`)

A long-lived daemon that manages batch processing and inbox polling. Batch consumption is driven by Postgres `LISTEN`/`NOTIFY` (via a dedicated pgxpool connection), with a 30-second safety timer as fallback. Housekeeping tasks (stale reclamation, backup scheduling) run on a separate 5-second ticker. The concurrent loops are: (a) **notification-driven consumption** — when any batch transitions to `status='queued'`, a Postgres trigger (`notify_batch_queued`) sends a `pg_notify` to the `batch_queued` channel; a dedicated `LISTEN` goroutine forwards the signal over a buffered channel, the daemon picks up the batch immediately (milliseconds) and forks a worker. (b) **safety timer** — a 30-second fallback poll in case a notification is dropped (connection blip, reconnection), guaranteeing no batch is stuck longer than 30s. (c) **stale batch reclamation** — reclaims stale batch owners (>15s heartbeat with active tasks) by signaling SIGTERM to the owner PID (if alive), quarantining tasks at or above `consumer.reclaim.max_retries` (default 3) to `failed`, resetting remaining processing→pending with an incremented attempt counter, calling `QuarantineFailedFiles` to move quarantined inbox files to `storage/errors/` and discard orphaned enrich tasks, then checking `HasPendingWork`. If the batch has remaining pending/processing/waiting tasks it is re-queued; otherwise (all tasks quarantined) it is set to `failed`. The stale owner row is then deleted (gated by `consumer.reclaim.enabled`, default `true`). (d) **stale task reclamation** — an age-based sweep that resets individual `processing` tasks back to `pending` (or `failed` if at max retries) when their `started_at` timestamp exceeds `consumer.reclaim.stale_task_after` (default 600s). This is the safety net for tasks that the runner's retry+FailTask fallback could not complete. The sweep runs at most once per `max(60s, stale_task_after/10)` to avoid unnecessary write-lock pressure. (e) **inbox polling** — periodically scans the consumption directory, deduplicates by MD5, creates `queued` batches, and lets the consumer loop pick them up. When `backup.enabled` is `true`, a **backup pool** (1 worker, 60s interval) picks up scheduled backup tasks. The main ticker and polling loop both use `backupMu.TryRLock()` to skip all operations while a backup is running, preventing concurrent data access. Re-reads config from disk on each poll tick so `consumer.polling` changes take effect without restart. Replaces the former `PollingScheduler`. Uses a PID file for single-instance enforcement and logs to `<config_dir>/logs/queue.log`. Can be daemonized via `--bg`.

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
- `kushim backup` — create a backup of database, config, and storage files
- `kushim restore` — restore from a backup archive

### Communication Flow

```
  HTTP Request
       │
       ▼
┌──────────────┐     enqueue tasks      ┌──────────────────┐
│    edub      │ ──────────────────────▶ │  PostgreSQL DB   │
│ (CGO_ENABLED │                         │  (status='       │
│     =0)      │                         │   queued')       │
└──────────────┘                         └──────┬───────────┘
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
auto-increment ID obtained from `RETURNING id`, date‑based storage paths
generated, paths updated via `UpdateDocumentPaths`. The transaction is
DB‑only (fast writes), bounded by a 5‑second context timeout.

The UUID serves as the stable external identifier for the API, while the
auto-increment ID is used for internal storage paths.

### 5. File Movement (pre‑transaction)

File I/O runs **before** the DB transaction to avoid timeouts on slow storage.
If file I/O fails, any partially‑written storage files are cleaned up and the
original is quarantined — no DB row is created. Three‑way branch:

- **OCR case**: Move OCR temp file → processed storage, copy original → original storage.
- **Optimized text case**: Copy original → original storage, move optimized file → processed storage.
- **Unoptimized text case** (optimization failed or skipped): Copy original → both processed and original storage.

### 6. Transaction and Cleanup

After files are in place, a short transaction creates the document record and
updates storage paths. On transaction failure (statement error or commit
failure), both storage files are removed and the original is quarantined. The
deferred rollback also handles temporary file cleanup (OCR / optimized PDF
temp files). On success, the inbox original is removed.

### 7. FTS Indexing

The `document` table has a `text_search_vector` column of type `tsvector`,
`GENERATED ALWAYS AS (to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(text_content, ''))) STORED`,
backed by a GIN index (`idx_document_tsv`). Queries use `plainto_tsquery('simple', ...)`
for tokenization and `@@` for matching. Rank and snippet are produced by `ts_rank`
and `ts_headline`.

### 8. Async Enrichment (Post-Consume)

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
 3. **LLM Classification (first pass)** — the reduced content, along with available document types
     and tag suggestions, is sent to the configured LLM adapter. The adapter is selected by
     `llm.adapter` (`openai-compatible`, `anthropic`, or `custom`) and the request body
     is built dynamically from the model's capability flags (reasoning, structured output,
     temperature, etc.) looked up from the model catalog registry. Returns structured JSON:
     title, type, tags, people (with types like author, sender), language.
     For non-Latin names (Korean, Arabic, Cyrillic, etc.), the LLM is prompted to
     provide a `name_romanized` field alongside the original name.
     If the response is entirely empty, one automatic retry is performed.

 3a. **Document Type Refinement (second pass, optional)** — if TextRank actually reduced the
    document content (i.e. `document.WordCount > target_word_count`), a second LLM call
    re-evaluates only the document type using head+tail of the raw full text. The full
    conversation history (system + user prompt + first-pass assistant response) is sent
    so providers with prompt caching (OpenAI, Anthropic, DeepSeek) re-use the shared
    prefix. The head+tail is extracted via `ExtractHeadTailWords` (default 600 head, 400
    tail words) separated by a `[...]` marker. On error the first-pass type is preserved.
    Configured via `enricher.contentanalyzer.doc_type_refinement` (enabled by default).
4. **Tag Normalization** — LLM-extracted tags are normalized to canonical space-separated
   form via `NormalizeTags`: lowercased, hyphens/underscores→spaces, non-alpha stripped,
   whitespace collapsed, deduplicated. This ensures the LLM's hyphenation instructions
   or symbol handling don't produce OOD tokens in the embedding model.
5. **Post-LLM Tag Consolidation** — Normalized tags are re-matched against canonical
   tag embeddings via `Consolidate` (delegated to the matcher interface), fixing casing
   and synonym mismatches that survive the normalization step.
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
4. **Stores native script** — only when the model independently determines a
   person's genuine native form (e.g. a Japanese author in a Japanese document);
   left empty for document-language renderings of foreign names.

This pipeline is non-blocking: documents are stored and searchable immediately via
tsvector, with enrichment arriving asynchronously.

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
3. **Enricher settings** — tag matcher engine/timeout, reduce-target-words, chunk size, Hugot model/backend;
   text reducer engine/timeout/target-words; enricher workers
4. **Progress** — background tasks download tessdata language files and the Hugot ONNX model;
   the UI polls `GET /wizard/config/status` every 3 seconds
5. **Completion** — all downloads are finished; the server is ready to run.
   If any required external tools are missing, a notice lists what to install
   (engine binaries, required companions like tesseract/unpaper, and the curl
   prerequisite). The wizard does not block completion — tools can be installed
   later.

The wizard server uses a stripped-down version of the full server stack: it bootstraps
the config directory (via `config.Bootstrap`), initializes the PostgreSQL schema, creates
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
enricher workers; content analyzer enabled toggle + adapter/provider/model cascading selectors (loaded from model catalog) + token + temperature + reasoning;
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

Engine names (e.g. `"mupdf"`, `"gosseract"`) are now defined as
exported package-level vars in `internal/config/config.go`:

| Group             | Constants                                                                                                        |
| ----------------- | ---------------------------------------------------------------------------------------------------------------- |
| `OCR`             | `Gosseract` (`"gosseract"`), `OcrMyPdf` (`"ocrmypdf"`)                                                           |
| `PdfOptimizer`    | `MuPDF` (`"mupdf"`), `GS` (`"gs"`)                                                                               |
| `TextExtractor`   | `MuPDF` (`"mupdf"`), `GoPdf` (`"gopdf"`), `PdfToText` (`"pdftotext"`)                                            |
| `TextReducer`     | `TextRank` (`"textrank"`)                                                                                        |
The content analyzer no longer uses engine constants — it selects an adapter via `llm.adapter`
(`openai-compatible`, `anthropic`, `custom`) resolved dynamically from the
capability registry.

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

The search system provides two tiers of document retrieval, backed by a PostgreSQL tsvector generated column and a dynamic SQL query builder:

### Tier 1: Simple tsvector Search (`GET /api/v1/documents/search?q=...`)

Quick keyword search for users who just need to find documents by content. The query is sanitized and passed to `plainto_tsquery('simple', ...)` against the `text_search_vector` generated column. Results are ranked by `ts_rank` and include highlighted `<b>` snippets via `ts_headline`. Before returning, the snippet is HTML-escaped via `sanitizeSnippetHTML` which preserves `<b>`/`</b>` highlighting while escaping all other HTML to prevent XSS. No metadata filtering — search terms match against `title` and `text_content` columns.

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
   - **Sorting**: Whitelisted column names (`title`, `mime_type`, `file_size`, `created_at`). When a tsquery is present, defaults to `ts_rank` instead.
   - **Limit/Offset**: Standard `LIMIT ? OFFSET ?` pagination

3. **tsvector generated column** — The `document` table's `text_search_vector` column is `GENERATED ALWAYS AS (to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(text_content, ''))) STORED`. Queries use `plainto_tsquery('simple', ...)` for tokenization and `@@` for matching, backed by a GIN index (`idx_document_tsv`).

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

PostgreSQL tsvector provides zero-operational-overhead full-text search with transactionally
consistent indexing (via `GENERATED ALWAYS AS`). The `search.Engine` abstraction provides a
clear upgrade path to Meilisearch, ZincSearch, or Elasticsearch if needed.

---

## See Also

- [Roadmap](roadmap.md) — Implementation status and priority queue
- [Database Reference](reference/database.md) — Data models, schema, tsvector column, indexes
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
| Queue-driven batch processing     | Both API consume endpoints create batches with `status='queued'` and return immediately. The `kushim queue` daemon polls for queued batches (via Postgres LISTEN/NOTIFY), enforces its own concurrency limit (`server.max_concurrent_batches`, default 4), and forks workers. No in-process semaphore needed in `edub`.                                                     |
| CGo-heavy adapters run in subprocesses | Long-running CGo calls starve the Go scheduler's goroutine preemption (heartbeat goroutines never fire inside Tesseract/MuPDF calls). Following the `internal-mupdf-clean` and `kushim hugot` precedents, any adapter wrapping a substantial third-party native library — one that can run for more than a second or two, or that may have its own internal threading or crash surface — gets its own subcommand and is forked as a child process via `exec.CommandContext`. Thin, fast, well-understood CGo calls (page counts, plain text extraction) stay in-process. |

---

## Known Limitations

- **tsvector `simple` config**: No CJK word segmentation. Upgrade to `'english'` or a custom config for stemming if needed.
- **External dependencies**: `pdftotext`, `ghostscript`, and `ocrmypdf` required at runtime (only when using external-tool adapters; Go-native adapters have no runtime deps). The ocrmypdf adapter additionally requires `tesseract` and `unpaper` as companions, with `pngquant` recommended for image optimization. `curl` is required for any download (tessdata, Hugot model). Pre-flight checks at the consume entry point surface these requirements with actionable install hints.
- **Build‑time**: Requires `gcc`, `gcc-c++`, `make`, `autotools` for Leptonica/Tesseract/MuPDF compilation. Additionally `libtokenizers.a` downloaded as a pre-built binary for Hugot. The web UI requires Node.js 24 (see `.nvmrc` — run `nvm use` to activate). Use `docker compose up` to avoid installing any host-side toolchain — the multi-stage Dockerfile handles all build dependencies inside a container.
- **MuPDF / Tesseract CGo isolation**: Long-running CGo operations (OCR via gosseract, PDF cleanup via pdf_clean_file) are isolated into forked subprocesses (`kushim internal-ocr`, `kushim internal-mupdf-clean`) so that crashes or scheduler starvation in the C library affect only the child. The parent receives a normal Go error and can fall back or retry. Thin CGo calls like page counting remain in-process.
- **Malformed PDFs**: MuPDF's `pdf_clean_file` may fail on PDFs with invalid patterns
  or bogus font metrics. The call runs in a **subprocess** (`kushim internal-mupdf-clean`)
  — a crash kills only the child and returns a normal Go error to the parent. When MuPDF
  fails, the configured fallback engine is invoked, or the original file is used as the
  processed copy if no fallback is set. Configure via `pdfoptimizer.fallback: 'gs'` to
  use Ghostscript as the alternative.

  Additionally, the **page render pipeline** has a hard cap of 100 MP
  (`MUPDF_MAX_RENDER_PIXELS`) on the pixel area of any single rendered page.
  This runs before pixmap allocation, preventing a malicious or malformed PDF
  with an extreme MediaBox from causing an unbounded memory allocation (memory-
  exhaustion DoS). The ceiling allows large-format posters (~40×60 in at
  200 DPI) while keeping the RGB buffer under ~300 MB.
- **Hugot ORT backend**: ONNX Runtime downloaded at runtime on first use — requires internet access. The Go backend has no runtime deps. ORT's CPU memory arena and memory pattern pre-allocation are disabled by default (`HugotConfig.CpuMemArena=false`, `MemPattern=false`) to cap idle RSS at ~2.2–2.5 GB rather than retaining peak-inference buffers (~4–5 GB). This adds ~10–20% per-inference latency from buffer re-allocation, which is dwarfed by text extraction, OCR, and LLM API latency in the enrichment pipeline. Toggle to `true` in `DefaultConfig` to restore ORT defaults if performance is unacceptable.
- **Role enforcement**: All API routes are gated by `RequireRole` middleware. The auth middleware reads the user's role from the database (for both JWT and API key paths) and injects it into request context. Three roles exist: `admin` (user management, logs), `editor` (all mutations, batch operations, consume), and `viewer` (read-only access). Self-service `/me` endpoints are accessible to any authenticated user. The `auth_enabled: false` config bypasses all middleware, making roles irrelevant.
- **Matcher as external process**: The tag matcher runs as a separate process (`kushim hugot`). If it's not running, tag CRUD operations return `503 Service Unavailable`, and enrichment falls back to LLM-only tags (no semantic tag matching). The matcher must be started before `edub` for full functionality.
- **Queue daemon forks kushim**: The `kushim queue` daemon finds the `kushim` binary in PATH or as a sibling of the `edub` binary. If `kushim` is not found, batch processing fails. The API consume endpoints no longer fork directly — they create `queued` batches for the daemon to pick up.

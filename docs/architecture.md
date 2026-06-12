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
- **Two Binaries**: **kushim** (CLI document processing) and **edub** (REST API server). Static C dependencies (Tesseract, Leptonica, MuPDF) linked into both.

See [Roadmap](roadmap.md) for the implementation status and upcoming priorities.

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

After a `consume` task completes, an `enrich` task is automatically enqueued
(via `Dispatcher.Next()` after `CompleteTask`). The enrichment pipeline:

1. **Text Reduction** (optional, configurable threshold) — if the document exceeds
   `enricher.textreducer.target_words`, TextRank extracts the most salient content.
2. **Semantic Tag Pre-filtering** — document content is embedded via Hugot and cosine-
   matched against all known tag embeddings. Top-N matches above `min_similarity`
   are passed to the LLM as suggestions.
3. **LLM Classification** — the reduced content, along with available document types
   and tag suggestions, is sent to the configured LLM provider. Returns structured
   JSON: title, type, tags, people (with types like author, sender), language.
4. **Post-LLM Tag Consolidation** — LLM output labels are re-matched against canonical
   tag embeddings via `MatchEach`, fixing casing, hyphenation, and synonym mismatches.
5. **New Tag Cache Update** — any new tags created during enrichment are immediately
   encoded via Hugot and added to the embedding cache, making them available for
   matching against subsequent documents.
6. **Result Logging** — token usage stats and prompt text are logged.

This pipeline is non-blocking: documents are stored and searchable immediately via
FTS5, with enrichment arriving asynchronously.

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
│   └── 2024/03/19/1.pdf
└── 2024/                         # Processed files (OCR'd or optimized)
    └── 03/19/1.pdf
```

Date‑based (`year/month/day/documentID.ext`) to avoid "too many files in one directory"
at scale. Dual storage preserves originals alongside processed versions.

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

## Key Design Decisions

| Decision                        | Why                                                                                                                                                                                                              |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| PNG for OCR, JPEG for PDF       | Two encodes for two consumers. PNG avoids libjpeg version conflicts between Leptonica and MuPDF. JPEG for fpdf is pure Go.                                                                                       |
| Text rendering mode 3 (`3 Tr`)  | PDF standard for invisible‑but‑selectable text. Works in all viewers. Set via `SetTextRenderingMode(3)` before `Text()` calls. (Used by gosseract adapter.)                                                      |
| Embedded CID font               | LiberationSans registered via `AddUTF8FontFromBytes`. CID Type0 font with Identity-H encoding + ToUnicode CMap makes every language selectable and extractable, even when the font lacks glyphs for that script. |
| Static C libraries              | Tesseract, Leptonica, and MuPDF linked statically. Single binary, no runtime deps. See `build-leptonica-tesseract.md`.                                                                                           |
| fpdf over MuPDF PDF creation    | fpdf is pure Go; MuPDF's PDF writing API requires additional CGo. (Only relevant for gosseract adapter.)                                                                                                         |
| TextRank over LLM summarization | Extractive summarization via graph-based ranking is deterministic, zero-cost, privacy-preserving. Reduces token count before LLM call — cost saving and faster.                                                  |
| Hugot with Go backend (default) | Pure Go inference — static linking via `libtokenizers.a`. No external runtime deps. ORT backend available for ONNX acceleration, auto-downloads `libonnxruntime.so`.                                             |
| `runWithTimeout` wrapper        | Ensures timeout detection for every adapter call, regardless of whether the adapter checks `ctx.Done()` internally. Eliminates the need for per-adapter cancellation wiring.                                     |
| Seeded tag vocabulary (Dewey)   | 110+ tags organized by Dewey Decimal Classification. Provides a sensible default taxonomy for LLM classification without requiring user setup.                                                                   |

---

## Known Limitations

- **FTS5**: No CJK word segmentation; text duplicated in `document` and `document_fts` tables
- **Web UI detail page**: The document detail page displays metadata and PDF preview but does not yet show tags, people, or document type. Edit functionality (title, tags, people, type) is pending the Tier 2 API endpoints.
- **External dependencies**: `pdftotext`, `ghostscript`, and `ocrmypdf` required at runtime (only when using external-tool adapters; Go-native adapters have no runtime deps)
- **Build‑time**: Requires `gcc`, `gcc-c++`, `make`, `autotools` for Leptonica/Tesseract/MuPDF compilation. Additionally `libtokenizers.a` downloaded as a pre-built binary for Hugot
- **MuPDF**: Compiled from source (1.27.2) via `make build-deps`
- **Malformed PDFs**: MuPDF's `pdf_clean_file` may fail on PDFs with invalid patterns
  or bogus font metrics. When this happens, the original file is used as the processed
  copy — ingestion is not blocked. Set `pdfoptimizer.fallback: 'gs'` to fall
  back to Ghostscript for these files.
- **Hugot ORT backend**: ONNX Runtime downloaded at runtime on first use — requires internet access. The Go backend has no runtime deps.

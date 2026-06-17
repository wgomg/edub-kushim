# edub-kushim — User Manual

Two binaries: **kushim** (CLI for document processing) and **edub** (HTTP API server).
Both share the same OCR, text extraction, and PDF optimization pipeline.

---

## Quick Start

```bash
# One‑time setup — launches a web wizard at http://0.0.0.0:8420
kushim setup

# Or use terminal-based setup (headless / CI)
kushim setup --cli --languages eng,spa

# Process documents
cp my-documents/*.pdf ~/.config/edub-kushim/inbox/

# CLI mode (foreground, enqueue + process + show per-file progress)
kushim consume

# CLI mode (background, releases console immediately)
kushim consume --bg

# Resume an existing batch
kushim consume --batch <batch-id>

# API server mode
edub
curl -X POST http://localhost:3000/api/v1/consume
```

---

## CLI Reference

### `kushim version`

Print the application version.

```
kushim version
Document Management System v0.1.0
```

### `kushim setup`

Generate a config file, create required directories, initialize the SQLite
database schema, download OCR language data from `tessdata_fast`, and
download the Hugot embedding model (`BAAI/bge-m3`).

By default, `kushim setup` launches a **web-based setup wizard** at
`http://0.0.0.0:8420`. The wizard provides a four-step guided flow:

1. **Config directory** — specify where configuration, database, and models are stored
2. **Settings** — choose OCR engine, add languages, configure worker counts
3. **Progress** — shows download progress for tessdata and Hugot model
4. **Completion** — ready to run `edub` to start the server

Use `--cli` for terminal-based setup in headless or CI environments:

```
kushim setup --cli --languages eng,spa
```

| Flag                               | Default                | Description                                                 |
| ---------------------------------- | ---------------------- | ----------------------------------------------------------- |
| `--cli`                            | `false`                | Run terminal-based setup instead of the web wizard          |
| `--languages`                      | — (required for CLI)   | Comma‑separated OCR language codes (e.g. `eng,spa,deu`)     |
| `--inbox-path`                     | _config-dir_`/inbox`   | Consumption directory (scanned for files)                   |
| `--storage-path`                   | _config-dir_`/storage` | Processed file storage root                                 |
| `--database-path`                  | _config-dir_`/data`    | SQLite database directory                                   |
| `--consumer-pdfoptimizer-engine`   | `mupdf`                | PDF optimizer: `mupdf` or `gs`                              |
| `--consumer-pdfoptimizer-fallback` | —                      | Fallback PDF optimizer binary (ignored when engine is `gs`) |
| `--consumer-ocr-engine`            | `gosseract`            | OCR engine: `gosseract` or `ocrmypdf`                       |
| `--reset-database`                 | `false`                | Drop all tables and re-run schema + seeders                 |

The flags `--inbox-path`, `--storage-path`, and `--database-path` accept either
absolute paths or paths starting with `~` (expanded to the home directory).

### `kushim consume`

Scan the inbox directory, create one task per file, and process them.
Streams per-file progress to stdout.

```
kushim consume
```

#### `--force`

Override a stale PID file lock when resuming a batch. Useful if the
previous process was killed with `SIGKILL` and the PID file was left behind.

```
kushim consume --batch 550e8400-e29b-41d4-a716-446655440000 --force
```

#### `kushim consume cancel <batch-id>`

Cancel a running batch. Pending tasks are marked as `cancelled` in the
database and a `SIGTERM` is sent to the process currently processing the
batch. Any task that was in‑flight at the moment of cancellation is also
marked as `cancelled`.

```
kushim consume cancel 550e8400-e29b-41d4-a716-446655440000
```

Output:

```
Batch 550e8400-e29b-41d4-a716-446655440000: 5 pending + 1 processing cancelled, signal sent to PID 12345
```

If the process is no longer running (e.g. already crashed):

```
Process 12345 is no longer running
5 pending tasks cancelled
```

If no process was found (batch finished or never started):

```
No running process found for batch 550e8400-e29b-41d4-a716-446655440000
5 pending tasks cancelled
```

Output during processing (default mode):

```
[1/3] report.pdf → queued
[2/3] invoice.pdf → queued
[3/3] contract.pdf → queued
Waiting for completion...
  [1/3] consume  report.pdf ... processing
  [1/3] consume  report.pdf ... done
  [2/3] consume  invoice.pdf ... processing
  [2/3] consume  invoice.pdf ... done
  [3/3] consume  contract.pdf ... processing
  [3/3] consume  contract.pdf ... done

Summary: 3 files, 6 tasks — all successful
```

Each file produces two linked tasks (`consume` then `enrich`). Both
are shown as they progress. The `enrich` tasks appear as they become
pending after their associated `consume` task completes. Partial
failures show per-task error messages:

```
  [3/3] consume  contract.pdf ... failed: text extraction failed
```

#### `--bg`

Enqueue all files, then hand off processing to a detached child process
and return immediately. The console is released.

```
kushim consume --bg
```

Output:

```
Batch: 550e8400-e29b-41d4-a716-446655440000
Files: 3
Use 'kushim task list --batch 550e8400-e29b-41d4-a716-446655440000' to track progress.
```

The child process runs `consume --batch <id>` independently and inherits
the same config and database path. Use `kushim task list` to monitor progress.

#### `--batch <id>`

Resume processing of an already-enqueued batch. Skips the inbox scan
and picks up where the batch left off. Useful for retrying failed tasks
or restarting a background batch.

```
kushim consume --batch 550e8400-e29b-41d4-a716-446655440000
```

Output on resume:

```
Resuming batch 550e8400-e29b-41d4-a716-446655440000 (2 pending)...
  [2/3] consume  invoice.pdf ... done
  [3/3] consume  contract.pdf ... done

Summary: 3 files, 6 tasks — all successful
```

If the batch is already finished:

```
batch already finished
```

If the batch does not exist:

```
batch not found
```

`--bg` and `--batch` are mutually exclusive.

### `kushim search`

Full‑text search across indexed documents using SQLite FTS5.

```
kushim search "budget report"
kushim search --limit 10 --offset 0 "quarterly earnings"
kushim search --rebuild-index
```

| Flag              | Default | Description                                |
| ----------------- | ------- | ------------------------------------------ |
| `--limit`         | `20`    | Max results (1–100)                        |
| `--offset`        | `0`     | Result offset for pagination               |
| `--rebuild-index` | `false` | Rebuild FTS5 index from the document table |

The query is wrapped in double-quotes for FTS5 phrase escaping (allowing
AND/OR operators, `"phrase"`, and prefix wildcards like `budg*`).
Results show highlighted matches (ANSI bold/yellow).

Output example:

```
─── #42 ─────────────────────────────────────────────
  quarterly-report-2024.pdf
  The <b>budget</b> forecast for Q3 shows a <b>15% increase</b> in revenue
  rank=0.4213  |  1.2 MB  |  2024-03-19
```

### `kushim task list`

List tasks with optional filters.

```
kushim task list
kushim task list --batch 550e8400-e29b-41d4-a716-446655440000
kushim task list --batch 550e8400-e29b-41d4-a716-446655440000 --status failed
kushim task list --status pending --limit 50
```

| Flag       | Default | Description                                                                                           |
| ---------- | ------- | ----------------------------------------------------------------------------------------------------- |
| `--batch`  | all     | Filter by batch UUID                                                                                  |
| `--status` | all     | Filter by status: `waiting`, `pending`, `processing`, `completed`, `failed`, `cancelled`, `discarded` |
| `--limit`  | `20`    | Max results (1–100)                                                                                   |
| `--offset` | `0`     | Result offset for pagination                                                                          |

Output:

```
TASK ID                              STATUS       BATCH        FILE
--------------------------------------------------------------------------------
660e8400-e29b-41d4-a716-446655440001 completed    550e8400-e2… report.pdf
770e8400-e29b-41d4-a716-446655440002 completed    550e8400-e2… invoice.pdf
880e8400-e29b-41d4-a716-446655440003 failed       550e8400-e2… contract.pdf
```

### `kushim task status`

Show detailed information about a single task.

```
kushim task status 660e8400-e29b-41d4-a716-446655440001
```

Output:

```
Task ID:    660e8400-e29b-41d4-a716-446655440001
Batch ID:   550e8400-e29b-41d4-a716-446655440000
Status:     completed
File:       report.pdf
Created:    2024-03-19T10:30:00Z
Started:    2024-03-19T10:30:05Z
Completed:  2024-03-19T10:30:12Z
Document: 550e8400-e29b-41d4-a716-446655440000
```

Note: `Error:` only appears when the task has failed.

```

### `kushim task retry`

Reset a failed task's status to `pending` so a worker retries it.

```

kushim task retry 880e8400-e29b-41d4-a716-446655440003

```

Output:

```

Task "880e8400-e29b-41d4-a716-446655440003" retried — status reset to pending

```

Only failed tasks can be retried. The task is picked up on the next
worker poll cycle.

---

## API Reference

The API server listens on `localhost:3000` by default (configurable in YAML).

### Health Check

```

GET /health

````

Response `200`:

```json
{
  "status": "healthy",
  "version": "0.1.0",
  "time": "2024-03-19T10:30:00Z"
}
````

### List Documents

```
GET /api/v1/documents?limit=50&offset=0&sort_by=created_at&sort_order=desc
```

| Query param  | Default      | Description                                                  |
| ------------ | ------------ | ------------------------------------------------------------ |
| `limit`      | `50`         | Max documents (1–100)                                        |
| `offset`     | `0`          | Pagination offset                                            |
| `sort_by`    | `created_at` | Sort column: `title`, `mime_type`, `file_size`, `created_at` |
| `sort_order` | `desc`       | Sort direction: `asc` or `desc`                              |

Response `200` — array of [DocumentResponse](#documentresponse).

### Get Document

```
GET /api/v1/documents/{id}
```

Response `200` — single [DocumentResponse](#documentresponse).

### Get Document File

```
GET /api/v1/documents/{id}/file
```

Returns the raw PDF file bytes for preview. Response `200` with `Content-Disposition: inline`.
Returns `415 Unsupported Media Type` for non-PDF documents.

### Search Documents

```
GET /api/v1/documents/search?q=<query>&limit=50&offset=0
```

| Query param | Default      | Description                                              |
| ----------- | ------------ | -------------------------------------------------------- |
| `q`         | — (required) | FTS5 query (supports AND, OR, NOT, `"phrase"`, prefix\*) |
| `limit`     | `50`         | Max results (1–100)                                      |
| `offset`    | `0`          | Pagination offset                                        |

Response `200` — array of `FTSDocumentResponse` (adds `rank`, `snippet`, `text_content`).

### Structured Search

Searches documents with combined full-text and metadata filters.

```
POST /api/v1/documents/search
Content-Type: application/json

{
  "query": "quarterly report",
  "tags": ["finance", "budget"],
  "people": [{ "name": "John Doe", "type": "author" }],
  "document_type": "invoice",
  "language": "eng",
  "mime_type": "application/pdf",
  "date_created": { "from": "2024-01-01", "to": "2024-12-31" },
  "date_modified": { "from": null, "to": "2024-06-01" },
  "file_size": { "min": 0, "max": 10485760 },
  "sort_by": "created_at",
  "sort_order": "desc",
  "limit": 50,
  "offset": 0
}
```

Response `200` — `SearchResponse`:

```json
{
  "results": [
    {
      "document_id": "550e8400-e29b-41d4-a716-446655440000",
      "title": "report.pdf",
      "rank": 0.4213,
      "snippet": "The <b>budget</b> forecast...",
      "tags": [{ "id": 1, "name": "finance" }],
      "people": [{ "id": 1, "name": "John Doe", "person_type_name": "author" }]
    }
  ],
  "total": 42
}
```

### Autocomplete (Tags, People, Document Types, Person Types)

```
GET /api/v1/tags?q=fin&limit=10
GET /api/v1/people?q=john&limit=10
GET /api/v1/people-types
GET /api/v1/document-types
```

Response `200` — array of `{ id, name }` (people-types and document-types add `description`).

### Saved Searches

Save, list, and delete named search configurations.

```
POST /api/v1/saved-searches
Content-Type: application/json

{ "name": "Invoices from Q1", "filter": { "tags": ["finance"], "document_type": "invoice", ... } }
```

Response `201`:

```json
{ "id": 1 }
```

```
GET /api/v1/saved-searches
```

Response `200`:

```json
[
  { "id": 1, "name": "Invoices from Q1", "filter": { ... }, "created_at": "2024-03-19T10:30:00Z" }
]
```

```
DELETE /api/v1/saved-searches/{id}
```

Response `204 No Content`.

### Configuration (Wizard API)

Read and update the configuration via the API. These endpoints are used by
both the web wizard and the in-app settings page.

```
GET /wizard/config
```

Returns the current configuration as a `ConfigResponse` JSON object with
`app`, `consumer`, and `enricher` sections plus `available_engines` for UI dropdowns.
Returns defaults from `DefaultConfig("")` when no config has been bootstrapped yet,
so the response always has a complete shape.

```
PUT /wizard/config
Content-Type: application/json

{ "config_dir": "/home/user/.config/edub-kushim" }
```

Two-phase API:
- **Bootstrap phase** — send `{ "config_dir": "..." }` to create directories,
  write skeleton config, and initialize the database. Returns `200` with
  `{ "config_dir": "..." }`.
- **Update phase** — send settings key-value pairs (dot notation) to update
  the config and trigger background downloads:

```json
{
  "consumer.ocr.engine": "gosseract",
  "consumer.ocr.languages": ["eng", "spa"],
  "consumer.workers": 2,
  "enricher.workers": 2
}
```

Returns `201` with `{ "pending_tasks": 3 }` when downloads are enqueued,
or `200` with `{ "configured": true }` when all dependencies are already present.

```
GET /wizard/config/status
```

Returns `{ "configured": bool, "pending_tasks": int, "errors": []string }`.
The wizard and settings page poll this endpoint to track download progress.

---

### Consume

Enqueue all files in the consumption directory as tasks.

```
POST /api/v1/consume
```

Response `202`:

```json
{
  "batch_id": "550e8400-e29b-41d4-a716-446655440000",
  "total_files": 5,
  "enqueued": 5,
  "_links": {
    "tasks": "/api/v1/tasks?batch=550e8400-e29b-41d4-a716-446655440000"
  }
}
```

### List Tasks

```
GET /api/v1/tasks
GET /api/v1/tasks?batch=<batch_id>
GET /api/v1/tasks?batch=<batch_id>&status=pending&limit=20&offset=0
GET /api/v1/tasks?status=failed
```

| Query param | Default | Description                                                                                 |
| ----------- | ------- | ------------------------------------------------------------------------------------------- |
| `batch`     | all     | Filter by batch UUID                                                                        |
| `status`    | all     | Filter: `waiting`, `pending`, `processing`, `completed`, `failed`, `cancelled`, `discarded` |
| `limit`     | `20`    | Max results (1–100)                                                                         |
| `offset`    | `0`     | Pagination offset                                                                           |

Response `200`:

```json
{
  "batch_id": "550e8400-e29b-41d4-a716-446655440000",
  "summary": {
    "batch_id": "550e8400-e29b-41d4-a716-446655440000",
    "total": 5,
    "waiting": 0,
    "pending": 2,
    "processing": 1,
    "completed": 1,
    "failed": 1,
    "cancelled": 0
  },
  "tasks": [
    {
      "task_id": "660e8400-e29b-41d4-a716-446655440001",
      "batch_id": "550e8400-e29b-41d4-a716-446655440000",
      "file_name": "report.pdf",
      "status": "completed",
      "document_id": "550e8400-e29b-41d4-a716-446655440000",
      "error": null,
      "created_at": "2024-03-19T10:30:00Z",
      "started_at": "2024-03-19T10:30:05Z",
      "completed_at": "2024-03-19T10:30:12Z"
    }
  ]
}
```

The `summary` field is only present when a `batch` filter is applied.

### Get Task

```
GET /api/v1/tasks/{taskID}
```

Response `200` — single [TaskResponse](#taskresponse).

### List Batches

```
GET /api/v1/batches
GET /api/v1/batches?status=pending&limit=10
```

| Query param | Default | Description                                             |
| ----------- | ------- | ------------------------------------------------------- |
| `status`    | all     | Filter to batches with at least one task in this status |
| `limit`     | `20`    | Max results (1–100)                                     |
| `offset`    | `0`     | Pagination offset                                       |

Response `200`:

```json
{
  "batches": [
    {
      "batch_id": "550e8400-e29b-41d4-a716-446655440000",
      "total": 5,
      "pending": 2,
      "processing": 1,
      "completed": 1,
      "failed": 1,
      "cancelled": 0
    }
  ]
}
```

### Batch Summary

```
GET /api/v1/batches/{batchID}
```

Response `200`:

```json
{
  "batch_id": "550e8400-e29b-41d4-a716-446655440000",
  "total": 5,
  "waiting": 0,
  "pending": 2,
  "processing": 1,
  "completed": 1,
  "failed": 1,
  "cancelled": 0
}
```

### Global Summary

```
GET /api/v1/summary
```

Response `200`:

```json
{
  "total_batches": 3,
  "total_files": 150,
  "waiting": 3,
  "pending": 10,
  "processing": 2,
  "completed": 130,
  "failed": 5,
  "cancelled": 3,
  "discarded": 0,
  "total_size_gb": 12.45
}
```

### Response Types

#### DocumentResponse

```json
{
  "document_id": "550e8400-e29b-41d4-a716-446655440000",
  "title": "document.pdf",
  "md5_checksum": "d41d8cd98f00b204e9800998ecf8427e",
  "sha512_checksum": "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
  "mime_type": "application/pdf",
  "file_size": 102400,
  "language": "eng",
  "document_type_id": 1,
  "created_at": "2024-03-19T10:30:00Z",
  "modified_at": "2024-03-19T10:30:00Z"
}
```

When returned from the single-document endpoint (`GET /api/v1/documents/{id}`),
additional fields are populated:

```json
{
  "document_id": "550e8400-e29b-41d4-a716-446655440000",
  "title": "document.pdf",
  "md5_checksum": "d41d8cd98f00b204e9800998ecf8427e",
  "sha512_checksum": "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
  "mime_type": "application/pdf",
  "file_size": 102400,
  "language": "eng",
  "document_type_id": 1,
  "document_type_name": "invoice",
  "tags": [
    { "id": 1, "name": "finance" },
    { "id": 2, "name": "quarterly" }
  ],
  "people": [
    {
      "id": 1,
      "name": "John Doe",
      "person_type_id": 1,
      "person_type_name": "author",
      "person_type_description": "Document author"
    }
  ],
  "created_at": "2024-03-19T10:30:00Z",
  "modified_at": "2024-03-19T10:30:00Z"
}
```

#### FTSDocumentResponse

Same as DocumentResponse with these extra fields:

```json
{
  "rank": 0.4213,
  "snippet": "The <b>budget</b> forecast...",
  "text_content": ""
}
```

`text_content` is reserved for future use and currently returns an empty string.

#### TaskResponse

```json
{
  "task_id": "660e8400-e29b-41d4-a716-446655440001",
  "batch_id": "550e8400-e29b-41d4-a716-446655440000",
  "file_name": "report.pdf",
  "status": "completed",
  "document_id": "550e8400-e29b-41d4-a716-446655440000",
  "error": null,
  "created_at": "2024-03-19T10:30:00Z",
  "started_at": "2024-03-19T10:30:05Z",
  "completed_at": "2024-03-19T10:30:12Z"
}
```

#### BatchSummaryResponse

```json
{
  "batch_id": "550e8400-e29b-41d4-a716-446655440000",
  "total": 5,
  "waiting": 0,
  "pending": 2,
  "processing": 1,
  "completed": 1,
  "failed": 1,
  "cancelled": 0,
  "discarded": 0
}
```

---

## Configuration Reference

A commented example config with all supported keys and defaults is available at
[`config.example.yaml`](../config.example.yaml) in the project root. The file below
is what `kushim setup` generates at `~/.config/edub-kushim/config.yaml`.

```yaml
app:
  environment: development # development | production
  log_level: info # silent | fatal | error | info | debug
  # log_file: <config-dir>/kushim.log   # optional log file path

server:
  host: localhost
  port: 3000
  read_timeout: 60s # Go duration string (e.g. 60s, 30s)
  write_timeout: 60s
  idle_timeout: 60s

database:
  type: sqlite # currently only sqlite
  path: '~/.config/edub-kushim/data'
  name: edub.db # SQLite database file name

storage:
  consumption_dir: '~/.config/edub-kushim/inbox'
  storage_dir: '~/.config/edub-kushim/storage'

consumer:
  workers: 1 # concurrent file processing workers
  delete_original: false
  textextractor:
    engine: 'mupdf' # mupdf | gopdf | pdftotext
    timeout: 120
  pdfoptimizer:
    engine: 'mupdf' # mupdf | gs
    timeout: 120
    # fallback: 'gs'            # secondary optimizer when primary fails
  ocr:
    engine: 'gosseract' # gosseract | ocrmypdf
    languages: [eng] # required — set via kushim setup --languages
    timeout: 120

enricher:
  workers: 1 # concurrent enrichment workers
  textreducer:
    engine: 'textrank'
    timeout: 120
    target_words: 2000 # optional: reduce text before LLM
  contentanalyzer:
    engine: 'llmopenai' # llmopenai | llmanthropic | llmdeepseek | llmollama
    timeout: 120
    llm:
      openai:
        base_url: 'https://api.openai.com/v1'
        model: 'gpt-4o'
        # token: 'sk-...'
      anthropic:
        base_url: 'https://api.anthropic.com/v1'
        model: 'claude-sonnet-4-5'
        # token: 'sk-ant-...'
      deepseek:
        base_url: 'https://api.deepseek.com'
        model: 'deepseek-v4-flash'
        # token: 'sk-...'
      ollama:
        base_url: 'http://localhost:11434'
        model: 'llama3.2'
  tagmatcher:
    engine: 'hugot' # hugot | (empty = skip)
    timeout: 120
    reduce_target_words: 4000 # text reduction before tag matching
    chunk_size: 0 # 0 = use model's max_position_embeddings
    hugot:
      model: 'BAAI/bge-m3'
      backend: 'ort' # ort (ONNX Runtime) | GO
```

### Key sections

| Section                        | Purpose                                                 |
| ------------------------------ | ------------------------------------------------------- |
| `app`                          | Environment mode and log verbosity                      |
| `server`                       | HTTP listen address, timeouts                           |
| `database`                     | SQLite storage location and file name                   |
| `storage`                      | Inbox and processed file directories                    |
| `consumer`                     | Pipeline: which tools to use, which files to accept     |
| `consumer.textextractor`       | Text extraction engine (mupdf, gopdf, pdftotext)        |
| `consumer.pdfoptimizer`        | PDF optimizer (mupdf, gs) + optional fallback           |
| `consumer.ocr`                 | OCR engine (gosseract, ocrmypdf) + language data        |
| `consumer.workers`             | Concurrent file processing workers (default 1)          |
| `enricher`                     | Async classification pipeline                           |
| `enricher.textreducer`         | Text summarization before LLM (TextRank)                |
| `enricher.contentanalyzer`     | LLM provider for document classification (OpenAI, etc.) |
| `enricher.contentanalyzer.llm` | Per-provider config (base URL, model, token)            |
| `enricher.tagmatcher`          | Semantic tag matching via Hugot (embeddings)            |
| `enricher.tagmatcher.hugot`    | Hugot-specific settings (model, backend)                |

---

## Task Lifecycle

Each file produces two linked tasks:

1. **consume** — text extraction, OCR if needed, PDF optimization,
   checksum calculation, storage to disk, database record creation.
2. **enrich** — text reduction (TextRank), LLM content analysis
   (document type, tags, people), Hugot semantic tag matching.

Enrich tasks start in `waiting` status and transition to `pending` only
after their associated consume task completes successfully. This
ensures enrichment never runs before the document is fully ingested.

### Task statuses

| Status       | Description                                                   |
| ------------ | ------------------------------------------------------------- |
| `waiting`    | Enrich tasks waiting for their consume prerequisite to finish |
| `pending`    | Ready for a worker to pick up                                 |
| `processing` | Currently being handled by a worker                           |
| `completed`  | Finished successfully                                         |
| `failed`     | Finished with an error (retryable via `kushim task retry`)    |
| `cancelled`  | Cancelled via `kushim consume cancel`                         |
| `discarded`  | Enrich task orphaned because its parent consume task failed   |

---

## Running the API Server

```bash
# Start with default config
edub
```

The server starts the task queue workers (consume, enrich, and config pools)
automatically. Logs are written to stdout. Graceful shutdown on
SIGINT/SIGTERM.

```bash
# Check version
edub version
# Document Management System v0.1.0

# Start server (default when no command is given)
edub
```

## Settings Page

The main web UI includes a **Settings** page at `/settings` that provides
a structured form for all user-configurable settings:

- **OCR**: engine selector, languages list (add/remove), timeout
- **Consumer**: worker count, PDF optimizer engine, text extractor engine,
  delete original after processing toggle
- **Enricher**: worker count, content analyzer engine, tag matcher engine,
  text reducer engine

Changes are saved via the `/wizard/config` API and trigger background
downloads for any missing tessdata or Hugot model files. A spinner and
pending-task counter show download progress.

## Build: Setup Wizard

The setup wizard is a separate SvelteKit SPA located in `web-wizard/`. To
rebuild it:

```bash
make wizard-build         # Build SvelteKit wizard, copy to internal/wizard/static
```

The wizard uses the same design system (clay/gold/lapis/parchment palette)
and is embedded into the `kushim` binary via `//go:embed` on
`internal/wizard/static/`.

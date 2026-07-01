# edub-kushim — User Manual

Two binaries: **kushim** (CLI for document processing and matcher server) and **edub** (HTTP API server).
Both share the same OCR, text extraction, and PDF optimization pipeline.

The `edub` API server is a pure Go binary (`CGO_ENABLED=0`) that does not link any
C libraries. It **forks** `kushim` child processes for document processing and
communicates with an external **matcher process** (`kushim hugot`) for
semantic tag matching via a Unix domain socket.

---

## Quick Start

### Docker Compose (no host-side toolchain required)

```bash
git clone <repo-url> && cd edub-kushim
# Set your OCR language(s) in docker-compose.yml, then:
docker compose up
```

Open http://localhost:3000, configure your LLM provider in `/settings`, and drop PDFs into `./inbox/`. The first build compiles everything from source (~minutes); subsequent builds are cached.

### Manual

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
| `--consumer-ocr-engine`            | `gosseract`            | OCR engine: `gosseract` or `ocrmypdf`                       |
| `--consumer-textextractor-engine`  | `mupdf`                | Text extractor: `mupdf`, `gopdf`, or `pdftotext`            |
| `--consumer-pdfoptimizer-engine`   | `mupdf`                | PDF optimizer: `mupdf` or `gs`                              |
| `--consumer-pdfoptimizer-fallback` | —                      | Fallback PDF optimizer binary (ignored when engine is `gs`) |
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

Override a stale batch lease when resuming a batch. Useful if the
previous process was killed with `SIGKILL` and the lease was not released.

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

### Pre-flight Tool Check

Before scanning the inbox or resuming a batch, `kushim consume` checks that all
required external tools are available in `PATH`:

- **Engine binaries**: `ocrmypdf`, `gs`, `pdftotext` — checked only when the
  corresponding config engine is selected.
- **Required companions**: `tesseract` and `unpaper` — needed by the ocrmypdf
  adapter (`--clean` + `--language` flags).
- **Prerequisite**: `curl` — required for any model/language download.

If any required tool is missing, the command prints a block of install hints
and exits with a non-zero status. Optional companions (`pngquant`) and
tesseract language packs are shown as advisory notes but do not block.

Example output with ocrmypdf + tesseract + unpaper missing:

```
Cannot consume — the following required tools are not installed:

  OCR engine "ocrmypdf" (binary not found in PATH)
    Debian/Ubuntu:  sudo apt install ocrmypdf
    Arch:           sudo pacman -S ocrmypdf
    macOS:          brew install ocrmypdf

  Companion "tesseract" — OCR engine that ocrmypdf wraps (core dependency)
    Debian/Ubuntu:  sudo apt install tesseract-ocr
    Arch:           sudo pacman -S tesseract
    macOS:          brew install tesseract

  Companion "unpaper" — used by ocrmypdf's --clean flag (page cleanup)
    Debian/Ubuntu:  sudo apt install unpaper
    Arch:           sudo pacman -S unpaper

Install the missing tools, or switch to a built-in engine via `kushim setup` or the Settings page.
```

The language-pack and optional-companion advisory follows the pre-flight block
when the OCR engine is ocrmypdf:

```
Note: ocrmypdf uses the system tesseract, which reads its own tessdata directory.
Make sure the tesseract language packs for your configured languages are installed:

  Configured languages: eng, spa
    Debian/Ubuntu:  sudo apt install tesseract-ocr-eng  sudo apt install tesseract-ocr-spa
    Arch:           sudo pacman -S tesseract-data-eng  sudo pacman -S tesseract-data-spa
    macOS:          brew install tesseract (bundles common languages)
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

### `kushim hugot`

Start the matcher RPC server over a Unix domain socket. This process
hosts the Hugot embedding model and provides semantic tag matching,
encoding, and consolidation services to both the API server and CLI workers.

```

kushim hugot
kushim hugot --socket /path/to/custom/matcher.sock

```

| Flag       | Default                                        | Description                     |
| ---------- | ---------------------------------------------- | ------------------------------- |
| `--socket` | `<config_dir>/kushim-hugot.sock`             | Unix socket path for RPC        |

The server listens on the Unix socket and exposes the following RPC
endpoints:

| Endpoint                        | Method | Description                                  |
| ------------------------------- | ------ | -------------------------------------------- |
| `POST /rpc/v1/encode`           | POST   | Encode text into embedding vectors            |
| `POST /rpc/v1/match`            | POST   | Match document content against candidate tags |
| `POST /rpc/v1/consolidate`      | POST   | Consolidate LLM output labels to known tags   |
| `POST /rpc/v1/add-to-store`     | POST   | Add new tag names to the embedding store      |
| `POST /rpc/v1/remove-from-store`| POST   | Remove tag names from the embedding store     |
| `GET /health`                   | GET    | Health check                                 |

The matcher must be started before `edub` for full functionality.
If the matcher is not running, tag CRUD operations return `503 Service Unavailable`.

---

### `kushim backup`

Create a backup of the database, configuration, and storage files.

```
kushim backup
kushim backup --path /custom/backup/dir

```

| Flag     | Default                              | Description                               |
| -------- | ------------------------------------ | ----------------------------------------- |
| `--path` | Config `backup.path` (or `<config_dir>/backups/`) | Override output directory |

The backup creates a timestamped `tar.gz` archive containing:
- `edub.db` — SQLite database snapshot via `VACUUM INTO` (compact, consistent)
- `config.yaml` — Configuration file at backup time
- `storage/` — Full storage directory tree (originals, processed, errors)
- `manifest.json` — Backup metadata (version, timestamp, sizes, config SHA256 hash)

Retention is applied after the backup: if the number of archives exceeds `backup.keep`, the oldest are removed.

### `kushim restore`

Restore database, configuration, and storage from a backup archive.

```
kushim restore /path/to/edub-backup-2026-06-30T02-00-00.tar.gz
kushim restore /path/to/backup.tar.gz --force
kushim restore /path/to/backup.tar.gz --dry-run

```

| Flag       | Description                                                |
| ---------- | ---------------------------------------------------------- |
| `--force`  | Skip confirmation prompt and PID file check                |
| `--dry-run`| Validate the archive without making any changes            |

The restore process:
1. Validates the `tar.gz` archive and reads the manifest
2. Checks that the queue daemon is not running (refuses unless `--force`)
3. Prompts for confirmation (skipped with `--force`)
4. Extracts the archive to a temporary directory
5. Replaces storage (via atomic rename-swap), config, and database (last)
6. Prints restart instructions

---

## API Reference

The API server listens on `0.0.0.0:3000` by default (configurable in YAML).

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

| Query param | Default | Description                                                                                                    |
| ----------- | ------- | -------------------------------------------------------------------------------------------------------------- |
| `download`  | `false` | When `true`, sets `Content-Disposition: attachment` to force a file download dialog instead of inline preview. |

### Download Documents (Batch)

Downloads multiple documents as a ZIP archive.

```
POST /api/v1/documents/download
Content-Type: application/json

{
  "document_ids": ["uuid1", "uuid2", "uuid3"]
}
```

The request body may also be sent as `application/x-www-form-urlencoded` with `document_ids` as a JSON string.

Validation:

| Condition                          | Response                                 |
| ---------------------------------- | ---------------------------------------- |
| Empty `document_ids`               | `400` — `"document_ids is required"`     |
| Count exceeds `max_download_files` | `400` — `"too many documents, max: <N>"` |
| Non-existent IDs                   | `400` — `"documents not found: <ids>"`   |
| Total size exceeds limit           | `400` — `"total size exceeds limit"`     |

Response `200` with `Content-Type: application/zip` and `Content-Disposition: attachment; filename="documents.zip"`.
ZIP entry names follow the pattern `{sanitized_title}_{document_id_prefix}.{ext}` with extension derived from the document's MIME type.

Config limits (see [Configuration Reference](#configuration-reference)):

- `server.max_download_files` — max files in a single batch (default 50)
- `server.max_download_size_mb` — max total uncompressed size in MB (default 500)
- `server.max_batch_delete` — max documents in a single batch delete (default 50)

### Batch Delete Documents

Deletes multiple documents in a single request. Returns partial failure information
when some documents cannot be deleted.

```
POST /api/v1/documents/batch-delete
Content-Type: application/json

{
  "document_ids": ["uuid1", "uuid2", "uuid3"]
}
```

Validation:

| Condition                        | Response                                 |
| -------------------------------- | ---------------------------------------- |
| Empty `document_ids`             | `400` — `"document_ids is required"`     |
| Count exceeds `max_batch_delete` | `400` — `"too many documents, max: <N>"` |

Response `200` with partial failure support:

```json
{
  "deleted": 2,
  "failed": [{ "id": "uuid3", "error": "not found" }]
}
```

When no documents could be deleted (all failed), the response is `400`. Each document is
processed independently — the database record is deleted first, then files are best-effort
removed from disk. File removal failures are logged but do not fail the operation.

### Batch Assign Tags

Assigns tags to multiple documents in a single request. Supports two modes:

- **`add`**: appends tags to each document (existing tags are preserved, duplicates ignored)
- **`replace`**: clears all existing tags from each document, then adds the specified tags
  (wrapped in a database transaction for atomicity)

```
POST /api/v1/documents/batch-tags
Content-Type: application/json

{
  "document_ids": ["uuid1", "uuid2"],
  "tag_ids": [1, 2, 3],
  "mode": "add"
}
```

| Field          | Type       | Required | Description               |
| -------------- | ---------- | -------- | ------------------------- |
| `document_ids` | `string[]` | yes      | UUIDs of documents to tag |
| `tag_ids`      | `int[]`    | yes      | Tag IDs to assign         |
| `mode`         | `string`   | yes      | `"add"` or `"replace"`    |

Validation:

| Condition            | Response                                                |
| -------------------- | ------------------------------------------------------- |
| Empty `document_ids` | `400` — `"document_ids is required"`                    |
| Empty `tag_ids`      | `400` — `"tag_ids is required"`                         |
| Invalid `mode`       | `400` — `"mode must be 'add' or 'replace'"`             |
| Non-existent tag ID  | `404` — returned immediately, no documents are modified |

All tag IDs are validated before any document is modified. If any tag does not exist, the
entire request is rejected and no documents are changed. Partial failures (e.g., a document
not found) return `200` with a `failed` array:

```json
{
  "assigned": 1,
  "failed": [{ "id": "uuid2", "error": "not found" }]
}
```

In replace mode, the clear-and-add sequence runs inside a SQLite transaction per document,
so a mid-operation failure rolls back all tag changes for that specific document.

### Update Document

```
PUT /api/v1/documents/{id}
Content-Type: application/json

{
  "title": "Updated Title",
  "document_type_id": 2,
  "language": "eng"
}
```

| Field              | Type     | Required | Description                                        |
| ------------------ | -------- | -------- | -------------------------------------------------- |
| `title`            | `string` | yes      | New title for the document                         |
| `document_type_id` | `int`    | yes      | Must be ≥ 1 and reference an existing type         |
| `language`         | `string` | yes      | Language code (defaults to `"und"` if empty)       |
| `text_content`     | `string` | no       | Updated text content; omitted to preserve existing |

Response `204 No Content`. Returns `404` if document or document type is not found.

### Delete Document

```
DELETE /api/v1/documents/{id}
```

Response `204 No Content`. Deletes the database record (junction tables via cascade, FTS index via trigger), then best-effort removes the original and storage files from disk. File removal failures are logged but do not affect the response. Returns `404` if document is not found.

### Add Document Tag

```
POST /api/v1/documents/{id}/tags
Content-Type: application/json

{
  "tag_id": 1
}
```

Response `204 No Content`. Idempotent (duplicate adds are silently ignored). Returns `404` if document or tag is not found.

### Remove Document Tag

```
DELETE /api/v1/documents/{id}/tags
Content-Type: application/json

{
  "tag_id": 1
}
```

Response `204 No Content`. Returns `404` if document is not found.

### Add Document People

```
POST /api/v1/documents/{id}/people
Content-Type: application/json

{
  "people_id": 1,
  "people_type_id": 1
}
```

Both `people_id` and `people_type_id` are required. Response `204 No Content`. Idempotent (duplicate adds are silently ignored). Returns `404` if document, person, or people type is not found.

### Remove Document People

```
DELETE /api/v1/documents/{id}/people
Content-Type: application/json

{
  "people_id": 1,
  "people_type_id": 1
}
```

Both `people_id` and `people_type_id` are required. Response `204 No Content`. Returns `404` if document is not found.

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

Returns the current configuration as a `ConfigResponse` JSON object with `app`
(boolean `initialized`), `server` (host, port, auth_enabled), `consumer`, and `enricher` sections
(including LLM provider tokens) plus `available_engines` for UI dropdowns. Returns
defaults from `DefaultConfig("")` when no config has been bootstrapped yet,
so the response always has a complete shape.

```
PUT /wizard/config
Content-Type: application/json

{ "config_dir": "/home/user/.config/edub-kushim" }
```

Two-phase API:

- **Bootstrap phase** — send `{ "config_dir": "..." }` to create directories,
  write skeleton config, and initialize the database. Returns `200` with
  `{ ... }`.
- **Update phase** — send settings key-value pairs (dot notation) to update
  the config and trigger background downloads:

```json
{
  "server.port": 3000,
  "consumer.ocr.engine": "gosseract",
  "consumer.ocr.languages": ["eng", "spa"],
  "consumer.ocr.timeout": 120,
  "consumer.workers": 2,
  "consumer.textextractor.engine": "mupdf",
  "consumer.textextractor.timeout": 120,
  "consumer.pdfoptimizer.engine": "mupdf",
  "consumer.pdfoptimizer.fallback": "",
  "consumer.pdfoptimizer.timeout": 120,
  "enricher.workers": 2,
  "enricher.textreducer.engine": "textrank",
  "enricher.textreducer.timeout": 120,
  "enricher.textreducer.target_words": 2000,
  "enricher.contentanalyzer.engine": "llmopenai",
  "enricher.contentanalyzer.timeout": 120,
  "enricher.contentanalyzer.llm.openai.base_url": "https://api.openai.com/v1",
  "enricher.contentanalyzer.llm.openai.model": "gpt-4o",
  "enricher.contentanalyzer.llm.openai.token": "",
  "enricher.tagmatcher.timeout": 120,
  "enricher.tagmatcher.reduce_target_words": 4000,
  "enricher.tagmatcher.chunk_size": 0,
  "enricher.tagmatcher.hugot.model": "BAAI/bge-m3",
  "enricher.tagmatcher.hugot.backend": "ort"
}
```

Returns `201` with `{ "pending_tasks": 3, "missing_tools": [...] }` when downloads are enqueued,
or `200` with `{ "configured": true, "missing_tools": [...] }` when all dependencies are already
present. The `missing_tools` array lists any hard-blocking tool-availability issues
(missing engine binaries, required companions, or the curl prerequisite).

```
GET /wizard/config/status
```

Returns `{ "configured": bool, "pending_tasks": int, "errors": []string, "tools": [...], "missing_tools": [...] }`.
The `tools` array contains the full availability status for every relevant external tool
(engine binaries, curl prerequisite, ocrmypdf companions, tesseract language-pack hints).
`missing_tools` is the hard-blocking subset (missing engines, required companions, curl).
The wizard and settings page poll this endpoint every 3 seconds to track download progress
and refresh tool-status warnings.

---

### Consume

Enqueue all files in the consumption directory as tasks. Before scanning
the inbox, the server checks that all required external tools are available
(see [Pre-flight Tool Check](#pre-flight-tool-check) for the engine list).

```
POST /api/v1/consume
```

Response `202` (all checks pass, files enqueued):

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

Response `422 Unprocessable Entity` (required tools missing):

```json
{
  "error": "Cannot consume: required external tools are not installed.",
  "missing_tools": [
    {
      "engine": "ocrmypdf",
      "category": "ocr",
      "command": "ocrmypdf",
      "available": false,
      "install_hints": {
        "Debian/Ubuntu": "sudo apt install ocrmypdf",
        "Arch": "sudo pacman -S ocrmypdf",
        "macOS": "brew install ocrmypdf"
      },
      "languages": ["eng", "spa"],
      "lang_hints": [...],
      "companions": [
        {
          "command": "tesseract",
          "purpose": "OCR engine that ocrmypdf wraps (core dependency)",
          "available": false,
          "required": true,
          "install_hints": {
            "Debian/Ubuntu": "sudo apt install tesseract-ocr"
          }
        }
      ]
    }
  ]
}
```

### Upload Files

Upload files via multipart form. Before scanning the uploaded files, the server checks
that all required external tools are available (see [Pre-flight Tool Check](#pre-flight-tool-check)).

```
POST /api/v1/consume/upload
```

The request must be `multipart/form-data` with one or more `files` parts.

Response `202` (files accepted):

```json
{
  "batch_id": "550e8400-e29b-41d4-a716-446655440000",
  "accepted": 3,
  "rejected": [{ "name": "notes.docx", "reason": "unsupported type: .docx" }],
  "_links": {
    "tasks": "/api/v1/tasks?batch=550e8400-e29b-41d4-a716-446655440000"
  }
}
```

Response `413 Payload Too Large` (body exceeds `server.max_upload_size`):

```json
{
  "error": "upload exceeds max_upload_size (100 MB)"
}
```

Response `422 Unprocessable Entity` (all files rejected or missing tools):

```json
{
  "error": "no supported files",
  "rejected": [{ "name": "readme.txt", "reason": "unsupported type: .txt" }]
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

### Batch Cancel

```
POST /api/v1/batches/{batchID}/cancel
```

Cancels a running batch. Pending tasks are marked as `cancelled` in the
database and a `SIGTERM` is sent to the worker process currently processing
the batch. Any task that was in-flight at the moment of cancellation is also
marked as `cancelled`. Idempotent — calling cancel on an already‑settled
batch returns 200 with zero counts.

Response `200`:

```json
{
  "batch_id": "550e8400-e29b-41d4-a716-446655440000",
  "cancelled_pending": 5,
  "cancelled_processing": 1,
  "signal_sent": true
}
```

If the worker process is no longer running:

```json
{
  "batch_id": "550e8400-e29b-41d4-a716-446655440000",
  "cancelled_pending": 5,
  "cancelled_processing": 0,
  "signal_sent": false
}
```

### User CRUD

Manage user accounts with bcrypt password hashing.

#### Create User

```
POST /api/v1/users
Content-Type: application/json

{
  "username": "jdoe",
  "password": "securepass123"
}
```

| Field      | Type     | Required | Description                        |
| ---------- | -------- | -------- | ---------------------------------- |
| `username` | `string` | yes      | Unique username                    |
| `password` | `string` | yes      | Password, minimum 8 characters     |

Validation:

| Condition                    | Response                             |
| ---------------------------- | ------------------------------------ |
| Empty username               | `400` — `"Username is required"`     |
| Empty password               | `400` — `"Password is required"`     |
| Password < 8 characters      | `400` — `"Password must be at least 8 characters"` |
| Duplicate username           | `409` — `{"error":"username already exists"}` |

Response `201`:

```json
{
  "id": 1,
  "username": "jdoe",
  "created_at": "2026-06-26T12:00:00Z"
}
```

#### Get User

```
GET /api/v1/users/{id}
```

Response `200` — single `UserResponse` (excludes `password_hash` and `api_key`). Returns `404` if not found.

#### List Users

```
GET /api/v1/users?limit=50&offset=0
```

| Query param | Default | Description         |
| ----------- | ------- | ------------------- |
| `limit`     | `50`    | Max results (1–100) |
| `offset`    | `0`     | Pagination offset   |

Response `200`:

```json
{
  "users": [
    { "id": 1, "username": "jdoe", "created_at": "2026-06-26T12:00:00Z" }
  ],
  "total": 1
}
```

#### Update User

```
PUT /api/v1/users/{id}
Content-Type: application/json

{
  "username": "jdoe",
  "password": "newpass456"
}
```

| Field      | Type     | Required | Description                            |
| ---------- | -------- | -------- | -------------------------------------- |
| `username` | `string` | yes      | New username                           |
| `password` | `string` | no       | New password (omit to keep current)    |

Validation:

| Condition                    | Response                             |
| ---------------------------- | ------------------------------------ |
| Empty username               | `400` — `"Username is required"`     |
| Password < 8 chars (if set) | `400` — `"Password must be at least 8 characters"` |
| Duplicate username           | `409` — `{"error":"username already exists"}` |
| Non-existent user            | `404`                                |

Response `200` — single `UserResponse`.

#### Delete User

```
DELETE /api/v1/users/{id}
```

Response `204 No Content`. Returns `404` if not found.

### Dashboard

```
GET /api/v1/dashboard
```

Returns the 30 most recent activity events across documents, tasks, and batches, plus the 20 most recent batches with per-batch task status summaries, owner state, orphan detection, and duration (when settled).

Response `200`:

```json
{
  "recent_batches": [
    {
      "batch_id": "550e8400-e29b-41d4-a716-446655440000",
      "source": "web",
      "created_at": "2026-06-25T10:30:00Z",
      "total": 5,
      "waiting": 0,
      "pending": 2,
      "processing": 1,
      "completed": 1,
      "failed": 1,
      "cancelled": 0,
      "discarded": 0,
      "owner_state": "live",
      "orphaned": false,
      "duration_ms": 42000
    }
  ],
  "activity": [
    {
      "event_type": "document_uploaded",
      "title": "report.pdf",
      "timestamp": "2026-06-25T10:30:00Z",
      "link": "/documents/550e8400-e29b-41d4-a716-446655440000"
    },
    {
      "event_type": "task_completed",
      "title": "report.pdf",
      "timestamp": "2026-06-25T10:29:00Z",
      "link": "/tasks/task-uuid"
    },
    {
      "event_type": "batch_created",
      "title": "web",
      "timestamp": "2026-06-25T10:28:00Z",
      "link": "/tasks?batch=batch-uuid"
    }
  ]
}
```

| Field            | Type      | Description                                                                          |
| ---------------- | --------- | ------------------------------------------------------------------------------------ |
| `batch_id`       | `string`  | Batch UUID                                                                           |
| `source`         | `string`  | Origin: `"cli"`, `"web"`, or `"upload"`                                             |
| `created_at`     | `string`  | RFC 3339 timestamp                                                                   |
| `total`          | `int`     | Total task count in batch                                                            |
| `waiting`        | `int`     | Tasks waiting for their prerequisite                                                 |
| `pending`        | `int`     | Tasks ready for a worker                                                             |
| `processing`   | `int`     | Tasks currently being processed                                                      |
| `completed`    | `int`     | Tasks finished successfully                                                          |
| `failed`       | `int`     | Tasks that failed                                                                    |
| `cancelled`    | `int`     | Tasks cancelled via cancel endpoint                                                  |
| `discarded`    | `int`     | Enrich tasks orphaned by a failed consume                                            |
| `owner_state`  | `string`  | `"none"`, `"live"`, or `"stale"`                                                     |
| `orphaned`     | `bool`    | True when owner is not live but tasks remain pending or processing                   |
| `duration_ms`  | `int`     | Batch duration in milliseconds. Present only when the batch is fully settled.        |

#### Activity Event (`activity[]`)

| Field        | Type     | Description                                                              |
| ------------ | -------- | ------------------------------------------------------------------------ |
| `event_type` | `string` | One of: `"document_uploaded"`, `"task_completed"`, `"task_failed"`, `"batch_created"` |
| `title`      | `string` | Document title, file name, file path basename, batch source, or task ID fallback |
| `timestamp`  | `string` | RFC 3339 timestamp                                                       |
| `link`       | `string` | Navigable link: `/documents/{id}`, `/tasks/{id}`, `/tasks?batch={id}`   |

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

server:
  host: 0.0.0.0
  port: 3000
  read_timeout: 60s # Go duration string (e.g. 60s, 30s)
  write_timeout: 60s
  idle_timeout: 60s
  max_concurrent_batches: 2 # max concurrent forked worker processes
  max_download_files: 50 # max files in a single batch download
  max_download_size_mb: 500 # max total size in MB for batch download
  max_batch_delete: 50 # max documents in a single batch delete
  auth_enabled: false # when false, API endpoints bypass JWT auth

database:
  type: sqlite # currently only sqlite
  path: '~/.config/edub-kushim/data'
  name: edub.db # SQLite database file name

storage:
  consumption_dir: '~/.config/edub-kushim/inbox'
  storage_dir: '~/.config/edub-kushim/storage'

consumer:
  workers: 1 # concurrent file processing workers
  max_files_per_batch: 10 # max files per consume batch (0 = unlimited)
  polling:
    enabled: false # auto-consume on a schedule
    interval: 5 # polling interval in minutes
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
    timeout: 120
    reduce_target_words: 4000 # text reduction before tag matching
    chunk_size: 0 # 0 = use model's max_position_embeddings
    hugot:
      model: 'BAAI/bge-m3'
      backend: 'ort' # ort (ONNX Runtime) | GO
```

> **ORT memory**: When using the `ort` backend, ORT's CPU memory arena and memory-pattern
> pre-allocation are disabled by default (internal `CpuMemArena: false`, `MemPattern: false`).
> This keeps idle RSS at ~2.2–2.5 GB instead of ~4–5 GB. These are internal fields not
> present in `config.yaml` — toggle via `DefaultConfig` if latency is preferred over
> memory usage.

### Key sections

| Section                        | Purpose                                                                |
| ------------------------------ | ---------------------------------------------------------------------- |
| `app`                          | Environment mode and log verbosity                                     |
| `server`                       | HTTP listen address, timeouts, max concurrent batches, download limits |
| `database`                     | SQLite storage location and file name                                  |
| `storage`                      | Inbox and processed file directories                                   |
| `consumer`                     | Pipeline: which tools to use, which files to accept                    |
| `consumer.max_files_per_batch` | Max files per consume batch (default 10, 0 = unlimited)                |
| `consumer.polling`             | Auto-consume scheduler settings (enabled, interval)                    |
| `consumer.reclaim`             | Auto-resume of interrupted batches (enabled, default `true`)           |
| `consumer.textextractor`       | Text extraction engine (mupdf, gopdf, pdftotext)                       |
| `consumer.pdfoptimizer`        | PDF optimizer (mupdf, gs) + optional fallback                          |
| `consumer.ocr`                 | OCR engine (gosseract, ocrmypdf) + language data                       |
| `consumer.workers`             | Concurrent file processing workers (default 1)                         |
| `enricher`                     | Async classification pipeline                                          |
| `enricher.textreducer`         | Text summarization before LLM (TextRank)                               |
| `enricher.contentanalyzer`     | LLM provider for document classification (OpenAI, etc.)                |
| `enricher.contentanalyzer.llm` | Per-provider config (base URL, model, token)                           |
| `enricher.tagmatcher`          | Semantic tag matching via Hugot (embeddings)                           |
| `enricher.tagmatcher.hugot`    | Hugot-specific settings (model, backend)                               |

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
| `discarded`  | Enrich task orphaned because its parent consume task failed. Re-activated to `pending` when the parent is retried and succeeds. |

---

## Running the API Server

```bash
# Start the matcher first (required for tag matching)
kushim hugot &

# Start the API server
edub
```

The server starts the **config pool** for background downloads (tessdata,
Hugot model). Logs are written to stdout. Graceful shutdown on SIGINT/SIGTERM.

When `POST /api/v1/consume` or `POST /api/v1/consume/upload` is called, the
server enqueues tasks in the database and **forks** `kushim consume --batch <id>`
as a child process. The child performs the actual document processing
(consumption + enrichment) and communicates with the matcher process for
semantic tag matching.

The maximum number of concurrent forked workers is controlled by
`server.max_concurrent_batches` (default 2). If the limit is reached,
subsequent consume requests receive `429 Too Many Requests`.

```bash
# Check version
edub version
# Document Management System v0.1.0

# Start server (default when no command is given)
edub
```

The `kushim` binary must be available in PATH or as a sibling of the `edub`
binary. If it is not found, consume requests will fail with a 500 error.

## Settings Page

The main web UI includes a **Settings** page at `/settings` with two tabs:

### Configuration Tab

A single-page form for all user-configurable settings:

- **Server**: host, port, max upload/download sizes, max download files
- **OCR**: engine selector, timeout, data directory, languages list (add/remove)
- **Consumer**: workers, delete-original toggle
- **Text extractor**: engine, timeout
- **PDF optimizer**: engine, fallback, timeout
- **Enricher**: workers
- **Content analyzer (LLM)**: engine, timeout, and provider-specific Base URL,
  model, token (with show/hide toggle)
- **Tag matcher**: engine, timeout, reduce target words, chunk size, Hugot model,
  Hugot backend (ort/GO)
- **Text reducer**: engine, timeout, target words

Changes are saved via the `/wizard/config` API and trigger background
downloads for any missing tessdata or Hugot model files. A spinner and
pending-task counter show download progress.

**Tool-status warnings** appear inline beneath each engine selector when
the selected external tool is missing from `PATH`. When the OCR engine is
`ocrmypdf`, additional advisory blocks show:

- **Tesseract language packs** — which system packages to install for each
  configured language (always shown, since system tesseract is separate
  from the app's downloaded tessdata).
- **Companion tools** — status of `tesseract`, `unpaper` (required), and
  `pngquant` (optional), each with install hints.

A **sticky amber banner** at the top of the app layout shows whenever
required tools are not installed, with a link to the settings page.

### Users Tab

The Users tab provides user account management:

- **DataTable** listing all users with Username, Created At, and Actions (edit, delete) columns
- **Create User** button opens a modal with Username (required) and Password (required, min 8 characters) fields
- **Edit** button opens a modal with Username pre-filled and Password optional (placeholder: "Leave blank to keep current")
- **Delete** opens a confirmation dialog; on confirm the user is permanently removed
- Pagination controls with configurable page sizes (10/25/50/100)

## Build: Setup Wizard

The setup wizard is a separate SvelteKit SPA located in `web-wizard/`. To
rebuild it:

```bash
nvm use        # activate the Node.js version from .nvmrc
make wizard-build         # Build SvelteKit wizard, copy to internal/wizard/static
```

The wizard uses the same design system (clay/gold/lapis/parchment palette)
and is embedded into the `kushim` binary via `//go:embed` on
`internal/wizard/static/`.

## Systemd Service Files

Example systemd unit files are provided at `deploy/systemd/`:

| File | Process |
|------|---------|
| `kushim-hugot.service` | Matcher server (`kushim hugot`) |
| `kushim-queue.service` | Batch queue daemon (`kushim queue`) |
| `edub.service` | API server (`edub`) |

The `edub` service declares `Wants=kushim-hugot.service` (not `Requires=`) —
the API starts even if the matcher is down; tag CRUD returns 503 until the
matcher is reachable.

### Quick Setup

```bash
# 1. Create a dedicated system user
sudo useradd -r -m -d /var/lib/edub-kushim -s /usr/sbin/nologin edub

# 2. Install binaries
sudo cp dev/bin/kushim /usr/local/bin/
sudo cp dev/bin/edub   /usr/local/bin/

# 3. Initialize config as the dedicated user
sudo -u edub kushim setup --cli --languages eng,spa,...

# 4. Copy and enable services
sudo cp deploy/systemd/*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now kushim-hugot.service
sudo systemctl enable --now kushim-queue.service
sudo systemctl enable --now edub.service
```

### Customizing User and Permissions

The example files do not hardcode a user — they run as whatever user systemd
defaults to (typically `root` if installed system-wide). To run under a
dedicated user, add these directives to each `.service` file:

```ini
[Service]
User=edub
Group=edub
Environment=HOME=/var/lib/edub-kushim
StateDirectory=edub-kushim
```

| Directive | Purpose |
|-----------|---------|
| `User=` / `Group=` | Runs the process under a non-root system account |
| `Environment=HOME=/var/lib/edub-kushim` | Sets `$HOME` so `utils.ConfigDir()` resolves to `/var/lib/edub-kushim/.config/edub-kushim` (see [Architecture: Config Directory](architecture.md#config-directory)) |
| `StateDirectory=edub-kushim` | systemd creates `/var/lib/edub-kushim` with correct ownership before the service starts |

If the application's writable paths (config, database, storage, inbox, logs)
are under the user's home directory, `ProtectSystem=full` is safe — it only
mounts `/usr`, `/etc`, `/boot`, and `/efi` read-only. Home and `/var` remain
writable.

### Logging

Each process writes to its own log file under `<configDir>/logs/`:

| Process | Log file |
|---------|----------|
| `kushim` (CLI) | `kushim.log` |
| `kushim hugot` (matcher) | `hugot.log` |
| `kushim queue` (daemon) | `queue.log` |
| `edub` (API server) | `edub.log` |

systemd's `StandardOutput=journal` and `StandardError=journal` are set so
the services also appear in the journal:

```bash
journalctl -u edub.service
journalctl -u kushim-hugot.service
journalctl -u kushim-queue.service
```

The per-process log files are for persistent debugging and auditing; the
journal captures stdout/stderr (including startup messages before the log
file is opened).

### Service Dependencies and Order

```
network.target
      |
      +-- kushim-hugot.service  (After=network.target)
      |
      +-- kushim-queue.service  (After=network.target)
      |
edub.service          (After=kushim-hugot.service, Wants=kushim-hugot.service)
```

`kushim-queue.service` has no dependency on the matcher — it only reads
the database and forks consumer children.

The matcher should be fully started before the API server. The example files
use a soft dependency (`Wants=`) so `edub` doesn't fail to start if the
matcher hasn't been initialized. The API handles the unavailable matcher
gracefully with a 503 status on tag CRUD endpoints.

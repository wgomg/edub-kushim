# edub-kushim — User Manual

Two binaries: **kushim** (CLI for document processing and matcher server) and **edub** (HTTP API server).
Both share the same OCR, text extraction, and PDF optimization pipeline.

The `edub` API server is a pure Go binary (`CGO_ENABLED=0`) that does not link any
C libraries. The `kushim queue` daemon **forks** `kushim` workers for document processing; `edub` only enqueues tasks and
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

Open http://localhost:3001, configure your LLM provider in `/settings`, and drop PDFs into `./inbox/`. The first build compiles everything from source (~minutes); subsequent builds are cached.

Two modes:

- **External database (default)** — `docker compose up` runs the `edub` container only; you provide PostgreSQL yourself and point the config's `database.host` at it.
- **Bundled PostgreSQL (quickstart)** — `docker compose -f docker-compose.yml -f docker-compose.quickstart.yml up --build` (or `make compose-quickstart`) also starts a PostgreSQL 17 container. The `edub` service waits for the DB health check, and on first boot the entrypoint runs `kushim setup --cli` with `--db-dsn postgres://edub:edub@db:5432/edub?sslmode=disable --admin-user admin --admin-password admin`, so database, migrations, seeders, and the admin account are created automatically. Data persists in the `edub-db-data` volume; the DB is not exposed on the host. Requires Docker Compose v2. Quickstart credentials are defaults — change them beyond local evaluation.

### Pre-built binaries (Linux amd64/arm64)

Download the latest release from [GitHub Releases](https://github.com/wgomg/edub-kushim/releases) — no host toolchain required:

```bash
curl -LO https://github.com/wgomg/edub-kushim/releases/latest/download/kushim_linux_amd64.tar.gz
curl -LO https://github.com/wgomg/edub-kushim/releases/latest/download/edub_linux_amd64.tar.gz
curl -LO https://github.com/wgomg/edub-kushim/releases/latest/download/checksums.txt
sha256sum -c checksums.txt
tar xzf kushim_linux_amd64.tar.gz
tar xzf edub_linux_amd64.tar.gz
sudo mv kushim_linux_amd64 /usr/local/bin/kushim
sudo mv edub_linux_amd64 /usr/local/bin/edub
```

Use the `_arm64` tarballs on ARM64 hosts. Binaries are built on Ubuntu 24.04 runners against glibc — for other environments, build from source (see `docs/reference/frontend.md`).

### Manual

```bash
# One‑time setup — launches a web wizard at http://0.0.0.0:8420
kushim setup

# Or use terminal-based setup (headless / CI)
kushim setup --cli --languages eng,spa

# Process documents
cp my-documents/*.pdf ~/.config/edub-kushim/inbox/

# CLI mode (enqueue + direct-fallback if queue empty + show per-file progress)
kushim consume

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
Document Management System v2.9.0
```

### `kushim setup`

Generate a config file, create required directories, initialize the PostgreSQL
database schema (auto-creates the database if it doesn't exist), download OCR language
data, and download the Hugot embedding model (`BAAI/bge-m3`).

By default, `kushim setup` launches a **web-based setup wizard** at
`http://0.0.0.0:8420`. The wizard provides a six-step guided flow:

1. **Config directory** — specify where configuration, database, and models are stored
2. **Consumer settings** — choose OCR engine, add languages, configure worker counts, select supported file types, enable the DOCX/ODT converter
3. **Enricher settings** — LLM provider/model, tag matcher, text reducer
4. **Progress** — shows download progress for tessdata and Hugot model
5. **Admin user** — optionally create an admin user account (username + password)
6. **Completion** — ready to run `edub` to start the server

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
| `--consumer-ocr-engine`            | `gosseract`            | OCR engine: `gosseract` or `ocrmypdf`                       |
| `--consumer-textextractor-engine`  | `mupdf`                | Text extractor: `mupdf`, `gopdf`, or `pdftotext`            |
| `--consumer-pdfoptimizer-engine`   | `mupdf`                | PDF optimizer: `mupdf` or `gs`                              |
| `--consumer-pdfoptimizer-fallback` | —                      | Fallback PDF optimizer binary (ignored when engine is `gs`) |
| `--admin-user`                     | —                      | Admin username (prompted if omitted)                        |
| `--admin-password`                 | —                      | Admin password (prompted if omitted)                        |
| `--db-dsn`                         | —                      | PostgreSQL DSN, e.g. `postgres://user:pass@host:5432/db?sslmode=disable`. When set, overrides the individual `database.*` fields and is persisted to config as `database.dsn` |
| `--reset-database`                 | `false`                | Drop all tables and re-run schema + seeders                 |

The flags `--inbox-path` and `--storage-path` accept either
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

### `kushim enrich <document-id>`

Run enrichment (LLM classification: title, document type, tags, people, language)
on a single **already-consumed** document. Does not re-extract text or re-OCR —
the document's existing text content from the database is re-classified.

```
kushim enrich 550e8400-e29b-41d4-a716-446655440000
```

Creates a queued batch with a single pending enrich task. The `kushim queue` daemon
picks it up and processes it. Run `kushim queue` if the daemon is not running.

Output:

```
Batch 660e8400-e29b-41d4-a716-446655440001 queued — run 'kushim queue' to process
```

| Condition | Message |
| --------- | ------- |
| Document not found | `Error: document <id> not found` |
| Re-enrich already queued (dedup) | `Error: re-enrich already queued for document <id>` |

Works on hosts without OCR/PDF tools because no consume tasks are created.

### `kushim thumbnails <mode>`

Backfill thumbnails for documents that don't have one yet (`has_thumbnail = FALSE`).
Enqueues `pending` thumbnail tasks into a new `queued` batch — nothing is
processed inline; run `kushim queue` (or leave the daemon running) to generate
the thumbnails.

```
kushim thumbnails --all
kushim thumbnails --batch 550e8400-e29b-41d4-a716-446655440000
kushim thumbnails --document 550e8400-e29b-41d4-a716-446655440000
```

Exactly one mode is required:

| Mode | Scope |
| ---- | ----- |
| `--all` | Every document missing a thumbnail (paginated, 500 per page) |
| `--batch <id>` | Documents whose completed `consume` task is in the given batch |
| `--document <id>` | A single document |

`--force` proceeds even when `consumer.thumbnail.enabled` is `false`.

Output:

```
Batch 660e8400-e29b-41d4-a716-446655440001 queued with 12 thumbnail task(s) — run 'kushim queue' to process
```

| Condition | Message |
| --------- | ------- |
| Nothing to do (`--all` / `--batch`) | `No documents missing thumbnails.` / `No documents missing thumbnails in that batch.` |
| All candidates already pending (dedup) | `N document(s) already have a pending thumbnail task.` |
| Document not found (`--document`) | `Error: document <id> not found` |
| Document already has a thumbnail or a task is queued (`--document`) | `Error: document <id> already has a thumbnail or a task is already queued` |
| Thumbnails disabled in config | `Error: thumbnails are disabled in config (consumer.thumbnail.enabled); use --force to override` |

Documents whose thumbnail task is already `pending` are skipped — the dedup key
`thumbnail:doc:<uuid>` (unique partial index) prevents duplicate queued tasks.
Failed tasks fall outside the index, so a re-run re-enqueues them.

#### Scheduled backfill

`consumer.thumbnail.backfill_interval` (days, `0` = disabled) and
`consumer.thumbnail.backfill_time` (`HH:MM`, default `"02:00"`) enable an
automatic `--all` sweep: the `kushim queue` daemon forks
`kushim thumbnails --all` when the schedule is due and no backfill batch is
already queued or processing. The last run is derived from the most recent
`thumbbackfill` batch, so daemon restarts do not reset the schedule; the first
run waits for the preferred time of day. Sub-daily intervals (e.g. `0.5` =
every 12 hours) are supported. Both fields are exposed in the settings UI
(Thumbnails section).

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

### Paused Batch Guard

If one or more batches are paused due to LLM provider credit/balance errors,
`kushim consume` (without `--batch`) refuses with a hard error listing the
paused batch IDs:

```
Error: 2 paused batch(es) exist due to LLM credit/balance errors: 550e8400-e29b-41d4-a716-446655440000, 660e8400-e29b-41d4-a716-446655440001
Resolve the billing issue, then resume or cancel each paused batch before creating new ones.
```

The polling daemon (`kushim queue`) also skips inbox scanning ticks when
paused batches exist — it logs a line and waits for the next interval.
Resolving the billing issue and resuming or cancelling all paused batches
lifts the guard automatically.

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

#### `--batch <id>`

Resume processing of an already-enqueued batch. Skips the inbox scan
and picks up where the batch left off. Useful for retrying failed tasks
or restarting a background batch.

```
kushim consume --batch 550e8400-e29b-41d4-a716-446655440000
```

Output on resume (progress lines are printed only for tasks that change state):

```
  [2/3] consume  invoice.pdf ... done
  [3/3] consume  contract.pdf ... done

Summary: 3 files, 6 tasks — all successful
```

If the batch is paused due to an LLM provider credit/balance error:

```
Batch 550e8400-e29b-41d4-a716-446655440000 is paused due to an LLM provider credit/balance error.
Resolve the billing issue, then re-queue the batch and run this command again.
```

If the batch is already finished, the command re-acquires it, finds no
pending work, and prints the summary with zero progress lines. If the batch
is being processed by another process, the command refuses:

```
batch 550e8400-e29b-41d4-a716-446655440000 is being processed by PID 12345 (use --force to override)
```

### `kushim search`

Full‑text search across indexed documents via PostgreSQL tsvector.

```
kushim search "budget report"
kushim search --limit 10 --offset 0 "quarterly earnings"
kushim search --rebuild-index
```

| Flag              | Default | Description                                |
| ----------------- | ------- | ------------------------------------------ |
| `--limit`         | `20`    | Max results (1–100)                        |
| `--offset`        | `0`     | Result offset for pagination               |
| `--rebuild-index` | `false` | Reindex the tsvector GIN index (`idx_document_tsv`) |

The query is passed to `plainto_tsquery('simple', ...)` which tokenizes the input and inserts `&` between tokens.
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
TASK ID                              TYPE     STATUS       BATCH        FILE
--------------------------------------------------------------------------------
660e8400-e29b-41d4-a716-446655440001 consume  completed    550e8400-e2… report.pdf
770e8400-e29b-41d4-a716-446655440002 enrich   completed    550e8400-e2… invoice.pdf
880e8400-e29b-41d4-a716-446655440003 consume  failed       550e8400-e2… contract.pdf
```

### `kushim task status`

Show detailed information about a single task.

```
kushim task status 660e8400-e29b-41d4-a716-446655440001
```

Output:

```
Task ID:    660e8400-e29b-41d4-a716-446655440001
Type:       consume
Batch ID:   550e8400-e29b-41d4-a716-446655440000
Status:     completed
File:       report.pdf
Created:    2024-03-19T10:30:00Z
Started:    2024-03-19T10:30:05Z
Completed:  2024-03-19T10:30:12Z
Document ID: 550e8400-e29b-41d4-a716-446655440000
```
Note: `Error:` only appears when the task has failed.

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

### `kushim user create`

Create a user with a specific role. Useful for bootstrapping admin
access on systems where `kushim setup` has already been run but no
admin user exists. Connects directly to the existing database —
no authentication required (CLI-only).

```
kushim user create --username admin --password secret123 --role admin
kushim user create --username bob --password "my$ecureP@ss1" --role editor
```
Flags:

| Flag         | Required | Default | Description                                                   |
| ------------ | -------- | ------- | ------------------------------------------------------------- |
| `--username` | Prompted | —       | Username for the new account                                  |
| `--password` | Prompted | —       | Password (use in scripts; omitted for masked interactive prompt) |
| `--role`     | No       | `admin` | Role: `admin`, `editor`, or `viewer`                          |

When flags are omitted, the command prompts interactively:

1. Username is read from stdin
2. Password is read with masked input (`term.ReadPassword`)
3. Password is confirmed (must match)

Output on success:

```
user 'admin' created with role 'admin'
```

If the username already exists:

```
Error: user 'admin' already exists
```

If the role is invalid:

```
Error: invalid role "superadmin": must be admin, editor, or viewer
```

If passwords do not match:

```
Error: passwords do not match
```

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
| `--bg`     | `false`                                        | Run in the background (re-executes without `--bg`) |

The server listens on the Unix socket and exposes the following RPC
endpoints:

| Endpoint                        | Method | Description                                  |
| ------------------------------- | ------ | -------------------------------------------- |
| `POST /rpc/v1/encode`           | POST   | Encode text into embedding vectors            |
| `POST /rpc/v1/match`            | POST   | Match document content against the tag embedding store |
| `POST /rpc/v1/consolidate`      | POST   | Consolidate LLM output labels to known tags   |
| `POST /rpc/v1/add-to-store`     | POST   | Add new tag names to the embedding store      |
| `POST /rpc/v1/remove-from-store`| POST   | Remove tag names from the embedding store     |
| `GET /health`                   | GET    | Health check                                 |

The matcher must be started before `edub` for full functionality.
If the matcher is not running, tag CRUD operations still succeed — only the tag embedding cache is not updated (errors are logged) and enrichment falls back to LLM-only tags.

See the [Tag Matcher guide](tag-matcher.md) for configuration, memory, and CPU
tuning (`chunk_size` is the main memory lever and defaults to a safe 4096 tokens).

---



### `kushim config`

View and edit configuration values from the terminal. Git-style positional interface: no arguments dumps the full config, one argument reads a key, two arguments write a key-value pair.

```
kushim config
kushim config server.port
kushim config server.port 8080
kushim config --unset backup.path
kushim config --validate
kushim config --path
```

| Mode | Example | Description |
| ---- | ------- | ----------- |
| `dump all` | `kushim config` | List all config keys and values as `key = value` lines (like `git config --list`) |
| `get` | `kushim config server.port` | Print a single key's value. Scalars print raw, arrays/maps as YAML |
| `set` | `kushim config server.port 8080` | Set a key to a value. Type auto-detected: `true`/`false` → bool, integers → int, floats → float64, comma-separated → `[]string`, otherwise string |
| `--unset` | `kushim config --unset backup.path` | Remove a key — reverts to its default value on next `config.Load` |
| `--validate` | `kushim config --validate` | Load and validate the config file, reporting any errors |
| `--path` | `kushim config --path` | Print the absolute path to the config file |
| `--help` | `kushim config --help` | Print usage help |

Keys use dot notation (snake_case), matching the YAML key names:

```
kushim config server.port               → 3000
kushim config server.port 8080          → sets server.port to 8080
kushim config consumer.ocr.languages    → eng, spa
kushim config consumer.ocr.languages eng,spa,deu  → sets to [eng spa deu]
kushim config --unset backup.path       → removes backup.path (reverts to default)
```

Atomic write safety: `set` and `--unset` write to a temporary directory, run `config.Load` for full validation (including business rules such as non-empty OCR languages and valid backup intervals), then atomically rename into place. An invalid value leaves the config file unchanged.

Exit codes: `0` on success, `1` on any error (missing key, invalid value, validation failure).

---

### `kushim backup`

Create a backup of the database, configuration, and storage files.

```
kushim backup
kushim backup --path /custom/backup/dir
kushim backup --mode database
kushim backup --mode documents --path /mnt/nas/documents

```

| Flag     | Default                              | Description                               |
| -------- | ------------------------------------ | ----------------------------------------- |
| `--path` | Config `backup.path` (or `<config_dir>/backups/`) | Override output directory |
| `--mode` | `full` | What to include: `full` (database + documents), `database` (DB dump only), or `documents` (storage only) |

The backup creates a timestamped `tar.gz` archive whose contents depend on the
mode (the manifest records the mode; `config.yaml` is always included):

- `full` — `edub.sql`, `config.yaml`, `storage/`, `manifest.json`
- `database` — `edub.sql`, `config.yaml`, `manifest.json`
- `documents` — `config.yaml`, `storage/`, `manifest.json`

`storage/` is archived whole, so it includes every subdir — `originals/`,
`processed/`, `errors/` (with `duplicated/`), `thumbnails/`, `trash/`, and
`orphaned/`.

Archive names are prefixed with the mode: `edub-backup-full-<timestamp>.tar.gz`,
`edub-backup-database-<timestamp>.tar.gz`, `edub-backup-documents-<timestamp>.tar.gz`.

Manual backups never apply retention — only scheduled backups prune old
archives, using the schedule's `keep` for that mode.

### `kushim mirror`

Mirror the storage tree to a destination with `rsync --delete`, producing a
faithful, browsable second copy of the documents. Unlike backups (tar
archives), the mirror is a live tree: files deleted from `storage/` are
deleted from the destination on the next run. It is **not** a restore source —
`kushim restore` only reads backup archives.

```
kushim mirror
kushim mirror --path /mnt/nas/documents
kushim mirror --path user@nas:/srv/documents

```

| Flag     | Default                    | Description                                                                  |
| -------- | -------------------------- | ---------------------------------------------------------------------------- |
| `--path` | Config `mirror.path`       | Destination: local path or rsync remote target (`[user@]host:path`)          |

The command waits for the backup lock (mirror, backup, and migrations never
run concurrently) and for in-flight consume/enrich/thumbnail tasks to drain,
then runs `rsync -a --delete --info=stats2 --timeout=600 <storage>/ <dest>` and
writes a small `.edub-mirror.json` diagnostics file (timestamp, file count,
bytes, app version) into the destination. Because the held lock blocks new
task claims, the manual mirror is safe even with polling enabled — no
polling-window coordination needed. The 10-minute rsync idle timeout aborts
stalled transfers (unreachable remotes, hung ssh) so the backup lock is never
held indefinitely; Ctrl-C also kills the spawned `ssh` for remote targets.

Prerequisites:

- `rsync` must be installed (`sudo apt install rsync`, `brew install rsync`, …)
- For remote targets, passwordless ssh access to the host must be configured
  (ssh key setup is the user's responsibility)

The destination is validated at config load and before each run: it must not
resolve (through symlinks) inside `storage_dir` or the backup directory, must
not contain or equal the storage dir, and must not start with `-`.

Warning: the mirror is faithful — `--delete` propagates deletions. Keep tar
backups (`kushim backup`) as the mitigation for accidental mass deletion.

### `kushim restore`

Restore database and storage from a backup archive. The archived configuration is preserved as `config.yaml.restored`, not applied.

```
kushim restore /path/to/edub-backup-2026-06-30T02-00-00.tar.gz
kushim restore /path/to/backup.tar.gz --force
kushim restore /path/to/backup.tar.gz --dry-run
kushim restore /path/to/backup.tar.gz --temp-dir /mnt/scratch

```

| Flag         | Description                                                                   |
| ------------ | ----------------------------------------------------------------------------- |
| `--force`    | Skip confirmation prompt and PID file check                                   |
| `--dry-run`  | Validate the archive without making any changes                               |
| `--temp-dir` | Directory for extraction staging (default: next to `storage.storage_dir`)     |
The restore process:

1. Validates the `tar.gz` archive and reads the manifest
2. Checks restore tooling: `host` runtime requires `psql` on PATH; `docker`/`podman` require the runtime binary and `database.container`; `remote` refuses with manual-restore guidance (skipped for `documents`-mode archives)
3. Checks that the queue daemon is not running (refuses unless `--force`)
4. Prompts for confirmation (skipped with `--force`)
5. Pre-flights disk space: refuses corrupt/hand-made manifests (a mode-relevant size of zero), and refuses when the staging directory (or the storage parent, for cross-device staging) lacks ~1.05× the required extraction size — the error names the directory and suggests `--temp-dir`
6. Extracts the archive to a temporary directory created next to `storage.storage_dir` (or under `--temp-dir` when given) — not `/tmp`, which on modern desktops is a quota-limited tmpfs
7. Executes the SQL dump against the database via `psql` (schema drop + recreate + data insert; the dump's own transaction keeps it atomic) — skipped for `documents`-mode archives
8. Rewrites database storage paths from the archived storage directory (manifest `storage_dir`, or `storage.storage_dir` from the archived config for old backups) to the current one — `document.storage_path`, `document.original_path`, and `orphaned_file.file_path`; skipped when the directories match or the archived dir can't be determined (`~`-relative archived paths are skipped with a warning). Also skipped for `documents`-mode archives (no DB was restored; ensure `storage.storage_dir` matches or update the DB manually)
9. Replaces storage — when the extracted storage is on the same filesystem as `storage.storage_dir` it is renamed directly into place (atomic, no copy); otherwise it is copied into a sibling `storage-swap-*` directory first and then swapped in, so an interrupted copy never touches the live storage. Skipped for `database`-mode archives
10. Saves the archived config as `config.yaml.restored` — the current configuration stays intact (update `storage.storage_dir` in it to match the rewritten paths before applying)
11. Prints restart instructions

Archives created before backup modes existed have no `mode` in the manifest and
restore as `full`.

The restore runs `psql` per `database.runtime`: on the host (`host`, requires
`psql` installed — the `postgresql-client` package), or inside the named
container (`docker`/`podman`, requires `database.container`). With
`runtime: remote` the restore refuses before touching anything — unzip the
archive and run `psql -f edub.sql` manually on the remote host.

---

### `kushim migrate`

Run the database or storage migration logic from the CLI, without going through the config task queue. Both subcommands refuse to start while another migration or backup holds the backup lock, wait for in-flight tasks to drain, and persist the new settings to `config.yaml` only after the operation succeeds.

```
kushim migrate database --host <h> --port <p> --user <u> --password <p> --database <db> [--sslmode <mode>]
kushim migrate storage --storage-dir <new> [--consumption-dir <new>]
```

#### `kushim migrate database`

Copies the current database to a new PostgreSQL server and points `config.yaml` at it, running the same steps as the `migrate-db` config task: pre-migration safety snapshot (config `backup.path`, retention-capped), SQL dump (schema + data, `goose_db_version` preserved), destination validation (refuses databases with tables but no edub migration history; skips restore when the destination already holds data), and a deferred `config.yaml` update. The destination restore runs `psql` per the current config's `database.runtime` (host binary or exec into the current container, connecting over TCP to the destination); `runtime: remote` refuses before dumping.

| Flag         | Description                                                        |
| ------------ | ------------------------------------------------------------------ |
| `--host`     | Destination host (required)                                        |
| `--port`     | Destination port (required)                                        |
| `--user`     | Destination user (required)                                        |
| `--password` | Destination password (required, or set `KUSHIM_DB_PASSWORD`)       |
| `--database` | Destination database name (required)                               |
| `--sslmode`  | Destination SSL mode (default: `disable`)                          |

A password passed on the command line is visible in process listings and shell history; prefer setting `KUSHIM_DB_PASSWORD` and omitting `--password`.

#### `kushim migrate storage`

Relocates the storage and consumption directories to new paths: rewrites pending consume-task payloads and the database paths (`document.storage_path`, `document.original_path`, `orphaned_file.file_path`), moves the storage subdirs and inbox files per `storage.migration_mode` (`copy` — copy-then-delete, default — or `move` — rename with cross-filesystem copy fallback), and persists the new dirs to `config.yaml`. At least one of `--storage-dir` or `--consumption-dir` must be provided.

| Flag                 | Description                                      |
| -------------------- | ------------------------------------------------ |
| `--storage-dir`      | New storage directory                            |
| `--consumption-dir`  | New consumption (inbox) directory                |

Paths that resolve under a system location (`/proc`, `/sys`, `/dev`, `/boot`) are rejected. If neither directory actually changes, the command reports a no-op and exits successfully. Run `kushim migrate database` before `kushim migrate storage` — the storage migration reads the updated config (including the new database connection) from disk.

### `kushim storage`

Storage maintenance subcommands.

```
kushim storage thumbnails cleanup [--dry-run]
```

#### `kushim storage thumbnails cleanup`

Detects and removes thumbnail files in `storage/thumbnails/` whose document no longer exists in the database (permanently deleted or never created), then prunes the emptied date directories. Files modified within the last 30 seconds are skipped to avoid racing an in-flight thumbnail task.

| Flag         | Description                                              |
| ------------ | -------------------------------------------------------- |
| `--dry-run`  | List orphaned thumbnails without removing anything       |

The same sweep also runs automatically on every hourly trash purge, regardless of whether any expired documents were deleted.

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
  "version": "2.9.0",
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
| `sort_by`    | `created_at` | Sort column: `title`, `file_size`, `created_at` |
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

Returns the raw processed file bytes for preview. Response `200` with `Content-Disposition: inline`.

| Query param | Default | Description                                                                                                    |
| ----------- | ------- | -------------------------------------------------------------------------------------------------------------- |
| `download`  | `false` | When `true`, sets `Content-Disposition: attachment` to force a file download dialog instead of inline preview. |

### Get Document Thumbnail

```
GET /api/v1/documents/{id}/thumbnail
```

Returns the document's generated thumbnail (first page of the PDF, or the
source image) as a JPEG cropped to a fixed 3:4 aspect ratio, with
`Cache-Control: public, max-age=86400`. Response `404` when the document has
no thumbnail (`has_thumbnail: false`, or the file is missing).

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

Soft-deletes multiple documents in a single request. Returns partial failure information
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
processed independently — files are moved to the trash directory and the row is marked
deleted (see [Trash / Soft Delete](#trash--soft-delete)). Returns `404` (via the `failed`
array) for documents that do not exist or are already in the trash.

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

In replace mode, the clear-and-add sequence runs inside a database transaction per document,
so a mid-operation failure rolls back all tag changes for that specific document.

### Batch Set Document Type

Sets the document type of multiple documents in a single request. The type is applied to all
documents in one atomic `UPDATE` (single round trip).

```
POST /api/v1/documents/batch-type
Content-Type: application/json

{
  "document_ids": ["uuid1", "uuid2"],
  "document_type_id": 2
}
```

| Field              | Type       | Required | Description                          |
| ------------------ | ---------- | -------- | ------------------------------------ |
| `document_ids`     | `string[]` | yes      | UUIDs of documents to update         |
| `document_type_id` | `int`      | yes      | Document type ID to assign           |

Validation:

| Condition                        | Response                                                       |
| -------------------------------- | -------------------------------------------------------------- |
| Empty `document_ids`             | `400` — `"document_ids is required"`                           |
| `document_type_id` < 1           | `400` — `"Invalid document type"`                              |
| Count exceeds `max_batch_delete` | `400` — `"too many documents, max: <N>"`                       |
| Non-existent type ID             | `404` — returned immediately, no documents are modified        |

The type is validated before any document is modified. If it does not exist, the entire
request is rejected and no documents are changed. Partial failures (e.g., a document not
found) return `200` with a `failed` array:

```json
{
  "updated": 1,
  "failed": [{ "id": "uuid2", "error": "not found" }]
}
```

When no documents could be updated (all failed), the response is `400`. Setting the type
back to `undetermined` (id 1) is allowed.

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

Response `204 No Content`. Soft-deletes the document: its original, processed, and thumbnail
files (when present) are moved to the trash directory (`<storage_dir>/trash/<document_id>/`)
and the row is marked with `deleted_at` instead of being removed. The document disappears
from lists, search, and dashboard counts but can be restored from the trash (see
[Trash / Soft Delete](#trash--soft-delete)). Returns `404` if the document is not found or
already in the trash.

### Trash / Soft Delete

Soft-deleted documents are moved to the trash and can be restored or permanently deleted.
Trashed documents are excluded from all queries (lists, search, dashboard, counts) and from
checksum-based duplicate detection, so re-ingesting a deleted file creates a new document.

#### List Trash

```
GET /api/v1/trash?limit=50&offset=0
```

Returns paginated trashed documents ordered by `deleted_at` descending:

```json
{
  "documents": [
    {
      "id": 1,
      "document_id": "uuid",
      "title": "Invoice.pdf",
      "original_type": "application/pdf",
      "file_size": 1024,
      "page_count": 2,
      "language": "eng",
      "deleted_at": "2026-08-01T00:00:00Z",
      "created_at": "2026-07-01T00:00:00Z"
    }
  ],
  "total": 1,
  "limit": 50,
  "offset": 0
}
```

#### Get Trash Document

```
GET /api/v1/trash/{id}
```

Returns full metadata for a single trashed document (including checksums, page/word/char
counts, language, document type, timestamps). Returns `404` if the document is not in the
trash.

#### Restore Document

```
POST /api/v1/trash/{id}/restore
```

Moves the files back to the main storage directories (the thumbnail, when present, returns
to its date-based path under `storage/thumbnails/`) and clears `deleted_at`. Response
`204 No Content`. Returns `404` if the document is not in the trash. Missing files are
skipped (partial restore) rather than failing the operation.

#### Permanently Delete Document

```
DELETE /api/v1/trash/{id}
```

Removes the database row (junction tables cascade), the document's trash directory, and any
thumbnail file left in `storage/thumbnails/` (documents trashed before thumbnails were
moved to trash keep their thumbnail there). Response `204 No Content`. Returns `404` if
the document is not in the trash.

#### Batch Permanent Delete

```
POST /api/v1/trash/batch-delete
Content-Type: application/json

{
  "document_ids": ["uuid1", "uuid2"]
}
```

Same validation and response shape as batch delete (`deleted`/`failed`); documents not in
the trash are reported as failed.

#### Batch Restore

```
POST /api/v1/trash/batch-restore
Content-Type: application/json

{
  "document_ids": ["uuid1", "uuid2"]
}
```

Restores multiple trashed documents. Response `200` with `{"restored": N, "failed": [...]}`;
`400` when no document could be restored.

#### Purge Expired

```
POST /api/v1/trash/purge
```

Permanently deletes all trashed documents whose `deleted_at` is older than
`storage.trash.retention_days` (default 30 days). Response `200` with `{"purged": N}`. The
same purge also runs automatically in the background every hour; each cycle also sweeps
orphaned thumbnails in `storage/thumbnails/` whose document row no longer exists, whether
or not any rows were deleted.

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
| `q`         | — (required) | tsquery plain text (tokenized with `plainto_tsquery`) |
| `limit`     | `50`         | Max results (1–100)                                      |
| `offset`    | `0`          | Pagination offset                                        |

Response `200` — array of `FTSDocumentResponse` (adds `rank`, `snippet`, `text_content`).

> **Security note**: The `snippet` field is HTML-escaped before returning. Only `<b>`/`</b>` highlighting tags from `ts_headline` are preserved; all other HTML is escaped to prevent XSS.

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
  "date_created": { "from": "2024-01-01", "to": "2024-12-31" },
  "date_modified": { "from": null, "to": "2024-06-01" },
  "file_size": { "min": 0, "max": 10485760 },
  "sort_by": "created_at",
  "sort_order": "desc",
  "limit": 50,
  "offset": 0
}
```

`people[].type` is a person relationship type such as `author` or `sender`. The
reserved value `"person"` matches the person across **all** their relationship
types (e.g. a person linked to one document as author and to another as
recipient matches once). Because `person` is reserved, creating or renaming a
people type to `person` is rejected with `400`.

Response `200` — `SearchResponse`:

```json
{
  "results": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
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

Response `200` — `PersonListResponse` envelope: `{ "results": [{ "id": 1, "name": "…", "document_count": N }], "total": N }` (people-types and document-types add `description`).

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

### LLM Model Discovery

Returns the available adapters, providers, and models from the model catalog. Used by the settings UI to populate cascading selectors.

```
GET /api/v1/llm/models
```

Response `200`:

```json
{
  "adapters": {
    "anthropic": ["anthropic"],
    "openai-compatible": ["deepseek", "mistral", "openai", "qwen", "zhipu"]
  },
  "providers": {
    "openai": [
      {
        "id": "gpt-5.4-nano",
        "capabilities": {
          "supports_reasoning": true,
          "max_input_tokens": 400000,
          "max_output_tokens": 128000,
          "supports_temperature": false,
          "supports_response_schema": true
        }
      }
    ],
    "deepseek": [
      {
        "id": "deepseek-v4-flash",
        "capabilities": {
          "supports_reasoning": true,
          "reasoning_efforts": ["high", "max"],
          "max_input_tokens": 1000000,
          "max_output_tokens": 384000,
          "supports_temperature": true,
          "supports_response_schema": true
        }
      }
    ]
  }
}
```

### Configuration (Wizard API)

Read and update the configuration via the API. These endpoints are used by
both the web wizard and the in-app settings page.

```
GET /wizard/bootstrap
```

Public endpoint (no authentication required). Returns only non-sensitive fields
needed by the SPA to decide whether to show the login screen:

```json
{
  "auth_enabled": true,
  "missing_tools": []
}
```

The `missing_tools` array lists hard-blocking tool-availability issues
(missing engines, required companions, or the curl prerequisite).

```
GET /wizard/config
```

Returns the current configuration as a `ConfigResponse` JSON object with `app`
(boolean `initialized`), `server` (host, port, max_upload_size, max_download_files,
max_download_size_mb, max_concurrent_batches, auth_enabled), `consumer`, and `enricher` sections
(including LLM provider tokens) plus `available_engines` for UI dropdowns. Returns
defaults from `DefaultConfig("")` when no config has been bootstrapped yet,
so the response always has a complete shape. **Authentication required (admin role).**

```
PUT /wizard/config
Content-Type: application/json

{ "config_dir": "/home/user/.config/edub-kushim" }
```

Three-phase API:

- **Bootstrap phase** — send `{ "config_dir": "..." }` to create directories,
  write skeleton config, and initialize the database. Returns `200` with
  `{ ... }`.
- **Update phase** — send settings key-value pairs (dot notation) to update
  the config and trigger background downloads (see "Database connection
  changes" below for the third phase):

```json
{
  "server.port": 3000,
  "server.max_batch_delete": 50,
  "server.auth_enabled": true,
  "consumer.ocr.engine": "gosseract",
  "consumer.ocr.languages": ["eng", "spa"],
  "consumer.ocr.timeout": 120,
  "consumer.workers": 2,
  "consumer.reclaim.stale_task_after": 600,
  "consumer.textextractor.engine": "mupdf",
  "consumer.textextractor.timeout": 120,
  "consumer.pdfoptimizer.engine": "mupdf",
  "consumer.pdfoptimizer.fallback": "",
  "consumer.pdfoptimizer.timeout": 0,
  "enricher.workers": 2,
  "enricher.textreducer.engine": "textrank",
  "enricher.textreducer.timeout": 120,
  "enricher.textreducer.target_words": 2000,
  "enricher.contentanalyzer.enabled": true,
  "enricher.contentanalyzer.timeout": 120,
  "enricher.contentanalyzer.prompt_template": "",
  "enricher.contentanalyzer.pause_on_credit_error": true,
  "enricher.contentanalyzer.doc_type_refinement.enabled": true,
  "enricher.contentanalyzer.doc_type_refinement.head_words": 600,
  "enricher.contentanalyzer.doc_type_refinement.tail_words": 400,
  "enricher.contentanalyzer.llm.adapter": "openai-compatible",
  "enricher.contentanalyzer.llm.provider": "openai",
  "enricher.contentanalyzer.llm.model": "gpt-4o",
  "enricher.contentanalyzer.llm.token": "",
  "enricher.contentanalyzer.llm.request_delay": 1,
  "enricher.contentanalyzer.fallbacks": [
    {
      "enabled": false,
      "llm": {
        "adapter": "openai-compatible",
        "provider": "deepseek",
        "model": "deepseek-chat",
        "token": "",
        "temperature": 0,
        "request_delay": 0
      }
    }
  ],
  "enricher.tagmatcher.timeout": 120,
  "enricher.tagmatcher.reduce_target_words": 4000,
  "enricher.tagmatcher.chunk_size": 4096,
  "enricher.tagmatcher.hugot.model": "BAAI/bge-m3",
  "enricher.tagmatcher.hugot.backend": "ort"
}
```

Returns `201` with `{ "pending_tasks": 3, "missing_tools": [...] }` when downloads are enqueued,
or `200` with `{ "configured": true, "missing_tools": [...] }` when all dependencies are already
present. The `missing_tools` array lists any hard-blocking tool-availability issues
(missing engine binaries, required companions, or the curl prerequisite).

**Database connection changes.** When the request body changes any `database.*` setting
(`host`, `port`, `user`, `password`, `database`, `sslmode`), the update is **deferred**:
the settings are not written to `config.yaml` yet. The API returns `202` with
`{ "pending_tasks": 1, "message": "migration(s) queued — settings apply once the migration completes" }`
and enqueues a `migrate-db` background task that copies the current database into the new
one (schema + data), then persists the new connection settings once the copy succeeds. Both
`edub` and `kushim queue` detect the change and reconnect automatically. Non-database
settings from the same request apply immediately. While a migration is running, further DB
changes answer `409`. A failed migration is retried automatically on the next save (or via
`POST /wizard/config/retry`) and is idempotent — the copy is skipped when the destination
already holds data.

**Storage directory changes.** Changing `storage.storage_dir` or `storage.consumption_dir`
is also deferred: the API returns `202` and enqueues a `migrate-storage` background task
that moves existing files to the new locations (storage subdirs `processed/`, `originals/`,
`errors/`, `orphaned/`, `trash/`, `thumbnails/`, plus inbox files), rewrites `document.storage_path`/
`original_path`, `orphaned_file.file_path`, and the `file_path` of pending consume tasks,
and only then persists the new directories to `config.yaml`. The move strategy is controlled
by `storage.migration_mode`: `copy` (copy then delete — safe, needs extra disk space;
default) or `move` (rename — faster, falls back to copy across filesystems). When both the
database and storage directories change in the same request, `migrate-db` runs first and
`migrate-storage` second, so the storage task operates on the new database. Storage-dir
changes answer `409` while a storage migration is in flight.

```
GET /wizard/config/status
```

Returns `{ "configured": bool, "pending_tasks": int, "errors": []string, "tools": [...], "missing_tools": [...] }`.
The `tools` array contains the full availability status for every relevant external tool
(engine binaries, curl prerequisite, ocrmypdf companions, tesseract language-pack hints).
`missing_tools` is the hard-blocking subset (missing engines, required companions, curl).
The wizard and settings page poll this endpoint every 3 seconds to track download progress
and refresh tool-status warnings. **Authentication required (admin role).**

---

### Re-enrich Document

Trigger enrichment (LLM classification) for a single already-consumed document.
No re-extraction or re-OCR — the existing text content from the database is re-classified.

```
POST /api/v1/documents/{id}/reenrich
```

Response `202 Accepted`:

```json
{
  "batch_id": "660e8400-e29b-41d4-a716-446655440001",
  "_links": {
    "tasks": "/api/v1/tasks?batch=660e8400-e29b-41d4-a716-446655440001"
  }
}
```

| Condition | Status |
| --------- | ------ |
| Document not found | `404` |
| Re-enrich already queued (active task with same dedup key) | `409` |

### Filter Languages

Returns the distinct languages available in the document corpus, for use in structured search filter dropdowns.

```
GET /api/v1/filter-languages
```

Response `200` — array of strings:

```json
["eng", "spa", "deu"]
```

### Supported MIME Types

Returns the configured/constant set of supported MIME types with metadata, used by the frontend to build dynamic file input accept attributes. This endpoint returns the compile-time configuration, not what happens to be in the database.

```
GET /api/v1/supported-mime-types
```

Response `200` — array of objects:

```json
[
  {"mime_type": "application/pdf", "extension": ".pdf", "label": "PDF"},
  {"mime_type": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "extension": ".docx", "label": "DOCX"},
  {"mime_type": "application/vnd.oasis.opendocument.text", "extension": ".odt", "label": "ODT"},
  {"mime_type": "image/tiff", "extension": ".tiff", "label": "TIFF"},
  {"mime_type": "image/jpeg", "extension": ".jpg", "label": "JPEG"},
  {"mime_type": "image/png",  "extension": ".png", "label": "PNG"}
]
```

| Field       | Type     | Description                                     |
| ----------- | -------- | ----------------------------------------------- |
| `mime_type` | `string` | The MIME type string                            |
| `extension` | `string` | Canonical file extension (e.g. `.jpg`, `.tiff`) |
| `label`     | `string` | Short format name for display (e.g. "PDF")      |

### Batch Retry

Reset all failed tasks in a batch to pending.

```
POST /api/v1/batches/{batchID}/retry
```

Response `200`:

```json
{
  "retried": 3
}
```

Idempotent — returns 0 retried when no failed tasks remain.

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
  "rejected": [{ "name": "notes.txt", "reason": "unsupported type: .txt" }],
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
GET /api/v1/tasks?status=active
```

| Query param | Default | Description                                                                                 |
| ----------- | ------- | ------------------------------------------------------------------------------------------- |
| `batch`     | all     | Filter by batch UUID                                                                        |
| `status`    | all     | Filter: `waiting`, `pending`, `processing`, `completed`, `failed`, `cancelled`, `discarded`. Special value `active` = `pending` + `processing` + `waiting`, ordered processing-first (longest-running first), then oldest-queued |
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
    "cancelled": 0,
    "discarded": 0,
    "orphaned": false
  },
  "tasks": [
    {
      "task_id": "660e8400-e29b-41d4-a716-446655440001",
      "batch_id": "550e8400-e29b-41d4-a716-446655440000",
      "task_type": "consume",
      "file_name": "report.pdf",
      "payload_doc_id": "",
      "status": "completed",
      "document_id": 42,
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
      "cancelled": 0,
      "discarded": 0,
      "orphaned": false
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
  "cancelled": 0,
  "discarded": 0,
  "orphaned": false
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

Manage user accounts with bcrypt password hashing and role assignment.

#### Create User

```
POST /api/v1/users
Content-Type: application/json

{
  "username": "jdoe",
  "password": "securepass123",
  "role": "editor"
}
```

| Field      | Type     | Required | Description                                        |
| ---------- | -------- | -------- | -------------------------------------------------- |
| `username` | `string` | yes      | Unique username                                    |
| `password` | `string` | yes      | Password, minimum 12 characters, must contain at least one uppercase letter, lowercase letter, digit, and special character |
| `role`     | `string` | no       | Role assignment: `"admin"`, `"editor"`, `"viewer"`. Defaults to `"viewer"` |

Validation:

| Condition                    | Response                             |
| ---------------------------- | ------------------------------------ |
| Empty username               | `400` — `"Username is required"`     |
| Empty password               | `400` — `"password is required"`     |
| Password < 12 characters     | `400` — `"password must be at least 12 characters"` |
| Password lacks uppercase, lowercase, digit, or special character | `400` — `"password must contain at least one uppercase letter, lowercase letter, digit, and special character"` |
| Duplicate username           | `409` — `{"error":"username already exists"}` |

Response `201`:

```json
{
  "id": 1,
  "username": "jdoe",
  "role": "editor",
  "created_at": "2026-06-26T12:00:00Z"
}
```

#### Get Current User (Me)

Returns the authenticated user's own profile. Self-service endpoint accessible to any authenticated user regardless of role.

```
GET /api/v1/me
```

Authorization: Bearer token or API key required.

Response `200` — single `UserResponse` (excludes `password_hash` and `api_key_hash`). Returns `404` if user was deleted, `401` if unauthenticated.

#### Get User (Admin)

```
GET /api/v1/users/{id}
```

Response `200` — single `UserResponse` (excludes `password_hash` and `api_key_hash`). Returns `404` if not found.

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
    { "id": 1, "username": "jdoe", "role": "editor", "created_at": "2026-06-26T12:00:00Z" }
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
  "password": "newpass456",
  "role": "admin"
}
```

| Field      | Type     | Required | Description                            |
| ---------- | -------- | -------- | -------------------------------------- |
| `username` | `string` | yes      | New username                           |
| `password` | `string` | no       | New password (omit to keep current)    |
| `role`     | `string` | no       | New role: `"admin"`, `"editor"`, `"viewer"` (omit to keep current) |

Validation:

| Condition                    | Response                             |
| ---------------------------- | ------------------------------------ |
| Empty username               | `400` — `"Username is required"`     |
| Password < 12 chars (if set) | `400` — `"password must be at least 12 characters"` |
| Password lacks uppercase, lowercase, digit, or special character (if set) | `400` — `"password must contain at least one uppercase letter, lowercase letter, digit, and special character"` |
| Invalid role (if set)       | `400` — validation error             |
| Duplicate username           | `409` — `{"error":"username already exists"}` |
| Non-existent user            | `404`                                |

Response `200` — single `UserResponse`.

#### Delete User

```
DELETE /api/v1/users/{id}
```

Response `204 No Content`. Returns `404` if not found.

#### Self-service API Keys

Self-service endpoints for the current user to manage their own API key. No admin privileges required — any authenticated user can manage their own key.

```
POST /api/v1/me/api-key     # Generate own key (201)
DELETE /api/v1/me/api-key    # Revoke own key (204)
PUT /api/v1/me/api-key       # Rotate own key (200)
GET /api/v1/me/api-key       # Get own key status (200)
```

Authorization: Bearer token or API key required. Returns `401` if unauthenticated.

### Dashboard

```
GET /api/v1/dashboard
```

Returns up to 25 active tasks (`pending`/`processing`/`waiting`, processing-first), plus the 20 most recent batches with per-batch task status summaries, owner state, orphan detection, and duration (when settled).

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
  "inbox_files": 3,
  "originals_size_bytes": 10485760,
  "processed_size_bytes": 8388608,
  "running_tasks": {
    "count": 2,
    "tasks": [
      {
        "task_id": "660e8400-e29b-41d4-a716-446655440001",
        "batch_id": "550e8400-e29b-41d4-a716-446655440000",
        "task_type": "consume",
        "file_name": "report.pdf",
        "payload_doc_id": "",
        "status": "processing",
        "document_id": null,
        "error": null,
        "label": "Consume: report.pdf",
        "created_at": "2026-06-25T10:30:00Z",
        "started_at": "2026-06-25T10:30:05Z",
        "completed_at": null
      },
      {
        "task_id": "660e8400-e29b-41d4-a716-446655440002",
        "batch_id": "",
        "task_type": "config",
        "file_name": "",
        "payload_doc_id": "",
        "status": "pending",
        "document_id": null,
        "error": null,
        "label": "Database migration",
        "created_at": "2026-06-25T10:28:00Z",
        "started_at": null,
        "completed_at": null
      }
    ]
  }
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

#### Active Task (`running_tasks`)

Object with `count` (total active tasks across all task types, uncapped) and `tasks` (the active task list, capped at 25, ordered processing-first then oldest-queued).

| Field         | Type     | Description                                                                                                            |
| ------------- | -------- | ---------------------------------------------------------------------------------------------------------------------- |
| `count`       | `int`    | Total number of active tasks (uncapped)                                                                                |
| `tasks`       | `array`  | Active tasks, capped at 25, ordered processing-first then oldest-queued                                                |
| `tasks[].task_id` | `string` | Task UUID                                                                                                          |
| `tasks[].batch_id` | `string` | Batch UUID (empty for `backup`/`config` tasks)                                                                     |
| `tasks[].task_type` | `string` | `consume`, `enrich`, `thumbnail`, `config`, or `backup`                                                            |
| `tasks[].file_name` | `string` | File name from the payload, if any                                                                                 |
| `tasks[].status` | `string` | `pending`, `processing`, or `waiting`                                                                              |
| `tasks[].label` | `string` | Human-readable title: `TaskType: file name` for consume/enrich/thumbnail (e.g. `"Consume: report.pdf"`), else derived from task type + dedup key (e.g. `"Backup (full)"`, `"Database migration"`, `"Download tessdata (eng)"`) |
| `tasks[].created_at` | `string` | RFC 3339 timestamp                                                                                                |
| `tasks[].started_at` | `string` | RFC 3339 timestamp, `null` while queued                                                                           |

### Response Types

#### DocumentResponse

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "title": "document.pdf",
  "md5_checksum": "d41d8cd98f00b204e9800998ecf8427e",
  "sha512_checksum": "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
  "original_type": "application/pdf",
  "file_size": 102400,
  "language": "eng",
  "document_type_id": 1,
  "has_thumbnail": true,
  "created_at": "2024-03-19T10:30:00Z",
  "modified_at": "2024-03-19T10:30:00Z"
}
```

When returned from the single-document endpoint (`GET /api/v1/documents/{id}`),
additional fields are populated:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "title": "document.pdf",
  "md5_checksum": "d41d8cd98f00b204e9800998ecf8427e",
  "sha512_checksum": "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
  "original_type": "application/pdf",
  "file_size": 102400,
  "language": "eng",
  "document_type_id": 1,
  "document_type_name": "invoice",
  "has_thumbnail": true,
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

> **Security note**: The `snippet` field is HTML-escaped. Only `<b>`/`</b>` highlighting tags from `ts_headline` are preserved; all other HTML in the document text is escaped to prevent XSS injection.

#### TaskResponse

```json
{
  "task_id": "660e8400-e29b-41d4-a716-446655440001",
  "batch_id": "550e8400-e29b-41d4-a716-446655440000",
  "task_type": "consume",
  "file_name": "report.pdf",
  "payload_doc_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "completed",
  "document_id": 42,
  "error": null,
  "label": "report.pdf",
  "created_at": "2024-03-19T10:30:00Z",
  "started_at": "2024-03-19T10:30:05Z",
  "completed_at": "2024-03-19T10:30:12Z"
}
```

`document_id` is the numeric database row ID; `payload_doc_id` carries the
document UUID when the task payload references one. `label` is
`TaskType: file name` for consume/enrich/thumbnail tasks with a payload file
(e.g. `"Consume: report.pdf"`), the file name alone for other task types,
otherwise derived from the task type and dedup key (e.g. `"Backup (full)"`,
`"Database migration"`, `"Download tessdata (eng)"`, `"Download Hugot model"`);
for other task types it falls back to the task type name (`"Consume"`, `"Enrich"`,
`"Thumbnail"`).

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
  "discarded": 0,
  "orphaned": false
}
```

`owner_state` (`"none"`, `"live"`, `"stale"`) is included when set.

---

## Configuration Reference

A commented example config with all supported keys and defaults is available at
[`config.example.yaml`](../config.example.yaml) in the project root. The file below
mirrors that reference and is what `kushim setup` generates at
`~/.config/edub-kushim/config.yaml` (the generated file only contains the keys
the setup flow writes; everything else falls back to defaults).

```yaml
app:
  environment: development # development | production
  log_level: info # silent | fatal | error | warn | info | debug
  logging:
    max_size: 100       # MB per file before rotation
    max_backups: 7      # rotated files to keep
    max_age: 30         # days before deleting old backups
    compress: true      # gzip rotated files

server:
  host: 0.0.0.0
  port: 3000
  read_timeout: 60s
  write_timeout: 60s
  idle_timeout: 60s
  max_concurrent_batches: 4 # max concurrent worker processes (enforced by the queue daemon)
  max_upload_size: 100 # max multipart upload size in MB
  max_download_files: 50 # max files in a single batch download
  max_download_size_mb: 500 # max total size in MB for batch download
  max_batch_delete: 50 # max documents in a single batch delete
  auth_enabled: false # when false, API endpoints bypass JWT auth
  session_secret: '' # 64-char hex key for JWT signing; auto-generated if empty

database:
  type: postgres # database engine
  # Connection via discrete fields (recommended):
  host: localhost
  port: 5432
  user: edub
  password: edub
  database: edub
  sslmode: disable
  # Where restores run psql: host (psql installed on this machine), docker/podman
  # (psql executed inside the named container), or remote (automated restore disabled).
  runtime: host
  # container: edub-postgres   # required when runtime is docker/podman
  # Or use a DSN string instead (overrides discrete fields):
  # dsn: "postgres://edub:edub@localhost:5432/edub?sslmode=disable"

storage:
  consumption_dir: '~/.config/edub-kushim/inbox'
  storage_dir: '~/.config/edub-kushim/storage'
  migration_mode: 'copy' # how files relocate when storage/consumption dirs change: 'copy' (safe) or 'move' (fast)
  trash:
    retention_days: 30 # days before soft-deleted documents are permanently purged

consumer:
  workers: 1 # concurrent file processing workers
  max_files_per_batch: 10 # max files per consume batch (0 = unlimited)
  supported_files: ['.pdf', '.png', '.jpg', '.jpeg', '.tiff', '.tif', '.docx', '.odt'] # file extensions to process
  polling:
    enabled: false # auto-consume on a schedule
    interval: 5 # polling interval in minutes
    # windows:               # optional: restrict polling to specific time ranges
    #   - start: "02:00"
    #     end: "06:00"
  reclaim:
    enabled: true # auto-resume batches interrupted by crashes or errors
    max_retries: 3 # max consecutive reclamation retries before quarantining
    stale_task_after: 600 # seconds after which a processing task is considered stale
  converter:
    enabled: false     # enable LibreOffice headless DOCX/ODT → PDF conversion
    binary: 'libreoffice' # command name (on PATH) or full path like '/opt/libreoffice/bin/soffice'
    timeout: 300       # per-document conversion timeout
  textextractor:
    engine: 'mupdf' # mupdf | gopdf | pdftotext
    timeout: 120
  pdfoptimizer:
    engine: 'mupdf' # mupdf | gs
    timeout: 0                  # 0 = disabled (no artificial deadline); >0 sets a per-attempt cap in seconds
    # fallback: 'gs'            # secondary optimizer when primary fails
  ocr:
    engine: 'gosseract' # gosseract | ocrmypdf
    languages: [eng] # required — set via kushim setup --languages
    timeout: 120
    # ocr_workers: 0  # parallel OCR goroutines per document; 0 = auto (CPU count / (max_concurrent_batches × consumer.workers))
  thumbnail:
    enabled: true  # generate per-document thumbnails (grid view)
    engine: 'mupdf' # mupdf (only implemented engine)
    dpi: 72
    max_width: 400  # output width; height = max_width * 4/3 (fixed 3:4 crop)
    quality: 80     # JPEG quality, 1-100
    timeout: 30     # per-document subprocess cap; 0 = disabled
    workers: 1      # concurrent thumbnail worker goroutines

enricher:
  workers: 1 # concurrent enrichment workers
  textreducer:
    engine: 'textrank'
    timeout: 120
    target_words: 2000 # optional: reduce text before LLM
  contentanalyzer:
    enabled: false # disabled by default — enable after configuring LLM
    timeout: 120
    # pause_on_credit_error: true  # pause batch on LLM provider credit/balance errors
    # prompt_template: ''  # optional custom LLM prompt; see docs for available placeholders
    # doc_type_refinement:
    #   enabled: true
    #   head_words: 600
    #   tail_words: 400
    llm:
      adapter: 'openai-compatible' # openai-compatible | anthropic
      provider: 'openai'           # auto-populated from model catalog
      model: 'gpt-4o'              # auto-populated from model catalog
      # token: 'sk-...'
      # temperature: 0.0
      # request_delay: 1  # seconds to sleep after each LLM request (0 = off)
    # fallback:               # optional second LLM tried on provider errors
    #   enabled: false
    #   llm:
    #     adapter: 'openai-compatible'
    #     provider: 'deepseek'
    #     model: 'deepseek-chat'
    #     token: ''
    #     temperature: 0.0
    #     request_delay: 1
  tagmatcher:
    timeout: 120
    reduce_target_words: 4000 # text reduction before tag matching
    chunk_size: 4096 # max tokens per inference chunk; 0 = model max (8180 for bge-m3) — see docs/tag-matcher.md
    hugot:
      model: 'BAAI/bge-m3'
      backend: 'ort' # ort (ONNX Runtime) | GO

backup:
  # enabled: true
  # path: /var/backups                  # fallback directory (also used by manual CLI)
  # schedules:                          # each mode can appear at most once
  #   - mode: full                      # full: edub.sql + storage + config
  #     interval: 7                     # days between backups
  #     time: "03:00"                   # preferred time of day (HH:MM)
  #     keep: 4                         # max archives to retain per mode (0 = unlimited)
  #   - mode: database                  # database: edub.sql + config only
  #     interval: 1
  #     time: "02:00"
  #     keep: 30
  #   - mode: documents                 # documents: storage + config only
  #     interval: 3
  #     time: "02:30"
  #     keep: 10
  #     path: /mnt/nas/documents        # optional per-schedule override

mirror:
  # enabled: true
  # path: /mnt/nas/documents            # local path or [user@]host:path (ssh key setup is yours)
  # interval: 1                         # days between mirrors
  # time: "02:00"                       # preferred time of day (HH:MM)
```

> **Matcher memory**: The tag matcher is the largest single memory consumer in
> the stack. With the default `chunk_size: 4096`, BGE-M3 idles at ~2.2–2.5 GB
> and peaks at ~4–6 GB per request; `chunk_size: 0` (full context, 8180
> tokens) can exceed 10 GB per request and OOM small hosts. ORT's CPU memory
> arena and memory-pattern pre-allocation are also disabled by default
> (internal `CpuMemArena: false`, `MemPattern: false`) to cap idle RSS instead
> of retaining ~4–5 GB of peak-inference buffers. See the
> [Tag Matcher guide](tag-matcher.md) for full memory/CPU tuning.

### Key sections

| Section                        | Purpose                                                                |
| ------------------------------ | ---------------------------------------------------------------------- |
| `app`                          | Environment mode and log verbosity                                     |
| `server`                       | HTTP listen address, timeouts, max concurrent batches, download limits |
| `database`                     | PostgreSQL connection: host, port, user, password, database, sslmode, or DSN |
| `storage`                      | Inbox and processed file directories                                   |
| `storage.trash`                | Trash retention: `retention_days` (default 30) before soft-deleted documents are permanently purged |
| `backup`                       | Scheduled backups: `enabled`, fallback `path`, and a `schedules` list — one entry per mode (`full`/`database`/`documents`) with its own `interval`, `time`, `keep`, and optional `path`. Deprecated flat `interval`/`time`/`keep` fields are auto-converted to a single `full` schedule. `backup.enabled: true` without any schedule (and without flat fields) is a config error — add at least one entry to `schedules` |
| `mirror`                       | Faithful rsync copy of the storage tree: `enabled`, `path` (local path or `[user@]host:path` remote target), `interval` (days), `time` (HH:MM). Runs with `rsync -a --delete` — deletions propagate to the destination. Requires `rsync` on PATH; remote targets need passwordless ssh. `mirror.enabled: true` without a `path` is a config error |
| `consumer`                     | Pipeline: which tools to use, which files to accept                    |
| `consumer.max_files_per_batch` | Max files per consume batch (default 10, 0 = unlimited)                |
| `consumer.polling`             | Auto-consume scheduler settings (enabled, interval)                    |
| `consumer.reclaim`             | Auto-resume of interrupted batches (enabled, max_retries 3, stale_task_after 600s) |
| `consumer.converter`           | Optional DOCX/ODT → PDF conversion via LibreOffice headless (enabled, binary, timeout) |
| `consumer.textextractor`       | Text extraction engine (mupdf, gopdf, pdftotext)                       |
| `consumer.pdfoptimizer`        | PDF optimizer (mupdf, gs) + optional fallback                          |
| `consumer.ocr`                 | OCR engine (gosseract, ocrmypdf) + language data                       |
| `consumer.workers`             | Concurrent file processing workers (default 1)                         |
| `enricher`                     | Async classification pipeline                                          |
| `enricher.textreducer`         | Text summarization before LLM (TextRank)                               |
| `enricher.contentanalyzer`     | LLM-based document classification (adapter/provider/model)                         |
| `enricher.contentanalyzer.llm` | Adapter/provider/model config (flat structure with capability flags)                |
| `enricher.contentanalyzer.prompt_template` | Custom Go `text/template` for the LLM prompt; empty = built-in default |
| `enricher.contentanalyzer.pause_on_credit_error` | Pause the batch when the LLM provider returns a credit/balance error (default `true`) |
| `enricher.contentanalyzer.llm.request_delay` | Seconds to sleep after each LLM request (default `1`; `0` = off, max `60`) — for rate-limited providers |
| `enricher.contentanalyzer.fallbacks` | Ordered list of fallback LLM configs (each `{enabled, llm}` with the same shape as `llm`), tried in order when the primary fails with a provider error — API down/unreachable, credit/rate-limit, malformed response, provider-side timeout. Disabled by default; validated (adapter/provider/model required, request_delay 0–60) only for each `enabled: true` entry |
| `enricher.contentanalyzer.doc_type_refinement` | Second-pass doc type refinement with head+tail of raw text (enabled, head_words, tail_words) |
| `enricher.tagmatcher`          | Semantic tag matching via Hugot (embeddings) — see [Tag Matcher guide](tag-matcher.md) for memory/CPU tuning |
| `enricher.tagmatcher.hugot`    | Hugot-specific settings (model, backend)                               |

---

## Configuration Timeout Guidelines

Each configurable timeout is a deadline for a specific processing stage. Setting
it too low causes tasks to fail with `context deadline exceeded` errors. Setting
it too high delays error detection and can mask hangs.

A timeout of `0` disables the artificial deadline — the stage runs without a
timeout. This is useful during troubleshooting but not recommended for
production, since a hung subprocess or unresponsive LLM provider can consume a
worker indefinitely.

### Server timeouts

`server.read_timeout`, `server.write_timeout`, `server.idle_timeout` (default: 60s)

These control the HTTP server connection lifecycle and protect against slow
clients, not against processing duration.

| Field | What it guards | Becomes premature below |
|---|---|---|
| `read_timeout` | Time to receive the full request (headers + body) | **5s** — a 100 MB multipart upload at moderate bandwidth needs at least 30s |
| `write_timeout` | Time to send the full response | **5s** — large JSON responses (search results, batch listings) can take several seconds |
| `idle_timeout` | Time a keep-alive connection stays idle | **1s** — keep-alive becomes useless, causing connection churn |

The default 60s is fine for most deployments. Lower `read_timeout` if you
control client upload speeds and want tighter slow-loris protection.

### Pipeline stage timeouts

All pipeline timeouts accept `0` to disable the timeout. Production
deployments should always set a positive value to prevent stuck tasks.

---

#### `consumer.textextractor.timeout` (default: 120s)

| Default | Minimum practical | Premature failure below |
|---|---|---|
| 120s | 5s | 5s |

The MuPDF engine extracts text from native PDFs in milliseconds. If you
switch to `pdftotext`, large or complex PDFs may need more time. For scanned
PDFs (which have no embedded text), this stage is nearly instant because it
returns empty text and hands off to OCR — the timeout that matters is the
OCR one.

- Set based on the **largest text-bearing document** in your workload.
- `pdftotext` on a 100 MB text-heavy PDF with complex tables can take 30–60s.

---

#### `consumer.ocr.timeout` (default: 120s)

| Default | Minimum practical | Premature failure below |
|---|---|---|
| 120s | 10s | 10s |

OCR is the most time-sensitive stage. The gosseract engine renders each page
at 200 DPI via MuPDF and runs Tesseract per page. Total time is proportional
to:

- **Number of pages** — linear scaling (a 20-page document takes ~20× a
  1-page document at same complexity).
- **Page complexity** — dense text, tables, mixed scripts, and small font
  sizes increase Tesseract time.
- **Languages enabled** — each additional language adds lookup time.
- **CPU cores** — the machine-wide budget is `ocr_workers ≈ cores / (max_concurrent_batches × consumer.workers)`; each concurrent batch runs its own `internal-ocr` child with that many page workers. A small pool (2–4) is usually sufficient because the MuPDF render loop is a sequential producer (sub-second render vs 3–10s OCR per page).

A single typical page with one language completes in 3–10s on modern hardware.
At the default 120s, the cap is roughly 12–40 pages depending on complexity.
If your documents regularly exceed that, raise the timeout proportionally.

- Monitor task durations in the dashboard or task list. Set timeout to at
  least **2× the observed maximum** for your typical documents.
- Values below 10s will time out even single-page documents.

---

#### `consumer.pdfoptimizer.timeout` (default: 0 — disabled)

| Default | Minimum practical | Premature failure below |
|---|---|---|
| 0 (disabled) | 5s | 5s |

PDF optimization recompresses and downsamples images. It defaults to disabled
because it is CPU-intensive and primarily benefits storage size, not
functional accuracy.

When enabling it:

- A single-page image PDF takes 5–15s.
- Multi-page documents scale linearly with page count and image density.
- The timeout caps **each attempt** — if a fallback optimizer is configured
  and the primary times out, the fallback gets the same timeout budget.

---

#### `consumer.converter.timeout` (default: 300s)

| Default | Minimum practical | Premature failure below |
|---|---|---|
| 300s | 30s | 10s |

LibreOffice headless conversion loads the entire document (fonts, layout
engine, renderer) and writes a PDF. For a simple text DOCX this takes 2–5s.
For a 100-page document with embedded images, complex tables, and uncommon
fonts, it can take 60–120s. The default 300s accounts for the heaviest
documents.

- Only applies when `consumer.converter.enabled` is `true`.
- Set based on the **largest office document** in your workload.
- If you see `context deadline exceeded` errors from the pipeline, raise this
  timeout before lowering any other stage timeout.

---

#### `enricher.textreducer.timeout` (default: 120s)

| Default | Minimum practical | Premature failure below |
|---|---|---|
| 120s | 10s | 10s |

The text reducer runs before LLM analysis using the local **TextRank** engine
(CPU-only, no network). It splits text into sentence-sized chunks, computes
TF-IDF scores, builds a similarity graph, and runs weighted PageRank. Runtime
depends on the number of sentences in the document (roughly proportional to
pages):

- A typical **5–15 page** document completes in **milliseconds to ~1s**.
- A large **200–300 page** document produces thousands of sentence-chunks, and
  the PageRank adjacency matrix scales as O(chunks²) — expect **1–5 seconds**.

The default 120s is generous. You can safely lower it to 30s and still handle
typical 200–300 page documents. For workloads that never exceed a few dozen
pages, 10s is sufficient.

---

#### `enricher.contentanalyzer.timeout` (default: 120s)

| Default | Minimum practical | Premature failure below |
|---|---|---|
| 120s | 10s | 10s |

LLM-based document classification. Total time depends on:

- **Provider latency** — network round-trip + provider queueing + token
  generation. Cloud providers typically respond in 5–30s for a moderate
  prompt; self-hosted models may take longer.
- **Input size** — text is pre-reduced by the text reducer, but the prompt
  still includes full metadata and tag context.
- **Doc type refinement** — when enabled, the analyzer makes two sequential
  LLM calls instead of one. The timeout covers both calls.

The default 120s accommodates two-pass refinement plus provider variability.
If you disable refinement (`doc_type_refinement.enabled: false`), 60s is
often sufficient.

The configured `llm.request_delay` also counts against this per-pass budget
(the sleep runs after each request inside the same deadline), so keep it well
below the timeout.

When a fallback LLM is configured and the primary fails with a provider error,
the fallback retry runs inside the **same** per-pass deadline — the timeout
budget is shared between primary and fallback, not doubled. A provider timeout
on the primary therefore leaves too little time for the fallback to succeed;
for fallback-heavy workloads raise the timeout or keep `request_delay` low.

- Values below 10s will time out even a single fast LLM response under normal
  network conditions.

---

#### `enricher.tagmatcher.timeout` (default: 120s)

| Default | Minimum practical | Premature failure below |
|---|---|---|
| 120s | 10s | 10s |

Semantic tag matching via the Hugot embedding model over a Unix domain socket.
Total time depends on:

- **Tag store size** — each tag in the store requires a similarity computation
  against the document embedding. A 10k-tag store takes ~31s on first cache
  build; subsequent matches on the same model load are faster.
- **Text length** — controlled by `reduce_target_words` (default 4000 words).      
  Longer text increases encoding time.
- **Model size** — larger models (`BAAI/bge-m3` is ~1.3 GB) take longer per
  inference.

| Tag store size | Typical match time |
|---|---|
| 1k | 3–10s |
| 10k | 10–60s |
| 100k+ | 120–300s |

- Raise the timeout if you have a large tag store (100k+) or observe
  `context deadline exceeded` errors on tag match tasks.
- Values below 10s will time out even modest tag stores.
- The matcher server mirrors this value into its `WriteTimeout` at startup —
  after changing it, restart `kushim hugot` for the server side to take
  effect.

---

### General tuning approach

1. **Start with defaults** and run your typical workload.
2. **Check for `context deadline exceeded` errors** in failed tasks.
3. **Identify which stage timed out** — the error includes the component name
   (e.g., `ocr: context deadline exceeded`).
4. **Check task duration** via `kushim task list --batch <id>` or the
   dashboard. Compare completed task durations to the timeout.
5. **Raise the timeout for that stage** to at least 2× the observed maximum
   duration, or lower it if the observations show it is excessively generous.

A well-tuned set of timeouts catches genuine hangs without being so tight
that normal variation causes spurious failures. No single number fits every
workload — the defaults are conservative starting points.

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

### Batch statuses

| Status       | Description                                                   |
| ------------ | ------------------------------------------------------------- |
| `queued`     | Batch created, waiting for a worker to pick it up        |
| `processing` | Batch currently being processed by a worker                   |
| `completed`  | All tasks finished successfully                               |
| `failed`     | One or more tasks failed                                       |
| `paused`     | Batch paused due to an LLM provider credit/balance error. Resolve billing and re-queue. |
| `cancelled`  | Batch cancelled via `kushim consume cancel`                    |

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
server enqueues tasks in the database with `status='queued'`; the `kushim queue` daemon picks up the batch and **forks** `kushim consume --batch <id>`

The maximum number of concurrent worker processes is controlled by
`server.max_concurrent_batches` (default 4). The `kushim queue` daemon enforces
the limit — additional queued batches wait until a worker slot frees up.

```bash
# Check version
edub version
# Document Management System v2.9.0

# Start server (default when no command is given)
edub
```

The `kushim` binary must be runnable from PATH (or the queue daemon must be
started with a resolvable `os.Args[0]`, e.g. a full path) because the daemon
re-execs its own binary to fork workers. If `kushim queue` cannot re-exec,
batches stay queued and are never processed — the API consume endpoints still
accept and enqueue files.

## Settings Page

The main web UI includes a **Settings** page at `/settings` with two tabs:

### Configuration Tab

A single-page form for all user-configurable settings:

- **Server**: host, port, max upload/download sizes, max download files,
  max concurrent batches, max batch delete, authentication enabled toggle
- **OCR**: engine selector, timeout, data directory, languages list (add/remove)
- **Consumer**: workers, max files per batch, supported file types (checkbox list, PDF always on), DOCX/ODT converter (enabled toggle, binary path, timeout)
- **Thumbnails**: enabled toggle, engine (mupdf), DPI, max width (px), quality (1–100), timeout (seconds; 0 = disabled), workers (1 = single worker)
- **Polling**: enabled toggle, interval, active windows (start/end pairs, add/remove)
- **Reclaim**: auto-resume toggle, max retries, stale task after (seconds)
- **Text extractor**: engine, timeout
- **PDF optimizer**: engine, fallback, timeout
- **Enricher**: workers
- **Content analyzer (LLM)**: enabled toggle, timeout, adapter (openai-compatible/anthropic),
  provider (filtered by adapter), model (filtered by provider, loaded from model catalog),
  token, temperature, pause on credit error toggle,
  request delay (seconds to sleep after each LLM request; 0 = off, max 60),
  reasoning toggle (shown when model supports it),
  reasoning effort (shown when reasoning enabled),
  doc type refinement (enabled, head words, tail words)
- **Tag matcher**: engine, timeout, reduce target words, chunk size, Hugot model,
  Hugot backend (ort/GO)
- **Text reducer**: engine, timeout, target words
- **Storage**: consumption directory, storage directory
- **Trash**: retention period in days (`storage.trash.retention_days`, default 30)
- **Database**: host, port, user, password, database name, SSL mode

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
- **Create User** button opens a modal with Username (required) and Password (required; the server enforces a 12-character minimum plus uppercase/lowercase/digit/special-character rules)
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

The `kushim setup` command (both CLI and wizard) generates concrete,
pre-filled service files to `<configDir>/systemd/`. Template/reference files
are also provided at `deploy/systemd/` as `@.service` templates for server
deployments where the admin wants explicit control.

| File | Process |
|------|---------|
| `kushim-hugot.service` | Matcher server (`kushim hugot`) — `Type=notify` |
| `kushim-queue.service` | Batch queue daemon (`kushim queue`) |
| `edub.service` | API server (`edub`) |
| `edub-kushim.target` | Groups all three services via `PartOf=` |

All three services are grouped under an `edub-kushim.target` unit via
`PartOf=`. This means one command manages all of them:
`sudo systemctl enable --now edub-kushim.target`.

The `kushim hugot` service uses `Type=notify` + `NotifyAccess=main` — it
calls `sd_notify(READY=1)` after the model is loaded, tag cache is built,
and the Unix socket is listening. Dependents (`edub`, `kushim-queue`) use
`After=kushim-hugot.service` and `Wants=kushim-hugot.service` so they wait
for actual readiness, not just process start. `Wants=` is used (not
`Requires=`) because the architecture is designed to degrade gracefully
without the matcher — tag CRUD still succeeds and only the embedding cache is skipped until the matcher is reachable.

The queue service also waits for the matcher (`After=kushim-hugot`) because
it forks workers that immediately call matcher RPC. Waiting avoids the
connection-refused race on first boot.

### Quick Setup — Generated Files (Recommended)

`kushim setup` writes concrete files to `<configDir>/systemd/`. No template
variables, no manual editing:

```bash
# 1. Create a dedicated system user
sudo useradd -r -m -d /var/lib/edub-kushim -s /usr/sbin/nologin edub

# 2. Install binaries
sudo cp dev/bin/kushim /usr/local/bin/
sudo cp dev/bin/edub   /usr/local/bin/

# 3. Initialize config as the dedicated user
sudo -u edub kushim setup --cli --languages eng,spa,...

# 4. Install and start all services (single command)
sudo cp /var/lib/edub-kushim/.config/edub-kushim/systemd/* /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now edub-kushim.target
```

Alternatively, copy the path printed at the end of `kushim setup` for the
`sudo cp` command.

### Quick Setup — Template Files (Server Deployments)

For server deployments where you want explicit control, use the `@.service`
template files from `deploy/systemd/`:

```bash
sudo useradd -r -m -d /var/lib/edub-kushim -s /usr/sbin/nologin edub
sudo cp dev/bin/kushim /usr/local/bin/
sudo cp dev/bin/edub   /usr/local/bin/
sudo -u edub kushim setup --cli --languages eng,spa,...

# Copy and enable template services — %i is the system user
sudo cp deploy/systemd/*.service deploy/systemd/*.target /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now edub-kushim@edub.target
```

The `%i` specifier expands to the instance name (e.g., `edub`), which sets
`User=`, `Group=`, `Environment=HOME=/home/<user>`, and the socket path.
Non-standard home directories (e.g., `/var/lib/edub-kushim`) require a
drop-in override for `Environment=HOME`.

### Timeout Considerations

The hugot service has `TimeoutStartSec=120` because loading the embedding
model and building the tag cache can take significant time:
- Model load: ~10s
- Tag cache build: ~31s for 10k tags (batch size 32, ~100ms per ONNX
  inference batch)
- Total: ~41s for 10k tags

For deployments with 100k+ tags, raise the timeout via a drop-in:
```ini
[Service]
TimeoutStartSec=600
```

### Memory Limits

The matcher holds the embedding model in RAM (~2.2–2.5 GB idle with BGE-M3 and
the default `chunk_size: 4096`). To guarantee that a misconfigured matcher
can only kill itself — never the whole host — add a drop-in for the
`kushim-hugot` service with hard memory caps:

```ini
[Service]
MemoryHigh=6G   # throttle under pressure before the hard cap
MemoryMax=8G    # hard cap: systemd OOM-kills only the matcher
```

Adjust the values to your `chunk_size` (see the
[Tag Matcher guide](tag-matcher.md) for per-configuration peaks).

### Generated vs Template Files

| Aspect | Generated (`kushim setup`) | Template (`deploy/systemd/`) |
|--------|---------------------------|------------------------------|
| User/Group | Current user | `%i` specifier |
| Binary paths | Resolved from `os.Executable()` | Hardcoded `/usr/local/bin/` |
| Home directory | Resolved from `os.UserHomeDir()` | Assumes `/home/%i` |
| Socket path | Explicit `--socket` with full path | `/home/%i/.config/edub-kushim/...` |

### Customizing User and Permissions

The generated files from `kushim setup` use the user that ran setup. The
template files use `%i` to specify the user at install time. Both sets run
under a non-root system account when configured correctly.

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

Log files are automatically rotated when they reach a configured size
(default 100 MB). Rotated files are gzip-compressed and appear alongside
the active `.log` as `{name}-{timestamp}.log.gz`. The number of old
backups to keep and the maximum age (in days) before deletion are
configurable. See the `app.logging` section in `config.yaml`.

### Service Dependencies and Order

```
                     edub-kushim.target
                     Wants & After
                    /        |        \
                   ▼         ▼         ▼
        kushim-hugot   kushim-queue      edub
        (Type=notify)  (After=hugot,   (After=hugot,
                        Wants=hugot)    Wants=hugot)
              |
              +-- network.target (After=network.target)
```

`kushim-queue.service` and `edub.service` both declare `Wants=` (not
`Requires=`) on the matcher — they start even if the matcher is down, and
the API degrades gracefully: tag CRUD still succeeds, with the tag embedding
cache skipped (errors logged) until the matcher comes back. The queue daemon
only reads the database under default
config (`backup.enabled=false`); it forks consumer children that talk to the
matcher, so waiting via `After=` avoids the connection-refused race.

The edub-kushim.target groups all three with `PartOf=`, making stop/restart
transitive:

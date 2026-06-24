# CLI Commands (`internal/commands/`)

## `commands.go`

### Globals

`commandSets` map with `"cli"` key. CLI set contains: `version`, `consume`, `search`, `task`, `setup`, `serve-matching`.

> **Note**: The `edub` binary no longer uses `CommandRunner` from the commands package. It has its own standalone runner in `cmd/edub/runner.go` that only handles the `version` command (server mode is the default when no command matches).

### Structs

- `Command` — `Name`, `Description`, `Handler func(container *Container, args []string) error`
- `CommandRunner` — `container *Container`, `commands map[string]Command`
  - **Methods**: `NewCommandRunner(container, set string) *CommandRunner`, `ExecuteCommand(name, args) error`

### Functions

- `ListCommands() []Command`
- `PrintUsage()` — Prints CLI usage, calls `os.Exit(1)`
- `versionHandler(container, args) error` — Prints `"Document Management System v{version}"`

---

## `consume.go`

### Functions

- `consumeHandler(c, args) error` — Enqueues files in paired (consume + enrich) tasks; `--bg` for background processing (spawns subprocess with `--batch`), `--batch <id>` for batch resume, `--force` to override a stale lease when resuming a batch. Subcommand `cancel <batch-id>` cancels a running batch.
- `consumeCancelHandler(c, args) error` — Cancels pending + processing tasks via DB, reads batch owner PID from batch_owner table, sends SIGTERM.
- `pollBatch(ctx, queries, cp, ep, logger, batchID) error` — Streams per-file progress to stdout; stops when no pending/processing tasks remain.
- `taskDisplayInfo(t) taskDisplay` — Extracts index, filename, task type from payload.
- `totalFiles(tasks) int` — Counts total files from max file_index in payloads.

---

## `container.go`

### Struct

- `Container` — `config`, `logger`, `db`, `engine`, `cache`, `dispatcher`, `pools struct { consume *pool.Pool; enrich *pool.Pool; config *pool.Pool }`
  - **Methods**:
    - `NewContainer(cfg, logger)` — No DB
    - `NewContainerWithDB(cfg, logger, db)` — With provided DB
    - `GetDB() (*sql.DB, error)` — Creates DB lazily, runs goose migrations on first open
    - `GetCache() (*cache.Cache, error)` — Builds tag embedding cache lazily via `cache.BuildTagCache`
    - `GetDispatcher() (*task.Dispatcher, error)` — Lazily creates dispatcher. Creates a `MatcherClient` connected to the Unix socket at `<config_dir>/kushim-matcher.sock`, builds `TagService` wired through the client, and `Enricher` with the matcher.
    - `GetPool(taskType) (*pool.Pool, error)` — Returns the pool for "consume", "enrich", or "config", lazily creates with dispatcher. Config pool uses 1 worker and 5s poll interval.
    - `GetSearchEngine() (*search.Engine, error)`
    - `Close()` — Stops all pools if created, closes DB

---

## `serve_matching.go`

### Function

- `serveMatchingHandler(c, args) error` — Starts the matcher RPC server over a Unix domain socket. Accepts optional `--socket <path>` flag (default `<config_dir>/kushim-matcher.sock`). Creates a Hugot embedding session, builds the tag cache from the database, exposes HTTP endpoints:

| Endpoint                        | Method | Description                                                                 |
| ------------------------------- | ------ | --------------------------------------------------------------------------- |
| `POST /rpc/v1/encode`           | POST   | Accepts `{"texts": [...]}`, returns `{"embeddings": [[...], ...]}`          |
| `POST /rpc/v1/match`            | POST   | Accepts `{"doc_id", "input", "candidate_tags"}`, returns `{"matches": []}` |
| `POST /rpc/v1/consolidate`      | POST   | Accepts `{"doc_id", "queries"}`, returns `{"results": []}`                  |
| `POST /rpc/v1/add-to-store`     | POST   | Accepts `{"names": [...]}`, encodes and adds to embedding store             |
| `POST /rpc/v1/remove-from-store`| POST   | Accepts `{"names": [...]}`, removes from embedding store                    |
| `GET /health`                   | GET    | Returns `{"ok": true}`                                                      |

Listens on a Unix socket (cleaned up on shutdown). Handles SIGTERM/SIGINT for graceful shutdown.

---

## `flags.go`

### Struct

- `FlagParser` — `args []string`, `pos int`, `used map[int]bool`, `rest []string`
  - **Methods**: `NewFlagParser(args)`, `Help(helpText) bool`, `String(flag, *dst) error`, `Int(flag, *dst, min, max) error`, `Bool(flag, *dst) error`, `Rest() []string`

---

## `search.go`

### Functions

- `searchHandler(c, args) error` — Parses flags (`--limit`, `--offset`, `--rebuild-index`), runs search, formats results with ANSI highlighting
- `highlightSnippet(s) string` — Replaces `<b>`/`</b>` with ANSI color codes
- `formatSize(bytes) string` — Human-readable size
- `rebuildIndex(c) error` — Calls `RebuildDocumentFTS`

---

## `setup.go`

### Functions

- `RunSetup(args, logger) error` — Dispatches to either `runSetupWizard` (default) or `runSetupCLI` (when `--cli` flag is set).
- `runSetupWizard(args, logger) error` — Starts a standalone HTTP wizard server on `0.0.0.0:8420` serving the embedded SvelteKit wizard SPA. The server bootstraps config on first PUT request.
- `runSetupCLI(args, logger) error` — Accepts `--languages`, `--inbox-path`, `--storage-path`, `--database-path`, `--consumer-pdfoptimizer-fallback`, `--consumer-pdfoptimizer-engine`, `--consumer-ocr-engine`, `--reset-database`. Downloads Tesseract language data and Hugot model (`BAAI/bge-m3`). Creates config.yaml, directories, initializes database schema (or resets it with `--reset-database`).
- `setupHandler(c, args) error` — Returns error telling the user to use `kushim setup` for the wizard or `kushim setup --cli --languages ...` for terminal setup

---

## `task.go`

### Functions

- `taskHandler(c, args) error` — Routes to list, status, retry subcommand
- `taskListHandler(c, args) error` — Lists tasks with batch/status/type/limit/offset filters; columns: TASK ID, TYPE, STATUS, BATCH, FILE
- `taskStatusHandler(c, args) error` — Shows task details with type, timestamps, document DB ID, document UUID, error for failed tasks
- `taskRetryHandler(c, args) error` — Re-enqueues a failed task as pending

---

## See Also

- [Task System](task-system.md) — Dispatcher, CRUD, and pool used by CLI commands
- [Config & Utils](config-and-utils.md) — Configuration loading, logger
- [Pipeline](pipeline.md) — Consumption engine used by consume command

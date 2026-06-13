# CLI Commands (`internal/commands/`)

## `commands.go`

### Globals

`commandSets` map with `"cli"` and `"server"` keys. Server set only contains `version`. CLI set contains: `version`, `consume`, `search`, `task`, `setup`.

### Structs

- `Command` — `Name`, `Description`, `Handler func(container *Container, args []string) error`
- `CommandRunner` — `container *Container`, `commands map[string]Command`
  - **Methods**: `NewCommandRunner(container, set string) *CommandRunner`, `ExecuteCommand(name, args) error`

### Functions

- `ListCommands() []Command`
- `PrintUsage()` — Prints CLI usage, calls `os.Exit(1)`
- `PrintServerUsage()` — Prints server usage, calls `os.Exit(1)`
- `versionHandler(container, args) error` — Prints `"Document Management System v{version}"`

---

## `consume.go`

### Functions

- `consumeHandler(c, args) error` — Enqueues files in paired (consume + enrich) tasks; `--bg` for background processing (spawns subprocess with `--batch`), `--batch <id>` for batch resume, `--force` for stale PID file override. Subcommand `cancel <batch-id>` cancels a running batch.
- `consumeCancelHandler(c, args) error` — Cancels pending + processing tasks via DB, reads PID file, sends SIGTERM.
- `pollBatch(ctx, queries, cp, ep, logger, batchID) error` — Streams per-file progress to stdout; stops when no pending/processing tasks remain.
- `taskDisplayInfo(t) taskDisplay` — Extracts index, filename, task type from payload.
- `totalFiles(tasks) int` — Counts total files from max file_index in payloads.

---

## `container.go`

### Struct

- `Container` — `config`, `logger`, `db`, `engine`, `cache`, `dispatcher`, `pools struct { consume *pool.Pool; enrich *pool.Pool }`
  - **Methods**:
    - `NewContainer(cfg, logger)` — No DB
    - `NewContainerWithDB(cfg, logger, db)` — With provided DB
    -     `GetDB() (*sql.DB, error)` — Creates DB lazily, runs goose migrations on first open
    - `GetCache() (*cache.Cache, error)` — Builds tag embedding cache lazily via `cache.BuildTagCache`
    - `GetDispatcher() (*task.Dispatcher, error)` — Lazily creates dispatcher with cache
    - `GetPool(taskType) (*pool.Pool, error)` — Returns the pool for "consume" or "enrich", lazily creates with dispatcher
    - `GetSearchEngine() (*search.Engine, error)`
    - `Close()` — Stops both pools if created, closes DB

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

- `RunSetup(args, logger) error` — Accepts `--languages`, `--inbox-path`, `--storage-path`, `--database-path`, `--consumer-pdfoptimizer-fallback`, `--consumer-pdfoptimizer-engine`, `--consumer-ocr-engine`, `--reset-database`. Downloads Tesseract language data and Hugot model (`BAAI/bge-m3`). Creates config.yaml, directories, initializes database schema (or resets it with `--reset-database`).
- `downloadFile(url, dest) error` — Downloads via curl
- `setupHugotModel(ctx, configDir, logger) error` — Downloads Hugot model using `hugot.DownloadModel`, renames to model short name
- `setupHandler(c, args) error` — Returns error (setup must be run without config)

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

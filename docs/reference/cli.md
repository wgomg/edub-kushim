# CLI Commands (`internal/commands/`)

## `commands.go`

### Globals

`commandSets` map with `"cli"` key (CLI commands) and `"server"` key (edub commands: `version`). CLI set contains: `version`, `consume`, `search`, `hugot`, `task`, `user`, `setup`, `enrich`, `queue`, `storage`, `backup`, `config`, `restore`.

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

### Globals

- `ErrBatchPaused` — Sentinel error returned by `pollBatch` when the runner has paused the batch mid-flight due to an LLM provider credit/balance error. The caller suppresses it (`err = nil`) so paused is not treated as a failure.

### Functions

- `consumeHandler(c, args) error` — Enqueues files in paired (consume + enrich) tasks; direct-fallback if queue empty, `--batch <id>` for batch resume, `--force` to override a stale lease when resuming a batch. Subcommand `cancel <batch-id>` cancels a running batch. If paused batches exist (from unresolved LLM credit/balance errors), `kushim consume` (without `--batch`) refuses with an error listing the paused batch IDs. After `pollBatch` returns, if the batch completed normally it calls `setBatchTerminalStatus`; if the batch was paused (`ErrBatchPaused`) it suppresses the error so the caller returns cleanly.
- `consumeCancelHandler(c, args) error` — Cancels pending + processing tasks via DB, reads batch owner PID from batch_owner table, sends SIGTERM.
- `pollBatch(ctx, queries, cp, ep, logger, batchID) error` — Streams per-file progress to stdout; returns `nil` when all tasks finish, `ErrBatchPaused` when the runner pauses the batch, or `ctx.Err()` on cancellation.
- `setBatchTerminalStatus(ctx, queries, batchSvc, batchID) error` — Computes a terminal status (`completed` or `failed`) from task outcomes. Has a defense-in-depth guard: if the batch is already in a terminal state (`completed`, `failed`, `cancelled`, `paused`), it returns `nil` without modifying the batch.
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
    - `GetDispatcher() (*task.Dispatcher, error)` — Lazily creates dispatcher. Creates a `MatcherClient` connected to the Unix socket at `<config_dir>/kushim-hugot.sock`, builds `TagService` wired through the client, and `Enricher` with the matcher.
    - `GetPool(taskType) (*pool.Pool, error)` — Returns the pool for "consume", "enrich", or "config", lazily creates with dispatcher. Config pool uses 1 worker and 5s poll interval.
    - `GetSearchEngine() (*search.Engine, error)`
    - `Close()` — Stops all pools if created, closes DB

---

## `serve_matching.go`

### Function

- `serveHugotHandler(c, args) error` — Starts the matcher RPC server over a Unix domain socket. Accepts optional `--socket <path>` flag (default `<config_dir>/kushim-hugot.sock`). Creates a Hugot embedding session, builds the tag cache from the database, exposes HTTP endpoints:

| Endpoint                         | Method | Description                                                                |
| -------------------------------- | ------ | -------------------------------------------------------------------------- |
| `POST /rpc/v1/encode`            | POST   | Accepts `{"texts": [...]}`, returns `{"embeddings": [[...], ...]}`         |
| `POST /rpc/v1/match`             | POST   | Accepts `{"doc_id", "input"}`, returns `{"matches": []}` |
| `POST /rpc/v1/consolidate`       | POST   | Accepts `{"doc_id", "queries"}`, returns `{"results": []}`                 |
| `POST /rpc/v1/add-to-store`      | POST   | Accepts `{"names": [...]}`, encodes and adds to embedding store            |
| `POST /rpc/v1/remove-from-store` | POST   | Accepts `{"names": [...]}`, removes from embedding store                   |
| `GET /health`                    | GET    | Returns `{"ok": true}`                                                     |

Listens on a Unix socket (cleaned up on shutdown). Handles SIGTERM/SIGINT for graceful shutdown.

---

## `flags.go`

### Struct

- `FlagParser` — `args []string`, `pos int`, `used map[int]bool`, `rest []string`
  - **Methods**: `NewFlagParser(args)`, `Help(helpText) bool`, `String(flag, *dst) error`, `Int(flag, *dst, min, max) error`, `Bool(flag, *dst) error`, `Rest() []string`

---

## `enrich.go`

### Functions

- `enrichHandler(c, args) error` — Creates a queued batch with a single pending enrich task for a given document UUID. Validates the document exists, creates the batch (source `"reenrich"`, status `"queued"`), then creates an `"enrich"` task with dedup key `"enrich:doc:<uuid>"`. Returns the batch ID. Prints document-not-found and already-queued errors.

---

## `search.go`

### Functions

- `searchHandler(c, args) error` — Parses flags (`--limit`, `--offset`, `--rebuild-index`), runs search, formats results with ANSI highlighting
- `highlightSnippet(s) string` — Replaces `<b>`/`</b>` with ANSI color codes
- `formatSize(bytes) string` — Human-readable size
- `rebuildIndex(c) error` — Runs `REINDEX INDEX idx_document_tsv`

---

## `setup.go`

### Functions

- `RunSetup(args, logger) error` — Dispatches to either `runSetupWizard` (default) or `runSetupCLI` (when `--cli` flag is set).
- `runSetupWizard(args, logger) error` — Starts a standalone HTTP wizard server on `0.0.0.0:8420` serving the embedded SvelteKit wizard SPA. The server bootstraps config on first PUT request.
- `runSetupCLI(args, logger) error` — Accepts `--languages` through `--consumer-pdfoptimizer-fallback` (same as wizard), plus `--admin-user`, `--admin-password`, `--reset-database`. Generates and persists a session secret. Downloads Tesseract language data and Hugot model (`BAAI/bge-m3`). Creates config.yaml, directories, initializes database schema (or resets it with `--reset-database`). Prompts interactively for admin username/password when omitted. Creates the admin user via `service.User.Create()`. After setup completes, generates systemd service files to `<configDir>/systemd/` via `config.GenerateServiceFiles` and prints copy/install instructions.
- `setupHandler(c, args) error` — Returns error telling the user to use `kushim setup` for the wizard or `kushim setup --cli --languages ...` for terminal setup

---

## `task.go`

### Functions

- `taskHandler(c, args) error` — Routes to list, status, retry subcommand
- `taskListHandler(c, args) error` — Lists tasks with batch/status/type/limit/offset filters; columns: TASK ID, TYPE, STATUS, BATCH, FILE
- `taskStatusHandler(c, args) error` — Shows task details with type, timestamps, document DB ID, document UUID, error for failed tasks
- `taskRetryHandler(c, args) error` — Re-enqueues a failed task as pending

---

## `storage.go`

### Functions

- `storageHandler(c, args) error` — Entry point for `kushim storage`. Routes subcommands, currently only `orphans`.
- `orphansHandler(c, args) error` — Manages orphaned files. Creates `docker.Orphaned` service from the container and dispatches to:
  - `orphansList(svc, args)` — Lists pending orphaned files with ID, key, source dir, size, status
  - `orphansScan(svc, args)` — Runs detection + quarantine
  - `parseAndRun(svc, args, action)` — Parses `--id` flag and runs Delete/Restore
  - `parseAndRunOrAll(svc, args, single, bulk)` — Parses `--id` or `--all` and runs MoveToInbox
  - `orphansBulk(svc, args, label, action)` — Runs bulk action (DeleteAll, MoveAllToInbox)

### Subcommands

```
kushim storage orphans list
kushim storage orphans scan
kushim storage orphans delete --id <n>
kushim storage orphans restore --id <n>
kushim storage orphans move-to-inbox --id <n>
kushim storage orphans move-to-inbox --all
kushim storage orphans delete-all
kushim storage orphans move-to-inbox-all
```

---

## `queue.go`

### Functions

- `queueHandler(c, args) error` — Starts the batch queue daemon for background consumption and inbox polling. Accepts `--bg` to daemonize (re-execs without `--bg` and returns). In daemon mode: checks PID file for single-instance, sets up signal handling (SIGTERM/SIGINT → graceful shutdown with PID file cleanup), opens log file at `<config_dir>/logs/queue.log`, spawns a polling goroutine, and runs three concurrent scheduling paths:
  - **Notification-driven consumption** — A dedicated `LISTEN` goroutine connects to Postgres via a separate pgxpool (MinConns=1, MaxConns=1) and listens on the `batch_queued` channel. When a batch transitions to `status='queued'`, a database trigger sends a `pg_notify` that wakes the goroutine, which forwards the signal over a buffered channel (capacity 4). The daemon picks the batch up in milliseconds, calls `consumeNextQueuedBatch` which forks `kushim consume --batch <id> --force`.
  - **Safety timer** — A 30-second timer guarantees consumption runs at least once every 30s, even if a notification is dropped (connection blip, reconnection). Resets on each notification-driven consumption.
  - **Housekeeping ticker** — A 5-second ticker for stale reclamation and backup scheduling (unchanged from the original polling approach). Runs:
    - **Stale batch reclamation** — Lists batch owners with stale heartbeats (>15s) and active tasks, signals SIGTERM to the owner PID (if the process is still alive), quarantines tasks at or above `consumer.reclaim.max_retries` (default 3) to `failed`, resets remaining processing→pending with an incremented attempt counter, re-queues the batch, deletes the stale owner row. Gated by `consumer.reclaim.enabled` (default `true`); when `false`, stale batches remain in `processing` state and are not resumed.
    - **Stale task reclamation** — An age-based sweep (`reclaimStaleTasks`) that resets individual `processing` tasks back to `pending` (or `failed` if at max retries) when their `started_at` exceeds `consumer.reclaim.stale_task_after` (default 600s). Gated by `consumer.reclaim.enabled` and rate-limited to at most once per `max(60s, stale_task_after/10)` so the sweep does not run on every 5s tick.
- `reclaimStaleBatches(ctx, batchSvc, logger) error` — Iterates stale batch owners, signals SIGTERM to the owner PID (if alive), quarantines tasks at or above `consumer.reclaim.max_retries` to `failed`, resets remaining processing→pending with incremented attempt counter, re-queues, deletes owner. Only called when `consumer.reclaim.enabled` is `true`.
- `reclaimStaleTasks(ctx, batchSvc, cfg, logger) error` — Age-based sweep for individual tasks stuck in `processing` beyond `consumer.reclaim.stale_task_after`. Delegates to `service.Batch.ResetStaleProcessingTasks` which quarantines (attempts ≥ maxRetries → `failed`) and resets (attempts < maxRetries → `pending` + increment) in a single transaction. Logs the count of reclaimed tasks.
- `consumeNextQueuedBatch(ctx, client, batchSvc, maxConcurrent, logger) error` — Picks and dispatches the next queued batch.
- `runPollingLoop(ctx, c, client, batchSvc, maxConcurrent)` — Goroutine that runs on its own dynamic ticker (configured by `consumer.polling.interval`, minimum 1 minute). Re-reads config from disk on each iteration via `config.Reload` for live config updates. Checks capacity (`CountQueuedBatches + CountLiveBatches < MaxConcurrentBatches`) and missing external tools before calling `consumption.ScanAndEnqueue`. Replaces the former `PollingScheduler` in `edub`.
- `pollingTick(ctx, c, client, batchSvc, maxConcurrent, missingTools)` — Single polling iteration: capacity check, missing tools check, then delegates to `consumption.ScanAndEnqueue` to scan inbox, deduplicate, and create a `queued` batch with consume+enrich task pairs.
- `maybeScheduleBackup(ctx, c, client) error` — Calls `backup.IsBackupDue` (DB-driven: cooldown check, active task check, schedule derivation from last completed backup). If due, enqueues a `"backup"` task via the dispatcher. No longer uses `backup-state.json`.

The daemon also starts a **backup pool** (1 worker, 60s poll interval) when `backup.enabled` becomes `true` (checked lazily on each housekeeping tick, so enabling backup at runtime via config reload creates the pool without a restart). The pool executes scheduled backup tasks via `BackupTaskHandler`. The ticker and polling loop check the DB-backed `backup_lock` table via `IsBackupLocked` — backup scheduling and polling are skipped while a backup is in progress, while stale reclamation continues to run unconditionally.

---

## `config.go`

### Functions

- `configHandler(c, args) error` — Entry point for `kushim config`. Parses `--unset`, `--validate`, `--path`, `--help` flags, then dispatches on positional arg count: 0 → `dumpAllConfig`, 1 → `getConfigValue`, 2+ → `setConfigValue`.
- `validateConfig(configDir) error` — Calls `config.Load` and prints `config.yaml is valid` on success.
- `dumpAllConfig(configDir) error` — Reads config via Viper and prints all keys as `key = value` lines (``git config --list`` style). Scalars print raw, arrays join with `, `.
- `getConfigValue(configDir, key) error` — Reads a single dot-notation key via Viper and prints its value. Scalars print raw, arrays/maps print as YAML.
- `setConfigValue(configDir, key, rawValue) error` — Parses the value via `parseValue`, calls `atomicSetConfig`, prints `key = value` confirmation.
- `unsetConfigValue(configDir, key) error` — Reads YAML into a map, deletes the nested key via `deleteNestedKey`, writes back through the atomic validation pipeline.
- `deleteNestedKey(m map[string]any, key string) bool` — Recursively walks a nested map using dot-notation segments and deletes the leaf. Returns `false` if the key path does not exist.
- `atomicSetConfig(configDir, body map[string]any) error` — Viper read-modify-write with atomic safety: writes to a temp directory, runs `config.Load(tmpDir)` for full validation (including `finalizeConfig` business rules), then renames the file into place. Writes config with `0600` permissions (owner read/write only).
- `parseValue(raw string) any` — Auto-detects value type: `true`/`false` → `bool`, parseable integer → `int`, parseable float → `float64`, contains comma → `[]string` (trimmed), otherwise `string`.
- `printValue(val any)` — Prints a value matching CLI output specs: scalars raw, arrays/maps via `yaml.Marshal`.

---

## `backup.go`

### Functions

- `backupHandler(c, args) error` — `kushim backup [--path <dir>]` — Runs a synchronous backup: checks preconditions (`IsBackupLocked`, `CountProcessingTasks`, polling status), acquires `AcquireBackupLock`, creates a `tar.gz` archive of the DB (via app-level SQL dump), config file, and storage directory, prints the result, applies retention cleanup, releases the lock.
- `restoreHandler(c, args) error` — `kushim restore <backup-file.tar.gz> [--force] [--dry-run]` — Validates the archive, checks preconditions, acquires `AcquireBackupLock`, checks the running daemon's PID file, prompts for confirmation (unless `--force`), extracts to a temp dir, executes the SQL dump against the database, then replaces storage and config, releases the lock.

---

## See Also

- [Task System](task-system.md) — Dispatcher, CRUD, and pool used by CLI commands
- [Config & Utils](config-and-utils.md) — Configuration loading, logger
- [Pipeline](pipeline.md) — Consumption engine used by consume command

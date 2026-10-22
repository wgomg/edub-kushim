# CLI Commands (`internal/ops/`)

The process layer is split by role: the verb table and the `kushim` verbs live in `internal/ops/cli`, the queue daemon and its LISTEN/NOTIFY loop in `internal/ops/queue`, the per-batch child in `internal/ops/consume`, the setup flow in `internal/ops/setup`, the dependency-injection container in `internal/ops/container`, the shared flag parser in `internal/ops/flags`, and the config task handler in `internal/ops/configtask`.

## `commands.go` (`ops/cli`)

### Globals

`commandSets` map with `"cli"` key (CLI commands) and `"server"` key (edub commands: `version`). CLI set contains: `version`, `consume`, `search`, `hugot`, `task`, `user`, `setup`, `enrich`, `thumbnails`, `queue`, `storage`, `backup`, `mirror`, `config`, `llm`, `restore`, `migrate`, `tagmeasure`, `tagreconcile`, `tagidentity`, `tagaudit`. The `consume`, `queue`, and `setup` entries delegate to `consume.Run`, `queue.Run`, and `setup.RunSetup` respectively.

> **Note**: The `edub` binary has its own standalone runner in `cmd/edub/runner.go` that only handles the `version` command (server mode is the default when no command matches).

### Structs

- `Command` — `Name`, `Description`, `Handler func(c *container.Container, args []string) error`
- `CommandRunner` — `container *container.Container`, `commands map[string]Command`
  - **Methods**: `NewCommandRunner(c, set string) *CommandRunner`, `ExecuteCommand(name, args) error`

### Functions

- `PrintUsage()` — Prints CLI usage, calls `os.Exit(1)`
- `versionHandler(c, args) error` — Prints `"Document Management System v{version}"`

---

## `consume.go` (`ops/consume`)

### Globals

- `ErrBatchPaused` — Sentinel error returned by `pollBatch` when the runner has paused the batch mid-flight due to an LLM provider credit/balance error. The caller suppresses it (`err = nil`) so paused is not treated as a failure.

### Functions

- `Run(c, args) error` — Enqueues files in paired (consume + enrich) tasks; direct-fallback if queue empty, `--batch <id>` for batch resume, `--force` to acquire the batch ownership directly (overriding the queue daemon's placeholder owner row when resuming a forked batch instead of failing the plain acquire first). Subcommand `cancel <batch-id>` cancels a running batch. If paused batches exist (from unresolved LLM credit/balance errors), `kushim consume` (without `--batch`) refuses with an error listing the paused batch IDs. After `pollBatch` returns, if the batch completed normally it calls `setBatchTerminalStatus`; if the batch was paused (`ErrBatchPaused`) it suppresses the error so the caller returns cleanly.
- `IsAlive(pid) bool` — Signal-0 liveness check for a batch owner PID, shared with the queue daemon's stale-owner reclamation.
- `consumeCancelHandler(c, args) error` — Cancels pending + processing tasks via DB, reads batch owner PID from batch_owner table, sends SIGTERM.
- `pollBatch(ctx, run batchRun, logger) error` — Starts the batch's pools and streams per-file progress to stdout; returns `nil` when all tasks finish, `ErrBatchPaused` when the runner pauses the batch, or `ctx.Err()` on cancellation. A batch carrying `thumbbackfill` tasks runs only on the backfill pool; every other batch runs consume, enrich and thumbnail.
- `setBatchTerminalStatus(ctx, queries, batchSvc, batchID) error` — Computes a terminal status (`completed` or `failed`) from task outcomes. Has a defense-in-depth guard: if the batch is already in a terminal state (`completed`, `failed`, `cancelled`, `paused`), it returns `nil` without modifying the batch.
- `taskDisplayInfo(t) taskDisplay` — Extracts index, filename, task type from a payload via `task.ReadPayload`.
- `totalFiles(tasks) int` — Counts total files from max file_index in payloads.
- `fetchPools(c) (poolSet, error)` — Builds the consume, enrich, thumbnail and thumbbackfill pools a batch runs through.
- `runBatchThroughPools(ctx, cancel, c, run batchRun) error` — The post-acquire sequence both halves of `Run` share: sets the runner's owner id, watches signals, starts the batch heartbeat, drives `pollBatch`, sets the terminal status, triggers the orphan scan, and releases the ownership row with a 5s timeout.

### Structs

- `poolSet` — the four `*task.Pool`s of a batch (`consume`, `enrich`, `thumbnail`, `thumbbackfill`); `forThumbbackfill(thumbbackfill bool) []*task.Pool` selects the backfill pool alone for `thumbbackfill` batches and consume+enrich+thumbnail otherwise.
- `batchRun` — per-batch driver state: `client`, `owner`, `ownerID`, `batchID`, `thumbbackfill`, `pools`.

---

## `advisories.go` (`ops/consume`)

### Functions

- `printToolBlock(missing)` — Prints the missing-required-tools block with per-system install hints and the built-in-engine hint.
- `printOcrmypdfAdvisory(cfg, allTools)` — Prints the ocrmypdf note (system tesseract reads its own tessdata dir) with the configured languages, the per-language tesseract pack commands and the optional-companion install hints.

---

## `container.go` (`ops/container`)

### Struct

- `Container` — `cfg` (atomic.Pointer), `logger`, `db`, `client`, `engine`, `catalog`, `enqueuer`, `runner`, `store`, `services`, `matcherClient`, `tagToolsRunner`, and `pools map[types.TaskType]*task.Pool`
  - **Methods**:
    - `NewContainer(cfg, logger)` — No DB
    - `NewContainerWithDB(cfg, logger, db)` — With provided DB
    - `GetConfig() *config.Config` / `SetConfig(cfg)` — Atomic load/store of the live config (the queue daemon swaps it on hot reload)
    - `GetLogger() *log.Logger`
    - `LogFileConfig(name) log.LogFileConfig` — File-log config for `<config_dir>/logs/<name>` with the configured rotation settings
    - `GetTagService() (*curation.Tag, error)` — Ensures the task wiring (and its services) is initialized and returns the tag service the `tagidentity` verb resolves pending identity evidence through
    - `GetDB() (*sql.DB, error)` — Creates DB lazily, runs goose migrations on first open
    - `GetClient() (*database.Client, error)` — Lazily wraps the DB
    - `InvalidateDB()` — Closes the DB and drops every cached dependency (client, catalog, enqueuer, runner, engine, store, services, tag services, matcher client, tag-tools runner, pools) so the next accessor rebuilds from the current configuration
    - `ReconnectClient() (*database.Client, error)` — Opens a new DB from the current config, invalidates, and returns the new client
    - `SocketPath() string` — `<config_dir>/kushim-hugot.sock`
    - `GetMatcherClient() *matcher.MatcherClient` — Lazily builds the RPC client over the socket, capping request bodies at the configured reduce-target size
    - `GetTagToolsRunner() *tools.Runner` — Lazily builds the `textreducer`, `contentanalyzer` and `tagmatcher` tools over the matcher client
    - `GetCatalog() (*Catalog, error)` — Lazily builds the task type catalog together with the runner, store and tag services, so `GetCatalog`, `GetEnqueuer`, `GetRunner` and `GetTagService` work in any call order. Builds the curation tag service (`Tag` wired through the matcher client) and `Enricher` with the matcher.
    - `GetEnqueuer() (*task.Enqueuer, error)` — Lazily builds the transactional enqueue over the catalog.
    - `GetRunner() (*task.Runner, error)` — Builds the runner over the store and registers every task handler (consume, enrich, thumbnail, thumbbackfill, config, migrate-db, migrate-storage, backup, mirror, tagreconcile, tagmeasure, tagidentity, tagaudit)
    - `SetRunnerOwnerID(id) error` — Per-process batch owner UUID
    - `GetPool(taskType) (*task.Pool, error)` — Returns (or lazily creates) the pool for a task type from the `poolSpecs` table (worker count and poll interval per type)
    - `GetSearchEngine() (*search.Engine, error)`
    - `Close()` — Stops every built pool, closes the embedder, the services, and the DB

---

## `hugot.go`

### Function

- `serveHugotHandler(c, args) error` — Starts the matcher RPC server over a Unix domain socket. Accepts `--bg` (re-executes without `--bg` and returns) and optional `--socket <path>` (default `<config_dir>/kushim-hugot.sock`). Creates a Hugot embedding session, builds the tag cache from the database, exposes HTTP endpoints:

| Endpoint                         | Method | Description                                                                |
| -------------------------------- | ------ | -------------------------------------------------------------------------- |
| `POST /rpc/v1/encode`            | POST   | Accepts `{"texts": [...]}`, returns `{"embeddings": [[...], ...]}`         |
| `POST /rpc/v1/match`             | POST   | Accepts `{"doc_id", "input"}`, returns `{"matches": []}` |
| `POST /rpc/v1/rank`              | POST   | Accepts `{"doc_id", "queries"}`, returns `{"results": [{"candidates": [{"tag", "similarity"}]}]}` |
| `POST /rpc/v1/add-to-store`      | POST   | Accepts `{"names": [...]}`, encodes and adds to embedding store            |
| `POST /rpc/v1/remove-from-store` | POST   | Accepts `{"names": [...]}`, removes from embedding store                   |
| `GET /health`                    | GET    | Returns `{"ok": true}`                                                     |

Listens on a Unix socket (cleaned up on shutdown). Handles SIGTERM/SIGINT for graceful shutdown.

---

## `flags.go` (`ops/flags`)

### Struct

- `FlagParser` — `args []string`, `used map[int]bool`, `rest []string`
  - **Methods**: `NewFlagParser(args)`, `Help(helpText) bool`, `String(flag, *dst) error`, `Int(flag, *dst, min, max) error`, `Bool(flag, *dst) error`, `Rest() []string`

---

## `enrich.go`

### Functions

- `enrichHandler(c, args) error` — Flag-aware entry point. A positional document id routes to `enrichDocument`; `--limit N`, `--all`, or no arguments route to `enrichMany` with the effective limit (default `consumer.max_files_per_batch`, `--all` uncapped). `--limit` and `--all` are mutually exclusive and cannot be combined with a document id; `--limit` must be a positive integer.
- `enrichDocument(c, documentID) error` — Single-document path: creates a queued batch (source `"reenrich"`, status `"queued"`) with one pending `"enrich"` task (dedup key `"enrich:doc:<uuid>"`). Returns the batch ID. Prints document-not-found and already-queued errors.
- `enrichMany(c, limit) error` — Bulk path: calls `ReEnrich.ReEnrichMany` (limit <= 0 selects every live document) and prints the batch id with the enqueued count, skipped count, and the `kushim queue` hint.
- `newReEnrichService(c) (*service.ReEnrich, error)` — Builds the `ReEnrich` service from the container's client, task store, and batch service.

---

## `search.go`

### Functions

- `searchHandler(c, args) error` — Parses flags (`--limit`, `--offset`, `--rebuild-index`), runs search, formats results with ANSI highlighting and sizes via `log.FormatBytes`
- `highlightSnippet(s) string` — Replaces `<b>`/`</b>` with ANSI color codes
- `rebuildIndex(c) error` — Delegates to `search.Engine.Reindex` (`REINDEX INDEX CONCURRENTLY idx_document_tsv`)

---

## `setup.go` (`ops/setup`)

### Functions

- `RunSetup(args, logger) error` — Dispatches to either `runSetupWizard` (default) or `runSetupCLI` (when `--cli` flag is set). Called both by `cmd/kushim/main.go` (before any config is loaded) and by the `setup` verb-table entry.
- `runSetupWizard(args, logger) error` — Starts a standalone HTTP wizard server on `0.0.0.0:8420` serving the embedded SvelteKit wizard SPA. The server bootstraps config on first PUT request.
- `runSetupCLI(args, logger) error` — Accepts `--languages` through `--consumer-pdfoptimizer-fallback` (same as wizard), plus `--db-dsn`, `--admin-user`, `--admin-password`, `--reset-database`. `--db-dsn` persists `database.dsn` (overrides the individual `database.*` fields) and the DB connection uses it. Generates and persists a session secret. Writes config.yaml through Viper, reloads it, creates the directories, opens the database once and initializes the schema (or resets it with `--reset-database`), then downloads Tesseract language data and the Hugot model (`BAAI/bge-m3`). Prompts interactively for admin username/password when omitted. Creates the admin user via `service.User.Create()`. After setup completes, generates systemd service files to `<configDir>/systemd/` via `config.GenerateServiceFiles` and prints copy/install instructions.

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

- `storageHandler(c, args) error` — Entry point for `kushim storage`. Routes subcommands: `orphans` and `thumbnails`.
- `newOrphanedService(c) (*service.Orphaned, error)` — Builds the orphaned-file service from the container: database client, task store, and batch service.
- `orphansHandler(c, args) error` — Manages orphaned files. Builds the `service.Orphaned` service via `newOrphanedService(c)` and dispatches to:
  - `orphansList(svc, args)` — Lists pending orphaned files with ID, key, source dir, size, status
  - `orphansScan(svc, args)` — Runs detection + quarantine
  - `parseAndRun(svc, args, action)` — Parses `--id` flag and runs Delete/Restore
  - `parseAndRunOrAll(svc, args, single, bulk)` — Parses `--id` or `--all` and runs MoveToInbox
  - `orphansBulk(svc, args, label, action)` — Runs bulk action (DeleteAll, MoveAllToInbox)
- `thumbnailsHandler(c, args) error` — Runs `kushim storage thumbnails cleanup [--dry-run]`. Builds the `service.Orphaned` service via `newOrphanedService(c)` and calls `CleanupOrphanedThumbnails`, printing each orphaned path and a summary (`--dry-run` lists without removing).

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
kushim storage thumbnails cleanup [--dry-run]
```

---

## `queue.go` (`ops/queue`)

The daemon's Postgres `LISTEN` goroutine lives in the same package (`notify.go`).

### Functions

- `Run(c, args) error` — Starts the batch queue daemon for background consumption and inbox polling. Accepts `--bg` to daemonize (re-execs without `--bg` and returns). In daemon mode: checks PID file for single-instance, sets up signal handling (SIGTERM/SIGINT → graceful shutdown with PID file cleanup), opens log file at `<config_dir>/logs/queue.log`, spawns a polling goroutine, and runs three concurrent scheduling paths:
  - **Notification-driven consumption** — A dedicated `LISTEN` goroutine connects to Postgres via a separate pgxpool (MinConns=1, MaxConns=1) and listens on the `batch_queued` channel. When a batch transitions to `status='queued'`, a database trigger sends a `pg_notify` that wakes the goroutine, which forwards the signal over a buffered channel (capacity 4). The daemon picks the batch up in milliseconds, calls `consumeNextQueuedBatch` which forks `kushim consume --batch <id> --force`.
  - **Safety timer** — A 30-second timer guarantees consumption runs at least once every 30s, even if a notification is dropped (connection blip, reconnection). Resets on each notification-driven consumption.
  - **Housekeeping ticker** — A 5-second ticker that drives the config reload/reconnect, the per-task pool ensures and schedules, the empty-batch sweep, and stale reclamation. Runs:
    - **Stale batch reclamation** — Lists batch owners with stale heartbeats (>15s) and active tasks, signals SIGTERM to the owner PID (if the process is still alive), quarantines tasks at or above `consumer.reclaim.max_retries` (default 3) to `failed`, resets remaining processing→pending with an incremented attempt counter, re-queues the batch, deletes the stale owner row. Gated by `consumer.reclaim.enabled` (default `true`); when `false`, stale batches remain in `processing` state and are not resumed.
    - **Stale task reclamation** — An age-based sweep (`reclaimStaleTasks`) that resets individual `processing` tasks back to `pending` (or `failed` if at max retries) when their `started_at` exceeds `consumer.reclaim.stale_task_after` (default 600s). Blocking-class tasks (backup, mirror, tagreconcile, thumbbackfill, tagmeasure, tagidentity, tagaudit) are swept only when the maintenance lock is not held fresh by their own claim (30-minute freshness window, 5-minute heartbeat), so a live holder is never reset nor robbed of its lock. Gated by `consumer.reclaim.enabled` and rate-limited to at most once per `max(60s, stale_task_after/10)` so the sweep does not run on every 5s tick.
    - **Empty queued batch sweep** — Removed: the transactional enqueue can no longer commit a batch without tasks, so the zombie sweep has nothing left to reap.
- `reclaimStaleBatches(ctx, cfg, client, batchSvc, logger) error` — Iterates stale batch owners, signals SIGTERM to the owner PID (if alive), quarantines tasks at or above `consumer.reclaim.max_retries` to `failed`, resets remaining processing→pending with incremented attempt counter, re-queues, deletes owner. Only called from `daemon.reclaimStale` when `consumer.reclaim.enabled` is `true`.
- `reclaimStaleTasks(ctx, batchSvc, cfg, logger) error` — Age-based sweep for individual tasks stuck in `processing` beyond `consumer.reclaim.stale_task_after`. Delegates to `service.Batch.ResetStaleProcessingTasks` which quarantines (attempts ≥ maxRetries → `failed`) and resets (attempts < maxRetries → `pending` + increment) in a single transaction; blocking-class tasks go through ownership-gated variants keyed on the maintenance lock (token match + freshness window). Logs the count of reclaimed tasks.
- `consumeNextQueuedBatch(ctx, client, batchSvc, maxConcurrent, logger) error` — Picks and dispatches the next queued batch.
- `daemon.runPollingLoop(ctx, maxConcurrent)` — Goroutine that runs on its own dynamic ticker (configured by `consumer.polling.interval`, minimum 1 minute). Reads config from `c.GetConfig()` (atomically loaded by the queue daemon's 5-second housekeeping ticker, so config changes propagate from disk within ~5 seconds without a restart) and the current database client from the daemon's atomic pointer, rebuilding its batch service each iteration so `consumer.reclaim.max_retries` reloads take effect. Checks capacity (`CountQueuedConsumeBatches + CountLiveBatches < MaxConcurrentBatches`) and missing external tools (recomputed only when the config pointer changes) before calling `consumption.ScanAndEnqueue`.
- `pollingTick(ctx, c, client, batchSvc, maxConcurrent, missingTools)` — Single polling iteration: capacity check, missing tools check, then delegates to `consumption.ScanAndEnqueue` to scan the inbox, deduplicate, and enqueue a `queued` batch with one consume task and its waiting children per new file.
- `maybeScheduleBackup(ctx, c, cfg) error` — Iterates `cfg.Backup.Schedules`, deferring while a backup task is active (`CountActiveBackupTasks`) and calling `backup.IsBackupDue` per schedule (per-mode 5-minute cooldown + per-mode last-completed derivation). Each due schedule runs the `backup` definition's builder with a `BackupBuildInput` capturing the schedule's `mode`, `path`, and `keep` at enqueue time, and enqueues the task set through `EnqueueBatch` with origin `schedule`. The enable and maintenance-lock gates live in `ensureBackup`.
- `maybeScheduleMirror(ctx, c, cfg) error` — Defers while a mirror task is active (`CountActiveMirrorTasks`) and calls `backup.IsMirrorDue` (5-minute cooldown + last-completed derivation, same anchor as backups). When due it runs the `mirror` definition's builder with a `MirrorBuildInput` capturing the config's `path` and `interval` at enqueue time, and enqueues through `EnqueueBatch` with origin `schedule`. The enable and maintenance-lock gates live in `ensureMirror`.
- `maybeScheduleTask(ctx, c, taskType, scheduleName, active, due, input) error` — Shared scheduling core used by both `maybeScheduleBackup` and `maybeScheduleMirror`: active-count check, due check, then builder + `EnqueueBatch`.
- `maybeScheduleAnchored(ctx, c, client, cfg, state, spec) error` — The parameterized state-anchored scheduler behind thumbnail backfill and the tag reconcile, tag measure, tag identity and tag audit passes: seeds `lastRun` from the spec's last-batch query (virtual anchor at the next occurrence of the preferred time when none exists), computes the next due from `scheduleState`, defers while the spec's active count is non-zero or (for lock-gated specs) while the maintenance lock is held, then runs the spec's execute.
- `executeThumbnailBackfill(ctx, c, client, state, timing, next) error` — Runs the `thumbbackfill` definition's builder (the all-documents scan) and enqueues the resulting batch with origin `schedule` and schedule name `thumbnail-backfill`, advancing the anchor only after a successful enqueue so a failed run retries next tick.
- `executeTagReconcile(ctx, c, client, state, timing, next) error` / `executeTagMeasure(...)` / `executeTagIdentity(...)` — Enqueue one task of the type in its own batch through `enqueueScheduled`.
- `enqueueScheduled(ctx, c, state, timing, taskType, scheduleName, name) error` — Runs the type definition's builder and enqueues it via `enqueueScheduledBatch`, advancing the anchor only after a successful enqueue and logging the schedule.
- `enqueueScheduledBatch(ctx, c, taskType, scheduleName, input) (task.EnqueueResult, error)` — Builds the task set through the task type catalog and enqueues it with origin `schedule`; shared by `maybeScheduleTask` and the anchored executes.
- `startPool(ctx, c, taskType, state *poolState) error` / `stopPool(state *poolState)` — Start/stop one task type's pool through its `poolState`, so the tick can start it lazily on enable and stop it on disable/reconnect/shutdown.

### Structs

- `daemon` — the daemon's runtime state: `c`, `client` (atomic.Pointer, republished on reconnect), `batchSvc`, the notification listener (`notifyCancel`, `notifyCh`), the six pool states (`backupPool`, `mirrorPool`, `tagReconcilePool`, `tagMeasurePool`, `tagIdentityPool`, `tagAuditPool`), the five schedule states (`thumbnailBackfill`, `tagReconcile`, `tagMeasure`, `tagIdentity`, `tagAudit`), and `lastStaleTaskClaim`.
  - **Methods**: `tick(ctx)` (one housekeeping sweep: config reload + reconnect, the per-task ensure calls, stale reclamation), `reloadConfig(ctx)` (hot-reloads config; on a connection change it reconnects, republishes the client, and restarts the pools and the LISTEN listener), `ensureBackup` / `ensureMirror` / `ensureTagReconcile` / `ensureTagMeasure` / `ensureTagIdentity` / `ensureTagAudit` (start or stop the type's pool from the enabled gate and run its schedule — the four tag pools always run, so only their schedules obey the enabled gate), `reclaimStale(ctx, cfg)`, `startNotify(ctx)` / `stopNotify()`, `stopPools()`, `runPollingLoop(ctx, maxConcurrent)`.
- `poolState` — `pool *task.Pool`, `started bool`; one per daemon-owned task type.
- `scheduleState` — `lastRun`, `runTiming`, `pendingRephase`; `nextDue(timing)` returns the steady interval anchor when the timing is unchanged and the closest upcoming occurrence when it changed (setting `pendingRephase`), and `markRun(timing)` advances the anchor after a successful run.
- `anchoredTaskSpec` — one state-anchored scheduled task: `name`, `enabled(cfg)`, `lastBatch(ctx, queries)`, `timing(cfg)`, `active(ctx, queries)`, `lockGated`, `execute(...)`. The five instances are `thumbnailBackfillSpec`, `tagReconcileSpec`, `tagMeasureSpec`, `tagIdentitySpec` and `tagAuditSpec`; the tag identity spec pins its own cadence instead of reading config.

The daemon also starts a **backup pool** (1 worker, 60s poll interval) when `backup.enabled` becomes `true` (checked lazily on each housekeeping tick, so enabling backup at runtime via config reload creates the pool without a restart). When backup is disabled at runtime via config reload, the pool is stopped and cleaned up on the same 5s ticker. The pool executes scheduled backup tasks via `BackupTaskHandler` (which reads config via a getter closure for the backup root fallback; the mode/path/keep come from the task payload, validated against the configured backup roots). The ticker and polling loop check the DB-backed maintenance-lock row of `system_signal` via `IsMaintenanceLocked` — backup scheduling and polling are skipped while a backup is in progress, while stale reclamation continues to run unconditionally.

The daemon likewise starts a **mirror pool** (1 worker, 60s poll interval) when `mirror.enabled` becomes `true` (same lazy start/stop semantics as the backup pool). It executes scheduled mirror tasks via `MirrorTaskHandler`, which runs with the maintenance lock already held (acquired at claim time, in the claim transaction), drains in-flight tasks, runs `rsync -a --delete --info=stats2 --timeout=600` over the storage tree, and writes a `.edub-mirror.json` state file into the destination. A 5-minute heartbeat (`TouchMaintenanceLockForTask`, token-guarded) refreshes the lock while rsync runs so long mirrors never trip the 30-minute staleness window.

The **tagmeasure pool** (1 worker, 5s poll interval, always running) executes measurement tasks via `TagMeasureTaskHandler`, which drains in-flight tasks, heartbeats the lock, and forks `kushim tagmeasure --run <batch-id>`, capturing the child's printed report line as the task result. The pool always claims, so a manual `kushim tagmeasure` enqueue runs even with the schedule off; `enricher.tag_measure.enabled` gates only automatic enqueuing through the anchored scheduler (tag-measure spec, anchored on the last completed `tagmeasure` batch, dedup key `tagmeasure`).

The **tagreconcile pool** (1 worker, 60s poll interval, always running) executes reconciliation tasks via `TagReconcileTaskHandler`, which drains in-flight tasks, heartbeats the lock, and forks `kushim tagreconcile --run <batch-id>`, capturing the child's `tagreconcile-report: <json>` line as the task result. The pool runs unconditionally so a manual `kushim tagreconcile` enqueue is always claimed; `enricher.tag_reconcile.enabled` gates only automatic enqueuing through the same anchored scheduler (tag-reconcile spec, anchored on the last completed `tagreconcile` batch, dedup key `tagreconcile`).

The **tagidentity pool** (1 worker, 5s poll interval, always running) executes identity-adjudication batches via `TagIdentityTaskHandler`, which drains in-flight tasks, heartbeats the lock, and forks `kushim tagidentity --run <batch-id>`, capturing the child's `tagidentity-report: <json>` line (pending/resolved/folded/aliased/deferred/failed) as the task result. Runs come from the review surface's reevaluate action, a manual `kushim tagidentity`, and the pass's own anchored schedule — a fixed daily 03:30 pass with no config keys that fires only while `enricher.tag_adjudicator.enabled` is set; batches are deduplicated while a run is active (dedup key `tagidentity`), and a run with the adjudicator off only counts the backlog.

The **tagaudit pool** (1 worker, 5s poll interval, always running) executes coverage-audit tasks via `TagAuditTaskHandler`, which drains in-flight tasks, heartbeats the lock, and forks `kushim tagaudit --run <batch-id>`, capturing the child's `tagaudit-report: <json>` line (sweep, sampled/flagged counts, coverage tallies by class, fills/flags/proposals, judge call counts, pending coverage; `zero_work` when the unchanged store skipped the derivation and the draw was empty) as the task result. Runs come from the anchored scheduler (tag-audit spec, anchored on the last completed `tagaudit` batch, dedup key `tagaudit`), a manual `kushim tagaudit`, and `POST /api/v1/tags/audit/reevaluate`; the pool always claims, so only the schedule obeys `enricher.tag_audit.enabled`.

---

## `config.go`

### Functions

- `configHandler(c, args) error` — Entry point for `kushim config`. Parses `--unset`, `--validate`, `--path`, `--help` flags, then dispatches on positional arg count: 0 → `dumpAllConfig`, 1 → `getConfigValue`, 2+ → `setConfigValue`.
- `validateConfig(configDir) error` — Calls `config.Load` and prints `config.yaml is valid` on success.
- `loadConfigFile(configDir) (*viper.Viper, bool, error)` — Loads config.yaml into a Viper instance; the bool reports whether the file exists (a missing file is not an error).
- `dumpAllConfig(configDir) error` — Reads config via Viper and prints all keys as `key = value` lines (``git config --list`` style). Scalars print raw, arrays join with `, `.
- `getConfigValue(configDir, key) error` — Reads a single dot-notation key via Viper and prints its value. Scalars print raw, arrays/maps print as YAML.
- `setConfigValue(configDir, key, rawValue) error` — Parses the value via `parseValue`, calls `atomicSetConfig`, prints `key = value` confirmation.
- `unsetConfigValue(configDir, key) error` — Reads YAML into a map, deletes the nested key via `deleteNestedKey`, writes back through `atomicWriteConfig`.
- `deleteNestedKey(m map[string]any, key string) bool` — Recursively walks a nested map using dot-notation segments and deletes the leaf. Returns `false` if the key path does not exist.
- `atomicSetConfig(configDir, body map[string]any) error` — Viper read-modify-write with atomic safety: sets the body keys on the loaded config and writes through `atomicWriteConfig`. Writes config with `0600` permissions (owner read/write only).
- `atomicWriteConfig(configDir, write func(tmpPath string) error) error` — Temp-write-validate-rename pipeline shared by `atomicSetConfig` and `unsetConfigValue`: writes through the callback into a temp directory inside the config dir, validates with `config.Load(tmpDir)` (including `finalizeConfig` business rules), then renames the file into place; a write or validation failure leaves the original untouched.
- `parseValue(raw string) any` — Auto-detects value type: `true`/`false` → `bool`, parseable integer → `int`, parseable float → `float64`, contains comma → `[]string` (trimmed), otherwise `string`.
- `printValue(val any)` — Prints a value matching CLI output specs: scalars raw, arrays/maps via `yaml.Marshal`.

---

## `llm.go`

### Functions

- `llmHandler(c, args) error` — Entry point for `kushim llm`, mirroring `configHandler`'s grammar over the catalog key space: parses `--unset` (String) plus `--reset`/`--providers`/`--models`/`--cascade` (Bool), then dispatches on positional count: 0 → `dumpCatalog`, 1 → `getCatalogValue`, 2+ → `setCatalogValue` (value = `strings.Join(rest[1:], " ")`). `--reset` routes to `llm.ResetCatalogFiles` (works even when `NewRegistry` fails — the mangled-catalog recovery path), `--unset` removes a whole entry through `RemoveProvider`/`RemoveModel` (the registry applies the in-use and orphan-model guards; `--cascade` also drops the provider's models); `--providers`/`--models` require `--reset` and `--cascade` requires `--unset`.
- `parseCatalogKey(key) (catalogKey, error)` — Parses `providers.<name>[.<field>]` and `models.<provider>/<model>[.<field>]`; the field is split off by known-name suffix (`trailingField`), so model ids containing dots stay addressable.
- `catalogKey` — `kind` (`providers`/`models`), `name`, `modelID`, `field`; an empty field selects the whole entry.
- `dumpCatalog(c) error` — Prints every catalog entry value as sorted `key = value` lines (`dumpValue` renders scalars and effort lists).
- `getCatalogValue(c, key) error` — Prints a field's value via `printValue` (a whole entry as YAML via `providerMap`/`modelMap`).
- `setCatalogValue(c, key, rawValue) error` — A field set applies through `applyField` onto the entry found by `findProviderInput`/`findModelInput`, then `SetProvider`/`SetModel`; a missing model is created with defaults plus the field (`AddModel`), a missing provider is refused (its required fields need the whole-entry form). A whole-entry set decodes a JSON object (`setProviderEntry`/`setModelEntry`, keys forced from the key path) into `AddProvider`/`SetProvider`/`AddModel`/`SetModel`. Prints `key = value`.
- `unsetCatalogValue(c, key, cascade) error` — Removes the entry and prints `unset <key>`; a field key is refused (clear the field by setting it to `""`).
- `fieldSpec[T]` — One table per catalog (`providerFieldSpecs`/`modelFieldSpecs`) binding each settable field name to its getter and setter (`textSetter`/`intSetter`/`boolSetter` over `parseValue`-coerced scalars, `listStrings` for `reasoning_efforts`); the key vocabulary (`providerFields`/`modelFields` via `fieldNames`), the entry maps, the dump and the value accessors all derive from it.

---

## `backup.go`

### Functions

- `backupHandler(c, args) error` — `kushim backup [--path <dir>] [--mode <full|database|documents>]` — Runs a synchronous backup: checks preconditions (`IsMaintenanceLocked`, `CountProcessingTasks`, polling status), acquires `AcquireBackupLock`, creates a `tar.gz` archive per the mode (`full` = DB dump + config + storage; `database` = DB dump + config only; `documents` = config + storage only), prints the result, releases the lock. Manual backups never apply retention.
- `restoreHandler(c, args) error` — `kushim restore <backup-file.tar.gz> [--force] [--dry-run] [--temp-dir <dir>]` — Validates the archive, checks restore tooling per `database.runtime` (`host` requires `psql` on PATH; `docker`/`podman` require the runtime binary and `database.container`; `remote` refuses with manual-restore guidance; skipped for `documents`-mode archives and `--dry-run`), checks preconditions, acquires `AcquireBackupLock`, checks the running daemon's PID file, prompts for confirmation (unless `--force`), runs `checkRestoreDiskSpace` (refuses corrupt/hand-made manifests with zero mode-relevant sizes and refuses when the staging parent — or the storage parent for cross-device staging — lacks ~1.05× the required size), creates the extraction staging dir next to `storage.storage_dir` (or under `--temp-dir`), restores per the manifest mode (`full`: SQL dump via `psql` + storage swap + path rewrite; `documents`: storage swap only; `database`: SQL dump via `psql` + path rewrite only; legacy manifests without `mode` restore as `full`), saves the archived config as `config.yaml.restored`, releases the lock. `ReplaceFiles` renames the extracted `storage/` onto `storage_dir` when both are on the same device, falling back to a `storage-swap-*` copy (completed before the swap) for cross-device staging.

---

## `mirror.go`

### Functions

- `mirrorHandler(c, args) error` — `kushim mirror [--path <dest>]` — Runs a synchronous mirror of the storage tree to a local path or rsync remote target (`[user@]host:path`). Unlike the backup command, it **waits** for the maintenance lock through `database.AcquireMaintenanceLock` (5s polls, logging each wait) instead of refusing, then drains in-flight tasks and runs `rsync -a --delete --info=stats2 --timeout=600 <storage>/ <dest>` (process-group-killed on Ctrl-C so the spawned `ssh` for remote targets is reaped too). Writes a `.edub-mirror.json` diagnostics file (timestamp, file count, bytes, app version) into the destination and prints the stats: total regular files (thousands-separated) plus the per-run breakdown parsed from rsync stats2 (`N transferred: N created, N updated, N deleted` — `updated` derived as transferred minus created, clamped at 0). Requires `rsync` on PATH; remote targets need passwordless ssh. Destination guards (symlink-resolved) refuse paths inside `storage_dir` or the backup directory, paths containing storage, and dash-prefixed destinations.

---

## `tagidentity.go`

### Functions

- `tagIdentityHandler(c, args) error` — `kushim tagidentity [--run <batch-id>]`, both modes routed through the queue. Without flags it enqueues a blocking task through `curation.TagIdentity.Enqueue` — one `queued` `tagidentity` batch with one `pending` task (dedup key `tagidentity`) — and prints the batch id with the `kushim queue` hint whether or not the daemon is running; a run already queued or processing is reported instead. With `--run <batch-id>` it is the queued run's execution mode: it validates the batch id and runs the pass, which takes the container's shared tag-tools runner (`GetTagToolsRunner`: `textreducer`, `contentanalyzer` and `tagmatcher` over the matcher client) and resolves the pending identity evidence through `admission.IdentityResolver` (criteria judged, same-intent pairs adjudicated, dispositions applied) under `enricher.tag_reconcile.max_rejudge`, printing the report line the queue handler captures. The pass takes no lock and drains nothing — the claiming parent holds the tokenized lock and already drained.
- `enqueueTagIdentity(c) error` — the no-flag path: builds the batch service over the container client and calls `curation.TagIdentity.Enqueue`.
- `runTagIdentityPass(c, batchID) error` — the `--run` path: builds `admission.NewIdentityResolver`, runs one pass, and prints the `tagidentity-report: <json>` line.

---

## `tagreconcile.go`

### Functions

- `tagReconcileHandler(c, args) error` — `kushim tagreconcile [--run <batch-id>]`, the manual trigger for the scheduled reconciliation. Without flags it enqueues a blocking task through `curation.TagReconcile.Enqueue` — one `queued` `tagreconcile` batch with one `pending` task (dedup key `tagreconcile`) — and prints the batch id with the `kushim queue` hint whether or not the daemon is running; a run already queued or processing is reported instead. With `--run <batch-id>` it is the queued run's execution mode: it validates the batch id, builds `reconcile.NewReconciler` over the container's client, tag service and tag-tools runner, runs one pass, and prints the report as the `tagreconcile-report: <json>` line the handler captures. The pass takes no lock and drains nothing — the claiming parent holds the tokenized lock and already drained. It opens on its store-changed gate: with the flag clear, a completed run behind it, and every work probe empty (the four pair re-check categories, the twins worklist, the stored-compound scan, the orphan and provisional grace sweeps, promoted-tag demotion, and the bootstrap log ingest), it prints a zero-work report (`zero_work: true`) without the store-wide reads; a failed run re-arms the flag.
- `enqueueTagReconcile(c) error` — the no-flag path: builds the batch service over the container client and calls `curation.TagReconcile.Enqueue`.
- `runTagReconcilePass(c, batchID) error` — the `--run` path: builds the reconciler, runs one pass, and prints the `tagreconcile-report: <json>` line.

---

## `tagmeasure.go`

### Functions

- `tagMeasureHandler(c, args) error` — `kushim tagmeasure [--run <batch-id>]`. Without flags it enqueues a blocking task and prints its batch id through `enqueueTagMeasure`. With `--run <batch-id>` (validated) it is the queued run's fork mode: no lock and no drain (the claiming parent holds the tokenized lock and already drained), and it logs the run's opening line (`tag measure run starting: N tags in store`), runs the pass (one REPEATABLE READ transaction over `internal/tags`, appending the evidence records with a fresh `run_id`) and prints the report line. The pass opens by checking its store-changed flag (`tag_measure_changed` in `system_signal`): with the flag clear and a completed run under the current rule set it prints a zero-work report (`zero_work: true`, no records) and writes nothing, leaving the previous run's readings and worklists standing; otherwise its opening transaction clears the flag and it sweeps, re-arming the flag if the sweep fails.
- `enqueueTagMeasure(c) error` — the no-flag path: builds the task store and batch service over the container client and calls `curation.TagMeasure.Enqueue`, printing the batch id with the `kushim queue` hint or the already-queued line.
- `runTagMeasurePass(c, batchID) error` — the `--run` path: builds the runner, runs one measurement pass, and prints the `tagmeasure-report: <json>` line.
- `printTagMeasureReport(c, report, batchID) error` — Marshals the run summary (per-operator counts, skip accounting, worklist sizes, versions) and prints it as the `tagmeasure-report: <json>` line the queue handler captures, with the human-readable summary going to the log.

---

## `tagaudit.go`

### Functions

- `tagAuditHandler(c, args) error` — `kushim tagaudit [--run <batch-id>]`. Without flags it enqueues a blocking task and prints its batch id through `enqueueTagAudit`. With `--run <batch-id>` (validated) it is the queued run's fork mode; it opens on its store-changed gate — a clear flag skips the store-wide pending-coverage derivation (the durable rows stand) while the pool-driven sweep still draws — and a failed run re-arms the flag.
- `enqueueTagAudit(c) error` — the no-flag path: builds the task store and batch service over the container client and calls `curation.TagAudit.Enqueue`, printing the batch id with the `kushim queue` hint or the already-queued line.
- `runTagAuditPass(c, batchID) error` — the `--run` path: it takes no lock (the claiming parent holds the tokenized lock and already drained), so the batch id only labels the run while the run id stays fresh. It builds `admission.NewAuditRunner` with the container's tag-tools runner, runs the coverage pass, and prints the report line the queue handler captures.

---

## See Also

- [Task System](task-system.md) — Enqueuer, CRUD, and pool used by CLI commands
- [Config & Utils](config-and-utils.md) — Configuration loading, logger
- [Pipeline](pipeline.md) — Consumption engine used by consume command

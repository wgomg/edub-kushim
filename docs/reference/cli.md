# CLI Commands (`internal/ops/`)

the process layer is split by role: the verb table and the `kushim` verbs live in `internal/ops/cli`, the queue daemon and its LISTEN/NOTIFY loop in `internal/ops/queue`, the batch dispatcher and launcher in `internal/ops/dispatch`, the per-batch child in `internal/ops/consume`, the setup flow in `internal/ops/setup`, the shared-services container in `internal/ops/container`, the shared flag parser in `internal/ops/flags`, and the migration task handlers in `internal/ops/configtask`.

## `commands.go` (`ops/cli`)

### Globals

`commandSets` map with `"cli"` key (CLI commands) and `"server"` key (edub commands: `version`). CLI set contains: `version`, `batch`, `consume`, `search`, `hugot`, `task`, `user`, `setup`, `enrich`, `thumbnails`, `queue`, `storage`, `backup`, `mirror`, `config`, `llm`, `restore`, `migrate`, `tagmeasure`, `tagreconcile`, `tagidentity`, `tagaudit`, `tagmaintenance`. The `consume`, `queue`, and `setup` entries delegate to `consume.Run`, `queue.Run`, and `setup.RunSetup` respectively, and `tagmaintenance` to `internal/ops/tagmaintenance.Run`.

> **Note**: The `edub` binary has its own standalone runner in `cmd/edub/runner.go` that only handles the `version` command (server mode is the default when no command matches).

### Structs

- `Command` — `Name`, `Description`, `Handler func(c *container.Container, args []string) error`
- `CommandRunner` — `container *container.Container`, `commands map[string]Command`
  - **Methods**: `NewCommandRunner(c, set string) *CommandRunner`, `ExecuteCommand(name, args) error`

### Functions

- `PrintUsage()` — Prints CLI usage, calls `os.Exit(1)`
- `versionHandler(c, args) error` — Prints `"Document Management System v{version}"`

---

## `batch.go` (`ops/cli`)

### Functions

- `batchHandler(c, args) error` — The `kushim batch` queue surface. Subcommands: `create <task> [args]` (enqueue; the owning producer builds the task set, the daemon processes), `run <batch-id>` (claim and run now, foreground), `stop`/`cancel <batch-id>` (cancel), `resume <batch-id>` (requeue, failed tasks stay failed), `restart <batch-id>` (requeue and retry the failed tasks), `list [--limit N] [--offset N]` (recent batches, newest first, via `service.Batch.ListOverviews`), `status <batch-id>` (one batch's state via `service.Batch.GetOverview`, with a hint to `kushim task list --batch`). Bare and `--help` print the help; an unknown subcommand is refused.
- `batchRunHandler(c, args) error` — Builds a static catalog and delegates to `dispatch.LaunchQueuedBatch` (which claims the batch and forks the task CLI, waiting for the child); argument and refusal errors surface as-is.
- `batchStopHandler(c, args) error` — Delegates to `task.CancelBatch`; the printed outcome mirrors `consumeCancelHandler`'s.
- `batchResumeHandler(c, args, includeFailed) error` — `resume` passes `includeFailed=false` and `restart` `true`; both delegate to `task.ResumeBatch` with `max_retries` from the config.
- `batchListHandler(c, args) error` / `batchStatusHandler(c, args) error` — The read-model surfaces; `--limit` (1–100, default 20) and `--offset` are validated and parse errors abort before any database access.
- `batchCreateHandler(c, args) error` — The enqueue path: each task name routes to its owning producer, which returns the ready task set the boundary enqueues with origin `cli` (the command builds one catalog and enqueuer at this boundary, carrying the dedup hooks of all nine types it creates; a pending type without a hook is refused by the enqueue instead of being created without deduplication). Supported tasks: `consume` (no arguments — the inbox scan via `consumption.IngestSpecsFromInbox`), `backup` (`backup.BackupSpecs`, flags `--mode`/`--path`/`--keep`), `mirror` (`backup.MirrorSpecs`, flag `--path`), `enrich` (explicit document ids via `enrichment.EnrichSpecs`, or `--all | --limit N` selection), `thumbnails` (bare all-documents form via `service.ThumbnailBackfillSpecs`, or `--from-batch <id> | --document <id>` selection), and the four tag verbs (their spec constructors); the `consume` and `enrich` creations also declare the measurement follow-up while `enricher.tag_measure.after_enrichment_batch` is set. The unknown task is refused, and the live-data selection flags parse, before any database access — `parseCreateSelection` validates the `enrich --all | --limit` and `thumbnails --from-batch | --document` flags without touching the database and defers the chosen enqueue until the boundary's enqueuer exists, routing it to `service.ReEnrich` or `service.ThumbnailBackfill`; the consume creation refuses while credit-paused batches exist (the same no-bypass rule the polling scan enforces), checked before the inbox scan.

---

## `consume.go` (`ops/consume`)

The `kushim consume` verb: the direct mode runs the inbox immediately, the batch mode executes one claimed batch through the shared execution library, and `cancel` cancels a running batch. The same file hosts the shared claimed-batch executor every task CLI's batch mode calls.

### Functions

- `Run(c, args) error` — `cancel <batch-id>` cancels a running batch; `--from-batch <id> --exec-token <token>` executes one claimed batch (the dispatcher and `kushim batch run` contract — the token is required, a bare selector errors pointing at `kushim batch run`); no flags means direct mode: scans the inbox and consumes its files immediately under the manual maintenance lock, creating no batch or task rows, with `--no-wait` exiting non-zero instead of waiting. Direct mode gates on the required external tools before running, reports per-file failures and continues, aborts the remaining run on a provider credit/balance error (`PauseBatch`-flagged), exits non-zero when anything failed, and triggers the orphan scan after a successful run. `--batch` fails with a pointer to `kushim batch run` / `kushim batch create consume`.
- `runBatchMode(c, batchID, execTokenParam) error` — The batch half of `Run`: the missing-tools gate (checked against the batch's task types), then `RunClaimedBatch` with the consume file-quarantine hook, the enrich bookend and the orphan scan on completion.
- `RunClaimedBatch(c, batchID, execTokenParam, store, registry, quarantine) (task.BatchRunResult, error)` — The shared claimed-batch executor (exported; `ops/cli` calls it for the `enrich` and `thumbnails` batch modes with a nil quarantine hook, over a store and registry the calling command assembled). Parses the execution token, takes the store and registry the calling command assembled (the handlers act on the same store the run does), assembles the `task.BatchRunDeps`, silences the logger, watches signals, streams the progress reporter and calls `task.RunBatch`; prints the paused pre-check hint and the mid-run pause notice, and warns on stdout when the run ends with the batch still processing (a settle refused with tasks remaining); returns the result (the caller maps cancel → non-zero, paused → zero).
- `directRun` / `newDirectRun(c, thumbnailsEnabled)` / `(r *directRun) process(ctx, files)` — The direct-mode driver: builds the command's own consume/enrich/thumbnail handlers and, per file, runs the consume step (checksum, synthetic-task handler call without child links), then the enrichment and — when enabled — the thumbnail step, each counted once and reported as it runs.
- `pauseRequested(err) bool` — Reports whether a handler error carries the `PauseBatch` flag (a direct run has no batch to pause, so it stops instead).
- `streamBatchProgress(ctx, queries, batchID)` — The read-only progress reporter goroutine: prints each task's transitions and one closing summary, returns when no pending or processing task is left; completion and settlement stay in the library.
- `parseExecToken(value) (uuid.UUID, error)` — Parses the required execution token; an empty or malformed token is refused.
- `batchHasConsumeTasks(ctx, client, batchID) (bool, error)` — Whether the batch carries at least one consume task (the missing-tools gate's condition).
- `consumeCancelHandler(c, args) error` — Delegates to `task.CancelBatch` and prints the outcome: a live lease is signalled, pending + waiting + processing tasks cancel, the lease and locks release, a terminal or unknown batch is refused. The dead-owner branch reports the owner PID found; the no-owner branch reports zero tasks.
- `taskDisplayInfo(t) taskDisplay` — Extracts index, filename, task type from a payload via `task.ReadPayload`.
- `totalFiles(tasks) int` — Counts total files from max file_index in payloads.

---

## `advisories.go` (`ops/consume`)

### Functions

- `printToolBlock(missing)` — Prints the missing-required-tools block with per-system install hints and the built-in-engine hint.
- `printOcrmypdfAdvisory(cfg, allTools)` — Prints the ocrmypdf note (system tesseract reads its own tessdata dir) with the configured languages, the per-language tesseract pack commands and the optional-companion install hints.

---

## `container.go` (`ops/container`)

### Struct

- `Container` — `cfg` (atomic.Pointer), `logger`, `db`, `client`, `engine`, `tag`, `matcherClient`, `tagToolsRunner`
  - **Methods**:
    - `NewContainer(cfg, logger)` — No DB
    - `NewContainerWithDB(cfg, logger, db)` — With provided DB
    - `GetConfig() *config.Config` / `SetConfig(cfg)` — Atomic load/store of the live config (the queue daemon swaps it on hot reload)
    - `GetLogger() *log.Logger`
    - `LogFileConfig(name) log.LogFileConfig` — File-log config for `<config_dir>/logs/<name>` with the configured rotation settings
    - `GetTagService() (*curation.Tag, error)` — Lazily builds the curation tag service over the client, logger and matcher client; errors only when the database is unreachable
    - `GetDB() (*sql.DB, error)` — Creates DB lazily, runs goose migrations on first open
    - `GetClient() (*database.Client, error)` — Lazily wraps the DB
    - `InvalidateDB()` — Closes the DB and drops every cached dependency (client, engine, tag service, matcher client, tag-tools runner) so the next accessor rebuilds from the current configuration
    - `ReconnectClient() (*database.Client, error)` — Opens a new DB from the current config, invalidates, and returns the new client
    - `SocketPath() string` — `<config_dir>/kushim-hugot.sock`
    - `GetMatcherClient() *matcher.MatcherClient` — Lazily builds the RPC client over the socket, capping request bodies at the configured reduce-target size
    - `GetTagToolsRunner() *tools.Runner` — Lazily builds the `textreducer`, `contentanalyzer` and `tagmatcher` tools over the matcher client
    - `GetSearchEngine() (*search.Engine, error)`
    - `Close()` — Closes the embedder and the DB

The container assembles no task catalog, enqueuer, handler or dispatcher: each
command builds those at its own boundary from these services. The ingest
family's handlers and claimed-batch execution live in
`internal/ops/consume` (§`consume.go`), every maintenance/migration/tag batch
mode builds its own store and single-type registry at its command boundary,
and the catalogs/enqueuers are constructed
per boundary with the dedup hooks of the types they enqueue (edub in
`internal/api`, the wizard in `internal/api/wizard` — which also runs the
setup-time dispatcher for migrations; see
[Task System](task-system.md) — Task wiring at the process boundaries).

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

- `enrichHandler(c, args) error` — Flag-aware entry point. A positional document id, `--all`, or `--limit N` routes to the direct path: immediate enrichment under the manual maintenance lock, no queue rows (`--no-wait` exits non-zero instead of waiting for the lock or in-flight work); with no flags the effective limit is `consumer.max_files_per_batch`, `--all` uncapped. `--from-batch <id> --exec-token <token>` routes to the batch mode. `--limit` and `--all` are mutually exclusive and cannot be combined with a document id; `--limit` must be a positive integer; `--batch` is refused with a pointer to `kushim batch run` / `kushim batch create enrich`.
- `newEnrichHandler(c, client) (task.Handler, error)` — Builds the enrich handler at the command boundary: the enricher over the container's tag service and matcher client.
- `runEnrichBatch(c, batchID, execTokenParam) error` — The batch mode: builds a task store and a single-type registry over the command's enrich handler and runs the claimed batch through `consume.RunClaimedBatch`, adding the batch's enrich bookend on completion.

---

## `search.go`

### Functions

- `searchHandler(c, args) error` — Parses flags (`--limit`, `--offset`, `--rebuild-index`), runs search, formats results with ANSI highlighting and sizes via `log.FormatBytes`
- `highlightSnippet(s) string` — Replaces `<b>`/`</b>` with ANSI color codes
- `rebuildIndex(c) error` — Delegates to `search.Engine.Reindex` (`REINDEX INDEX CONCURRENTLY idx_document_tsv`)

---

## `setup.go` (`ops/setup`)

### Functions

- `RunSetup(args, logger) error` — Refuses to run when a `config.yaml` already exists in the config directory (before flags are parsed; the error names the file to delete). Otherwise dispatches to either `runSetupWizard` (default) or `runSetupCLI` (when `--cli` flag is set). Called both by `cmd/kushim/main.go` (before any config is loaded) and by the `setup` verb-table entry.
- `runSetupWizard(args, logger) error` — Starts a standalone HTTP wizard server on `0.0.0.0:8420` serving the embedded SvelteKit wizard SPA. The server bootstraps the config directory and skeleton config on the first PUT request — no database connection is made. When the settings submit claims the target database (`PrepareFreshDatabase`: create if missing, refuse unless completely empty), the server initializes the schema, binds the client/enqueuer, and runs the setup-time dispatcher that executes the migration handlers in-process while no queue daemon exists.
- `runSetupCLI(args, logger) error` — Accepts `--languages` through `--consumer-pdfoptimizer-fallback` (same as wizard), plus `--db-dsn`, `--admin-user`, `--admin-password`, `--reset-database`. `--db-dsn` persists `database.dsn` (overrides the individual `database.*` fields) and the DB connection uses it. Generates and persists a session secret. Before any config file is written, claims the target database — `PrepareFreshDatabase` (create if missing, refuse unless completely empty), or a drop-and-recreate with `--reset-database` — and initializes the schema; a rejected target leaves no config behind. Then writes config.yaml through Viper, reloads it, creates the directories, downloads Tesseract language data and the Hugot model (`BAAI/bge-m3`). Prompts interactively for admin username/password when omitted. Creates the admin user via `service.User.Create()`. After setup completes, generates systemd service files to `<configDir>/systemd/` via `config.GenerateServiceFiles` and prints copy/install instructions.

---

## `task.go`

### Functions

- `taskHandler(c, args) error` — Routes to list and status subcommands; bare or `--help` prints the help with a pointer at `kushim batch restart <batch-id>` for failed-task recovery
- `taskListHandler(c, args) error` — Lists tasks with batch/status/type/limit/offset filters; columns: TASK ID, TYPE, STATUS, BATCH, FILE
- `taskStatusHandler(c, args) error` — Shows task details with type, timestamps, document DB ID, document UUID, error for failed tasks

---

## `storage.go`

### Functions

- `storageHandler(c, args) error` — Entry point for `kushim storage`. Routes subcommands: `orphans` and `thumbnails`.
- `newOrphanedService(c) (*service.Orphaned, error)` — Builds the orphaned-file service from the container: database client and a boundary enqueuer wired with the consume dedup hook and the `tagmeasure` dedup hook (the restore path re-ingests files, and the restore batch declares the measurement follow-up while `enricher.tag_measure.after_enrichment_batch` is set).
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

The daemon's Postgres `LISTEN` goroutine lives in `internal/ops/dispatch/notify.go` (`dispatch.ListenForBatchNotifications`), shared with the wizard's setup-time dispatcher.

### Functions

- `Run(c, args) error` — Starts the batch queue daemon for background consumption and inbox polling. Accepts `--bg` to daemonize (re-execs without `--bg` and returns). In daemon mode: checks PID file for single-instance, sets up signal handling (SIGTERM/SIGINT → graceful shutdown with PID file cleanup), opens log file at `<config_dir>/logs/queue.log`, and runs three concurrent scheduling paths:
  - **Notification-driven consumption** — A dedicated `LISTEN` goroutine connects to Postgres via a separate pgxpool (MinConns=1, MaxConns=1) and listens on the `batch_queued` and `lock_released` channels. When a batch transitions to `status='queued'`, a database trigger sends a `pg_notify` that wakes the goroutine, as does a released resource lock (`pg_notify` from the release queries); the goroutine forwards the signal over a buffered channel (capacity 4). The daemon picks the batch up in milliseconds; the dispatcher claims it — status, lease and holds in one transaction — and forks the batch's owning task CLI as `<cli> --from-batch <id> --exec-token <token>`.
  - **Safety timer** — A 30-second timer guarantees consumption runs at least once every 30s, even if a notification is dropped (connection blip, reconnection). Resets on each notification-driven consumption.
  - **Housekeeping ticker** — A 5-second ticker that drives the config reload/reconnect, the schedules, and stale reclamation. Runs:
    - **Stale batch reclamation** — Lists batch owners with stale heartbeats (>15s) and active tasks, signals SIGTERM to the owner PID (if the process is still alive), releases that listed ownership row (a fresh execution that took the batch over keeps its lease and the iteration is skipped), quarantines tasks at or above `consumer.reclaim.max_retries` (default 3) to `failed`, moves the quarantined inbox files by the returned task identities, resets remaining processing→pending with an incremented attempt counter, and decides the fate: the guarded settle when no work remains, the guarded requeue otherwise — any recovery failure after the release requeues so a released batch cannot be left ownerless. Gated by `consumer.reclaim.enabled` (default `true`); when `false`, stale batches remain in `processing` state and are not resumed.
    - **Stale task reclamation** — An age-based sweep (`reclaimStaleTasks`) that resets individual `processing` tasks back to `pending` (or `failed` if at max retries) when their `started_at` exceeds `consumer.reclaim.stale_task_after` (default 600s, decided by the database clock), then moves the quarantined consume tasks' inbox files immediately. Lock-holding tasks (backup, mirror, the four tag passes, thumbbackfill, migrate-db, migrate-storage) are swept only when no resource lock belongs fresh to their batch — a lock row carrying their batch's id, inside the 30-minute freshness window (5-minute heartbeat) — so a live holder is never reset nor robbed of its lock. Gated by `consumer.reclaim.enabled` and rate-limited to at most once per `max(60s, stale_task_after/10)` so the sweep does not run on every 5s tick.
    - **Empty queued batch sweep** — Removed: the transactional enqueue can no longer commit a batch without tasks, so the zombie sweep has nothing left to reap.
- `reclaimStaleBatches(ctx, cfg, client, batchSvc, logger) error` — Iterates stale batch owners, releases the listed ownership row first — scoped by `(batch_id, owner_id, pid)` plus the staleness re-check (`ReleaseStaleBatchOwner`; zero rows = the lease was adopted or heartbeated since the snapshot, iteration skipped), signals SIGTERM to the released owner's PID only when the release landed (if the process is still alive), quarantines tasks at or above `consumer.reclaim.max_retries` to `failed` and moves their files by identity, resets remaining processing→pending with incremented attempt counter, then the guarded settle (no work left) or guarded requeue; any recovery step failing after the release runs the same guarded requeue so the batch is never left ownerless. Only called from `daemon.reclaimStale` when `consumer.reclaim.enabled` is `true`.
- `requeueReleasedBatch(ctx, client, logger, batchID)` — Private: hands a batch whose stale lease was already released back to the dispatcher (guarded `processing → queued`), so a recovery failure after the release cannot strand it.
- `reclaimStaleTasks(ctx, client, batchSvc, cfg, logger) error` — Age-based sweep for individual tasks stuck in `processing` beyond `consumer.reclaim.stale_task_after` (cutoff evaluated by the database clock). Delegates to `service.Batch.ResetStaleProcessingTasks` which quarantines (attempts ≥ maxRetries → `failed` with the swept identities returned) and resets (attempts < maxRetries → `pending` + increment) in a single transaction; lock-holding tasks go through lock-aware variants exempting batches with a fresh lock row carrying their batch id. Moves the quarantined consume tasks' inbox files immediately and logs the swept identities with the count.
- `dispatch.Dispatcher` (`internal/ops/dispatch/dispatcher.go`) — The daemon's only queue claimant, with explicit dependencies: `dispatch.NewDispatcher(catalog, logger, host, maxConcurrent, wake, inProcess)` takes the task catalog the daemon built at boot, the daemon's logger, the host it dispatches for, the batch slot budget, its wake channel and the process's in-process registration (`nil` for the daemon, which is fork-only). `Dispatch(ctx, client, batchSvc) error` lists the queued batches oldest-first with their task types and sweeps them: eligibility comes from the task catalog (the batch-mode CLI surface and the host list, with a covered-first check for registration entries), the slot budget (`CountLiveNoHoldsBatches` scoped by the dispatcher's identity) gates only batches that acquire no locks, and each claimable candidate is claimed through `task.ClaimBatch` (one transaction: status, pid-0 lease under a fresh execution token, holds) and forked as the owning task CLI (`<cli> --from-batch <id> --exec-token <token>`). A launch failure stops the sweep.
- `Dispatcher.sweep(...)` / `.childExited(...)` / `.handleLaunchFailure(...)` — The sweep body and the bounded launch-failure policy: release lease and locks, requeue with backoff (5s, 10s), and the third consecutive failure fails the batch through `task.FailUnstartedBatch` instead of looping. `childExited` classifies a non-zero child exit by the lease row: pid 0 under this token is a launch failure; an adopted lease means the child owned its outcome.
- `dispatch.LaunchQueuedBatch(ctx, client, logger, catalog, batchID) error` (`launcher.go`) — The manual launcher behind `kushim batch run`: claims only queued batches (pointed refusals for missing, terminal, paused, processing-under-lease, and an unknown task type), forks the task CLI in the foreground and propagates a non-zero exit. No budget check.
- `requeueAfterLaunchFailure(ctx, client, logger, batchID, token)` — Private: releases the lease first (aborting when it is no longer ours), then the locks, and requeues the batch for the backoff retry.
- `resolveLaunchTarget(catalog, batchID, taskTypes, host)` / `batchLockSets(taskTypes)` / `launchBackoff` — Executor resolution (every type must be dispatchable on the host; the batch's owning command resolves by fixed precedence `consume > enrich > thumbnails > thumbbackfill`, so a mixed ingest batch forks `consume`, the type its enrich/thumbnail children hang from), the claim-side lock sets (holds plus the waits of the holding types), and the in-memory consecutive-failure tracker.
- `scheduler.pollingEntry()` (`scheduler.go`) — the unnamed inbox-polling entry of the scheduler's table: origin `polling`, no schedule name, no timing. `enabled` is `consumer.polling.enabled` plus `IsWithinActiveWindows`; the producer skips the occurrence while the maintenance lock is held (a query error consumes the occurrence too, logged) or an external tool is missing, then runs `consumption.ScanInboxSpecs`. The engine enqueues the returned set with origin `polling` and no schedule name; the consume dedup keys are the overlap mechanism, and the dispatcher's slot budget is the capacity mechanism.
- `scheduler` (`scheduler.go`) — the daemon's declarative scheduler; `newScheduler(logger)` builds it at daemon boot. Its state is the process logger plus three process-local pieces: `firstDue` (a never-run named entry's next-slot anchor), `lastFire` (the unnamed entry's last dispatch — a scan that finds nothing creates no batch row, so the batch table cannot anchor its cadence) and `scanInFlight` (refuses a second inbox scan while one runs). `run(ctx, client, enqueuer, cfg)` evaluates every entry of the current configuration once, logging per-entry failures without stopping the rest:
  - `evaluate(ctx, client, enqueuer, cfg, entry)` — one entry's evaluation: the enabled predicate on the freshly reloaded config, the active check for named entries (`CountActiveBatchesForSchedule` — any queued or processing batch of that schedule name; the unnamed entry has none), the due check, then the occurrence fires. The unnamed entry records its dispatch in `lastFire` before the in-flight check and runs `fire` on its own goroutine — the inbox scan lists and hashes files, which must never block dispatch, reclamation or shutdown; named entries fire synchronously. `PreconditionError` from the enqueue logs a skipped occurrence and continues; a zero-enqueued result leaves a named entry due.
  - `fire(ctx, client, enqueuer, cfg, entry)` — the producer's task set enqueued through `enqueueEntryBatch` with the entry's origin and schedule name; `task.NewBatchSpec` rejects a schedule name on a non-schedule origin and vice versa, so a table-entry mismatch fails loudly instead of enqueueing.
  - `entryDue` / `due` / `intervalDue` — named entries anchor on the last terminal batch (`completed`/`failed`/`cancelled`) of their schedule name, `NextBackupTime` over its `created_at`, falling back to the `firstDue` entry computed once per daemon lifetime; the unnamed entry is due when `max(interval, one minute)` has elapsed since its last dispatch.
- `entriesFor(cfg) []schedulerEntry` — the entry table, rebuilt every evaluation: one backup entry per configured mode (`backup:<mode>`, each carrying its mode/path/keep) plus the fixed mirror, thumbnail-backfill and four tag-pass entries, then the unnamed inbox-polling entry. `schedulerEntry` is data — `name`, `origin`, `enabled(cfg)`, `timing(cfg)` (named entries only) and `produce(ctx, cfg, client, logger)` — with no execution callback; a producer may return any task types, chained waiting children included, and the enqueue validates the set. The tag-identity entry pins its daily 03:30 cadence.
- `enqueueEntryBatch(ctx, enqueuer, entry, tasks) (task.EnqueueResult, error)` — builds the entry-origin batch spec and enqueues the producer-built task set in one transaction. The dispatched batches flow back through the dispatcher below: backup/mirror/tag batches are claimed and forked like any other batch.

### Dispatched maintenance and tag batches

Scheduled `backup`, `mirror` and tag batches are ordinary queued batches the dispatcher claims: the claim acquires the type's holds under the execution token and forks the type's own CLI (`kushim backup --from-batch`, `kushim mirror --from-batch`, `kushim tagreconcile|tagmeasure|tagidentity|tagaudit --from-batch`), which adopts the lease with its child pid, keeps the batch holds fresh across the whole run (lock heartbeat + drain) and stores the pass's report as the task result. A batch made only of tag types chained by waiting successors — the maintenance routine's batch — resolves to `kushim tagmaintenance --from-batch` instead, whose child registers all four pass handlers and runs the chain in order. Wording per pass:

- **tagmeasure** — the pass opens on its store-changed flag (`tag_measure_changed` in `system_signal`): with the flag clear and a completed run under the current rule set it prints a zero-work report (`zero_work: true`, no records) and writes nothing; otherwise its opening transaction clears the flag and it sweeps, re-arming the flag if the sweep fails. The batch id never enters the run keys — the run id stays fresh.
- **tagreconcile** — opens on its store-changed gate plus the work probe set: a clear flag with every probe empty prints `zero_work: true` without the store-wide reads; a failed run re-arms the flag.
- **tagidentity** — the child resolves pending identity evidence through `admission.IdentityResolver` (criteria judged, same-intent pairs adjudicated, dispositions applied) under `enricher.tag_reconcile.max_rejudge`; a run with the adjudicator off only counts the backlog.
- **tagaudit** — opens on its store-changed gate (a clear flag skips the store-wide pending-coverage derivation while the sample sweep still draws) and reports sweep, sampled/flagged counts, coverage tallies by class, fills/flags/proposals, judge call counts and pending coverage.
- **backup / mirror** — the dispatched children run `backup.RunBackup` / `backup.RunMirrorAndRecord` over the payload's mode/path/keep (validated against the configured roots); a 5-minute heartbeat refreshes the batch's locks while the long actions run, so a large mirror never trips the 30-minute staleness window.
- **tagmaintenance** — the routine child (`internal/ops/tagmaintenance`) registers the four pass handlers over its own catalog (the four constant dedup keys, so the waiting successors activate) and runs the batch's chain in order; a bare `kushim tagmaintenance` runs the same four handlers directly under the manual maintenance hold, creating no batch or task rows. Enqueue is `kushim batch create tagmaintenance`.

### Structs

- `daemon` — the daemon's runtime state: `c`, `catalog *task.Catalog` (the daemon's wiring map, built once at boot: per-type dedup hooks plus the mirror rsync precondition), `client` (atomic.Pointer, republished on reconnect), `batchSvc`, `sched *scheduler`, `dispatcher` (the `dispatch.Dispatcher` minted at startup, fork-only), the notification listener (`notifyCancel`, `notifyCh`), and `lastStaleTaskClaim`.
  - **Methods**: `tick(ctx)` (one housekeeping sweep: config reload + reconnect, `sched.run`, stale reclamation), `reloadConfig(ctx)` (hot-reloads config; on a connection change it reconnects, republishes the client, and restarts the LISTEN listener), `reclaimStale(ctx, cfg)`, `startNotify(ctx)` / `stopNotify()`.

The daemon keeps no pools: recurring enqueues are declared in `scheduler.go` as data — schedule name, batch origin, enabled predicate, timing and the producer that builds the task set — and their batches are ordinary queued batches the daemon's dispatcher claims and forks; the tag schedules' `enabled` gates control only automatic enqueueing, so a manually created batch still runs whenever the daemon dispatches it. The named entries never check the `maintenance` resource lock (an enqueued batch simply waits for its locks); only the polling entry's producer still gates its scan on `IsLockHeld`.

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

- `backupHandler(c, args) error` — `kushim backup [--from-batch <id> --exec-token <id>] [--path <dir>] [--mode <full|database|documents>] [--no-wait]`. **Batch mode** (`--from-batch` with `--exec-token`; the direct-mode flags are refused with it) executes one claimed backup batch: it builds the store and the single-type registry whose handler runs `backup.RunBackup` over the payload's mode/path/keep (validated against the configured backup roots), and calls `consume.RunClaimedBatch`. **Direct mode** runs a synchronous backup under the backup type's manual lock policy: acquires `maintenance` all-or-nothing with a NULL token (`task.RunDirect`, waiting by default for the lock and for in-flight tasks to drain; `--no-wait` exits non-zero instead), works with polling enabled, creates a `tar.gz` archive per the mode (`full` = DB dump + config + storage; `database` = DB dump + config only; `documents` = config + storage only), prints the result, releases the lock. Manual backups never apply retention.
- `restoreHandler(c, args) error` — `kushim restore <backup-file.tar.gz> [--force] [--dry-run] [--temp-dir <dir>]` — Validates the archive, checks restore tooling per `database.runtime` (`host` requires `psql` on PATH; `docker`/`podman` require the runtime binary and `database.container`; `remote` refuses with manual-restore guidance; skipped for `documents`-mode archives and `--dry-run`), checks its quiescence preconditions (the `maintenance` lock free, no in-flight tasks, polling disabled), try-acquires the maintenance lock with a NULL token and heartbeats it, checks the running daemon's PID file, prompts for confirmation (unless `--force`), runs `checkRestoreDiskSpace` (refuses corrupt/hand-made manifests with zero mode-relevant sizes and refuses when the staging parent — or the storage parent for cross-device staging — lacks ~1.05× the required size), creates the extraction staging dir next to `storage.storage_dir` (or under `--temp-dir`), restores per the manifest mode (`full`: SQL dump via `psql` + storage swap + path rewrite; `documents`: storage swap only; `database`: SQL dump via `psql` + path rewrite only; legacy manifests without `mode` restore as `full`), saves the archived config as `config.yaml.restored`, releases the lock. `ReplaceFiles` renames the extracted `storage/` onto `storage_dir` when both are on the same device, falling back to a `storage-swap-*` copy (completed before the swap) for cross-device staging.

---

## `mirror.go`

### Functions

- `mirrorHandler(c, args) error` — `kushim mirror [--from-batch <id> --exec-token <id>] [--path <dest>] [--no-wait]`. **Batch mode** executes one claimed mirror batch over the payload's destination. **Direct mode** runs a synchronous mirror of the storage tree to a local path or rsync remote target (`[user@]host:path`) under the mirror type's manual lock policy: checks `rsync` availability first, acquires `maintenance` and `storage` all-or-nothing with a NULL token (`task.RunDirect`, waiting by default for the locks and for in-flight tasks to drain; `--no-wait` exits non-zero instead), then runs `rsync -a --delete --info=stats2 --timeout=600 <storage>/ <dest>` (process-group-killed on Ctrl-C so the spawned `ssh` for remote targets is reaped too). Writes a `.edub-mirror.json` diagnostics file (timestamp, file count, bytes, app version) into the destination and prints the stats: total regular files (thousands-separated) plus the per-run breakdown parsed from rsync stats2 (`N transferred: N created, N updated, N deleted` — `updated` derived as transferred minus created, clamped at 0). Remote targets need passwordless ssh. Destination guards (symlink-resolved) refuse paths inside `storage_dir` or the backup directory, paths containing storage, and dash-prefixed destinations.

---

## `tagpass.go` (shared tag-verb plumbing)

All four tag verbs construct their task handler locally — a verb-specific `newReconcileHandler`,
`newIdentityHandler`, `newAuditHandler` or `newMeasureHandler` builds the domain runner over the
container's services and returns the `task.Handler` whose `Handle` runs the pass and returns the
serialized report (the pass bodies live in the domain packages: `reconcile.NewReconcileTaskHandler`,
`admission.NewIdentityTaskHandler`/`NewAuditTaskHandler`, `tags.NewMeasureTaskHandler`) — and map to the
same three-shape contract through the helpers in `tagpass.go`:

- `runTagPassDirect(c, taskType, noWait, handler)` — the no-flag mode: `task.RunDirect` under the type's manual lock declarations (hold `maintenance`, wait by default, `--no-wait` to fail fast), running the handler and printing the report JSON, creating no queue rows
- `runTagPassBatch(c, batchID, execToken, taskType, handler)` — executes one claimed tag batch: builds the store plus the single-type registry whose handler runs the pass and stores the report as the task result, then `consume.RunClaimedBatch`
- `resolveTagPassSelection(batchID, execToken)` — validates the batch-mode selector before anything touches the database: a batch-mode invocation requires `--exec-token` (a tokenless refusal points at `kushim batch run`), and a malformed token refuses before anything touches the database

## `tagidentity.go`

### Functions

- `tagIdentityHandler(c, args) error` — `kushim tagidentity [--from-batch <id> --exec-token <id>] [--no-wait]`. **Direct mode** runs the pass immediately under the manual maintenance hold and prints the report; **batch mode** executes one claimed batch whose handler resolves pending identity evidence through `admission.IdentityResolver` (criteria judged, same-intent pairs adjudicated, dispositions applied) under `enricher.tag_reconcile.max_rejudge`, storing the report on the task row. Enqueue is `kushim batch create tagidentity`.

---

## `tagreconcile.go`

### Functions

- `tagReconcileHandler(c, args) error` — `kushim tagreconcile [--from-batch <id> --exec-token <id>] [--no-wait]`, the manual trigger for scheduled reconciliation. **Direct mode** runs the pass immediately under the manual maintenance hold; **batch mode** executes one claimed batch whose handler builds `reconcile.NewReconciler` over the container's client, tag service and tag-tools runner (textreducer, contentanalyzer and tagmatcher over the matcher client) and stores the pass report on the task row. Both modes open on the pass's store-changed gate: with the flag clear, a completed run behind it, and every work probe empty (the four pair re-check categories, the twins worklist, the stored-compound scan, the orphan and provisional grace sweeps, promoted-tag demotion, and the bootstrap log ingest), the pass prints a zero-work report (`zero_work: true`) without the store-wide reads; a failed run re-arms the flag. Enqueue is `kushim batch create tagreconcile`.

---

## `tagmeasure.go`

### Functions

- `tagMeasureHandler(c, args) error` — `kushim tagmeasure [--from-batch <id> --exec-token <id>] [--no-wait]`. **Direct mode** runs the pass immediately under the manual maintenance hold: it logs the opening line (`tag measure run starting: N tags in store`), runs the pass (one REPEATABLE READ transaction over `internal/tags`, appending the evidence records with a fresh `run_id`) and prints the report. **Batch mode** stores the report on the task row instead. The pass opens by checking its store-changed flag (`tag_measure_changed` in `system_signal`): with the flag clear and a completed run under the current rule set it prints a zero-work report (`zero_work: true`, no records) and writes nothing, leaving the previous run's readings and worklists standing; otherwise its opening transaction clears the flag and it sweeps, re-arming the flag if the sweep fails. Enqueue is `kushim batch create tagmeasure`.

---

## `tagaudit.go`

### Functions

- `tagAuditHandler(c, args) error` — `kushim tagaudit [--from-batch <id> --exec-token <id>] [--no-wait]`. **Direct mode** runs the pass immediately under the manual maintenance hold; **batch mode** stores the report on the task row. The pass opens on its store-changed gate — a clear flag skips the store-wide pending-coverage derivation (the durable rows stand) while the sample sweep still draws — and a failed run re-arms the flag. The run id stays fresh so batch ids never enter the measurement substrate; enqueue is `kushim batch create tagaudit`.

---

## See Also

- [Task System](task-system.md) — Enqueuer, CRUD, and execution library used by CLI commands
- [Config & Utils](config-and-utils.md) — Configuration loading, logger
- [Pipeline](pipeline.md) — Consumption engine used by consume command

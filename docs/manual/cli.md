# edub-kushim — CLI Reference

`kushim` carries every operational verb; `edub` only serves the REST API and the web UI. Every
command accepts `--help` for its own usage.

Work runs in one of two ways, and each verb below says which one it uses:

- **Direct** — `kushim consume`, `kushim enrich`, `kushim thumbnails`, the tag passes, `backup`,
  `mirror` and `migrate` run immediately, in the current process, and create no batch or task
  rows. Direct runs coordinate through two named resource locks: pipeline work waits on
  `maintenance`, bulk storage writes wait on `storage`. Commands wait for the locks (and for
  in-flight tasks to drain) unless `--no-wait` makes them exit non-zero instead.
- **Queued** — `kushim batch create <task>` enqueues a batch; the `kushim queue` daemon claims
  it and forks the task's CLI in batch mode. `kushim batch run` executes one queued batch now,
  in the foreground.

The task and batch status vocabulary used throughout is defined in
[Tasks and batches](README.md#tasks-and-batches).

## Command index

| Command | Purpose |
|---|---|
| `kushim setup` | One-time initialization — web wizard or `--cli` terminal mode |
| `kushim queue` | The batch queue daemon (dispatch, scheduling, reaping) |
| `kushim hugot` | The tag matcher RPC server over a Unix socket |
| `edub` | The API server and web UI (`edub version` prints the version) |
| `kushim consume` | Process the inbox now (`cancel` subcommand cancels a batch) |
| `kushim enrich` | Re-run LLM classification on live documents |
| `kushim thumbnails` | Backfill missing thumbnails now |
| `kushim batch` | Queue surface: `create`, `run`, `stop`, `resume`, `restart`, `list`, `status` |
| `kushim tagreconcile` | Re-adjudicate stored tag evidence and sweep the vocabulary |
| `kushim tagmeasure` | Measure the tag store into versioned evidence records |
| `kushim tagidentity` | Adjudicate pending tag-identity claims |
| `kushim tagaudit` | Audit document coverage against the vocabulary |
| `kushim tagmaintenance` | The full routine: reconcile → identity → audit → measure |
| `kushim config` | View and edit `config.yaml` values (git-style get/set) |
| `kushim llm` | View and edit the provider and model catalogs |
| `kushim user create` | Create a user with a role |
| `kushim backup` | Create a `tar.gz` backup (database/documents/full) |
| `kushim restore` | Restore from a backup archive |
| `kushim mirror` | Mirror the storage tree with `rsync --delete` |
| `kushim migrate` | Move the database or the storage directories |
| `kushim storage` | Orphaned-file and orphaned-thumbnail maintenance |
| `kushim search` | Full-text search from the terminal |
| `kushim task` | Inspect tasks (`list`, `status`) |
| `kushim version` | Print the application version |

## Setup

### `kushim setup`

Generate the configuration, create the directories, download OCR language data and the Hugot
embedding model (`BAAI/bge-m3`), and claim the target database — auto-creating it when missing,
adopting it only when completely empty. A database that already contains tables aborts setup;
drop it or choose another name. Setup refuses to run over an existing configuration: to start
over, delete `~/.config/edub-kushim/config.yaml` first.

By default it launches the **web wizard** at `http://0.0.0.0:8420` with a six-step flow:
config directory → consumer settings → enricher settings (this step claims the database and
applies the schema) → asset downloads (tessdata, model) → admin user → completion. `edub` is
ready to start at the end.

For headless or CI use:

```bash
kushim setup --cli --languages eng,spa
```

| Flag | Default | Description |
|---|---|---|
| `--cli` | `false` | Terminal-based setup instead of the web wizard |
| `--languages` | — (required with `--cli`) | Comma-separated OCR codes (e.g. `eng,spa,deu`) |
| `--inbox-path` | `<config-dir>/inbox` | Consumption directory |
| `--storage-path` | `<config-dir>/storage` | Processed-file storage root |
| `--consumer-ocr-engine` | `gosseract` | `gosseract` or `ocrmypdf` |
| `--consumer-textextractor-engine` | `mupdf` | `mupdf`, `gopdf`, or `pdftotext` |
| `--consumer-pdfoptimizer-engine` | `mupdf` | `mupdf` or `gs` |
| `--consumer-pdfoptimizer-fallback` | — | Secondary optimizer binary (ignored for `gs`) |
| `--admin-user` | — | Admin username (prompted if omitted) |
| `--admin-password` | — | Admin password (prompted if omitted) |
| `--db-dsn` | — | PostgreSQL DSN; overrides `database.*` and persists as `database.dsn` |
| `--reset-database` | `false` | Drop all tables and re-run schema + seeders |

`--inbox-path` and `--storage-path` accept absolute paths or `~`-prefixed paths. Setup finishes
by writing concrete systemd service files to `<config-dir>/systemd/` and printing the install
commands (see [Running as a system service](#running-as-a-system-service)).

## Service commands

### `kushim queue`

The batch queue daemon. One instance runs continuously and, in a loop until signalled:

- listens for batch and lock notifications (`LISTEN` on PostgreSQL) with a periodic poll as a
  safety net;
- claims queued batches through the dispatcher — respecting `server.max_concurrent_batches`
  (re-read from the configuration on every sweep, so edits apply to the running daemon without
  a restart) — and forks each batch's task CLI in batch mode with the execution token;
- runs the declarative scheduler: the per-mode backup schedules, mirror, thumbnail backfill,
  the tag passes (identity keeps its own daily 03:30 cadence), and the optional inbox polling
  scan;
- reaps stale work: reclaiming dead leases and requeueing stuck tasks.

```bash
kushim queue          # run in the foreground
kushim queue --bg     # fork into the background (re-executes without --bg)
```

Operational notes:

- A PID file makes the daemon single-instance: a second start refuses with
  `queue daemon already running (PID …)` and stale PID files are removed automatically.
- `kushim` must be on `PATH` (or the daemon started with a resolvable path), because it
  re-executes its own binary to fork workers. If it cannot re-exec, batches stay queued and
  are never processed — the API still accepts and enqueues work.
- Ingest creation is guarded while batches are paused for LLM credits — see
  [Paused batch guard](#paused-batch-guard).
- Logs to `<config-dir>/logs/queue.log`.

### `kushim hugot`

Start the matcher RPC server over a Unix domain socket. This process hosts the Hugot embedding
model and serves semantic tag matching, encoding and consolidation to the API server and to
CLI workers.

```bash
kushim hugot
kushim hugot --socket /path/to/custom/matcher.sock
kushim hugot --bg
```

| Flag | Default | Description |
|---|---|---|
| `--socket` | `<config-dir>/kushim-hugot.sock` | Socket path for RPC |
| `--bg` | `false` | Run in the background (re-executes without `--bg`) |

Start the matcher **before** `edub`. Without it the API still runs: tag CRUD succeeds but the
embedding cache is not updated (errors are logged) and enrichment falls back to LLM-only tags.
For configuration, memory and CPU tuning (`chunk_size` is the main memory lever, default 4096
tokens) see the [semantic matching guide](../developer-guide/semantic-matching.md). Logs to
`<config-dir>/logs/hugot.log`.

### `edub`

Start the API server and web UI (default when no command is given):

```bash
edub              # serve on the configured host/port (0.0.0.0:3000 by default)
edub version      # print the version
```

The server runs no background pipeline work: it enqueues batches into the database and serves
HTTP; the queue daemon and the matcher are separate processes. Logs to
`<config-dir>/logs/edub.log`; graceful shutdown on SIGINT/SIGTERM. Ensure `kushim` is on
`PATH` if the queue daemon must process what the API enqueues.

## Ingest

### `kushim consume`

Scan the inbox and consume its files immediately, in this process, under the `maintenance`
lock. Creates no batch and no task rows — to enqueue the same work for the daemon, use
`kushim batch create consume`.

```bash
kushim consume
```

Output while running:

```text
Consuming 3 file(s)...
  [1/3] consume  report.pdf ... done
  [1/3] enrich   report.pdf ... done
  [2/3] consume  invoice.pdf ... done
  [2/3] enrich   invoice.pdf ... done

Summary: 3 files, 6 tasks — all successful
```

A failed step reports its error and the run continues with the next file; the command exits
non-zero when anything failed, and a failed file stays in the inbox so a later run picks it up
again.

`--no-wait` exits non-zero instead of waiting when the `maintenance` lock is held or in-flight
pipeline work is still draining — useful for unattended runs.

Before touching the inbox the verb runs the
[pre-flight tool check](#pre-flight-tool-check); it also stops on the first provider credit
error (a direct run has no batch to pause).

#### `kushim consume cancel <batch-id>`

Cancel a running or queued batch: pending and waiting tasks become `cancelled`, the process
holding the batch lease receives `SIGTERM` when it is alive, in-flight tasks are cancelled, and
the batch's locks and lease release. A settled batch refuses.

```text
Batch 550e8400-e29b-41d4-a716-446655440000: 5 pending + 1 processing cancelled, signal sent to PID 12345
```

When the lease holder is already gone the cancel proceeds without a signal; with no lease at
all the pending count is reported on its own. `kushim batch stop` is the same operation.

### `kushim enrich [<document-id>] [--limit N | --all] [--no-wait]`

Re-run enrichment (text reduction, LLM analysis: title, type, tags, people; semantic tag
matching) on **already-consumed** documents, now, in this process, under the `maintenance`
lock. No re-extraction or re-OCR happens; the stored text is re-classified. Creates no batch or
task rows — enqueue with `kushim batch create enrich` instead.

| Mode | Scope |
|---|---|
| `<document-id>` | A single document |
| no argument | The oldest `consumer.max_files_per_batch` live documents (default 10) |
| `--limit N` | The oldest N live documents |
| `--all` | Every live document |

Bulk selection is deterministic (live documents by internal id ascending), so repeated
`--limit` runs select the same head. `--limit` and `--all` are mutually exclusive and cannot
be combined with a document id.

```text
Enriching 2 document(s)...
  [1/2] 550e8400-e29b-41d4-a716-446655440000 ... done
  [2/2] 660e8400-e29b-41d4-a716-446655440001 ... done

Summary: 2 document(s) — all successful
```

A document whose enrich task is already queued is enriched again now *and* later by the queued
task — to avoid the double work, enqueue instead and let the daemon's dedup skip
already-queued documents. A provider credit error aborts the remaining run (no batch exists to
pause). Works on hosts without OCR/PDF tools: nothing here touches files.

### `kushim thumbnails <mode>`

Generate thumbnails now, in this process, for documents that lack one, under the `storage`
lock (waiting for `maintenance` to free first). Creates no batch or task rows.

```bash
kushim thumbnails --all
kushim thumbnails --document 550e8400-e29b-41d4-a716-446655440000
```

Exactly one mode is required:

| Mode | Scope |
|---|---|
| `--all` | Every document missing a thumbnail (paginated, 500 per page) |
| `--document <id>` | A single document |

`--force` proceeds even when `consumer.thumbnail.enabled` is `false`. When thumbnails are
disabled in config without `--force`, or the document already has a thumbnail, the command
errors with the reason.

Queued backfills (rendered later by the daemon):

| Command | Scope |
|---|---|
| `kushim batch create thumbnails` | Every document missing a thumbnail |
| `kushim batch create thumbnails --from-batch <id>` | Documents whose completed `consume` task is in that batch |
| `kushim batch create thumbnails --document <id>` | A single document |

Queued backfill tasks dedup by `thumbnail:doc:<uuid>`: already-pending documents are skipped in
bulk modes and surface as a conflict for `--document`. The backfill run holds the `storage`
lock for its whole batch and waits for in-flight thumbnail tasks to drain; regular ingest
thumbnails pause until it settles.

**Scheduled backfill** — `consumer.thumbnail.backfill_interval` (days, `0` = disabled) and
`consumer.thumbnail.backfill_time` (`HH:MM`, default `02:00`) make the queue daemon enqueue an
automatic `--all` sweep when due and none is already queued or processing. Sub-daily intervals
(e.g. `0.5` = every 12 hours) are supported; the anchor is the most recent terminal
`thumbnail-backfill` batch, so daemon restarts do not reset the schedule. Both fields are in
the settings UI (Processing → Thumbnails).

## Cross-cutting behavior

### Pre-flight tool check

Before `kushim consume` touches the inbox — direct or batch mode — it verifies the required
external tools in `PATH`:

- **Engine binaries**: `ocrmypdf`, `gs`, `pdftotext`, each checked only when the corresponding
  config engine is selected.
- **Required companions**: `tesseract` and `unpaper` (needed by the ocrmypdf adapter).
- **Prerequisite**: `curl` for any model/language download.

Missing required tools print a block of per-distro install hints and exit non-zero. Optional
companions (`pngquant`) and tesseract language packs are advisory notes only. With the
`ocrmypdf` engine the advisory also lists the language packs for the configured languages:

```text
Cannot consume — the following required tools are not installed:

  OCR engine "ocrmypdf" (binary not found in PATH)
    Debian/Ubuntu:  sudo apt install ocrmypdf
    Arch:           sudo pacman -S ocrmypdf
    macOS:          brew install ocrmypdf

  Companion "tesseract" — OCR engine that ocrmypdf wraps (core dependency)
    Debian/Ubuntu:  sudo apt install tesseract-ocr
    ...
```

The API exposes the same check: `POST /api/v1/consume` and `/consume/upload` answer `422` with
a `missing_tools` array ([api.md](api.md#conventions)), and the settings UI shows per-engine
warnings plus a sticky banner ([web-ui.md](web-ui.md)).

### Paused batch guard

While batches are paused due to LLM provider credit/balance errors, new ingest creation is
guarded: the queue daemon's polling scan skips its inbox scan (logging a line and waiting for
the next interval), and `kushim batch create consume` refuses with the paused batch ids:

```text
Error: 2 paused batch(es) exist due to LLM credit/balance errors: 550e8400-…, 660e8400-…
Resolve the billing issue, then resume or cancel each paused batch before creating new ones.
```

Direct `kushim consume` needs no guard: it creates no batch and stops on the same credit error.
Resolving billing and resuming or cancelling every paused batch lifts the guard automatically.

## Batches: `kushim batch`

The queue surface. The per-task commands (`kushim consume`, `kushim enrich`, …) run their work
directly; this command is what enqueues work for the `kushim queue` daemon.

### `kushim batch create <task> [args]`

```bash
kushim batch create consume
kushim batch create backup --mode full
kushim batch create enrich 550e8400-e29b-41d4-a716-446655440000
```

Supported tasks:

| Task | Arguments |
|---|---|
| `consume` | none — one ingest batch from the current inbox; refuses while credit-paused batches exist ([guard](#paused-batch-guard)) |
| `backup` | `--mode <full\|database\|documents>`, `--path <dir>`, `--keep N` (all optional) |
| `mirror` | `--path <dir>` (optional) |
| `enrich` | one or more document ids, or `--all` / `--limit N` (same oldest-first selection as the direct command) |
| `thumbnails` | none (all missing); `--from-batch <id>` or `--document <id>` for a subset |
| `tagreconcile`, `tagmeasure`, `tagidentity`, `tagaudit` | none |
| `tagmaintenance` | none — one batch chaining reconcile → identity → audit → measure as waiting successors |
| `migrate-db` | `--host <h> --port <p> --user <u> --password <p> --database <db> [--sslmode <mode>]` |
| `migrate-storage` | `--storage-dir <dir>`, `--consumption-dir <dir>` (at least one) |

```text
Batch 550e8400-e29b-41d4-a716-446655440000 queued with 1 task(s) — run 'kushim queue' to process
```

When every task is already covered by a pending task (dedup), no batch is created and the
command prints `Nothing enqueued`.

### `kushim batch run <batch-id>`

Claim a queued batch and run its task CLI now, in the foreground: one transaction claims the
batch (status, lease, resource locks), the command forks the task CLI with the execution token,
waits, and exits non-zero when the child fails. A paused batch points at `batch resume`, a
processing one at its lease holder (a stale lease is reclaimed by the daemon), a terminal one
refuses. Manual runs do not check `server.max_concurrent_batches` — the budget governs
automatic dispatch only.

```bash
kushim batch run 550e8400-e29b-41d4-a716-446655440000
```

### `kushim batch stop <batch-id>` (`cancel`)

Same operation as `kushim consume cancel`: cancel pending/waiting/in-flight tasks, signal a
live lease holder, release locks and lease.

### `kushim batch resume <batch-id>` / `kushim batch restart <batch-id>`

Requeue a paused, failed or cancelled batch. `resume` resets processing tasks and leaves failed
tasks failed; `restart` also retries the failed tasks. Both refuse while a live lease holds the
batch.

### `kushim batch list` / `kushim batch status <batch-id>`

`list` shows recent batches newest-first — status, origin, schedule, progress, owner state —
with `--limit N` (1–100, default 20) and `--offset N`. `status <batch-id>` shows one batch's
state: status, origin, task counts by state, lease owner and pid, and the orphan flag, with a
hint to `kushim task list --batch <id>` for task detail.

```bash
kushim batch list --limit 5
kushim batch status 550e8400-e29b-41d4-a716-446655440000
```

### Batch mode: the dispatcher's contract

Every batch executes as `kushim --executor <cli> --from-batch <id> --exec-token <token>` —
the form the queue daemon's dispatcher and `kushim batch run` use to fork the owning task CLI.
The execution token authorizes the child's task claims and lifecycle writes as the lease owner
of the claimed batch. It is never typed by hand: a `--from-batch` without a token errors
pointing at `kushim batch run <id>`, a terminal or unclaimed batch is refused, and a paused
batch reports its credit error instead of running. The child adopts the lease, repairs the
batch's entry state and runs every task type through the shared execution library; enrich and
thumbnail children activate as their consume parents complete.

## Tag passes

All five passes run two ways: **directly now** (manual `maintenance` hold, no queue rows, a
printed report) or **queued** via `kushim batch create <verb>`, which the daemon forks in batch
mode and whose report lands on the task row. The `--executor … --from-batch … --exec-token …`
form is the [dispatcher's contract](#batch-mode-the-dispatchers-contract), not a user surface.

### `kushim tagmeasure`

Run the tag extension measurement pass: one consistent snapshot of the tag store measured with
the extension algebra (per-value document mass, pairwise overlap coefficients, compound
reconstructibility, part coverage), written as versioned evidence records.

```bash
kushim tagmeasure                 # run now
kushim batch create tagmeasure    # enqueue for the daemon
```

`enricher.tag_measure.enabled` gates only the automatic scheduled enqueue.
`enricher.tag_measure.after_enrichment_batch` additionally follows production: every consume
and reenrich batch then declares a measurement follow-up that runs when the batch settles
(never while a measurement run is already active). Nothing acts on the measurements
automatically — they are evidence for review. The pass is a no-op when nothing changed: a
store-changed flag in `system_signal` is re-armed by every tag-store mutation (and by a failed
run), so an unchanged store prints a zero-work report and writes nothing.

### `kushim tagreconcile`

Re-adjudicate the stored tag evidence: stale, unjudged, drifted and aged pairs are re-checked,
stored compounds are split, twins are canonicalized, dead and provisional tags are swept, and
influence is revoked where the decision says so. Runs are bounded by the
`enricher.tag_reconcile` caps (`max_rejudge`, `max_retag`), and the first run is always
report-only. The latest completed run's report is served by the tag reconciliation report
surface ([api.md](api.md)).

```bash
kushim tagreconcile                 # run now
kushim batch create tagreconcile    # enqueue for the daemon
```

The pass is a no-op when nothing changed and nothing is due (empty work probes plus no aged or
grace-expired work) — it prints `zero_work: true` without the store-wide reads. Time still
creates work: an aged pair or an elapsed grace window makes the next run full. The daemon
dispatches reconciliation batches even with the schedule off; `enricher.tag_reconcile.enabled`
gates only the automatic scheduled enqueue.

### `kushim tagidentity`

Resolve pending tag-identity evidence: flagged candidates' canonical-form criteria are judged,
same-intent claims are adjudicated, and dispositions are applied — confirmed folds rename (or
alias) a name, a losing name becomes an alias of its winner, unanswered claims stay pending for
the next run.

```bash
kushim tagidentity                 # run now
kushim batch create tagidentity    # enqueue for the daemon
```

The pass also runs on its own daily schedule — fixed 03:30, no config key, anchored on the most
recent terminal `tagidentity` batch — gated on `enricher.tag_adjudicator.enabled`. Schedule,
manual command and the review surface's reevaluate action all create the same queued task,
deduplicated while a run is active.

### `kushim tagaudit`

Audit document coverage against the vocabulary: a sample plus below-floor documents is drawn
from the sweep register, the matcher suggests dimensions the document is about but does not
carry, an applicability judge confirms gaps, confirmed gaps are filled, gray calls are flagged
for review, and the durable coverage classes are recorded. A document is audited at most once
per sweep; flags surface in the **Coverage** tab of the tags page.

```bash
kushim tagaudit                 # run now
kushim batch create tagaudit    # enqueue for the daemon
```

The run requires the applicability judge (`enricher.tag_adjudicator`); without it, or when the
matcher is unavailable, no reading is taken and documents stay eligible for the next run.
Scheduling (`enricher.tag_audit`) is disabled by default and gates only automatic enqueuing.

### `kushim tagmaintenance`

The whole maintenance routine — reconciliation → identity resolution → coverage audit →
measurement — as one chained batch, each pass a waiting successor of its predecessor:

```bash
kushim tagmaintenance                 # run the routine now
kushim batch create tagmaintenance    # enqueue for the daemon
```

A failed pass discards its remaining branch and the batch settles failed;
`kushim batch restart` retries the routine from its root. Run directly, the first failed pass
stops the routine. Scheduling (`enricher.tag_maintenance`) is disabled by default and gates
only automatic enqueuing; while it is enabled the individual reconcile, measure and audit
schedules idle, and the daily identity pass keeps its own cadence — the routine's identity step
is a catch-up, not the only path to identity.

## Configuration and catalogs

### `kushim config`

View and edit `config.yaml` from the terminal. Git-style positional interface: no arguments
dumps everything, one argument reads a key, two arguments write a pair.

```bash
kushim config                     # dump all keys as "key = value"
kushim config server.port         # get (scalars raw, arrays/maps as YAML)
kushim config server.port 8080    # set (type auto-detected)
kushim config --unset backup.path # remove — reverts to the default on next load
kushim config --validate          # load and validate, report errors
kushim config --path              # print the absolute config file path
```

Keys use dot notation matching the YAML key names (`server.port`, `consumer.ocr.languages`).
Type detection on `set`: `true`/`false` → bool, integers, floats, comma-separated → list,
otherwise string. Writes are atomic: the new tree is fully validated (business rules included)
before it replaces the file, so an invalid value leaves the config unchanged. Exit codes: `0`
on success, `1` on any error. Keys and ranges are documented in
[configuration.md](configuration.md).

### `kushim llm`

View and edit the provider and model catalogs with the same grammar:

```bash
kushim llm                                          # dump every field
kushim llm providers.zhipu.adapter                  # get
kushim llm providers.zhipu.adapter custom-adapter   # set
kushim llm providers.groq '{"adapter":"openai-compatible","official_url":"https://api.groq.com/openai/v1"}'
kushim llm --unset providers.zhipu                  # remove (asks for confirmation)
kushim llm --unset providers.zhipu --cascade        # remove the provider and its models
kushim llm --reset [--providers | --models]         # restore embedded defaults
```

Keys have two roots: `providers.<name>[.<field>]` and `models.<provider>/<model>[.<field>]`. A
model is created by setting its first field; a provider must be created as a whole entry
(adapter and URL are required together). Setting a field to `""` clears it.

Guards: removing an entry the enricher configuration references (content analyzer, its
fallbacks, tag adjudicator, tag quality) fails with an in-use error; removing a provider that
still owns models fails until `--cascade`; `--unset` on a single field key is refused (clear it
with `""`). Exit codes mirror `kushim config`. The same catalog is edited from the settings UI
([web-ui.md](web-ui.md)) and the REST surface ([api.md](api.md)).

## Backup, mirror and migration

### `kushim backup`

```bash
kushim backup
kushim backup --path /custom/backup/dir
kushim backup --mode database
kushim backup --mode documents --path /mnt/nas/documents
```

| Flag | Default | Description |
|---|---|---|
| `--path` | config `backup.path` (or `<config-dir>/backups/`) | Output directory |
| `--mode` | `full` | `full` (database + documents), `database`, or `documents` |
| `--no-wait` | wait and drain | Exit non-zero instead of waiting for locks/drain |

Produces a timestamped `edub-backup-<mode>-<timestamp>.tar.gz` containing `config.yaml` plus
`edub.sql` (database/full) and/or `storage/` (documents/full), with a `manifest.json` recording
the mode. Manual backups never apply retention — only scheduled backups prune, using the
schedule's `keep` for that mode.

The command acquires the `maintenance` and `storage` locks (waiting out running backup, mirror,
migration or tag passes) and waits for in-flight consume/enrich/thumbnail tasks to drain;
`--no-wait` skips both waits and exits non-zero on conflict. Scheduled runs are enqueued by the
queue daemon's scheduler from the per-mode schedules in Settings → Safety.

### `kushim mirror`

Mirror the storage tree with `rsync --delete`, producing a faithful, browsable second copy —
a live tree where destination deletions follow source deletions. **Not** a restore source:
`kushim restore` only reads backup archives.

```bash
kushim mirror
kushim mirror --path /mnt/nas/documents
kushim mirror --path user@nas:/srv/documents
```

| Flag | Default | Description |
|---|---|---|
| `--path` | config `mirror.path` | Local path or `[user@]host:path` rsync target |
| `--no-wait` | wait and drain | Exit non-zero instead of waiting |

The run takes the same locks and drain, runs `rsync -a --delete --info=stats2 --timeout=600`,
and writes a `.edub-mirror.json` diagnostics file into the destination. The idle timeout
aborts stalled transfers so locks are never held indefinitely; Ctrl-C also kills a spawned
`ssh`. Prerequisites: `rsync` installed, and passwordless ssh for remote targets. The
destination is validated (never inside `storage_dir` or the backup directory, never starting
with `-`). The mirror is faithful — keep tar backups as the mitigation for accidental mass
deletion.

### `kushim restore <archive>`

Restore database and storage from a backup archive. The archived configuration is preserved as
`config.yaml.restored`, **not** applied.

```bash
kushim restore /path/to/edub-backup-full-2026-06-30T02-00-00.tar.gz
kushim restore backup.tar.gz --dry-run
kushim restore backup.tar.gz --force --temp-dir /mnt/scratch
```

| Flag | Description |
|---|---|
| `--force` | Skip the confirmation prompt and the running-daemon check |
| `--dry-run` | Validate the archive without changing anything |
| `--temp-dir` | Extraction staging directory (default: next to `storage.storage_dir`) |

The restore validates the archive and manifest, pre-flights the restore tooling and disk
space, refuses while the queue daemon is running (unless `--force`), extracts to staging,
executes the SQL dump via `psql` (skipped for `documents`-mode archives), rewrites storage
paths in the database to the current storage directory, swaps storage into place (atomic rename
on the same filesystem, copy-then-swap across devices), and prints restart instructions.

`psql` runs per `database.runtime`: `host` (needs `psql` on PATH), `docker`/`podman` (executed
inside `database.container`), or `remote` — which refuses before touching anything, telling you
to unzip and run `psql -f edub.sql` manually on the remote host. Archives predating backup
modes have no `mode` in the manifest and restore as `full`.

### `kushim migrate`

Run a migration from the CLI without enqueueing. Both subcommands coordinate through the
resource locks (`migrate database` refuses while `maintenance` is held; `migrate storage` takes
`maintenance` and `storage`, `--no-wait` exits non-zero instead), drain in-flight tasks, and
write the new settings to `config.yaml` only after the operation succeeds.

#### `kushim migrate database`

Copy the current database to a new PostgreSQL server and point `config.yaml` at it: safety
snapshot, SQL dump (`goose_db_version` preserved), destination validation (refuses a populated
non-edub database; skips restore when the destination already holds data), restore via `psql`
per `database.runtime`, then a deferred config update.

| Flag | Description |
|---|---|
| `--host`, `--port`, `--user`, `--database` | Destination (required) |
| `--password` | Destination password (required — or set `KUSHIM_DB_PASSWORD`) |
| `--sslmode` | Default `disable` |

A password on the command line is visible in process listings and shell history; prefer
`KUSHIM_DB_PASSWORD`.

#### `kushim migrate storage`

Relocate the storage and consumption directories: rewrite pending consume payloads and database
paths (`document.storage_path`, `document.original_path`, `orphaned_file.file_path`), move the
storage subdirs and inbox files per `storage.migration_mode` (`copy` — copy-then-delete,
default — or `move`), and persist the new directories. At least one of `--storage-dir` or
`--consumption-dir` is required; paths under `/proc`, `/sys`, `/dev` or `/boot` are rejected;
an unchanged pair reports a no-op. Run `migrate database` first — storage migration reads the
updated config (including the new database connection) from disk.

Queued equivalents: `kushim batch create migrate-db` / `migrate-storage`. Those run under
execution deadlines (30 minutes for `migrate-db`, 2 hours for `migrate-storage`) so a blocked
run fails into the retry path instead of hanging; direct subcommands run without a deadline.
Storage migrations resume naturally after an abort.

## Storage maintenance

### `kushim storage orphans <action>`

Manage orphaned files — files in the storage tree whose document row no longer exists (or was
never created):

| Action | Effect |
|---|---|
| `list` | List pending orphaned files (id, key, source, path, status) |
| `scan` | Run detection and quarantine |
| `delete --id <n>` | Delete one pending orphan |
| `restore --id <n>` | Restore one orphan to storage (enqueues the restore batch) |
| `move-to-inbox --id <n>` / `--all` | Move one, or all pending orphans, back to the inbox |
| `delete-all` | Delete all pending orphans |

```bash
kushim storage orphans scan
kushim storage orphans list
kushim storage orphans restore --id 7
```

The same surface exists in the UI on the orphaned-files page
([web-ui.md](web-ui.md#orphaned-files)).

### `kushim storage thumbnails cleanup [--dry-run]`

Remove thumbnail files in `storage/thumbnails/` whose document no longer exists, then prune
emptied date directories. Files modified within the last 30 seconds are skipped to avoid
racing an in-flight thumbnail task. `--dry-run` lists without removing. The same sweep runs on
every hourly trash purge.

## Users, tasks and search

### `kushim user create`

Create a user with a role. Connects directly to the database — no authentication required
(CLI-only). Useful to bootstrap admin access after setup.

```bash
kushim user create --username admin --password 'secret123!' --role admin
```

| Flag | Required | Default | Description |
|---|---|---|---|
| `--username` | prompted | — | Username |
| `--password` | prompted | — | Password (masked prompt with confirmation when omitted) |
| `--role` | no | `admin` | `admin`, `editor`, or `viewer` |

The server enforces the same password policy as the API (12 characters minimum with
uppercase, lowercase, digit and special character). A duplicate username or invalid role
errors with the reason. User management continues in the UI (Settings → Access) or over the
API ([api.md](api.md)).

### `kushim task list` / `kushim task status <task-id>`

```bash
kushim task list
kushim task list --batch 550e8400-e29b-41d4-a716-446655440000 --status failed
kushim task list --type enrich --limit 50
```

| Flag | Default | Description |
|---|---|---|
| `--batch` | all | Filter by batch UUID |
| `--status` | all | `waiting`, `pending`, `processing`, `completed`, `failed`, `cancelled`, `discarded`, or `active` (pending/processing/waiting) |
| `--type` | all | `consume`, `enrich`, `thumbnail`, `thumbbackfill`, `backup`, `mirror`, `tagreconcile`, `tagmeasure`, `tagidentity`, `tagaudit`, `tagmaintenance`, `migrate-db`, `migrate-storage` |
| `--limit` | `20` | Max results (1–100) |
| `--offset` | `0` | Pagination offset |

```text
TASK ID                              TYPE     STATUS       BATCH        FILE
--------------------------------------------------------------------------------
660e8400-e29b-41d4-a716-446655440001 consume  completed    550e8400-e2… report.pdf
880e8400-e29b-41d4-a716-446655440003 consume  failed       550e8400-e2… contract.pdf
```

`task status <task-id>` prints one task's detail (type, batch, status, file, timestamps,
document id, error when failed). Recover a failed task by resuming its batch:
`kushim batch restart <batch-id>`.

### `kushim search`

Full-text search across indexed documents (PostgreSQL tsvector).

```bash
kushim search "budget report"
kushim search --limit 10 --offset 0 "quarterly earnings"
kushim search --rebuild-index
```

| Flag | Default | Description |
|---|---|---|
| `--limit` | `20` | Max results (1–100) |
| `--offset` | `0` | Pagination offset |
| `--rebuild-index` | `false` | Reindex the tsvector GIN index |

The query goes through `plainto_tsquery('simple', …)` (tokens joined with `&`); matches are
highlighted in the output.

### `kushim version`

Print the application version:

```text
$ kushim version
Document Management System v4.0.2
```

## Running as a system service

`kushim setup` (wizard and CLI) writes concrete, pre-filled service files to
`<config-dir>/systemd/`. Template (`@.service`) files for server deployments live in
`deploy/systemd/`.

| File | Process |
|---|---|
| `kushim-hugot.service` | Matcher (`kushim hugot`) — `Type=notify` |
| `kushim-queue.service` | Batch queue daemon (`kushim queue`) |
| `edub.service` | API server (`edub`) |
| `edub-kushim.target` | Groups all three via `PartOf=` |

### Generated files (recommended)

```bash
# 1. dedicated system user
sudo useradd -r -m -d /var/lib/edub-kushim -s /usr/sbin/nologin edub

# 2. binaries
sudo cp dev/bin/kushim /usr/local/bin/
sudo cp dev/bin/edub   /usr/local/bin/

# 3. initialize the config as that user (path printed at the end of setup)
sudo -u edub kushim setup --cli --languages eng,spa

# 4. install and start everything
sudo cp /var/lib/edub-kushim/.config/edub-kushim/systemd/* /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now edub-kushim.target
```

### Template files (server deployments)

For explicit control use the templates in `deploy/systemd/` — `%i` is the instance name (the
system user) and expands to `User=`, `Group=`, `Environment=HOME=…` and the socket path:

```bash
sudo cp deploy/systemd/*.service deploy/systemd/*.target /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now edub-kushim@edub.target
```

Non-standard home directories (e.g. `/var/lib/edub-kushim`) need a drop-in overriding
`Environment=HOME`.

Generated files resolve user, home and binary paths from the running setup; templates hardcode
`/home/%i` and `/usr/local/bin/`. Both run under a non-root system account when configured
correctly; `ProtectSystem=full` is safe when the writable paths (config, storage, inbox, logs)
live under the user's home.

### Dependencies and order

```text
                    edub-kushim.target
                    Wants & After
                   /        |        \
                  ▼         ▼         ▼
      kushim-hugot   kushim-queue      edub
      (Type=notify)  (After/Wants      (After/Wants
                      hugot)            hugot)
```

`kushim-hugot` notifies readiness (`sd_notify(READY=1)`) only after the model is loaded, the
tag cache is built and the socket is listening — dependents wait for actual readiness, not just
process start. They use `Wants=` (not `Requires=`) so the stack degrades gracefully without the
matcher: tag CRUD still succeeds and only the embedding cache is skipped until it returns. The
queue daemon waits for the matcher because it forks workers that call matcher RPC immediately,
avoiding the connection-refused race on first boot. `systemctl enable --now edub-kushim.target`
manages all three as one unit.

### Timeouts and memory

The matcher service carries `TimeoutStartSec=120` because model load plus tag-cache build can
take tens of seconds (~40s for 10k tags); for 100k+ tags raise it via a drop-in:

```ini
[Service]
TimeoutStartSec=600
```

The matcher holds the embedding model in RAM (~2.2–2.5 GB idle with BGE-M3 and the default
`chunk_size`). Cap it so a misconfigured matcher can only kill itself:

```ini
[Service]
MemoryHigh=6G
MemoryMax=8G
```

Adjust to your `chunk_size` (see the
[semantic matching guide](../developer-guide/semantic-matching.md)).

### Logging

Each process writes its own log under `<config-dir>/logs/`:

| Process | Log file |
|---|---|
| `kushim` (CLI) | `kushim.log` |
| `kushim hugot` (matcher) | `hugot.log` |
| `kushim queue` (daemon) | `queue.log` |
| `edub` (API server) | `edub.log` |

The same four logs are tailed in the UI's log viewer ([web-ui.md](web-ui.md#logs)). systemd
additionally captures stdout/stderr in the journal (`journalctl -u edub.service`, …), including
startup messages emitted before the log file opens. Log files rotate at the configured size
(default 100 MB) with gzip-compressed backups, count and age governed by `app.logging`
([configuration.md](configuration.md)).

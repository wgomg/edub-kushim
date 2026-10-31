# edub-kushim — Configuration Reference

The configuration is one YAML file, `~/.config/edub-kushim/config.yaml` (the directory comes
from `kushim setup`). A fully commented example with every supported key and its default
lives at [`config.example.yaml`](../config.example.yaml) — that file is the authoritative key
list; this document explains what the sections do, how they interact, and how to tune them.

**Three ways to edit**, all equivalent — changes are picked up live (both `edub` and
`kushim queue` watch the file and reconnect where needed; only the matcher's startup values
need a `kushim hugot` restart, see [Timeouts](#timeouts)):

- **Settings UI** (admin): staged edits applied together with **Apply Changes** —
  [web-ui.md](web-ui.md#settings).
- **CLI**: `kushim config` git-style get/set, atomic and validated —
  [cli.md](cli.md#kushim-config).
- **Direct edit**: any editor; run `kushim config --validate` afterwards.

Writes are validated before they replace the file (an invalid value leaves it untouched), and
`kushim setup` refuses to run over an existing file.

## `app`

| Key | Default | Meaning |
|---|---|---|
| `environment` | `development` | `development` or `production`. Outside development the session cookie becomes `Secure` |
| `log_level` | `info` | `silent`, `fatal`, `error`, `warn`, `info`, `debug` |
| `logging.max_size` | `100` | MB per log file before rotation |
| `logging.max_backups` | `7` | Rotated files to keep |
| `logging.max_age` | `30` | Days before an old rotated file is deleted |
| `logging.compress` | `true` | Gzip rotated files |

## `server`

| Key | Default | Meaning |
|---|---|---|
| `host` / `port` | `0.0.0.0` / `3000` | Listen address for the API and web UI |
| `read_timeout` / `write_timeout` / `idle_timeout` | `60` | HTTP connection lifecycle — see [Timeouts](#timeouts) |
| `max_concurrent_batches` | `4` | Worker-process budget enforced by the queue daemon; re-read on every dispatch sweep, so edits apply to the running daemon without a restart |
| `max_upload_size` | `100` | Max multipart upload size, MB |
| `max_download_files` | `50` | Max documents in one batch download |
| `max_download_size_mb` | `500` | Max total batch-download size, MB |
| `max_batch_delete` | `50` | Max documents in one batch delete |
| `session_secret` | *(empty)* | 64-char hex key for JWT signing; auto-generated when empty (logged as a warning) |
| `auth_enabled` | `true` | Setup writes `false` on first run. When `false`, every request is treated as an authenticated admin and there is no login screen ([api.md](api.md#roles-public-endpoints-and-auth-disabled-mode)) |

## `database`

PostgreSQL connection — either the discrete fields (`host`, `port`, `user`, `password`,
`database`, `sslmode`) or a single `dsn` (which overrides them):

```yaml
database:
  host: localhost
  port: 5432
  user: edub
  password: edub
  database: edub
  sslmode: disable
  runtime: host     # host | docker | podman | remote
  # container: edub-postgres   # required for docker/podman
```

`runtime` decides **where `psql` runs** for restore and migration operations: on the host
(`host` — requires `psql` on PATH), executed inside `container` (`docker`/`podman`), or
`remote`, where automated restore refuses and you run `psql -f edub.sql` by hand on the
target. Changing `database.*` from the settings UI is deferred through a `migrate-db` task
([api.md](api.md#settings-apply)); `edub` and `kushim queue` reconnect automatically once it
applies.

## `storage`

| Key | Default | Meaning |
|---|---|---|
| `consumption_dir` | `<config-dir>/inbox` | The inbox that consume scans and uploads land in |
| `storage_dir` | `<config-dir>/storage` | Root of the document tree (`originals/`, `processed/`, `errors/`, `trash/`, `thumbnails/`, `orphaned/`) |
| `migration_mode` | `copy` | How directory changes relocate files: `copy` (copy-then-delete, safe, needs the space) or `move` (rename, falls back to copy across filesystems) |
| `trash.retention_days` | `30` | Days before soft-deleted documents are purged by the hourly sweep |

Changing either directory from the settings UI is deferred through a `migrate-storage` task;
the CLI equivalents are `kushim migrate storage` ([cli.md](cli.md#kushim-migrate-storage)).

## `consumer`

The ingestion pipeline: which files are accepted and which tools process them.

- `workers` (default `1`) — concurrent file-processing workers per batch run.
- `max_files_per_batch` (default `10`) — cap per consume batch; `0` = unlimited. Also the
  bulk-selection size for `kushim enrich` without arguments.
- `supported_files` — accepted extensions (built-in default `['.pdf']`; the setup wizard
  records your selection). Uploads are checked against detected MIME types with the same set.
- `polling` — the daemon's inbox scan: `enabled` (default `false`), `interval` (minutes),
  and optional `windows` (start/end `HH:MM` pairs restricting scans to time ranges). The
  scan skips its tick while batches are credit-paused
  ([cli.md](cli.md#paused-batch-guard)).
- `reclaim` — crash recovery: `enabled` (default `true`), `max_retries` (`3`),
  `stale_task_after` (seconds, `600`) after which a processing task counts as stale.
- `converter` — DOCX/ODT → PDF via LibreOffice headless: `enabled` (default `false`),
  `binary` (name on PATH or full path), `timeout` (seconds).
- `textextractor` — engine `mupdf` | `gopdf` | `pdftotext`, `timeout`.
- `pdfoptimizer` — engine `mupdf` | `gs`, `timeout` (`0` = disabled), optional `fallback`
  optimizer tried when the primary fails.
- `ocr` — engine `gosseract` (built-in, default) | `ocrmypdf` (external); `languages`
  (**required** — ISO 639-3, set by `kushim setup --languages`); optional `data_dir`
  (tessdata location); `timeout`; `ocr_workers` (parallel page workers per document —
  `0`/unset = auto: `CPU cores / (max_concurrent_batches × consumer.workers)`).
- `thumbnail` — `enabled` (default `true`), engine (`mupdf`, the implemented one), `dpi`
  (`72`), `max_width` (`400` px; height follows a fixed 3:4 crop), `quality` (`80`,
  JPEG 1–100), `timeout` (`30`), `workers` (`1`), and the scheduled backfill:
  `backfill_interval` (days, `0` = disabled) with `backfill_time` (`HH:MM`, default
  `02:00`) — the daemon enqueues an `--all` sweep when due
  ([cli.md](cli.md#kushim-thumbnails-mode)).

The tools these engines expect are checked before ingest runs — see the
[pre-flight tool check](cli.md#pre-flight-tool-check) and the settings UI's warnings.

## `enricher`

The classification pipeline. `workers` (default `1`) runs enrich tasks concurrently.

- `textreducer` — `textrank` engine (local, CPU-only), `timeout`, `target_words` (`2000`)
  — text is reduced before it reaches the LLM.
- `contentanalyzer` — the LLM classification step, **disabled by default**:
  - `timeout`, `min_tags` (lower bound requested from the analyzer; default `3`,
    validated `1–20`), `pause_on_credit_error` (default `true` — a provider credit error
    pauses the batch), `prompt_template` (custom Go template; empty = built-in),
    `doc_type_refinement` (`enabled`, `head_words` `600`, `tail_words` `400` — a second
    sequential LLM pass over the head and tail of the raw text).
  - `llm` — `adapter` (`openai-compatible` | `anthropic`), `provider`, `model` (both from
    the model catalog), `token`, `temperature`, `request_delay` (seconds to sleep after
    each request; default `1`, `0` = off, max `60` — for rate-limited providers).
  - `fallbacks` — an **ordered list** of `{enabled, llm}` entries tried when the primary
    fails with a provider error (API unreachable, credit/rate-limit, malformed response,
    provider timeout). Disabled entries are not validated; each enabled one requires
    adapter/provider/model.
- `tagmatcher` — semantic matching through the Hugot model: `timeout`,
  `reduce_target_words` (`4000`), `chunk_size` (**`4096`** — the memory lever: `0` uses the
  model's full context, `8180` tokens for BGE-M3, and can OOM small hosts),
  `min_suggestion_similarity` (injection bar, `0.52` per-model default), `top_n` (`15`
  suggestions reaching the analyzer, minimum `3`), and `hugot` (`model`
  `BAAI/bge-m3`, `backend` `ort` | `GO`). Full tuning: the
  [semantic matching guide](../developer-guide/semantic-matching.md).
- `tag_adjudicator` — optional LLM verdicts for tag identity and the language guard
  (`enabled` default `false`; `timeout` falls back to `contentanalyzer.timeout` when `0`;
  an empty `provider`/`model` inherits the content analyzer's LLM at use time).
- `tag_quality` — the admission judge for newly emitted tags (`enabled` default `false`,
  same timeout/LLM inheritance; the judge model should differ from the generator's).
- `tag_reconcile` — scheduled re-adjudication: `enabled` (`false`), `report_only`
  (`true` — the first run is always report-only), `interval` (days, `7`), `time`
  (`02:00`), `max_rejudge` (`200`), `max_retag` (`1000`), `dead_tag_grace_days` (`7`),
  `provisional_grace_days` (`30`).
- `tag_measure` — `enabled` (`false`), `interval` (days, `7`), `time` (`03:00`),
  `after_enrichment_batch` (`false` — additionally measure after every consume/enrich batch
  settles).
- `tag_audit` — `enabled` (`false`), `interval` (days, `7`), `time` (`04:00`),
  `sample_size` (`150` documents per run).
- `tag_maintenance` — the chained routine (reconcile → identity → audit → measure):
  `enabled` (`false`), `interval` (days, `7`), `time` (`02:00`). While enabled, the
  individual reconcile/measure/audit schedules idle; the daily identity pass keeps its own
  cadence.

Behavior of the five tag passes (direct vs queued, gating, no-op conditions) is documented
in [cli.md](cli.md#tag-passes); the underlying rules live in the
[tag specs](../specs/tags/overview.md) and the
[consolidation guide](../developer-guide/consolidation.md).

## `backup`

```yaml
backup:
  enabled: true
  path: /var/backups           # fallback directory (manual CLI uses it too)
  schedules:                   # each mode may appear at most once
    - mode: full               # edub.sql + storage + config
      interval: 7              # days
      time: "03:00"
      keep: 4                  # archives retained per mode (0 = unlimited)
    - mode: database
      interval: 1
      time: "02:00"
      keep: 30
    - mode: documents
      interval: 3
      time: "02:30"
      keep: 10
      path: /mnt/nas/documents # optional per-schedule output override
```

Validation: `enabled: true` without at least one schedule is a configuration error. The
deprecated flat `interval`/`time`/`keep` fields are converted into a single `full` schedule.
Manual `kushim backup` runs never apply retention — only schedules prune
([cli.md](cli.md#kushim-backup)).

## `mirror`

`enabled`, `path` (local path or `[user@]host:path`), `interval` (days), `time` (`HH:MM`).
`enabled: true` without a `path` is a configuration error. The mirror runs
`rsync -a --delete` — deletions propagate to the destination — and needs `rsync` on PATH
(passwordless ssh for remote targets) ([cli.md](cli.md#kushim-mirror)).

## How schedules behave

Every scheduled job — backups (per mode), mirror, thumbnail backfill, and the tag passes —
anchors its cadence on the **most recent terminal batch of its schedule name** (completed,
failed or cancelled). Changing an interval or preferred time therefore does not trigger a
catch-up run: the new timing takes effect at the first occurrence after that anchor, with the
previous interval already elapsed. Daemon restarts do not reset any schedule.

## Timeouts

Every pipeline timeout is a deadline for one stage and accepts **`0` to disable** it —
useful while troubleshooting, but a hung subprocess or unresponsive provider can then hold a
worker indefinitely, so production deployments should keep a positive value everywhere.

### HTTP server timeouts

`server.read_timeout`, `write_timeout`, `idle_timeout` (default `60` seconds each) guard the
connection, not processing:

| Field | Guards | Premature below |
|---|---|---|
| `read_timeout` | Receiving the full request (headers + body) — a large multipart upload needs headroom | ~5s |
| `write_timeout` | Sending the full response (large search/batch JSON can take seconds) | ~5s |
| `idle_timeout` | How long a keep-alive connection may idle | ~1s |

The default 60 suits most deployments; lower `read_timeout` only if you control upload
speeds and want tighter slow-loris protection.

### Pipeline stage timeouts

| Key | Default | Minimum practical | Premature below |
|---|---|---|---|
| `consumer.textextractor.timeout` | 120s | 5s | 5s |
| `consumer.ocr.timeout` | 120s | 10s | 10s |
| `consumer.pdfoptimizer.timeout` | 0 (disabled) | 5s | 5s |
| `consumer.converter.timeout` | 300s | 30s | 10s |
| `consumer.thumbnail.timeout` | 30s | — | — |
| `enricher.textreducer.timeout` | 120s | 10s | 10s |
| `enricher.contentanalyzer.timeout` | 120s | 10s | 10s |
| `enricher.tagmatcher.timeout` | 120s | 10s | 10s |

What drives each stage's real duration:

- **Text extraction** — native PDFs extract in milliseconds; `pdftotext` on large complex
  PDFs may need 30–60s. Scanned pages return empty text instantly and hand off to OCR.
- **OCR** — scales with page count, page complexity, and the number of languages. One
  typical page takes 3–10s (gosseract renders at 200 DPI, then Tesseract per page), so the
  default 120s covers roughly 12–40 pages. Values below 10s time out even single-page
  documents. Size it from your largest documents — at least 2× the observed maximum task
  duration.
- **PDF optimizer** — CPU-bound recompression (5–15s for a single image-heavy page); the
  timeout caps **each attempt**, so a configured fallback gets its own fresh budget.
- **Converter** — LibreOffice loads fonts, layout and renderer: 2–5s for a simple DOCX,
  60–120s for heavy 100-page documents. On `context deadline exceeded` from the pipeline,
  raise this before any other stage.
- **Text reducer** — local TextRank: milliseconds for typical documents, 1–5s for 200–300
  pages; 120s is generous.
- **Content analyzer** — provider latency plus input size; doc-type refinement makes two
  sequential calls inside the same budget, and `llm.request_delay` sleeps inside it too —
  keep both well under the timeout. When a fallback LLM runs, it shares the **same**
  per-pass deadline rather than doubling it, so a slow primary leaves the fallback little
  room: raise the timeout or lower `request_delay` for fallback-heavy setups.
- **Tag matcher** — scales with the tag store and text length (`reduce_target_words`):
  ~3–10s at 1k tags, ~10–60s at 10k, and 120–300s at 100k+. The matcher server mirrors
  this value into its socket write deadline **at startup** — restart `kushim hugot` after
  changing it.

> **Matcher memory**: the matcher is the largest single consumer in the stack. With the
> default `chunk_size: 4096`, BGE-M3 idles at ~2.2–2.5 GB and peaks around 4–6 GB per
> request; `chunk_size: 0` (full 8180-token context) can exceed 10 GB and OOM small hosts.
> See the [semantic matching guide](../developer-guide/semantic-matching.md).

### Tuning approach

1. Start from the defaults and run a typical workload.
2. Watch failed tasks for `context deadline exceeded` — the message names the stage
   (e.g. `ocr: context deadline exceeded`).
3. Compare actual durations (`kushim task list --batch <id>`, or the dashboard) against the
   timeout.
4. Set that stage's timeout to at least 2× the observed maximum — or lower it when the
   observations show it is excessively generous.

A well-tuned set catches real hangs without failing on normal variation; no single number
fits every workload.

## See also

- [`config.example.yaml`](../config.example.yaml) — every key with its default
- [web-ui.md](web-ui.md#settings) — editing through the settings UI (staged apply)
- [cli.md](cli.md#kushim-config) — editing from the terminal
- [api.md](api.md#settings-apply) — the same flow over REST
- [Timeouts](#timeouts) above; [semantic matching](../developer-guide/semantic-matching.md)
  and [consolidation](../developer-guide/consolidation.md) for deep tuning

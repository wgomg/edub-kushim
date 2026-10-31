# edub-kushim — User Manual

Two binaries make up edub-kushim: **kushim**, the CLI that owns document processing, the tag
matcher server and the batch queue daemon, and **edub**, the HTTP API server that hosts the web
UI. Both share the same OCR, text-extraction and PDF-optimization pipeline.

## The pieces

- **`edub`** — REST API and web UI. It accepts uploads and consume requests, enqueues them as
  batches (`queued`) in PostgreSQL, and serves the UI. It never runs pipeline work itself.
- **`kushim queue`** — the batch daemon. It claims queued batches and forks one
  `kushim --executor … --from-batch <id>` child process per batch; it also runs the inbox
  polling scan when enabled in the configuration.
- **`kushim hugot`** — the tag matcher (semantic embeddings) served over a Unix socket. It must
  be running before `edub` starts, otherwise tag operations answer `503`.
- **Direct CLI runs** (`kushim consume`, `kushim enrich`, …) do the work immediately in the
  current process, without creating batches or task rows.

For the design behind this split, see [Architecture](../architecture.md).

## Which document do I need?

| I want to… | Read |
|---|---|
| Install and try the system | [Quick Start](#quick-start) below |
| Look up a CLI command or operate the pipeline | [cli.md](cli.md) |
| Call the REST API from a script or client | [api.md](api.md) — usage, auth and workflows |
| Look up one endpoint, its parameters or schema | [Generated API reference](../api.md) |
| Use the web interface | [web-ui.md](web-ui.md) |
| Edit the configuration file or tune timeouts | [configuration.md](configuration.md) |
| Understand how tasks and batches behave | [Tasks and batches](#tasks-and-batches) below |
| Understand the design and pipeline | [Architecture](../architecture.md) |

## Quick Start

### Docker Compose (no host-side toolchain required)

```bash
git clone <repo-url> && cd edub-kushim
# Set your OCR language(s) — OCR_LANGUAGES (ISO 639-3, comma-separated) in docker-compose.yml
docker compose up
```

Open http://localhost:3001 (the container listens on 3000). On first boot, with no config file
present, the entrypoint runs `kushim setup --cli --languages …`, which writes the configuration
and applies database migrations and seeders.

You provide PostgreSQL yourself: point `DB_DSN` in `docker-compose.yml` at your server (the
`database` section of the configuration is the equivalent for non-containerized installs).

Two modes:

- **External database (default)** — `docker compose up` runs the `edub` container only.
- **Bundled PostgreSQL (quickstart)** —
  `docker compose -f docker-compose.yml -f docker-compose.quickstart.yml up --build` (or
  `make compose-quickstart`) also starts a PostgreSQL 17 container with a health check. On first
  boot the entrypoint creates the database, migrations, seeders and an admin account
  (`admin`/`admin` — change these beyond local evaluation). Data persists in the `edub-db-data`
  volume; the database is not exposed on the host. Requires Docker Compose v2.

> **Note** — the container runs `edub` only. Uploading files or calling `POST /api/v1/consume`
> enqueues work; processing it additionally requires the `kushim queue` daemon, and tag matching
> requires `kushim hugot`. The image ships both binaries — see [cli.md](cli.md) for how to run
> them.

### Pre-built binaries (Linux amd64/arm64)

Download the latest release from
[GitHub Releases](https://github.com/wgomg/edub-kushim/releases) — no host toolchain required:

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

Use the `_arm64` tarballs on ARM64 hosts. Binaries are built on Ubuntu 24.04 runners against
glibc; for other environments, build from source (see the
[Development section of the README](../README.md#development)).

### Manual

```bash
# One-time setup — launches the web wizard at http://0.0.0.0:8420
kushim setup

# Or terminal-based setup (headless / CI)
kushim setup --cli --languages eng,spa

# Start the supporting processes, then the API server
kushim hugot &    # tag matcher — must be up before edub
kushim queue &    # batch daemon — processes enqueued batches
edub              # API server + web UI on http://localhost:3000
```

Process documents either immediately, in the foreground:

```bash
cp my-documents/*.pdf ~/.config/edub-kushim/inbox/
kushim consume            # processes the inbox now; creates no batch or task rows
```

or enqueue them for the daemon instead:

```bash
kushim batch create consume    # queued; picked up by `kushim queue`
```

Both paths and every other verb are documented in [cli.md](cli.md).

## Tasks and batches

Every ingested file produces linked tasks: a **consume** task (text extraction, OCR when needed,
PDF optimization, checksum, storage, database record) with a **waiting enrich** child — and, when
thumbnails are enabled, a **waiting thumbnail** child. Enrichment (text reduction, LLM content
analysis, semantic tag matching) never starts before its consume task has completed: the
successor stays `waiting` until its parent activates it.

### Task statuses

| Status | Meaning |
|---|---|
| `waiting` | Parked until the parent task completes and activates it |
| `pending` | Ready for a worker to claim it |
| `processing` | Currently running in a worker |
| `completed` | Finished successfully |
| `failed` | Terminal failure; retryable through batch resume/restart |
| `cancelled` | Cancelled by the user |
| `discarded` | A successor whose parent failed (or a duplicate): its whole waiting branch goes with it, without counting as a failure. Restored to `waiting` when the parent is retried |

### Batch statuses

| Status | Meaning |
|---|---|
| `queued` | Created, waiting for the daemon (or a direct run) to pick it up |
| `processing` | A worker process is executing it |
| `completed` | All tasks finished successfully |
| `failed` | One or more tasks failed |
| `paused` | Held after an LLM provider credit/balance error — resolve billing, then resume or cancel |
| `cancelled` | Cancelled; terminal |

Watch them in the UI on the [tasks screen](web-ui.md#tasks), from the CLI with
`kushim task list` / `kushim batch list` ([cli.md](cli.md)), or over the API
([api.md](api.md)). The full lifecycle — activation, retries, reclamation and the queue
semantics — is explained in the
[task system guide](../developer-guide/task-system.md).

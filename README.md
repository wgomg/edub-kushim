# edub-kushim

A self-hosted document management system with automatic classification via LLMs.

Two binaries: **kushim** (CLI) for batch document processing and **edub** (server) for the REST API and web UI.

## Quick Start

### Docker Compose (recommended)

```bash
git clone <repo-url> && cd edub-kushim
# Set your OCR language(s) in docker-compose.yml, then:
docker compose up
```

Open http://localhost:3000, configure your LLM provider in `/settings`, and drop PDFs into `./inbox/`.

The first build compiles MuPDF, Tesseract, and other C libraries from source (~minutes); subsequent builds are cached by Docker.

### Manual (for development)

```bash
# Initial setup — creates config, downloads models, initializes DB
kushim setup

# Add documents to your inbox directory, then:
kushim consume

# Start the API server + web UI
edub
```

The `setup` command walks you through the required configuration (inbox path, storage path, LLM provider). After that, drop PDFs into your inbox and run `kushim consume` — documents are automatically processed, OCR'd if needed, and classified.

## How It Works

```
Inbox → Extract text → OCR (if scanned) → Optimize PDF → Store → Enrich
                                                                    │
                                                    ┌───────────────┘
                                                    ▼
                                            TextRank → Tag matching → LLM → Consolidate
```

Documents are searchable immediately via FTS5; enrichment (classification, tagging, people extraction) happens asynchronously.

## Documentation

| If you want to...                        | Start here                                    |
| ---------------------------------------- | --------------------------------------------- |
| Understand the design and pipeline       | [Architecture](docs/architecture.md)          |
| See what's done and what's next          | [Roadmap](docs/roadmap.md)                    |
| Find a specific package or function      | [Code Reference](docs/reference/overview.md)  |

## Key Features

- **LLM-powered classification** — automatic tags, document type, title, and people extraction via OpenAI, Anthropic, DeepSeek, or Ollama
- **Semantic tag matching** — Hugot embeddings with cosine similarity (Go or ONNX backend)
- **OCR pipeline** — Tesseract + MuPDF for image-only PDFs, with searchable PDF output
- **Full-text search** — SQLite FTS5 with sanitized query layer
- **Async processing** — task queue with worker pools, batch tracking, progress polling
- **Dual storage** — originals preserved alongside processed/OCR'd versions
- **Web UI** — SvelteKit SPA (optional, served by the edub binary)

## Development

### Prerequisites

- Go 1.22+
- gcc, gcc-c++, make, autotools, git, curl
- Node.js 24 (use [nvm](https://github.com/nvm-sh/nvm) — `.nvmrc` specifies the version, run `nvm use`)

### Build

```bash
# 1. Compile static C libraries (MuPDF, Tesseract, Leptonica, libpng)
make build-deps

# 2. Build Go binaries (kushim + edub) with required build tags
make build

# 3. Build everything including the web UI (requires Node.js)
make web-build && make build
```

The `Makefile` also provides containerized builds (`make build-glibc`, `make build-musl`) and a combined deployment image (`make build-tools-image`).

### Test

```bash
go test -tags "XLA,ORT" ./...
```

### Web UI (hot-reload)

When working on the SvelteKit frontend, use the dev server for live reloading instead of rebuilding the embedded static files each time:

```bash
cd web
npm ci
npm run dev     # starts at localhost:5173, proxies API to localhost:3000
```

For production, compile the UI and embed it in the Go binary:

```bash
make web-build  # builds to internal/static/build/
```

### Code generation

After modifying SQL queries in `internal/database/sql/queries/`:

```bash
sqlc generate
```

This regenerates the type-safe query methods under `internal/database/*.sql.go`.

### Configuration reference

A commented example config with all supported keys is available at [`config.example.yaml`](config.example.yaml) in the project root. Used as a reference for manual configuration or after `kushim setup` generates the initial file at `~/.config/edub-kushim/config.yaml`.

### Build Dependencies (C libraries)

Building from source requires compiling four static libraries from source (automated via `make build-deps`):

- **libpng** 1.6.43
- **Leptonica** (latest)
- **Tesseract** (latest)
- **MuPDF** 1.27.2
- **libtokenizers** (for Hugot Go backend)

See the [overview reference](docs/reference/overview.md) for the full dependency chain. Pre-built binaries are planned.

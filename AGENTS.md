# AGENTS.md

Deeper context lives in `docs/`: `architecture.md` (design, pipeline, process model), `roadmap.md` (implementation status), `user-manual.md` (CLI + API reference), and `reference/` (per-package code references — database, search, pipeline, task-system, tools, tests).

## Build

- **Never omit build tags.** All `go build`, `go test`, and `go fix` require `-tags "XLA,ORT"`. Non-negotiable.
- Build C deps first: `make build-deps`
- Build both binaries: `make build` → `dev/bin/kushim` + `dev/bin/edub`
- When the embedded SPA is needed: `make web-build && make build` (order matters — web-build must run first)
- Containerized builds: `make build-glibc`, `make build-musl`, `make build-tools-image`
- Go 1.26, module `github.com/wgomg/edub-kushim`
- `make fix` runs `go fix -tags "XLA,ORT" ./...`

## Two binaries, different CGo

- **`kushim`** — CLI, document processing, matcher server. Built with `CGO_ENABLED=1`. Links Tesseract, Leptonica, MuPDF, Hugot statically.
- **`edub`** — REST API server, web UI. Built with `CGO_ENABLED=0`. Pure Go, no C deps.
- The Makefile handles the split. If you invent a new build command, respect it.

## Process architecture

```
edub (CGO_ENABLED=0)── forks: kushim consume --batch <id> (per-batch processing)
                    └── Unix socket RPC: kushim hugot      (tag matcher)
```

- `edub` forks `kushim consume --batch <id>` as a child process when `POST /api/v1/consume` is called. `kushim` must be on PATH or sibling of `edub`.
- The matcher (`kushim hugot`) must be running before `edub` starts. Tag CRUD returns 503 otherwise.
- Socket: `<config-dir>/kushim-hugot.sock`.

## Config

- File: `~/.config/edub-kushim/config.yaml` · Example: `config.example.yaml`
- `kushim setup` → web wizard at `http://0.0.0.0:8420`
- `kushim setup --cli --languages eng,spa,...` → terminal mode
- Setup only runs when no config exists. Delete the config file to re-run.

## Code generation

After editing SQL in `internal/database/sql/queries/`:

```bash
sqlc generate
```

Regenerates `internal/database/*.sql.go`. Skipping this causes type mismatches.
Config: `sqlc.yaml` (v2, postgresql engine).

## Testing

**Most tests don't need CGo but require a PostgreSQL 16+ database.** Run one via Podman (preferred) or Docker:

```bash
# Podman (preferred)
podman run -d --name edub-test-pg \
  -e POSTGRES_USER=edub -e POSTGRES_PASSWORD=edub \
  -p 5432:5432 postgres:17

# Docker
docker run -d --name edub-test-pg \
  -e POSTGRES_USER=edub -e POSTGRES_PASSWORD=edub \
  -p 5432:5432 postgres:17
```

Set `TEST_DATABASE_URL` (omit to see which packages need it):

```bash
export TEST_DATABASE_URL="postgres://edub:edub@localhost:5432/edub?sslmode=disable"
```

Run tests with:

```bash
make test          # 60+ tests across 5 packages, CGO_ENABLED=0
make test-verbose  # same with -v
```

Manual equivalent:

```bash
CGO_ENABLED=0 TEST_DATABASE_URL="postgres://edub:edub@localhost:5432/edub?sslmode=disable" \
  go test -tags "XLA,ORT" -count=1 ./internal/database/ ./internal/search/ ./internal/task/ ./internal/api/handlers/ ./internal/consumption/
```

**Isolation**: Each test package gets its own database (`edub_test_<pkg_dir>`) via `runtime.Caller`. Databases are auto-dropped with `DROP ... WITH (FORCE)` when the last reference is released, so no manual cleanup is needed.

Covered: database queries, task lifecycle, search engine, API handlers, consumption pipeline
(with mock runner). Not covered: CLI commands, real OCR/PDF adapters.

The old `go test -tags "XLA,ORT" ./...` will fail without the full C toolchain installed.
See `docs/reference/tests.md` for full testing reference.

## Web UI (SvelteKit)

- Two SPAs: `web/` (main → `internal/static/build/`) and `web-wizard/` (setup wizard → `internal/wizard/static/`)
- Node.js 24 — `.nvmrc` specifies. Run `nvm use` first.
- Dev server: `cd web && npm ci && npm run dev` (proxies `/api` to `localhost:3000`)
- Both use `@sveltejs/adapter-static` with `fallback: 'index.html'`
- Svelte 5 with runes enabled everywhere. Tailwind CSS v4 via `@tailwindcss/vite`.
- Lint: `npm run lint` · Format: `npm run format`.

## Constraints

- `kushim consume` blocks if external tools are missing (`ocrmypdf` needs tesseract+unpaper; `pdftotext` needs poppler-utils).
- The `gosseract` OCR adapter uses MuPDF to render at 200 DPI and builds searchable PDFs with text rendering mode 3 (`3 Tr`).
- Hugot ORT backend downloads ONNX runtime on first use — needs internet. Go backend has no runtime deps.
- ORT defaults disable CPU memory arena and pattern pre-allocation (RSS ~2.2–2.5 GB vs ~4–5 GB). Toggle via `DefaultConfig` if latency matters more.
- Commit messages: Spanish, conventional commit format — see `~/.config/kilo/kilo.jsonc:commit_message`.
- sqlc config is at `sqlc.yaml` (v2, postgresql engine).

# AGENTS.md

Deeper context lives in `docs/`:

- `architecture.md` (design, pipeline, process model), `roadmap.md` (implementation status), `user-manual.md` (CLI + API reference)
- Developer guides (language/feature onboarding, in `docs/developer-guide/`):
  - `golang.md` — Go language features as used in this codebase
  - `frontend.md` — SvelteKit/Svelte 5 features as used in `web/` and `web-wizard/`
  - `postgresql.md` — PostgreSQL features as used in the schema and queries
  - `semantic-matching.md` — Hugot embedding model, tag matching, consolidation, the matcher daemon
  - `algorithms.md` — TextRank summarization, text normalization, token estimation
  - `cgo.md` — cgo and the C wrapper layer (MuPDF, Tesseract)
  - `ocr-pipeline.md` — OCR engines, searchable-PDF generation
  - `task-system.md` — task lifecycle, batch ownership, queue semantics
  - `llm.md` — LLM integration, prompts, model catalog, the enricher
- `reference/` (per-package code references — database, search, pipeline, task-system, tools, tests)

## Build

- **Never run bare `go build`/`go test`/`go fix`.** All builds and tests go through the
  Makefile, which always sets the `-tags "XLA,ORT"` tags and the CGo environment
  (`CGO_CPPFLAGS`, `CGO_LDFLAGS`, `PKG_CONFIG_PATH`). Non-negotiable.
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
edub (CGO_ENABLED=0)── enqueues batches (status='queued')
                    └── Unix socket RPC: kushim hugot (tag matcher)

kushim queue ── forks: kushim consume --batch <id> (per-batch processing)
```

- `edub` enqueues consume/enrich tasks with `status='queued'` when `POST /api/v1/consume` is called; the `kushim queue` daemon picks the batch up (Postgres LISTEN/NOTIFY + 30s safety timer) and forks `kushim consume --batch <id>` as a child process. `kushim` must be on PATH or sibling of `edub`.
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
make test          # 12 packages, no database needed, CGO_ENABLED=0
make test-verbose  # same with -v
make test-db       # 6 additional packages, requires PostgreSQL via TEST_DATABASE_URL
make test-backup   # backup package, requires PostgreSQL via TEST_DATABASE_URL
make test-cgo      # CGo-gated adapter tests (requires make build-deps first)
make test-one PKG=./internal/errs/   # single package; add RUN=Name to filter
```

**Isolation**: Each test package gets its own database (`edub_test_<pkg_dir>`) via `runtime.Caller`. Databases are auto-dropped with `DROP ... WITH (FORCE)` when the last reference is released, so no manual cleanup is needed.

Covered: database queries, task lifecycle, search engine, API handlers, consumption pipeline
(with mock runner). Not covered: CLI commands, real OCR/PDF adapters.

There is no supported bare `go test` invocation: the Makefile exports the CGo
environment and `-tags "XLA,ORT"` that bare invocations miss. See
`docs/reference/tests.md` for the full testing reference.

## Web UI (SvelteKit)

- Two SPAs: `web/` (main → `internal/static/build/`) and `web-wizard/` (setup wizard → `internal/wizard/static/`). The build dirs are gitignored but embedded via `//go:embed`; CI stages them with `make stage-web` (local builds) / `make stage-web-artifact` (downloaded CI artifact).
- Node.js 24 — `.nvmrc` specifies. Run `nvm use` first.
- Dev server: `cd web && npm ci && npm run dev` (proxies `/api` to `localhost:3000`)
- Both use `@sveltejs/adapter-static` with `fallback: 'index.html'`
- Svelte 5 with runes enabled everywhere. Tailwind CSS v4 via `@tailwindcss/vite`.
- Lint: `npm run lint` · Format: `npm run format`.

## Releases

`.github/workflows/release.yml` builds both binaries per arch (amd64 + arm64) and publishes a GitHub Release.

How to release:
1. Commit the version bump on `dev` (update `internal/version/version.go`, signed commit `chore(version): incrementa versión a X.Y.Z`), then merge into `master` locally: `git checkout master && git merge --ff-only dev && git push origin master`.
2. `git tag vX.Y.Z && git push origin vX.Y.Z` — the workflow publishes the release automatically: 4 tarballs (`kushim_linux_{amd64,arm64}`, `edub_linux_{amd64,arm64}`) + combined `checksums.txt`.

Manual runs (`workflow_dispatch` — only triggers from `master`):
- `publish: false` → build only, no release.
- `publish: true` + `tag_name` → draft release at that tag (the tag is auto-created at HEAD if missing).

Gotchas when touching this workflow:
- `download-tokenizers` pins per-arch sha256 in the Makefile; if upstream `latest/` moves, update `TOKENIZERS_SHA256_*`.
- C-deps cache key = `hashFiles('Makefile')` + arch; the cache only saves when the job succeeds.
- `upload-artifact` v4+ stores paths relative to their least common ancestor — the explicit staging step in the build job must stay.
- The `inputs` context keeps booleans typed: compare `inputs.publish == true`, not `== 'true'`.
- After editing the workflow, run `actionlint .github/workflows/release.yml` (and `act -l` if available).

## Constraints

- `kushim consume` blocks if external tools are missing (`ocrmypdf` needs tesseract+unpaper; `pdftotext` needs poppler-utils).
- The `gosseract` OCR adapter uses MuPDF to render at 200 DPI and builds searchable PDFs with text rendering mode 3 (`3 Tr`).
- Hugot ORT backend downloads ONNX runtime on first use — needs internet. Go backend has no runtime deps.
- ORT defaults disable CPU memory arena and pattern pre-allocation (RSS ~2.2–2.5 GB vs ~4–5 GB). Toggle via `DefaultConfig` if latency matters more.
- Commit messages: Spanish, conventional commit format — see `~/.config/kilo/kilo.jsonc:commit_message`.
- sqlc config is at `sqlc.yaml` (v2, postgresql engine).

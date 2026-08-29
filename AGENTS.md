# AGENTS.md

Deeper context lives in `docs/`:

- `architecture.md` (design, pipeline, process model), `roadmap.md` (implementation status),
  `user-manual.md` (CLI + API reference), `tag-matcher.md` (matcher design), `research/` (design notes)
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
  (`CGO_CPPFLAGS`, `CGO_LDFLAGS`). Non-negotiable.
- Fresh clones: the Go packages embed the SPA builds via `//go:embed`
  (`internal/static/build`, `internal/wizard/static` — both gitignored), so *any* Go
  build or test fails to compile until they exist. Build SPAs first:
  `make web-build wizard-build && make build` (order matters);
  if web/builds exist, `make stage-web` just copies them into place.
- Build C deps first (host toolchain needed): `make build-deps`
- Build both binaries: `make build` → `dev/bin/kushim` + `dev/bin/edub`
- Containerized (no host C toolchain): `make build-glibc`, `make build-musl`,
  `make build-tools-image`; CGo tests in containers: `make test-cgo-glibc`, `make test-cgo-musl`
- Go 1.26, module `github.com/wgomg/edub-kushim`
- `make fix` runs `go fix -tags "XLA,ORT" ./...`

## Two binaries, different CGo

- **`kushim`** (`cmd/kushim`) — CLI, document processing, matcher server. Built with
  `CGO_ENABLED=1`. Links Tesseract, Leptonica, MuPDF, tokenizers statically.
- **`edub`** (`cmd/edub`) — REST API server, web UI. Built with `CGO_ENABLED=0`. Pure Go, no C deps.
- The Makefile handles the split. If you invent a new build command, respect it.

## Process architecture

```
edub (CGO_ENABLED=0)── enqueues batches (status='queued')
                    └── Unix socket RPC: kushim hugot (tag matcher)

kushim queue ── forks: kushim consume --batch <id> (per-batch processing)
```

- `edub` enqueues consume/enrich tasks with `status='queued'` when `POST /api/v1/consume` is called;
  the `kushim queue` daemon (`internal/commands/queue.go`) picks the batch up (Postgres
  `LISTEN batch_queued` + 30s safety poll) and forks `kushim consume --batch <id>` as a child
  process. `kushim` must be on PATH or sibling of `edub`.
- The matcher (`kushim hugot`) must be running before `edub` starts. Tag CRUD returns 503 otherwise.
- Socket: `<config-dir>/kushim-hugot.sock`.

## Config

- File: `~/.config/edub-kushim/config.yaml` · Example: `config.example.yaml`
- PG defaults: `edub`/`edub` on `localhost:5432/edub`, sslmode=disable (CI + test containers use these)
- `kushim setup` → web wizard at `http://0.0.0.0:8420`; `kushim setup --cli --languages eng,spa,...` → terminal mode
- Setup only runs when no config exists. Delete the config file to re-run.

## Code generation & migrations

- After editing SQL in `internal/database/sql/queries/`: `sqlc generate`
  (regenerates `internal/database/*.sql.go`; skipping it causes type mismatches; config `sqlc.yaml`, v2, postgresql).
- Schema changes = new goose files in `internal/database/sql/schema/migrations/` — embedded via
  `//go:embed` and applied automatically (`goose.Up`, `goose.WithAllowMissing()`) plus idempotent
  seeders (`seed-tags.sql`, …) on setup/edub startup. Never edit baseline migrations retroactively.

## Testing

**Most tests don't need CGo but PostgreSQL-dependent ones need a PostgreSQL 16+ database.**
Run one via Podman (preferred) or Docker:

```bash
podman run -d --name edub-test-pg \
  -e POSTGRES_USER=edub -e POSTGRES_PASSWORD=edub \
  -p 5432:5432 postgres:17   # docker works identically
```

```bash
export TEST_DATABASE_URL="postgres://edub:edub@localhost:5432/edub?sslmode=disable"
```

```bash
make test          # 12 packages, no database needed, CGO_ENABLED=0
make test-verbose  # same with -v
make test-db       # 6 additional packages, requires PostgreSQL via TEST_DATABASE_URL
make test-backup   # backup package, requires PostgreSQL via TEST_DATABASE_URL
make test-cgo      # CGo-gated tests incl. internal/commands (requires make build-deps first)
make test-cgo-db   # consumption with CGo + DB (requires make build-deps + TEST_DATABASE_URL)
make test-one PKG=./internal/errs/   # single package; add RUN=Name to filter
make test-web      # web/ unit + component tests (vitest, no database)
make test-web-e2e  # web/ E2E smokes with mocked API (requires `npx playwright install chromium` first)
make vuln         # govulncheck over the Go vuln DB (CGO_ENABLED=0); CI runs it in the vulncheck job
make vuln-cgo     # CGo-enabled variant, full call graph (requires make build-deps first)
```

- **Isolation**: each test package gets its own database (`edub_test_<pkg_dir>`) via `runtime.Caller`,
  auto-dropped with `DROP ... WITH (FORCE)` — no manual cleanup. The PG user must be superuser
  (CI/containers are).
- Covered: database queries, task lifecycle, search engine, API handlers, consumption pipeline
  (mock runner), CLI commands (via `make test-cgo`). Not covered: real OCR/PDF adapters.
- CI (`test-cgo` job) builds C deps (cached under `build/`, key includes `hashFiles('Makefile')`)
  and runs `make test-cgo` + `make test-cgo-db` against a `postgres:17` service.
- Full testing reference: `docs/reference/tests.md`.

## Web UI (SvelteKit)

- Two SPAs: `web/` (main → `internal/static/build/`) and `web-wizard/` (setup wizard →
  `internal/wizard/static/`); both embedded via `//go:embed`. CI stages them with
  `make stage-web` (web job) / `make stage-web-artifact` (Go jobs consume the `web-assets` artifact).
- Node 24 per `.nvmrc`; the Makefile auto-sources nvm. Any npm/npx command must run
  under the `.nvmrc` Node version — run `nvm use` first (verify with `node -v`), never
  the shell's default Node (it rewrites `package-lock.json` with its own version-dependent format).
- Dev server: `cd web && npm ci && npm run dev` (proxies `/api` to `localhost:3000`)
- Both use `@sveltejs/adapter-static` with `fallback: 'index.html'`.
- Svelte 5 with runes everywhere. Tailwind CSS v4 via `@tailwindcss/vite`.
- Verify order: `npm run lint` (prettier --check + eslint) before `npm run build`;
  format-only lint failures → `npm run format` then re-lint, don't hand-edit.

## Git & releases

- Branch model: work on `dev`; `master` only advances via fast-forward merges from it
  (`git merge --ff-only`); PRs target `master`. Commits must be signed (`commit.gpgsign=true`).
- Commit messages: Spanish, conventional format, single line, imperative mood
  (e.g. `feat(api): agrega …`, no trailing period).
- `.github/workflows/release.yml` builds both binaries per arch (amd64 + arm64) and publishes a
  GitHub Release: 4 tarballs (`kushim_linux_{amd64,arm64}`, `edub_linux_{amd64,arm64}`) + `checksums.txt`,
  triggered by pushing a `vX.Y.Z` tag on `master`. Version lives in `internal/version/version.go`.
- Manual runs (`workflow_dispatch`, from `master` only): `publish: false` → build only;
  `publish: true` + `tag_name` → draft release (tag auto-created at HEAD if missing).
- Gotchas when touching this workflow:
  - `download-tokenizers` pins per-arch sha256 in the Makefile; if upstream `latest/` moves, update `TOKENIZERS_SHA256_*`.
  - C-deps cache key = `hashFiles('Makefile')` + arch; the cache only saves when the job succeeds.
  - `upload-artifact` v4+ stores paths relative to their least common ancestor — the explicit staging step in the build job must stay.
  - The `inputs` context keeps booleans typed: compare `inputs.publish == true`, not `== 'true'`.
  - After editing the workflow, run `actionlint .github/workflows/release.yml` (and `act -l` if available).

## Constraints

- `kushim consume` blocks if external tools are missing (`ocrmypdf` needs tesseract+unpaper;
  `pdftotext` needs poppler-utils) — the built-in alternative is the `gosseract` engine.
- The `gosseract` adapter renders pages via MuPDF at 200 DPI, OCRs word boxes with Tesseract, and
  writes an invisible-text overlay with go-pdf/fpdf + embedded font (`kushim_font.ttf`) to produce
  searchable PDFs — see `docs/developer-guide/ocr-pipeline.md`.
- Hugot ORT backend downloads the ONNX runtime on first use — needs internet. Go backend has no runtime deps.
- ORT defaults disable the CPU memory arena and pattern pre-allocation (RSS ~2.2–2.5 GB vs ~4–5 GB),
  set in `internal/tools/adapters/tagmatcher/hugot.go` (`WithCPUMemArena(false)`).

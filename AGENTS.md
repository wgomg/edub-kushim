# AGENTS.md

## Build

- **Never omit build tags.** All `go build`, `go test`, and `go fix` commands require `-tags "XLA,ORT"`. This is non-negotiable.
- Build C dependencies first: `make build-deps`
- Build Go binaries: `make build` → produces `dev/bin/kushim` and `dev/bin/edub`
- Build the web UI before the Go binary when you need the embedded SPA: `make web-build && make build`
- Containerized builds: `make build-glibc`, `make build-musl`, `make build-tools-image`

## Two binaries with different CGo requirements

- **`kushim`** — CLI, document processing, matcher server. Built with `CGO_ENABLED=1`. Links Tesseract, Leptonica, MuPDF, Hugot statically.
- **`edub`** — REST API server, web UI. Built with `CGO_ENABLED=0`. Pure Go, no C deps.

Do not mix up which binary has CGo. The Makefile handles this, but if you invent a new build command, respect this split.

## Process architecture (critical for agents doing integration work)

```
edub (CGO_ENABLED=0)
  ├── Forks: kushim consume --batch <id>   (per-batch document processing)
  └── Communicates via Unix socket: kushim hugot  (tag matcher RPC)
```

- `edub` needs `kushim` on its sibling path or in `PATH`. It forks `kushim` as a child process when `POST /api/v1/consume` is called.
- The matcher (`kushim hugot`) must run as a separate process before `edub` starts for tag CRUD to work. Tag endpoints return 503 if the matcher socket is unreachable.
- The matcher UNIX socket is at `<config-dir>/kushim-matcher.sock`.

## Config

- Config file: `~/.config/edub-kushim/config.yaml`
- Reference: `config.example.yaml` in repo root
- `kushim setup` — web wizard at `http://0.0.0.0:8420`
- `kushim setup --cli --languages eng,spa,...` — terminal mode
- Setup only works when no config exists. To re-run setup, delete the config file first.

## Code generation

After modifying any SQL file in `internal/database/sql/queries/`:

```bash
sqlc generate
```

This regenerates `internal/database/*.sql.go`. Forgetting this step causes type mismatches.

## Testing

```bash
go test -tags "XLA,ORT" ./...
```

Same build tags as building. There is no test suite split — the tags apply everywhere.

## Web UI (SvelteKit)

- Two SvelteKit SPAs: `web/` (main app → `internal/static/build/`) and `web-wizard/` (setup wizard → `internal/wizard/static/`)
- Node.js 24 — `.nvmrc` specifies the version. Run `nvm use` first.
- Dev server: `cd web && npm ci && npm run dev` (proxies `/api` to `localhost:3000`)
- Both use `@sveltejs/adapter-static` with `fallback: 'index.html'` for SPA mode
- Svelte 5 with runes enabled everywhere
- Tailwind CSS v4 via `@tailwindcss/vite`
- Lint: `npm run lint` (prettier + eslint). Format: `npm run format`.

## Important constraints

- `kushim consume` blocks if required external tools are missing (depends on configured engines — e.g. `ocrmypdf` needs tesseract, unpaper; `pdftotext` needs poppler-utils).
- The `gosseract` OCR adapter uses MuPDF to render pages to PNG at 200 DPI. It creates searchable PDFs with invisible-but-selectable text (text rendering mode 3).
- The Hugot ORT backend downloads ONNX runtime at first use — needs internet. Go backend has no runtime deps.
- ORT defaults disable CPU memory arena and pattern pre-allocation to keep RSS ~2.2–2.5 GB instead of ~4–5 GB. Toggle via config if performance is unacceptable.
- Commit messages: Spanish, conventional commit format (see `kilo.json`).
- Go module: `github.com/wgomg/edub-kushim`, Go 1.26.

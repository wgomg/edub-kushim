# SvelteKit Frontend

## Structure

Located in `web/`, built via `npm ci && npm run build`, output copied to `internal/static/build/` by `make web-build` for static embedding (via `//go:embed`).

## Routes

| Route             | File                          | Description                                                                                                                                                          |
| ----------------- | ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/`               | `+page.svelte`                | Dashboard / home — shows Task Status summary including `Waiting` count                                                                                               |
| `/documents`      | `documents/+page.svelte`      | Document list with structured search bar, filter panel, saved searches, sort/pagination; shows **Tags** and **People** columns inline; snippet column when searching |
| `/documents/[id]` | `documents/[id]/+page.svelte` | Document detail: metadata, tags, people, file, **Content Stats** (pages, words, characters)                                                                          |
| `/settings`       | `settings/+page.svelte`       | Full configuration form: server host/port; OCR engine/timeout/data dir/languages; consumer workers/delete-original; text extractor engine/timeout; PDF optimizer engine/fallback/timeout; enricher workers; content analyzer (LLM) engine/timeout + provider Base URL/model/token; tag matcher engine/timeout/reduce-target-words/chunk-size/Hugot model/backend; text reducer engine/timeout/target-words |
| `/tags`           | `tags/+page.svelte`           | Tag management: list, filter, create, edit, delete — name-only form via Modal.svelte                                                                                |
| `/people`         | `people/+page.svelte`         | People management with two tabs: **People** (name + native name, cascade delete warning) and **Person Types** (name + description, conflict handling)               |
| `/document-types` | `document-types/+page.svelte` | Document type management: list, create, edit, delete — name + description form with 409 conflict handling                                                            |
| `/tasks`          | `tasks/+page.svelte`          | Task/batch monitoring — shows **Type**, **Payload** (document ID + file name), **Started** and **Completed** columns                                                 |

## Key files

- **`src/lib/api.js`** — API client wrapping `fetch()` for all backend endpoints: documents, tasks, batches, summary, health, autocomplete, saved searches, config (`api.config.get()`, `api.config.update()`, `api.config.status()` via `/wizard/config`). Contains two fetch wrappers: `request()` (error-swallowing, used by existing call sites) and `requestRaw()` (returns `{ok, status, data}` for conflict-aware CRUD). Provides CRUD groups for reference data: **`api.tags`**, **`api.people`**, **`api.peopleTypes`**, **`api.documentTypes`** — each with `list(q, limit, offset)`, `create(body)`, `update(id, body)`, `delete(id)`. The `autocomplete.*` group (used by document pages) is distinct from the CRUD groups.
- **`src/lib/components/Modal.svelte`** — Reusable overlay modal with Escape/click-outside dismiss, centered card with clay/parchment styling. Uses Svelte 5 `{@render children()}` for form body content.
- **`src/lib/components/DataTable.svelte`** — Reusable sortable/paginated table component; supports `refreshKey` prop for external reload triggers; handles both array and `{results, total}` response formats; shows "X–Y of Z" pagination when total is available
- **`src/lib/components/SearchBar.svelte`** — Rich search input with field token chips (tags, people, document type, language, MIME, dates, size), autocomplete suggestions, keyboard navigation (arrow keys, Enter, Backspace for chip removal, Escape to close dropdown), and `field:value` syntax parsing
- **`src/lib/components/FilterPanel.svelte`** — Collapsible filter panel with sections for tags (autocomplete + chips), people (two-stage type + name selection), document type (dropdown), language (dropdown), MIME type (dropdown), date created (dual date pickers), date modified (dual date pickers), file size (min/max text input with unit parsing), and "Clear all filters" button
- **`src/lib/stores/filterStore.js`** — Reactive Svelte writable store for shared search filter state with `setPartial()`, `reset()`, and `fromQueryString()` methods; `queryString` derived store for serialization
- **`src/lib/stores/searchFilter.js`** — Query string utilities: `tokenizeQuery()` (tokenizes `field:value` syntax), `parseQueryString()` (converts string to filter object), `serializeFilter()` (converts filter object to string), `parseSize()`/`formatSize()` (file size parsing/formatting with KB/MB/GB), `parseDateRange()` (date range from string), `setPersonTypes()`/`getPersonTypes()` (person type set management)

---

# Build System

## Targets

```bash
make web-build         # Build SvelteKit UI, copy to internal/static/build
make build-deps        # Compile static C libs for glibc (requires gcc, make, autoconf, git, curl)
make build-deps-musl   # Cross-compile static C libs for musl (requires builder image)
make build             # Compile both Go binaries (kushim + edub) with -tags "XLA,ORT"
make build-glibc       # Build inside kushim-glibc-builder container (glibc)
make build-musl        # web-build + build inside kushim-musl-builder container (musl)
make build-glibc-image  # Create glibc builder container image
make build-musl-image   # Create musl builder container image
make build-tools-image  # Create full deployment image (localhost/edub-kushim:latest)
make build-glibc-deps   # Run build-deps inside glibc builder container
make build-musl-deps    # Run build-deps-musl inside musl builder container
make consume           # build + run kushim consume (inbox scan)
make clean             # remove binaries
make fix               # go fix -tags "XLA,ORT" ./...
```

## Build dependency chain

`make build-deps` compiles four static libraries from source:

1. **libpng** (`1.6.43`) — Downloaded from SourceForge. Minimal configure: `--disable-shared --enable-static`.
2. **Leptonica** — Cloned from GitHub. Built against local libpng. Disables TIFF, WebP, OpenJPEG, GIF, JPEG and programs.
3. **Tesseract** — Cloned from GitHub. Statically linked against local Leptonica and libpng. Disables curl, libarchive, OpenMP, legacy API, and graphics.
4. **MuPDF** (`1.27.2`) — Cloned from GitHub (with submodules). Configured with `HAVE_X11=no HAVE_GLUT=no shared=no`.

Additional C dependency for Hugot Go backend:

5. **libtokenizers** — Pre-built binary from `github.com/daulet/tokenizers/releases/latest` (`download-tokenizers` target). For musl builds, compiled from source via Cargo (`build-tokenizers`).

The built libraries are placed under `build/{libpng,leptonica,tesseract,mupdf,tokenizers}/local/`.

## Containerized builds

- **Build images**: `make build-glibc-image` (Containerfile.glibc) and `make build-musl-image` (Containerfile.musl) create builder containers with all required toolchains.
- **Cross-compilation**: `make build-glibc` and `make build-musl` run the Go build inside the respective containers, binding the workspace and Go module cache. Musl build runs `web-build` first to ensure the embedded UI is up-to-date.
- **Deployment image**: `make build-tools-image` creates the final production image (Containerfile.full).

## Build tags

```bash
go build -tags "XLA,ORT"  # Enables Hugot ONNX/XLA support
```

## CGo linking

The Makefile exports `CGO_ENABLED=1`, `CGO_CPPFLAGS`, and `CGO_LDFLAGS` so Go can find the headers and tokenizers library.
Linker flags are embedded in source files:

- `internal/tools/adapters/ocr/tesseract_link.go` — Static linking for Tesseract + Leptonica + libpng + platform libraries (`lstdc++`, `lm`, `lpthread`, `ldl`, `lz`)
- `internal/tools/adapters/mupdf_wrapper.go` — Static linking for MuPDF + platform libraries and `lfreetype`, `ljbig2dec`, `lmujs`, `lopenjp2`, `lz`, `lcrypto`

---

---

# Setup Wizard (`web-wizard/`)

A standalone SvelteKit SPA for initial configuration, embedded into the `kushim`
binary at `internal/wizard/static/`.

## Purpose

Provides a browser-based five-step setup flow when `kushim setup` is run (default).
Replaces the terminal-only setup for users who prefer a GUI.

## Routes

| Route | File             | Description                                                                 |
| ----- | ---------------- | --------------------------------------------------------------------------- |
| `/`   | `+page.svelte`   | Five-step wizard: config directory → consumer settings (server, OCR, text extractor, PDF optimizer) → enricher settings (LLM, tag matcher, text reducer) → progress → done |

The wizard layout uses the same design system as the main UI (clay/gold/lapis/
parchment palette) via Tailwind CSS.

## API client

**`src/lib/api.js`** — `configApi.get()`, `configApi.update(body)`, `configApi.status()` — communicates with `/wizard/config` endpoints proxied through the Vite dev server.

## Build

```bash
make wizard-build         # npm ci && npm run build, copy to internal/wizard/static
```

## See Also

- [Search](search.md) — Search engine, frontend filter state, query parser
- [API](api.md) — REST API endpoints consumed by the frontend
- [Overview](overview.md) — Project structure showing frontend file locations

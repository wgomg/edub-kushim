# SvelteKit Frontend

## Structure

Located in `web/`, built via `npm ci && npm run build`, output copied to `internal/static/build/` by `make web-build` for static embedding (via `//go:embed`).

## Routes

| Route             | File                          | Description                                   |
| ----------------- | ----------------------------- | --------------------------------------------- |
| `/`               | `+page.svelte`                | Dashboard / home                              |
| `/documents`      | `documents/+page.svelte`      | Document list with search, sort, pagination   |
| `/documents/[id]` | `documents/[id]/+page.svelte` | Document detail: metadata, tags, people, file |
| `/tags`           | `tags/+page.svelte`           | Tag management (list, create, delete)         |
| `/tasks`          | `tasks/+page.svelte`          | Task/batch monitoring (status, retry)         |

## Key files

- **`src/lib/api.js`** — API client wrapping `fetch()` for all backend endpoints
- **`src/lib/components/DataTable.svelte`** — Reusable sortable/paginated table component
- **`src/routes/layout.css`** — Global design tokens via CSS custom properties
- **`src/routes/+layout.svelte`** — App shell with nav sidebar and layout

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

## See Also

- [API](api.md) — REST API endpoints consumed by the frontend
- [Overview](overview.md) — Project structure showing frontend file locations

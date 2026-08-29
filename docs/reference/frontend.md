# SvelteKit Frontend

## Structure

Located in `web/`, built via `npm ci && npm run build`, output copied to `internal/static/build/` by `make web-build` for static embedding (via `//go:embed`).

## Routes

| Route             | File                          | Description                                                                                                                                                          |
| ----------------- | ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/`               | `+page.svelte`                | Dashboard / home — shows Task Status summary, storage panel, batch overview panel, active tasks strip (pending/processing/waiting cards, processing-first, with "View all" overflow link to `/tasks?status=active`), and document analytics panel (language/type distributions, tag frequency, missing counts). Includes auto-refresh toggle button. |
| `/documents`      | `documents/+page.svelte`      | Document list with structured search bar, filter panel, saved searches, sort/pagination; shows **Tags** (capped at 3 pills + `(+n)` badge), **People** (grouped by type, capped at 3 names per type line + per-line `(+n)` badge), and sortable **Pages** (`page_count`, em dash for 0/missing) columns inline; snippet column when searching. Batch delete, batch tag assign, and batch set-type buttons hidden for viewer role. Saved search Save/Delete hidden for viewer. |
| `/documents/[id]` | `documents/[id]/+page.svelte` | Document detail: PDF iframe preview, **Content Stats** (pages, words, characters). Editable metadata sidebar (title, type, language, tags, people). Controls hidden for viewer role. |
| `/settings`       | `settings/+page.svelte`       | Two-tab page: **Configuration** (existing full settings form with all sections, plus a Trash section with `storage.trash.retention_days` input) and **Users** (DataTable with role badge column, create/edit/delete user modals with role dropdown (viewer/editor/admin), password with placeholder "Leave blank to keep current" on edit, confirm dialog for delete). Admin-only — shows permission denied message for non-admin users. Username required and password required (on create) are enforced client-side; length and complexity rules are enforced server-side (12+ characters, uppercase/lowercase/digit/special). Unsaved-changes warning via `beforeunload` while the form is dirty. The Content analyzer section includes a **Fallback LLMs** list (add/remove/reorder rows, each with its own enable toggle, cascading adapter→provider→model selectors, token show/hide, temperature, request delay); the in-memory config is normalized with `ensureFallbacksArray` so the list exists even when the server omits it. |
| `/tags`           | `tags/+page.svelte`           | Tag management: list with document count column, filter, create, edit, delete — name-only form via Modal.svelte. Rows clickable → filtered documents page. Create/Edit/Delete buttons hidden for viewer role.                            |
| `/people`         | `people/+page.svelte`         | People management with two tabs: **People** (sortable paginated DataTable with name + native name + document count, search filter, rows clickable → filtered documents page) and **Person Types** (name + description, conflict handling). Create/Edit/Delete buttons hidden for viewer role. |
| `/document-types` | `document-types/+page.svelte` | Document type management: list with document count column, create, edit, delete — name + description form with 409 conflict handling. Rows clickable → filtered documents page. Create/Edit/Delete buttons hidden for viewer role.       |
| `/tasks`          | `tasks/+page.svelte`          | Task/batch monitoring — shows **Type**, **Payload** (document ID + file name), **Started** and **Completed** columns. Retry/Resume/Cancel buttons hidden for viewer role. With `?status=active` (or any `?status=`) in the URL the task DataTable replaces the batch table; `?status=active` titles the view "Active Tasks". |
| `/documents/orphaned` | `documents/orphaned/+page.svelte` | Orphaned file management: table of quarantined files with Key, Type, Source, Size, Detected columns; action buttons to Delete, Restore (uuid only), Move to Inbox; Scan Now button; bulk Delete All / Move All to Inbox; empty state prompts user to run a scan. Editor-only — shows permission denied message for viewer role. |
| `/trash` | `trash/+page.svelte` | Trash management for soft-deleted documents: DataTable (Title, Type, Size, Pages, Language, Deleted columns; fixed `deleted_at` desc order) with per-row Restore / Delete permanently icon actions; batch Restore selected / Delete permanently via row checkboxes (partial-failure warnings); Purge expired button (danger confirm). Editor-only — shows permission denied message for viewer role. |
| `/profile`        | `profile/+page.svelte`        | Self-service profile page for any authenticated user: account card (username, role badge, created date, API key status) and API key management (generate, rotate, revoke with copy-to-clipboard). Guarded — redirects to `/` when not authenticated. |
| `/logs`           | `logs/+page.svelte`           | Log viewer with tabbed interface (Kushim, Edub, Hugot, Queue), lines count control (±), auto-refresh toggle, monospace color-coded log line rendering with expandable long lines, jump-to-bottom button, URL-synced tab/lines state. |

## Key files

- **`src/lib/api.js`** — API client wrapping `fetch()` for all backend endpoints. Contains two fetch wrappers: `request()` (returns data or null, auto-attaches auth header, intercepts 401 by calling `POST /api/v1/auth/logout` to clear the HttpOnly cookie, then clears localStorage and redirects to `/login`) and `requestRaw()` (returns `{ok, status, data}`, auto-attaches auth header). Provides CRUD groups with `withAuth()` helper that injects `Authorization: Bearer` header from `authStore`. Exposes `api.auth.login(username, password)` using `requestRaw` so the frontend can distinguish 401 (bad credentials) from network errors, and `api.auth.logout()` (raw `fetch` to `POST /api/v1/auth/logout`, no auth header needed since the endpoint is whitelisted). Exposes `api.config.bootstrap()` (no auth, reads only `auth_enabled` + `missing_tools` from `GET /wizard/bootstrap`) alongside the existing admin-protected `api.config.get()`, `.update()`, `.status()`, `.retryFailed()`. Exposes `api.documents` with `.list()`, `.get()`, `.search()`, `.searchStructured()`, `.update()`, `.delete()`, `.reenrich()`, `.tags.add()/.remove()`, `.batchDelete(ids)`, `.batchAssignTags(ids, tagIds, mode)`, `.batchSetDocumentType(ids, documentTypeId)`, `.downloadBatch(ids)`, and `.people.add()/.remove()` for document management. Exposes `api.orphaned` with `.list()`, `.scan()`, `.delete(id)`, `.restore(id)`, `.moveToInbox(id)`, `.deleteAll()`, `.moveAllToInbox()` for orphaned file management. Exposes `api.logs.get(name, lines, signal)` for reading log files with optional abort signal. Exposes `api.me` with `.profile()`, `.generateKey()`, `.revokeKey()`, `.rotateKey()`, `.keyStatus()` for self-service profile and API key management. Exposes `api.trash` with `.list(limit, offset)` (maps `documents` → `{results, total}`), `.restore(id)`, `.permanentDelete(id)`, `.batchRestore(ids)`, `.batchPermanentDelete(ids)`, `.purge()` for trash management.
- **`src/lib/stores/authStore.js`** — Plain JS module (no Svelte runes, importable from `api.js` without compiler dependency). Module-level vars `_token`, `_user`, and `_authEnabled` initialized from `localStorage` at import time. Exports: `getToken()`, `getUser()`, `isAuthenticated()` (checks both token and user exist), `login(token, user)` (persists to localStorage), `logout()` (clears localStorage), `getRole()`, `isAdmin()`, `isEditor()`, `authEnabled()`, `setAuthEnabled(v)`, `roleBadgeClass(role)`, and `refreshMe()` (fetches `GET /api/v1/me` to update user profile from server). Used by `+layout.svelte` for auth guard, role-gated navigation, and logout button; by `login/+page.svelte` for login flow; by `api.js` for auth header injection; by multiple page components for role-gated UI controls.
- **`src/lib/components/Modal.svelte`** — Reusable overlay modal with Escape/click-outside dismiss, centered card with clay/parchment styling. Uses Svelte 5 `{@render children()}` for form body content.
- **`src/lib/components/ConfirmDialog.svelte`** — Themed confirmation dialog mounted once in the root layout. Driven imperatively via `confirmStore.confirm({ title, message, danger })` which returns a `Promise<boolean>`. Replaces all `window.confirm()` calls with a consistent clay/parchment/terracotta styled modal. Backdrop click, Escape, and Cancel resolve with `false`; Delete/OK resolves with `true`.
- **`src/lib/components/Toast.svelte`** — Global toast stack mounted in the root layout. Renders a fixed top-right stack (max 3) of transient operational errors and success messages. Driven imperatively via `toastStore.error()`, `toastStore.success()`, `toastStore.warning()`, `toastStore.info()`. Auto-dismisses (errors after 6s, others after 4s); click-to-dismiss via a close button.
- **`src/lib/stores/confirmStore.svelte.js`** — Module-level `$state` store exposing `confirm()` (returns `Promise<boolean>`) and `resolve(bool)`. Enforces a single-pending-request mutex: calling `confirm()` while another is pending resolves the pending request with `false` before installing the new one. SSR-safe (no browser globals at module scope).
- **`src/lib/stores/toastStore.svelte.js`** — Module-level `$state` store with `push({ variant, message })`, `dismiss(id)`, and convenience methods `error()`, `success()`, `warning()`, `info()`. Tiered timeouts (error: 6s, others: 4s). Caps visible toasts at 3 — drops oldest when exceeded. SSR-safe.
- **`src/lib/components/DataTable.svelte`** — Reusable sortable/paginated table component; supports `refreshKey` prop for external reload triggers; handles both array and `{results, total}` response formats; shows animated skeleton rows during every fetch (initial load, pagination, sort, search); shows "X–Y of Z" pagination when total is available; `onRowClick` prop with keyboard (Enter) and `focus-visible` ring support for clickable rows
- **`src/lib/components/SearchBar.svelte`** — Rich search input with field token chips (tags, people, document type, language, dates, size), autocomplete suggestions, keyboard navigation (arrow keys, Enter, Backspace for chip removal, Escape to close dropdown), and `field:value` syntax parsing
- **`src/lib/components/FilterPanel.svelte`** — Collapsible filter panel with sections for tags (autocomplete + chips), people (two-stage type + name selection), document type (dropdown), language (dropdown), date created (dual date pickers), date modified (dual date pickers), file size (min/max text input with unit parsing), missing language/type/untagged checkboxes, and "Clear all filters" button
- **`src/lib/stores/filterStore.js`** — Reactive Svelte writable store for shared search filter state with `setPartial()`, `reset()`, and `fromQueryString()` methods; `queryString` derived store for serialization
- **`src/lib/components/StoragePanel.svelte`** — Dashboard storage panel: stat cards (avg file size, total pages, total words), original type breakdown with horizontal bar chart + table (top 7 + "other"), cumulative storage trend SVG area chart and daily storage increase SVG bar chart (per-day `daily_bytes` with doc-count hover tooltips), both with auto-scaled axes and date labels
- **`src/lib/components/BatchOverviewPanel.svelte`** — Dashboard batch overview panel: recent batches table with task status bars, duration, owner state
- **`src/lib/components/ActiveTasksStrip.svelte`** — Dashboard active tasks strip: horizontally scrollable snap cards (fixed `w-64`) for `pending`/`processing`/`waiting` tasks, processing-first; card title is the task `label`, status chip + elapsed time (`running` from `started_at`, `queued` from `created_at`); whole card links to `/tasks/<task_id>`; "View all N →" overflow link to `/tasks?status=active` when total exceeds the 25-card cap; `aria-live="polite"` region announcing auto-refresh changes
- **`src/lib/components/DocumentAnalyticsPanel.svelte`** — Dashboard analytics panel: clickable stat cards for missing language/type/tags, language distribution bars, document type distribution bars, top tags table with mini progress bars. All rows/cards navigate to the documents page pre-filtered by the clicked item.
- **`src/lib/components/ProcessingHealthPanel.svelte`** — Dashboard processing health panel: task success rate, avg duration, active/orphaned batch counts, missing-tools count
- **`src/lib/components/UploadModal.svelte`** — Upload progress modal for `POST /api/v1/consume/upload`
- **`src/lib/utils/html.js`** — Shared HTML/formatting utilities: `escapeHtml(str)`, `formatSize(bytes)` (B/KB/MB/GB with null guard), `formatNumber(n)` (comma-separated via `toLocaleString`), `formatDuration(ms)`, `formatRelative(dateStr)` (compact elapsed time: `Ns`/`Nm`/`Nh`/`Nd` from an RFC 3339 string, `—` for null/invalid)
- **`src/lib/utils/statusChip.js`** — Shared status→Tailwind chip class map (`statusChipClasses(status)` with fallback) used by both the tasks table `statusBadge` and `ActiveTasksStrip` so the palette cannot drift
- **`src/lib/stores/searchFilter.js`** — Query string utilities: `tokenizeQuery()` (tokenizes `field:value` syntax including `missing:`), `parseQueryString()` (converts string to filter object with `missingLanguage`/`missingType`/`untagged`), `serializeFilter()` (converts filter object to string with `missing:lang`/`missing:type`/`missing:tags`; field values always wrapped in double quotes), `parseSize()`/`formatSize()` (file size parsing/formatting with KB/MB/GB), `parseDateRange()` (date range from string), `setPersonTypes()`/`getPersonTypes()` (person type set management)

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

1. **libpng** (`1.6.58`) — Downloaded from SourceForge. SHA-256 verification before extraction. Minimal configure: `--disable-shared --enable-static`.
2. **Leptonica** (commit `10bdea2`) — Cloned from GitHub, pinned to a specific commit. Built against local libpng. Disables TIFF, WebP, OpenJPEG, GIF, JPEG and programs.
3. **Tesseract** (`5.5.3`) — Cloned from GitHub, pinned to a specific tag (`--branch 5.5.3 --depth 1`). Statically linked against local Leptonica and libpng. Disables curl, libarchive, OpenMP, legacy API, and graphics.
4. **MuPDF** (`1.28.0`) — Cloned from GitHub (with submodules). Configured with `HAVE_X11=no HAVE_GLUT=no shared=no`.

Additional C dependency for Hugot Go backend:

5. **libtokenizers** — Pre-built binary from `github.com/daulet/tokenizers/releases/latest`, arch selected via `TOKENIZERS_ARCH` (`?= amd64`, set `arm64` for ARM runners). The tarball is SHA-256 verified against `TOKENIZERS_SHA256_{amd64,arm64}` before extraction — an upstream release that changes the asset fails the build until the Makefile hashes are updated. For musl builds, compiled from source via Cargo (`build-tokenizers`).

The built libraries are placed under `build/{libpng,leptonica,tesseract,mupdf,tokenizers}/local/`.

## Containerized builds

- **Build images**: `make build-glibc-image` (Containerfile.glibc) and `make build-musl-image` (Containerfile.musl) create builder containers with all required toolchains.
- **Cross-compilation**: `make build-glibc` and `make build-musl` run the Go build inside the respective containers, binding the workspace and Go module cache. Musl build runs `web-build` first to ensure the embedded UI is up-to-date.
- **Deployment image**: `make build-tools-image` creates the final production image (Containerfile.full).

## Build tags

All builds set `-tags "XLA,ORT"` (enables Hugot ONNX/XLA support) through the
Makefile: `make build-deps` compiles the C libraries first, then `make build`.
Bare `go build` invocations are not supported.

## CGo linking

The Makefile exports `CGO_ENABLED=1`, `CGO_CPPFLAGS`, and `CGO_LDFLAGS` so Go can find the headers and tokenizers library.
Linker flags are embedded in source files:

- `internal/tools/adapters/ocr/tesseract_link.go` — Static linking for Tesseract + Leptonica + libpng + platform libraries (`lstdc++`, `lm`, `lpthread`, `ldl`, `lz`)
- `internal/tools/adapters/mupdf_wrapper.go` — Static linking for MuPDF + platform libraries and `lfreetype`, `ljbig2dec`, `lmujs`, `lopenjp2`, `lz`, `lcrypto`

## GitHub Actions release

`.github/workflows/release.yml` publishes pre-built binaries for Linux amd64/arm64:
the `web` job builds both SPAs into an artifact, the per-arch `build` jobs compile
the C deps (cached under `build/`, keyed by Makefile hash + arch) and run
`make build-deps TOKENIZERS_ARCH=<arch> && make build` with version-injected
`LDFLAGS`, and the `release` job uploads 4 tarballs + a combined `checksums.txt`
to GitHub Releases (published on `v*` tag push, draft on `workflow_dispatch`).
See `AGENTS.md` → Releases for the full process and gotchas.

---

---

# Setup Wizard (`web-wizard/`)

A standalone SvelteKit SPA for initial configuration, embedded into the `kushim`
binary at `internal/wizard/static/`.

## Purpose

Provides a browser-based six-step setup flow when `kushim setup` is run (default).
Replaces the terminal-only setup for users who prefer a GUI.

## Routes

| Route | File             | Description                                                                 |
| ----- | ---------------- | --------------------------------------------------------------------------- |
| `/`   | `+page.svelte`   | Six-step wizard: config directory → consumer settings (server, storage, database, OCR, text extractor, PDF optimizer, supported file types, DOCX/ODT converter) → enricher settings (LLM, tag matcher, text reducer) → progress → admin user → done |

The wizard layout uses the same design system as the main UI (clay/gold/lapis/
parchment palette) via Tailwind CSS.

## API client

**`src/lib/api.js`** — `configApi.bootstrap()`, `configApi.get()`, `configApi.update(body)`, `configApi.status()`, `configApi.retryFailed()`, `configApi.createAdminUser(body)` — communicates with `/wizard/bootstrap` (public), `/wizard/config` (admin-protected), and `/wizard/admin-user` endpoints proxied through the Vite dev server.

## Build

```bash
make wizard-build         # npm ci && npm run build, copy to internal/wizard/static
```

## See Also

- [Search](search.md) — Search engine, frontend filter state, query parser
- [API](api.md) — REST API endpoints consumed by the frontend
- [Overview](overview.md) — Project structure showing frontend file locations

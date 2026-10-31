# SvelteKit Frontend

## Structure

`web/` builds through `nvm use && npm ci && npm run build`; the output is copied into
`internal/static/build/` by `make web-build` for static embedding (`//go:embed`). `vite.config.js`
sets `output.hashCharacters: 'base36'` because SvelteKit names client chunks with a bare content
hash and Go's embed pattern skips names starting with `_` (the base64url default alphabet includes
`_` and `-`; base36 is alphanumeric only).

Every route below is a SvelteKit `+page.svelte` under `web/src/routes/`.

## Routes

| Route | Description |
| ----- | ----------- |
| `/` | Dashboard: task status, storage, batch overview, active tasks and document analytics, with auto-refresh. |
| `/documents` | Document list: structured search bar, filters, saved searches, sort and pagination, batch actions; mutations hidden for viewer. |
| `/documents/[id]` | Document detail: self-hosted PDF viewer plus editable metadata (title, type, language, tags, people). |
| `/settings` | Settings hub: health summary, pipeline map, destination rows, staged-changes tray and command palette, behind the admin guard. |
| `/tags` | Tag workspace: vocabulary, new tags, coverage, overlap and deleted tabs, deep-linkable by hash. |
| `/people` | People and person-type management with search, document counts and CRUD. |
| `/document-types` | Document type CRUD with document counts and conflict handling. |
| `/tasks` | Batch and task monitoring with per-batch actions (retry, resume, cancel); `?status=` switches to the task table. |
| `/documents/orphaned` | Orphaned and errored file management: scan, delete, restore, move to inbox; editor-only. |
| `/trash` | Soft-deleted documents: restore, permanent delete and purge; editor-only. |
| `/profile` | Self-service profile and API key management (generate, rotate, revoke). |
| `/logs` | Log viewer with per-daemon tabs, line-count control, auto-refresh and expandable lines. |
| `/docs` | API reference rendered by Scalar from the server's OpenAPI spec; any authenticated user. |

## Key files

- **`src/lib/api.js`** — the API client. `request()` returns data or null and handles 401 by
  clearing the session and redirecting to `/login`; `requestRaw()` returns `{ok, status, data}`;
  `requestText()` returns the raw response body (used by the API docs page).
  Every `api.*` group (auth, config, documents, tags, tasks, autocomplete, logs) lives here.
- **`src/lib/stores/authStore.svelte.js`** — the single source of auth state: token and user in
  `localStorage`, the `authResolved` gate that keeps permission checks from flashing on a hard
  refresh, and the role helpers used by the page guards.
- **`src/lib/stores/settingsStore.svelte.js`** — settings state: loads and snapshots the admin
  config, exposes the flattened diff (token values redacted), stages catalog operations, and runs
  apply/discard including the deferred migration watch.
- **`src/lib/stores/filterStore.js`**, **`src/lib/stores/searchFilter.js`** — shared document-filter
  state and the `field:value` query parser/serializer used by the search bar and the URL.
- **`src/lib/stores/confirmStore.svelte.js`**, **`src/lib/stores/toastStore.svelte.js`** —
  imperative promise-based confirm dialogs (one pending at a time) and the global toast stack.
- **`src/lib/components/DataTable.svelte`** — reusable sortable/paginated table card with
  `refreshKey`, `onRowClick`, total reporting and loading/empty states.
- **`src/lib/components/SearchBar.svelte`**, **`src/lib/components/FilterPanel.svelte`** — rich
  search input with chips, autocomplete and keyboard navigation; collapsible panel over every
  filter field.
- **`src/lib/components/PdfViewer.svelte`**, **`src/lib/components/PdfPage.svelte`** — self-hosted
  pdf.js viewer: continuous scroll with lazy dual-IntersectionObserver rendering, zoom, page jump
  and find-in-PDF, on a per-page canvas plus selectable text layer.
- **`src/lib/components/StoragePanel.svelte`**, **`BatchOverviewPanel.svelte`**,
  **`ActiveTasksStrip.svelte`**, **`DocumentAnalyticsPanel.svelte`**,
  **`ProcessingHealthPanel.svelte`** — the dashboard panels.
- **`src/lib/components/Modal.svelte`**, **`ConfirmDialog.svelte`**, **`Toast.svelte`**,
  **`UploadModal.svelte`**, **`Icon.svelte`** — shared UI primitives.
- **`src/lib/components/settings/`** — the settings section components plus `ChangesTray.svelte`
  and `SettingsPalette.svelte` (the `/`/`Ctrl+K` command palette).
- **`src/lib/components/tags/`** — the tag review surfaces: `ProvisionalReview.svelte`,
  `DeletedTags.svelte`, `SiblingReview.svelte` and `TagReconcileStrip.svelte`.
- **`src/lib/components/TagReconcileReport.svelte`** — the full reconciliation report rendered on
  `tagreconcile` task pages.
- **`src/lib/pdf/`** — the pdf.js loader singleton, the worker wrapper with its polyfills, and the
  shared `Promise.withResolvers` shim.
- **`src/lib/utils/`** — `html.js` (escaping, size/number/duration/relative formatting),
  `statusChip.js` (status to chip classes) and `tagReconcile.js` (report phrase maps and totals).

Tests live beside their sources as `*.test.js`; the web test tiers are documented in
[tests.md](tests.md).

## Build System

```bash
make web-build      # build the SPA and copy it to internal/static/build
make wizard-build   # build the setup wizard and copy it to internal/api/wizard/static
make stage-web      # copy existing web/build and web-wizard/build into the embed dirs
```

Both SPAs build under the Node version pinned in `.nvmrc`, and the make targets source nvm; run
`nvm use && node -v` first for any manual npm command. The C dependency chain, build tags,
containerized builds and the release workflow are documented in `AGENTS.md`.

# Setup Wizard (`web-wizard/`)

A standalone SvelteKit SPA for initial configuration, embedded into the `kushim` binary at
`internal/api/wizard/static/`. `kushim setup` launches it by default; `--cli` selects terminal
mode.

## Routes

| Route | Description |
| ----- | ----------- |
| `/` | Six-step flow: config directory, consumer settings, enricher settings, progress, admin user, done. |

The layout uses the same design system as the main UI (clay/gold/lapis/parchment palette) via
Tailwind CSS.

## API client

**`src/lib/api.js`** — the `configApi` helpers over `/wizard/bootstrap` (public),
`/wizard/config` (admin) and `/wizard/admin-user`.

## Build

```bash
make wizard-build   # npm ci && npm run build, copied to internal/api/wizard/static
```

## See Also

- [API](api.md) — generated REST API reference (OpenAPI)
- [Project Tree](project-tree.md) — where the frontend files live

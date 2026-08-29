# Developer Guide — The SvelteKit of edub-kushim

This guide explains the JavaScript/Svelte/SvelteKit features used across the two
frontends — `web/` (the main UI) and `web-wizard/` (the setup wizard) — and how
they are used *here*, with real snippets and file references. It is aimed at
developers who know how to program but are new to Svelte (or Svelte 5's runes
model) and need to get productive in this repository fast.

It complements the other docs:

| Document | What it answers |
|---|---|
| `docs/reference/frontend.md` | Per-page/per-file inventory of both SPAs, build targets |
| `golang.md` | The Go side of this repository |
| `postgresql.md` | The PostgreSQL features behind the schema and queries |
| `semantic-matching.md`, `algorithms.md`, `cgo.md`, `ocr-pipeline.md`, `task-system.md`, `llm.md` | Topic deep dives (embeddings, TextRank, C wrappers, OCR, task semantics, LLM integration) |
| `user-manual.md` | How the product behaves for end users |
| **`frontend.md` (this)** | *How Svelte/SvelteKit/JS itself is used here* |

Everything here describes code that exists today. If a snippet looks wrong,
trust the code, not the doc.

---

## Table of contents

1. [Codebase map](#1-codebase-map)
2. [Orientation in ten ideas](#2-orientation-in-ten-ideas)
3. [SvelteKit project structure](#3-sveltekit-project-structure)
4. [Svelte 5 reactivity: the runes](#4-svelte-5-reactivity-the-runes)
5. [Components: props, children, events](#5-components-props-children-events)
6. [Template syntax](#6-template-syntax)
7. [Stores: three eras](#7-stores-three-eras)
8. [The API layer](#8-the-api-layer)
9. [Data fetching in pages](#9-data-fetching-in-pages)
10. [URL state sync](#10-url-state-sync)
11. [Forms](#11-forms)
12. [Auth and role gating](#12-auth-and-role-gating)
13. [The DataTable pattern and HTML-string cells](#13-the-datatable-pattern-and-html-string-cells)
14. [The design system: Tailwind v4](#14-the-design-system-tailwind-v4)
15. [Build and tooling](#15-build-and-tooling)
16. [The setup wizard](#16-the-setup-wizard)
17. [JavaScript language features in use](#17-javascript-language-features-in-use)
18. [Feature → file quick reference](#18-feature--file-quick-reference)
19. [Idioms checklist and gotchas](#19-idioms-checklist-and-gotchas)

---

## 1. Codebase map

```
web/                        the main SPA (admin + document management UI)
  src/
    app.html                HTML shell (SvelteKit placeholders)
    routes/                 every page is a route directory
      +layout.svelte        app shell: sidebar, auth guard, global overlays
      +layout.css           Tailwind v4 entry + design tokens (@theme)
      +page.svelte          dashboard
      login/+page.svelte    login form
      documents/+page.svelte        document list + structured search
      documents/[id]/+page.svelte   document detail (dynamic route)
      documents/orphaned/+page.svelte
      tags/ people/ document-types/ tasks/ tasks/[id]/ trash/ logs/
      profile/ settings/
    lib/
      api.js                the whole API client (fetch wrappers)
      icons.js              SVG-string icons + actionButton() builder
      tools.js              install hints for missing system tools
      components/           DataTable, Modal, Toast, ConfirmDialog, SearchBar,
                            FilterPanel, UploadModal, dashboard panels
      stores/               authStore.js, filterStore.js, searchFilter.js,
                            confirmStore.svelte.js, toastStore.svelte.js
      stubs/empty.mjs       Vite alias target (neutralizes Node builtins)
  svelte.config.js          adapter-static + fallback -> SPA mode
  vite.config.js            tailwind plugin, dev proxy, aliases
  package.json              Svelte 5 / Kit 2 / Tailwind 4 / Vite 8

web-wizard/                 the setup wizard (one page, six steps)
  src/
    app.css                 Tailwind v4 entry (10-color subset of the palette)
    routes/+layout.svelte   centered card shell
    routes/+page.svelte     ALL wizard steps (1274 lines)
    lib/api.js              bare fetch wrapper, no auth (BASE = '/wizard')
    lib/tools.js            same install-hint map as the main app
```

Both SPAs are **static single-page apps**: SvelteKit with
`@sveltejs/adapter-static` and `fallback: 'index.html'` (`web/svelte.config.js:9`).
There is no server-side rendering, no Node runtime — the built output is copied
into the Go binaries:

- `web` build → `internal/static/build/`, embedded with `//go:embed` and served
  by the `edub` API server at `/` (`internal/static/fs.go`).
- `web-wizard` build → `internal/wizard/static/`, embedded and served by
  `kushim setup` at `http://0.0.0.0:8420` (`internal/wizard/fs.go`).

Build order matters: `make web-build && make build` (the embed step fails if
the SPA output is missing — CI stages it with `make stage-web`).

---

## 2. Orientation in ten ideas

1. **A `.svelte` file is a component.** One file = script + markup (+ optional
   styles). Svelte compiles it at build time; there is no virtual DOM.
2. **Reactivity is language-level, not library-level.** In Svelte 5 the compiler
   understands `$state`, `$derived`, `$props`, `$effect` — you write plain
   JavaScript and the compiler instruments it. No hooks, no dependency arrays.
3. **Runes are the only style used here.** No legacy Svelte 4 syntax exists in
   this codebase: no `$:` labels, no `on:` directives, no `<slot>`. Grep
   confirms zero. If you know Svelte 4, translate: `let x = 0` + `$:` →
   `let x = $state(0)` + `$derived`; `on:click` → `onclick` prop; `<slot>` →
   `{@render children()}`.
4. **This is SPA mode.** Every route serves `index.html`; routing happens
   client-side with `goto()` / `<a href>` and the `$page` store. There are no
   `+page.js` load functions anywhere — pages fetch their own data in
   `onMount` (§9).
5. **It's plain JavaScript, not TypeScript.** The Svelte and JS files are
   `.js`/`.svelte`, no type annotations, JSDoc for the few public contracts
   (e.g. `DataTable.svelte:15`). Readability comes from discipline: one
   `$props()` destructure per component, small pure helpers in `lib/`.
6. **Stores come in three flavors that coexist** (§7): plain modules
   (`authStore.js` — no reactivity), classic Svelte 4 `writable()` stores
   (`filterStore.js`), and modern module-level `$state` stores
   (`confirmStore.svelte.js`, `toastStore.svelte.js`).
7. **All styling is Tailwind v4, CSS-first.** There is no `tailwind.config.js`;
   the palette is declared with `@theme` in CSS (§14). No scoped `<style>`
   blocks, no transitions — motion is Tailwind classes.
8. **The API layer centralizes everything** (`lib/api.js`): auth headers,
   JSON parsing, 401 handling, and the codebase-wide convention that a failed
   request yields `null` (§8).
9. **Two-way binding is `bind:`** — `bind:value` for inputs, `bind:this` for
   element references (which are `$state` variables here), `bind:checked` for
   checkboxes. Callbacks flow down as props; events flow up as `on*` props
   (§5–§6).
10. **State that must survive reloads lives in the URL.** Search filters,
    tabs, pagination, the wizard step — all mirrored into the query string with
    `replaceState`-style updates (§10).

---

## 3. SvelteKit project structure

SvelteKit is file-based routing. In `web/src/routes/`:

- `+page.svelte` = a page (one per route directory).
- `+layout.svelte` = a wrapper around all nested pages (there is exactly one
  layout, at the root; the wizard has one too).
- `[id]/+page.svelte` = a dynamic route; the param arrives as a **prop** in
  Svelte 5 (`web/src/routes/documents/[id]/+page.svelte:9`):

```js
let { params } = $props();
```

  and is used directly: `api.documents.get(params.id)` (`:55`).

- `src/app.html` is the HTML shell; `%sveltekit.head%` and `%sveltekit.body%`
  are where SvelteKit injects markup (`web/src/app.html:8,12`). Per-page head
  additions use `<svelte:head>` (`+layout.svelte:66` sets the favicon).
- The `$lib` alias (`src/lib`) is the import path for shared code:
  `import { api } from '$lib/api'`.
- Other `$` aliases come from SvelteKit itself:
  - `$app/navigation` — `goto`, `replaceState` (client-side navigation).
  - `$app/paths` — `resolve()` — **every** internal link/href goes through
    `resolve('/documents')` so links respect the app base path (19 uses).
  - `$app/stores` — `page` as an auto-subscribed store, read as `$page`.
  - `$app/state` — `page` in runes form, read as a plain object (`page.url`).

Both `$page` (store) and `page` (state) forms are used — see §10.

---

## 4. Svelte 5 reactivity: the runes

Runes are compiler-recognized functions. The four that matter here.

### `$state` — reactive variables

Declared like `let x = $state(0)`; assigning to `x` re-renders dependents.
Component state in this codebase is *all* `$state` (`+layout.svelte:16-19`):

```js
let missingTools = $state([]);
let uploadOpen = $state(false);
let authEnabled = $state(true);
let configLoaded = $state(false);
```

Notable usage patterns:

- **"Not yet loaded" state**: `let health = $state();` — no argument, so the
  initial value is `undefined` (`+page.svelte:12`).
- **DOM references are `$state` too** (with `bind:this`, §6): `let fileInput =
  $state(null)` (`UploadModal.svelte:14`), `let scrollContainer =
  $state(null)` (`logs/+page.svelte:22`).
- **Sets are state, but rebuilt, not mutated** (`DataTable.svelte:54`,
  `logs/+page.svelte:23`):

```js
const next = new Set(selectedKeys);   // copy
if (next.has(key)) next.delete(key);  // mutate the copy
else next.add(key);
selectedKeys = next;                  // reassign
```

  (Mutating a `Set` in place isn't tracked; assigning a new one is.)
- **Whole objects** in one `$state` (the filter form, `FilterPanel.svelte:8`).
  Deep reactivity means `f.tags.push(t)` *does* trigger updates — the object is
  proxied recursively. `toastStore` exploits this with `_toasts.push(...)`
  directly (§7).

### `$derived` — computed values

Plain form (an expression):

```js
let toasts = $derived(toastStore.toasts);                      // Toast.svelte:4
let docColumns = $derived(chunk(recentDocs, 5));               // +page.svelte:62
```

Function form (when a block body is needed) — note it's **called** in the
template: `topTypes()` (`StoragePanel.svelte:14,154`):

```js
const topTypes = $derived(() => {
	const sorted = [...originalTypeBreakdown].sort(...);
	// ...
	return [...top, other];
});
```

Block form `$derived.by(() => {...})` for statement-heavy derivations — the
70-line column builder of the documents page (`documents/+page.svelte:27`):

```js
let columns = $derived.by(() => {
	const cols = [...];
	if (filter.query) { ... }
	return cols;
});
```

A `$derived` is read-only: compute from state, never assign to it.

### `$props()` — component inputs

One destructure at the top of every component (`Modal.svelte:2`):

```js
let { open, title, onClose, children } = $props();
```

Defaults make props optional (`SearchBar.svelte:11-20`, `DataTable.svelte:37-52`):

```js
let {
	query = '',
	onSearch = () => {},
	fileSize = { min: null, max: null }
} = $props();
```

There is no `$bindable` in this codebase — child→parent communication is done
with callback props (`onSearch`, `onClose`, `onRowClick`) instead.

### `$effect` — side effects that follow state

The body re-runs whenever its dependencies change; the returned function is
the cleanup. Three idioms dominate:

**1. Timers with cleanup** — auto-refresh interval (`+page.svelte:37-49`, same
shape in `logs/+page.svelte:93-104`):

```js
$effect(() => {
	if (refreshInterval) clearInterval(refreshInterval);
	refreshInterval = null;
	if (autoRefresh) {
		refreshInterval = setInterval(() => fetchDashboard(), 10000);
	}
	return () => {
		if (refreshInterval) clearInterval(refreshInterval);
	};
});
```

**2. DOM wiring** — ResizeObserver tied to an element ref
(`StoragePanel.svelte:40-47`):

```js
$effect(() => {
	const el = chartEl;
	if (!el) return;
	const ro = new ResizeObserver(([entry]) => {
		containerWidth = Math.round(entry.contentRect.width);
	});
	ro.observe(el);
	return () => ro.disconnect();
});
```

**3. Redirects and guards** — the layout's auth redirect (§12).

Two refinements worth knowing:

- **`untrack`** breaks the dependency graph so a write inside the effect
  doesn't retrigger it. DataTable reacts to `refreshKey` changes but must not
  re-run because `load()` writes state (`DataTable.svelte:151-162`):

```js
$effect(() => {
	if (refreshKey) {
		untrack(() => {
			pageIndex = 0;
			load();
			syncUrl();
		});
	}
});
```

  (`untrack` is imported from `'svelte'`, `DataTable.svelte:1`.)
- **One-time init** is done with a guard flag (`if (!initialized) {
  initialized = true; ... }`, `DataTable.svelte:109-116`) rather than
  `$effect.pre`.

Not used anywhere here: `$state.raw`, `$inspect`, `$host`, `$effect.pre`,
named `{@snippet}` declarations.

---

## 5. Components: props, children, events

### A complete component: `Modal.svelte` (35 lines, in full)

This is the canonical shape of a presentational component here:

```svelte
<script>
	let { open, title, onClose, children } = $props();
</script>

{#if open}
	<div
		role="presentation"
		class="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
		onclick={onClose}
	>
		<div
			class="mx-4 w-full max-w-md rounded-lg border border-clay-800 bg-clay-950 p-6 shadow-xl"
			onclick={(e) => e.stopPropagation()}
			onkeydown={(e) => {
				if (e.key === 'Escape') onClose();
			}}
			role="dialog"
			aria-modal="true"
			aria-label={title}
			tabindex="-1"
		>
			<div class="mb-4 flex items-center justify-between">
				<h2 class="text-lg font-semibold text-parchment-200">{title}</h2>
				<button onclick={onClose} aria-label="Close dialog" class="...">&times;</button>
			</div>
			{@render children()}
		</div>
	</div>
{/if}
```

Things to notice:

- **Props**: `open`, `title`, `onClose` — the component is *controlled*; the
  parent decides visibility and closing. No `bind:this`, no imperative API.
- **`children`**: in Svelte 5 the content between a component's tags arrives as
  a `children` prop — a *snippet* — and is rendered with `{@render children()}`
  (`Modal.svelte:32`). This replaces `<slot>`.
- **Events are props**: `onclick={onClose}` and inline arrows with `e`
  (`e.stopPropagation()` so clicks inside the dialog don't hit the backdrop).
- **Keyboard**: manual `e.key === 'Escape'` check — Svelte 5 removed event
  modifiers, so there is no `onkeydown|escape`.

### Consuming `Modal`

The provider side is plain markup between the tags
(`settings/+page.svelte:1810`, `document-types/+page.svelte:161`):

```svelte
<Modal
	open={showUserModal}
	title={editingUser ? 'Edit User' : 'Create User'}
	onClose={() => (showUserModal = false)}
>
	<form onsubmit={(e) => { e.preventDefault(); saveUser(); }}>
		<!-- form fields -->
	</form>
</Modal>
```

The layout renders the page content the same way
(`+layout.svelte:199`): `{@render children()}`.

### Callback props everywhere

- `SearchBar` emits `onSearch(partial)` (`SearchBar.svelte:33-43`).
- `DataTable` emits `onselectionchange(rows)` and `onActionClick(e, row)`.
- Function props may have defaults: `onSearch = () => {}`.

There are **no** class-style components, no `export let` props, no slots, no
`<script module>` blocks anywhere.

---

## 6. Template syntax

Svelte templates are HTML plus `{...}` expressions and block tags.

### Blocks

- **`{#if}` / `{:else if}` / `{:else}`** — the universal loading gate
  (`tasks/[id]/+page.svelte:27`):

```svelte
{#if loading}
	...
{:else if !task}
	...
{:else}
	...
{/if}
```

- **`{#each ... as ... (key)}`** — always with a key expression for identity
  (`Toast.svelte:17`):

```svelte
{#each toasts as toast (toast.id)}
```

  With index destructuring for tuple-ish data (`settings/+page.svelte:765`):

```svelte
{#each Object.entries(hintsForEngine(...)) as [system, cmd], i (i)}
```

- **`{@const}`** — bind a local inside the template (`ConfirmDialog.svelte:6`):

```svelte
{@const p = confirmStore.pending}
```

- **`{@html ...}`** — raw HTML insertion. Used in exactly two places and both
  are controlled: DataTable cell renderers (`DataTable.svelte:283-287`) and
  static icon markup. The contract is documented at the call site —
  *callers must `escapeHtml` user data first* (§13).
- **`<svelte:head>`** — per-page head tags (`+layout.svelte:66`).
- **`<svelte:window>`** — document-level event listener
  (`tasks/+page.svelte:304`): `<svelte:window onclick={handlePageClick} />` for
  delegated clicks on `data-*` action buttons.

### Binding

- `bind:value` — form inputs (≈45 sites). Works deep into `$state` objects:
  `bind:value={cfg.server.host}` (`settings/+page.svelte:448`) and inside
  `{#each}` loops (`bind:value={w.start}`, `settings/+page.svelte:1019`).
- `bind:checked` — checkboxes/toggles (`settings/+page.svelte:560`).
- `bind:this` — element references, stored in `$state`:
  `bind:this={fileInput}` (`UploadModal.svelte:130`) — then
  `fileInput?.click()` opens the hidden file picker; `bind:this={chartEl}`
  (`StoragePanel.svelte:221`) feeds the ResizeObserver.
- **Uncontrolled inputs** with `oninput` when the value must pass through a
  function: `oninput={(e) => updateLanguage(i, e.currentTarget.value)}`
  (`settings/+page.svelte:844`). And a deliberate hybrid in FilterPanel:
  `bind:value={f.documentType}` for display plus `onchange` to push to the
  store (`FilterPanel.svelte:373`).

### Conditional classes

There is no `class:` directive — classes are computed inline:

```svelte
class="rounded-md px-2 py-1 text-xs ... ${batchTagMode === 'add' ? 'bg-gold-600 text-clay-950' : 'border border-clay-800 ...'}"
```

(`documents/+page.svelte:264`) — or with a variant map
(`Toast.svelte:18-21`): `{variantClasses[toast.variant] || variantClasses.info}`.

---

## 7. Stores: three eras

State shared across components lives in `src/lib/stores/`, and all three
approaches coexist. Which one to use is a judgment call — read the tradeoffs.

### Era 1 — plain module: `authStore.js` (no reactivity)

A module with private `let` bindings and getter functions, deliberately *not* a
Svelte store (`authStore.js:1-23`):

```js
let _token = '';
let _user = null;

export function getToken() { return _token; }
export function isAuthenticated() { return !!_token && !!_user; }
export function login(token, user) {
	_token = token;
	_user = user;
	localStorage.setItem('token', token);
	localStorage.setItem('user', JSON.stringify(user));
}
```

Why: `api.js` imports it (`api.js:1`) and must read the token synchronously
without dragging the Svelte compiler into the data layer. Reactivity for the UI
is achieved at the *refresh boundary*: the layout's `$effect` reads
`authStore.isAuthenticated()` and re-evaluates after login/logout/navigation.

### Era 2 — classic writable store: `filterStore.js`

Svelte 4-style `writable()` with a thin wrapper API
(`filterStore.js:4-23`):

```js
function createFilterStore() {
	const store = writable({ ...defaultFilter });

	return {
		subscribe: store.subscribe,
		set: store.set,
		update: store.update,
		setPartial(partial) {
			store.update((f) => ({ ...f, ...partial }));
		},
		reset() {
			store.set({ ...defaultFilter });
		}
	};
}

export const filterStore = createFilterStore();
export const queryString = derived(filterStore, ($f) => serializeFilter($f));
```

Components subscribe imperatively and copy values into local `$state`
(`documents/+page.svelte:141-167`), with a guard so the store's immediate
initial emission doesn't touch the URL:

```js
let subscribed = false;
filterStore.subscribe((f) => {
	filter = { query: f.query, /* ... */ };
	if (subscribed) {
		refreshKey++;
		// ... write ?q= into the URL via replaceState
	}
	subscribed = true;
});
```

The URL filter is applied *before* subscribing: `fromQueryString(q)` runs in
the script body (`documents/+page.svelte:136-139`), not in `onMount`. Child
`onMount`s fire before the parent's, so applying it in `onMount` let
`DataTable` start its first load with the stale store from the previous
visit, then trigger a second load after the URL was parsed (two racing
`POST /api/v1/documents/search` requests). In the script body the store is
correct before any child instantiates.

The frozen `defaultFilter` schema lives in `lib/stores/searchFilter.js:11`
(`Object.freeze({ ... })` — stores copy it, never mutate it).

### Era 3 — runes stores: `confirmStore.svelte.js`, `toastStore.svelte.js`

Module-level `$state` + an exported object with a getter. The `.svelte.js`
extension tells the Svelte compiler to instrument the file
(`confirmStore.svelte.js:1-25`, in full):

```js
let _pending = $state(null);

function confirm({ title, message, danger = false }) {
	return new Promise((resolve) => {
		if (_pending) {
			_pending.resolve(false);   // one pending dialog at a time
		}
		_pending = { title, message, danger, resolve };
	});
}

function resolve(val) {
	if (_pending) {
		_pending.resolve(val);
		_pending = null;
	}
}

export const confirmStore = {
	get pending() {
		return _pending;
	},
	confirm,
	resolve
};
```

Reading `confirmStore.pending` inside a template or `$derived` is reactive
(that's what `ConfirmDialog.svelte` does). The store keeps timer side effects
too — `toastStore.push` schedules its own `setTimeout` dismissal
(`toastStore.svelte.js:11-19`), and caps the stack at 3 toasts:

```js
if (_toasts.length >= 3) {
	_toasts.shift();          // deep reactivity: mutation of a $state array works
}
_toasts.push(toast);
```

Consumers await a promise that a component resolves later
(`+layout.svelte:47`):

```js
const ok = await confirmStore.confirm({
	title: 'Log Out',
	message: 'Are you sure you want to log out?',
	danger: true
});
if (!ok) return;
```

And toasts are one-liners: `toastStore.success('Document restored')`
(`trash/+page.svelte:127`).

The **rule of thumb**: runes stores for new cross-component state;
`filterStore`-style writable stores remain where they already work;
`authStore`-style plain modules for anything the data layer needs.

---

## 8. The API layer

`web/src/lib/api.js` is the single gateway to the backend (448 lines). Every
endpoint is a method on one exported `api` object with nested groups:
`api.documents`, `api.tags`, `api.tasks`, `api.config`, `api.trash`,
`api.logs`, ...

### The two fetch wrappers

`request()` — for reads; returns data or `null`
(`api.js:27-41`):

```js
async function request(path, opts = {}) {
	try {
		const res = await fetch(path, withAuth(opts));
		if (res.status === 401) {
			handleUnauthorized();
			return null;
		}
		if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
		if (res.status === 204 || res.headers.get('content-length') === '0') return null;
		return await res.json();
	} catch (err) {
		console.error(`API ${path}:`, err);
		return null;
	}
}
```

`requestRaw()` — for mutations that need the status; returns
`{ ok, status, data }`, and on network failure `{ ok: false, status: 0,
data: null }` (`api.js:43-60`). Used by the login form to distinguish 401
(bad credentials) from other failures.

**The contract**: every error collapses to `null` (or `ok: false`). Callers
null-coalesce at the call site: `.then((data) => data ?? [])` everywhere
(`api.js:79-82`).

### Auth wiring

`withAuth()` injects the Bearer header from `authStore` on every request
(`api.js:19-25`); `handleUnauthorized()` fires once (module flag), clears the
session, and redirects to `/login` (`api.js:7-17`).

### Request building

- Query strings by template literal, user input through `encodeURIComponent`
  (`api.js:86-89`).
- Optional params via `URLSearchParams` (`api.js:215-224`).
- JSON bodies: explicit `Content-Type` + `JSON.stringify` (`api.js:91-96`).
- Uploads: `FormData`, **no** content-type header (`api.js:343-349`).
- Abort signals pass through untouched — `api.logs.get(name, lines, signal)`
  (`api.js:445-447`), used by the logs page (§9).
- Two helpers bypass `fetch` for downloads: a hidden `<form>` submit for batch
  downloads, `window.open` for a single file (`api.js:138-153,431-434`).

---

## 9. Data fetching in pages

There are **no load functions** (`+page.js`) — every page fetches in
`onMount` into `$state`, and re-fetches on demand. Three patterns:

### 1. Simple fetch-on-mount

The dashboard runs one parallel batch with `Promise.all`
(`+page.svelte:19-35`):

```js
async function fetchDashboard() {
	if (fetching) return;               // overlap guard
	fetching = true;
	try {
		[health, recentDocs, dashboard] = await Promise.all([
			api.health(),
			api.documents.list(15, 0),
			api.dashboard()
		]);
	} finally {
		fetching = false;
	}
}
onMount(() => {
	fetchDashboard();
});
```

Auto-refresh is an opt-in `$effect`-managed interval (§4) plus an
`onDestroy` cleanup (`+page.svelte:37-52`).

### 2. AbortController with stale-response guard

The logs page aborts the previous request and ignores responses that are no
longer current — identity check, not timestamp (`logs/+page.svelte:55-71`):

```js
async function fetchLogs() {
	if (controller) controller.abort();
	const myController = new AbortController();
	controller = myController;
	loading = true;
	const data = await api.logs.get(activeTab, lines, myController.signal);
	if (controller !== myController) return;   // stale response, drop it
	// ...
	controller = null;
}
```

with `onDestroy(() => { if (controller) controller.abort(); })` (`:167-170`).

### 3. The DataTable owns its fetching

List pages hand DataTable a `fetch({ sortBy, sortOrder, limit, offset })`
callback; the table calls it and manages paging/sort internally
(`documents/+page.svelte:290-297`):

```js
function fetch({ sortBy, sortOrder, limit, offset }) {
	const body = { ...buildFilterBody(), sort_by: sortBy, sort_order: sortOrder, limit, offset };
	return api.documents.searchStructured(body);
}
```

Pages trigger reloads by bumping a `refreshKey` prop (a number);
DataTable watches it in an `untrack`ed effect (§4). Detail pages re-fetch after
mutations (`refreshDoc()`, `documents/[id]/+page.svelte:74-78`).

### Loading / error / empty states

The canonical three-state chain (`logs/+page.svelte:218-237`) — note that once
data exists, refresh errors don't blank the UI:

```svelte
{#if loading && logs.length === 0}
	<p>Loading…</p>
{:else if error && logs.length === 0}
	<p>{error}</p>
	<button onclick={fetchLogs}>Retry</button>
{:else if logs.length === 0}
	<p>No log entries yet</p>
{:else}
	...lines...
{/if}
```

---

## 10. URL state anything

State that should survive reload/share/back-button lives in the query string.
Three writing styles exist; all read via `$page.url.searchParams` (store form)
or `page.url.searchParams` (`$app/state` form).

### 1. `$effect` writing back with `goto(..., { replaceState: true })` — logs page

Read once at init, then mirror state into the URL whenever it changes
(`logs/+page.svelte:28-53`):

```js
function syncFromURL() {
	const params = $page.url.searchParams;
	const tab = params.get('tab');
	if (tab && tabs.some((t) => t.id === tab)) activeTab = tab;
	// ... same for lines ...
}
syncFromURL();

$effect(() => {
	const search = $page.url.search;
	const params = new URLSearchParams(search);
	params.set('tab', activeTab);
	params.set('lines', String(lines));
	const qs = params.toString();
	const current = $page.url.search;
	if ('?' + qs !== current && current !== qs) {
		goto(resolve(`/logs?${qs}`), { replaceState: true, noScroll: true });
	}
});
```

`replaceState: true` keeps it out of history; the equality check avoids
infinite ping-pong.

### 2. `replaceState` from `$app/navigation` — documents page

The filter store subscription writes `?q=` with the serialized filter
(`documents/+page.svelte:141-167`; `replaceState(resolve(...))` — see §7 for
the full pattern). The `?q=` value is a mini-language (`tag:"x"`,
`type:"scientific paper"`, `created:from..to`, `missing:lang`, `size:>10mb`)
parsed/serialized by the pure functions in `lib/stores/searchFilter.js` —
`parseQueryString` / `serializeFilter` — so the URL is the source of truth
for the filter. `serializeFilter` always wraps field values in double quotes
(the tokenizer strips them); unconditional quoting keeps multi-word tags,
types, languages, and person names intact through the URL round-trip.

### 3. Plain `history.replaceState` — settings tabs, wizard step

When no SvelteKit navigation is needed (`settings/+page.svelte:23-28`):

```js
function switchTab(tab) {
	activeTab = tab;
	const url = new URL(window.location.href);
	url.searchParams.set('tab', tab);
	history.replaceState(null, '', url.pathname + url.search);
}
```

The wizard mirrors its step the same way (`web-wizard/+page.svelte:30-34`).

Also in play: `page.subscribe(($p) => ...)` for reacting to URL changes
(`tasks/+page.svelte:234-236` reads `?batch=`), and DataTable's own
`urlSync="dt"` prop, which persists `<prefix>_size/_page/_sort/_order` params
itself (`DataTable.svelte:130-149`).

---

## 11. Forms

### Submit handlers

`<form onsubmit={handleSubmit}>` with `e.preventDefault()` as the first line —
no framework. The login flow (`login/+page.svelte:12-34`) shows the shape:
validate → set `loading` → call API → branch on result → `goto` in a `finally`
that clears `loading`.

### Dirty tracking + `beforeunload`

Settings compares JSON snapshots (`settings/+page.svelte:43-62`):

```js
function snapshotState() {
	originalCfg = JSON.stringify(cfg);
	originalMimeOptions = JSON.stringify(mimeTypeOptions);
}

function isConfigDirty() {
	if (!cfg) return false;
	return (
		JSON.stringify(cfg) !== originalCfg ||
		JSON.stringify(mimeTypeOptions) !== originalMimeOptions
	);
}
```

registered in `onMount` with proper cleanup
(`settings/+page.svelte:130-131`):

```js
window.addEventListener('beforeunload', handleBeforeUnload);
return () => window.removeEventListener('beforeunload', handleBeforeUnload);
```

The document detail page does the same with a **`$derived`** dirty flag instead
(`documents/[id]/+page.svelte:31-36`):

```js
let dirty = $derived(
	doc &&
		(editTitle !== doc.title ||
			editLanguage !== (doc.language ?? '') ||
			editDocumentTypeId !== (doc.document_type_id ?? 1))
);
```

### Validation

Manual per-field `$state` error strings (wizard admin form,
`web-wizard/+page.svelte:222-245`), supplemented by HTML constraints
(`minlength`, `pattern` — `settings/+page.svelte:1021-1024`). Inline list
editing uses immutable array replacement (`settings/+page.svelte:154-174`).

---

## 12. Auth and role gating

The auth surface is `authStore` functions (§7):

```js
export function isAdmin() { return getRole() === 'admin'; }
export function isEditor() {
	return getRole() === 'editor' || getRole() === 'admin';
}
export function authEnabled() { return _authEnabled; }
```

### Route-level guard

The layout redirects unauthenticated users — an `$effect` that waits for the
bootstrap call so the first paint doesn't flash-redirect (`+layout.svelte:31-40`):

```js
$effect(() => {
	if (
		configLoaded &&
		authEnabled &&
		!authStore.isAuthenticated() &&
		$page.url.pathname !== '/login'
	) {
		goto(resolve('/login'));
	}
});
```

`onMount` bootstrap (`+layout.svelte:42-51`) reads `/wizard/bootstrap` to learn
`auth_enabled` + `missing_tools`, then optionally refreshes the profile.

### Page-level gates

Editor-only pages show a permission message (`trash/+page.svelte:204-205`,
identical in `documents/orphaned/+page.svelte:170-171`):

```svelte
{#if authStore.authEnabled() && !authStore.isEditor()}
	<p class="text-parchment-500">You do not have permission to view this page.</p>
{:else}
	...
{/if}
```

Settings uses `!authStore.isAdmin()` (`settings/+page.svelte:376-377`).

### Action-level gates

The dual condition "auth off OR editor+" is the standard idiom
(`documents/+page.svelte:317`):

```svelte
{#if !authStore.authEnabled() || authStore.isEditor()}
	<button onclick={handleBatchDelete}>Delete selected ({selectedDocs.length})</button>
{/if}
```

Inside DataTable cell builders, actions return `''` for viewers
(`tasks/+page.svelte:223`): `if (authStore.authEnabled() && !authStore.isEditor()) return '';`.
The sidebar hides nav links per role (`+layout.svelte:71-74,103-112`), and the
profile page guards itself in `onMount` (`profile/+page.svelte:20-26`).

---

## 13. The DataTable pattern and HTML-string cells

`DataTable.svelte` (445 lines) is the workhorse list component: server-side
paging/sort, skeleton loading rows, `refreshKey` reloads, row selection via a
rebuilt `Set`, keyboard accessibility, URL sync. It takes 14 props, all with
defaults (`DataTable.svelte:37-52`).

**The unusual part: columns are functions returning HTML strings**, inserted
raw with `{@html ...}` (`DataTable.svelte:283-287`):

```js
{@html col.cell(row[col.key], row)}
```

The contract is documented at the call site: *callers build cells with
`escapeHtml`; raw insertion is required*. The documents page defines columns
in a `$derived.by` block, e.g. (`documents/+page.svelte:43-52`):

```js
{
	key: 'title',
	label: 'Title',
	sortable: true,
	cell: (title, row) =>
		`<a href="${resolve(`/documents/${row.id}`)}" class="...">${escapeHtml(title || '')}</a>`
}
```

The pair that makes this safe: `escapeHtml` from `lib/utils/html.js` for
**user data**, and `actionButton()` from `lib/icons.js` for **static icons**.

### `icons.js` — SVG strings + a button builder

Icons are plain SVG markup strings (16×16, `stroke="currentColor"` so CSS
`text-*` colors them), not components (`icons.js:1-21`). `actionButton()`
(`icons.js:24-40`) builds a `<button>` string with `data-*` attributes:

```js
return `<button ${attrs} class="${cls}" title="${tooltip}" aria-label="${tooltip}">${svg}</button>`;
```

Cells concatenate them (values pre-escaped by callers — noted in the JSDoc
contract at `icons.js:29`):

```js
return `${actionButton(EDIT_ICON, 'Edit', 'text-parchment-400 hover:text-gold-500', { 'data-edit-tag': row.id })}
${actionButton(DELETE_ICON, 'Delete', 'text-parchment-400 hover:text-terracotta-500', { 'data-delete-tag': row.id })}`;
```

(`tags/+page.svelte:46-47`). Clicks are **delegated**: DataTable's
`onActionClick` handler, or a `<svelte:window onclick>` on the tasks page
(`tasks/+page.svelte:304`), dispatch on `e.target.closest('[data-...]')`.

---

## 14. The design system: Tailwind v4

Tailwind v4 is **CSS-first**: no `tailwind.config.js`. The entry is
`web/src/routes/layout.css`, imported by the layout (`+layout.svelte:2`):

```css
@import 'tailwindcss';
@plugin '@tailwindcss/forms';

@theme {
	--color-clay-950: #1a1512;
	--color-clay-900: #2a221d;
	--color-gold-500: #c9953a;
	--color-lapis-500: #4d7cb5;
	--color-parchment-200: #e8dcc8;
	--color-terracotta-500: #b84a3a;
	/* full 11-shade scales for clay, gold, lapis, parchment, terracotta */
}

@layer base {
	:root {
		color-scheme: dark;
	}
	body {
		touch-action: manipulation;
		-webkit-tap-highlight-color: transparent;
	}
}
```

Each `--color-*` token becomes a utility automatically: `bg-clay-950`,
`text-gold-500`, `border-clay-800`, with opacity modifiers like
`bg-lapis-500/20`. The semantic roles:

| Token family | Role |
|---|---|
| `clay` | surfaces and backgrounds (`clay-950` is the app background) |
| `gold` | primary accent — CTAs, focus rings |
| `lapis` | secondary accent (admin badges) |
| `parchment` | text on dark surfaces |
| `terracotta` | destructive actions |

Aesthetic conventions to keep: `focus-visible:ring-2 focus-visible:ring-gold-500
focus-visible:outline-none` on interactive elements, `motion-reduce:animate-none`
next to every spinner, arbitrary values like `h-[75vh]` for the PDF iframe
(`documents/[id]/+page.svelte:165`), and a `peer-checked:` toggle built purely
from classes (`settings/+page.svelte:1251-1261`).

The wizard uses the same system with a 10-color subset in
`web-wizard/src/app.css:5-17`. `.prettierrc` points
`prettier-plugin-tailwindcss` at `layout.css`
(`"tailwindStylesheet": "./src/routes/layout.css"`) so Prettier sorts utility
classes consistently with the project's generated class set.

---

## 15. Build and tooling

### Scripts (`web/package.json:6-13`)

```json
"dev": "vite dev",
"build": "vite build",
"preview": "vite preview",
"prepare": "svelte-kit sync || echo ''",
"lint": "prettier --check . && eslint .",
"format": "prettier --write .",
"test": "vitest run",
"test:watch": "vitest",
"test:e2e": "playwright test"
```

Node 24 is required (`.npmrc` has `engine-strict=true`); run `nvm use` (`.nvmrc`)
before any npm/npx command — never the shell's default Node, which rewrites
`package-lock.json` with its own version-dependent format.

### Vite (`web/vite.config.js`)

- Plugins: `@tailwindcss/vite` + `sveltekit()`.
- **Dev proxy**: `/api` and `/health` → `http://localhost:3000`
  (`API_PORT` overridable). The wizard proxies `/wizard` →
  `http://0.0.0.0:8420` (`web-wizard/vite.config.js:8-12`).
- **The alias stub**: Node builtins `child_process` and `url` are aliased to
  `src/lib/stubs/empty.mjs` (a one-line `export default {}`). Some transitive
  dependency imports them; in the browser bundle they must resolve to nothing.
  The path is computed portably: `new URL('src/lib/stubs/empty.mjs',
  import.meta.url).pathname` (`vite.config.js:5`).

### ESLint + Prettier

ESLint 10 **flat config** (`eslint.config.js`): `@eslint/js` recommended +
`eslint-plugin-svelte` recommended + prettier configs last (they disable
conflicting rules), `.gitignore` honored via `includeIgnoreFile`, browser+node
globals, and the Svelte parser gets the project config. Prettier: tabs, single
quotes, no trailing commas, 100 cols, with the svelte and tailwind plugins
(`.prettierrc`).

### Testing

Vitest + `@testing-library/svelte` + Playwright, wired through the Makefile
(`make test-web`, `make test-web-e2e`). Full details in
[docs/reference/tests.md](../reference/tests.md).

- **Two vitest projects** in `web/vite.config.js` (`test.projects`, both
  `extends: true` so they inherit the `sveltekit()` plugin — which resolves
  `$app/*` — and the stub aliases): `unit` (node env) for pure logic + runes
  stores, `components` (jsdom + `svelteTesting()` from
  `@testing-library/svelte/vite` for auto-cleanup) for anything touching
  `localStorage` (`authStore.js`, `api.js`) and for component tests.
- **Co-located `*.test.js` files** (mirrors Go's `_test.go`), explicit
  `import { describe, it, expect, vi } from 'vitest'` — no globals, so the
  eslint config is untouched.
- **Mocking conventions**: `$app/navigation`/`$app/paths`/`$app/state` via
  `vi.mock`; `$lib/api` via `vi.hoisted` factories in component tests; global
  `fetch` via `vi.stubGlobal` in `api.test.js`.
- **E2E** (`web/e2e/`): Playwright drives the static build served by
  `scripts/serve-static.mjs` (SPA fallback to `index.html` — `vite preview`
  can't be used because it runs the SSR server, which 500s on
  `authStore.js`'s import-time `localStorage` access). `e2e/helpers.js`
  seeds auth into `localStorage` and mocks `/api|/wizard|/health` with
  `page.route`. Requires `npx playwright install chromium` once.

### How the pieces meet the backend

In production the SPAs are static files inside the Go binaries; the dev server
proxies API calls to a locally running backend. There is no build-time
knowledge of the API — just relative URLs (`/api/v1/...`, `/wizard/...`) and
the `resolve()` helper for internal links.

---

## 16. The setup wizard

`web-wizard/` is deliberately minimal: one layout, one giant page
(`+page.svelte`, 1274 lines), no components, no stores, no auth.

### The step machine

One `$state` number, seeded from the URL and mirrored back
(`web-wizard/+page.svelte:6,30-34`):

```js
let step = $state(parseInt(new URL(window.location.href).searchParams.get('step') || '1') || 1);

$effect(() => {
	const url = new URL(window.location.href);
	url.searchParams.set('step', String(step));
	history.replaceState(null, '', url.pathname + url.search);
});
```

Each step is an `{#if step === N}` block. The flow: 1 config dir → 2 consumer
settings → 3 enricher settings → 4 "Setting Things Up…" (download progress,
3 s polling of `/wizard/config/status` until `pending_tasks === 0 && configured`)
→ 5 create admin user → 6 done (systemd instructions). `onMount` resumes at the
right step for an already-initialized install (`+page.svelte:46-65`).

### The config contract

The wizard posts config as a **flat map of dotted keys** (`buildConfigBody()`,
`+page.svelte:121-162`):

```js
return {
	'server.port': Number(cfg.server.port),
	'consumer.ocr.engine': cfg.consumer.ocr.engine,
	'consumer.ocr.languages': cfg.consumer.ocr.languages.filter(Boolean),
	'consumer.supported_files': mimeTypeOptions
		.filter((o) => o.checked)
		.flatMap((o) => o.extensions),
	// ...
	'database.sslmode': cfg.database.sslmode
};
```

The Go side merges it with `config.SaveMap` (viper dot-keys → YAML, see
`internal/config/setup.go:76`). The main app's settings page repeats the same
dotted-map pattern (`settings/+page.svelte:176-261`).

### Wizard vs main app

| Aspect | web-wizard | web |
|---|---|---|
| API client | bare `fetch`, throws errors, no auth (`lib/api.js:1-21`) | `withAuth` + 401 redirect + `null`-on-error |
| Endpoints | `/wizard/config`, `/wizard/config/status`, `/wizard/admin-user` | `/api/v1/...`, `/health`, `/wizard/bootstrap` |
| State | one page's `$state` | stores + components |
| Dialogs | `window.confirm` | `confirmStore` + `ConfirmDialog` |
| URL writes | `history.replaceState` directly | `goto`/`replaceState` from `$app/navigation` |

---

## 17. JavaScript language features in use

A quick tour of the JS features you'll meet constantly (aimed at people whose
main language isn't JS):

- **ES modules**: `import ... from '...'` / `export` — the only module system
  (`"type": "module"` in package.json). The `$lib`, `$app/*` aliases are
  SvelteKit conveniences on top.
- **Destructuring**: `let { open, title, onClose, children } = $props()`;
  array destructuring with rest: `[health, recentDocs, dashboard] =
  await Promise.all([...])`; parameter destructuring:
  `function push({ variant = 'info', message })`.
- **Default values** in destructuring and function params: `onSearch = () => {}`.
- **Optional chaining** `?.` and **nullish coalescing** `??`:
  `getRole() { return _user?.role ?? ''; }`, `cfg?.available_file_types`,
  `data ?? []` — used everywhere instead of `&&`/`||` for defaults.
- **Template literals**: backtick strings with `${}` interpolation — URLs,
  query strings, HTML cells: `` resolve(`/documents/${row.id}`) ``.
- **Arrow functions**: `(f) => ({ ...f, ...partial })` — object shorthand
  `{ title, message, danger, resolve }` is `{ title: title, ... }`.
- **Promises / async-await**: every page does `await api.xxx()`; `Promise.all`
  for parallelism; `new Promise((resolve) => ...)` in confirmStore.
- **Sets**: `new Set()` for selection state, rebuilt immutably (§4).
- **Spread**: `{ ...f, ...partial }` (object merge), `[...originalTypeBreakdown]`
  (copy), `{ ...opts, headers }`.
- **Array methods** do the heavy lifting: `map`, `filter`, `find`, `some`,
  `reduce`, `flatMap`, `sort((a, b) => ...)`, `slice`, `includes`.
- **`Object.freeze`** for shared constants (`defaultFilter`), `Object.entries`
  for map-like iteration in templates.
- **`localStorage`** for session persistence (`authStore`), `URLSearchParams`
  for query-string work, `AbortController` for request cancellation, `fetch`
  for all HTTP.
- **JSDoc** for public contracts (`@type`, `@typedef`) — the project's
  type-documentation of choice.

---

## 18. Feature → file quick reference

| I want to see... | Go to |
|---|---|
| Svelte 5 runes (`$state`, `$derived`, `$effect`) | any page; `web/src/routes/logs/+page.svelte`, `web/src/routes/+page.svelte` |
| `$props()` with defaults | `web/src/lib/components/DataTable.svelte:37`, `SearchBar.svelte:11` |
| `children` snippet + `{@render children()}` | `web/src/lib/components/Modal.svelte`, `+layout.svelte:199` |
| Runes store (module `$state`) | `web/src/lib/stores/confirmStore.svelte.js`, `toastStore.svelte.js` |
| Classic writable store | `web/src/lib/stores/filterStore.js` |
| Plain module (no reactivity) | `web/src/lib/stores/authStore.js` |
| Fetch wrappers / 401 handling | `web/src/lib/api.js:7-60` |
| URL ⇄ state sync | `web/src/routes/logs/+page.svelte:28-53`, `documents/+page.svelte:136-162` |
| AbortController pattern | `web/src/routes/logs/+page.svelte:55-71` |
| DataTable columns + `{@html}` cells | `web/src/routes/documents/+page.svelte:24-113`, `DataTable.svelte:283` |
| SVG icons + `actionButton` | `web/src/lib/icons.js` |
| Design tokens (`@theme`) | `web/src/routes/layout.css`, `web-wizard/src/app.css` |
| Auth/role gates | `web/src/routes/+layout.svelte:31-40`, `trash/+page.svelte:204` |
| Modal + confirm dialog | `web/src/lib/components/Modal.svelte`, `ConfirmDialog.svelte` |
| Dirty tracking + beforeunload | `web/src/routes/settings/+page.svelte:43-62`, `documents/[id]/+page.svelte:31-36` |
| Wizard step machine | `web-wizard/src/routes/+page.svelte:6,30-34` |
| Dotted-key config map | `web-wizard/+page.svelte:121-162`, `web/src/routes/settings/+page.svelte:176-261` |
| Vite aliases / proxy | `web/vite.config.js`, `web-wizard/vite.config.js` |
| SPA mode config | `web/svelte.config.js` (adapter-static + fallback) |

---

## 19. Idioms checklist and gotchas

### Do

- **Declare state with `$state`, compute with `$derived`, side-effect with
  `$effect`** — never `$:` or `on:` (legacy syntax has zero presence here).
- **Destructure `$props()` once** at the top of `<script>`, with defaults.
- **Accept children as a prop and render with `{@render children()}`.**
- **Use event props** (`onclick`, `onsubmit`, `oninput`) and call
  `e.preventDefault()` first in submit handlers.
- **Return a cleanup function from `$effect`** for intervals, observers, and
  listeners.
- **Null-coalesce API results** (`data ?? []`) — `request()` returns `null` on
  any failure.
- **Escape user data in `{@html}` cells** with `escapeHtml` from
  `$lib/utils/html.js`; icons are safe, data is not.
- **Keep URL-visible state in the query string** via `replaceState`-style
  writes, and read it with `$page.url.searchParams` / `page.url.searchParams`.
- **`gofmt`-equivalent: `npm run format`** (Prettier sorts Tailwind classes
  too) and `npm run lint`.
- **`nvm use` before any npm command** — `engine-strict=true` refuses other Node
  versions, and the shell's default Node rewrites `package-lock.json`.

### Gotchas that bit people before

- **Mutating a `$state` Set/Map in place is invisible to Svelte.** Rebuild and
  reassign (`selectedKeys = new Set(...)`) — the code comments say exactly this
  (`DataTable.svelte:92-93`, `logs/+page.svelte:127-130`).
- **`$derived` variables must not be assigned.** If you need to write, use
  `$state` (and `$derived.by` for multi-statement reads).
- **`$effect` runs when *any* read dependency changes**, including writes it
  itself triggers — wrap state writes in `untrack` when reacting to a
  "command" prop like `refreshKey`.
- **`onMount` is one-shot; `$effect` is not.** For intervals that toggle, put
  the lifecycle in the effect, not in `onMount`.
- **Stores fire subscribers immediately.** Guard the first emission when the
  callback has side effects (the `subscribed` flag in
  `documents/+page.svelte:136-162`).
- **`request()` swallows errors.** Check `data === null` before reading
  fields; use `requestRaw()` when you need the status.
- **401 during any request redirects to `/login`.** That is by design
  (`handleUnauthorized`); don't wrap your own redirects around it.
- **`{@html}` skips all escaping.** Only static markup or
  `escapeHtml`-processed strings go in there (see the comments at
  `DataTable.svelte:398` and `icons.js:29`).
- **SPA mode: no load functions, no SSR.** `window`/`localStorage` are safe to
  touch at module or component init — but don't rely on anything server-side.
- **The dev proxy only forwards `/api`, `/health`, `/wizard`.** New backend
  prefixes need a vite.config.js addition.
- **The wizard and the main app share the config contract** (dotted keys →
  `SaveMap`). Changing one side without the other breaks setup.

---

*Last verified against the tree: 2026-08-03. If code and doc disagree, code wins.*

# Testing Reference

## Overview

The suite splits by what it needs to run: plain unit tests with no database, PostgreSQL-backed
tests, CGo-gated packages, and the web UI's Vitest and Playwright suites. The authoritative tier
membership is the target lists in the Makefile; this file covers the strategy and the patterns
behind them.

- **`make test`** — the fast CGO_ENABLED=0 tier, no database required: pure-logic packages across
  the backend (LLM registry and analyzer, configuration, auth, logging, text normalization, task
  vocabulary, tools runner, mirror helpers, API middleware, flag parser, the API-docs renderer).
- **`make test-db`** / **`make test-backup`** — the PostgreSQL-backed suites: database queries,
  search, task system, service layer, API handlers, consumption pipeline, tag measurement.
- **`make test-cgo`** / **`make test-cgo-db`** — packages that only compile with CGo plus those
  linking the CGo adapters; the `-db` variant adds PostgreSQL.
- **`make test-web`** — Vitest unit and component tests under `web/` (no database).
- **`make test-web-e2e`** — Playwright smokes against the static SPA build with a mocked API
  (`page.route` intercepts `/api|/wizard|/health`); requires `npx playwright install chromium`
  once.

All non-CGo tiers run with `CGO_ENABLED=0` (no Tesseract, MuPDF, or
Ghostscript required); `make test-cgo` and `make test-cgo-db` run with
`CGO_ENABLED=1` and need `make build-deps` first. Every invocation goes through
the Makefile, which exports the `-tags "XLA,ORT"` tags, the CGo environment and
the GOTOOLCHAIN setting that pins every go invocation to the toolchain version
declared in go.mod — the same version the CI and release workflows install via
`setup-go`, so the matching toolchain module is fetched into the Go module cache
on first use — bare `go test` is not supported, and to use a different toolchain
override the variable on the make command line (`make GOTOOLCHAIN=go1.27 test`),
since the Makefile assignment wins over the process environment.

### Quick Start

```bash
make test          # runs all non-CGo tests
make test-verbose  # same with verbose output
make test-db       # database-dependent tests (requires TEST_DATABASE_URL)
make test-cgo      # CGo-gated tests (requires make build-deps first)
make test-cgo-db   # CGo + DB: the DB-gated suites in CGo packages (requires make build-deps + TEST_DATABASE_URL)
make test-one PKG=./internal/errs/   # single package; add RUN=Name to filter
make test-web      # web/ vitest unit + component tests (no database)
make test-web-e2e  # web/ Playwright smokes with mocked API (requires npx playwright install chromium first)
make vuln          # govulncheck over the Go vuln DB (CGO_ENABLED=0)
make vuln-cgo      # CGo-enabled variant, full call graph (requires make build-deps first)
```

### Continuous Integration

`.github/workflows/ci.yml` runs the Go tiers and the web tests on every push to `dev`/`master` and every PR to `master`:

- `web` job: lints, runs `npm run test` (vitest), and builds the main SPA (`npm ci && npm run lint && npm run test && npm run build` in `web/`), then runs the E2E smokes (`npx playwright install --with-deps chromium && npm run test:e2e` — the E2E `webServer` builds the SPA itself and serves it with an SPA fallback). Then builds the wizard SPA, stages both with `make stage-web`, and uploads them as the `web-assets` artifact. Staging is required because `internal/static/build` and `internal/api/wizard/static` are gitignored but embedded via `//go:embed` — a fresh checkout has no assets to compile against.
- `test` job (depends on `web`): downloads `web-assets`, stages it with `make stage-web-artifact`, then runs `make test` and `make test-db` against a postgres:17 service container (`TEST_DATABASE_URL` pointing at `localhost:5432`).
- `test-cgo` job (depends on `web`): installs the C build prerequisites, builds the C libraries with `make build-deps TOKENIZERS_ARCH=amd64` (cached under `build/` keyed by `hashFiles('Makefile')`), then runs `make test-cgo` (no DB; DB-gated tests in `internal/ops/queue`, `internal/enrichment`, `internal/tags`, `internal/tags/admission` and `internal/tags/reconcile` skip) and `make test-cgo-db` (with DB; those tests actually run, plus `internal/ops/dispatch`'s DB-gated tests and the `internal/service` suite) against the same postgres:17 service container. This surfaces the CGo-only packages (`internal/ops/cli`) and the cgo-linked packages (`internal/ops/consume`, `internal/documents/textextractor`, consumption under CGo) on every push/PR.
- `vulncheck` job (depends on `web`): stages the `web-assets` artifact, installs govulncheck (pinned `@v1.7.0`), and runs `make vuln`. Like `make test`, it runs with `CGO_ENABLED=0`, so the CGo-gated packages (`cmd/kushim`, `internal/ops/cli`, `internal/ops/queue`) are excluded from the CI scan; `make vuln-cgo` covers them locally.

The `global` ruleset requires the `test` and `web` checks to pass before a PR to `master` can merge.

---

## How Tests Work

### PostgreSQL via TEST_DATABASE_URL

Every test creates a connection to a PostgreSQL database specified by the `TEST_DATABASE_URL`
environment variable. The schema is initialized via `database.InitializeSchema` on each
connection.

**Per-package isolation**: Each test package gets its own dedicated database
(`edub_test_<package_dir>`, e.g. `edub_test_database`, `edub_test_task`). The database
name is derived automatically from the calling test file's package directory via
`runtime.Caller`. This allows packages to run in parallel without cross-contamination
from concurrent `ResetTestDatabase` calls.

**Auto-cleanup**: Each call to `NewTestDB` registers a `t.Cleanup` handler that closes
the connection and releases a reference. When the last reference to a test database
is released, the database is dropped via `DROP DATABASE ... WITH (FORCE)` (PostgreSQL
13+), which terminates any lingering connections before dropping.

Within a test that needs a clean slate regardless of isolation, each call starts with
truncated tables, reset sequences, and re-inserted seed data via `ResetTestDatabase`:

```go
q, db := database.NewTestQueries(t)
defer db.Close()
resetDB(t, q)
```

The `resetDB` helper deletes all rows from every table, resets identity sequences, and
re-runs the seed SQL for `document_type`, `people_type`, `tag`, and `tag_alias`.

Foreign key constraints are enforced by PostgreSQL just as in production.
The connection pool opens with the neutral default ceiling
(`database.DefaultMaxOpenConns`: 25 max open, idle capped at five, 5 min
lifetime). The per-execution derived ceilings (a forked executor's pool sized
to its batch's workers, the daemon's budget-derived pool) exist only in the
production paths; test databases always run at the default ceiling.

### CGo-Free Runner Mock

The consumption pipeline integration tests use a **mock runner** that satisfies the
`runner` interface (defined in `internal/documents/consumption/consumer.go`). This avoids
the need for real Tesseract (gosseract) or MuPDF:

```go
type runner interface {
    ExtractText(ctx context.Context, path string) (*tools.TextExtractionResult, error)
    OCR(ctx context.Context, docId, path string) (*tools.OCRResult, error)
    OptimizePdf(ctx context.Context, docId, path string) (*tools.PdfOptimizationResult, error)
}
```

The mock (`integrationTestRunner` in `internal/documents/consumption/integration_test.go`)
returns configurable text, OCR results, and optimization results, or injects
errors to test failure paths.

### Function Variable Override Pattern

The OCR adapter uses a function variable + `init()` override to make CGo-dependent
code testable without CGo:

```go
// adapter.go — always compiled
var newGosseract = func(...) (OCR, error) {
    return nil, fmt.Errorf("gosseract requires CGo")
}
```

```go
// gosseract.go — //go:build cgo
func init() {
    newGosseract = func(...) (OCR, error) { return NewGosseract(...) }
}
```

On non-CGo builds, the default error-returning stub is used. When CGo is available,
`init()` overrides the variable with the real constructor. This pattern is borrowed
from `crypto/x509` root cert loading in the Go standard library.

### Test Fixtures

- **Minimal PDFs**: `testutil.MinimalTextPDF(content)` generates a spec-compliant
  PDF with the given text content. Used by both database tests (for `FileFromPath`)
  and consumption tests.
- **Temp directories**: `testutil.NewTestConfig(t)` creates a full config with temp
  inbox, storage, data, and OCR directories. Cleanup is handled via the returned
  `func()`.
- **Deterministic UUIDs**: `database.CreateTestDocument` generates UUIDs from
  the current nanosecond timestamp (unique per call, no external dep).

---

## Running Tests

### Makefile targets

```bash
make test          # CGO_ENABLED=0, 60s timeout (no DB needed)
make test-verbose  # same with -v output
make test-web      # web/ vitest unit + component tests (no database)
make test-web-e2e  # web/ Playwright smokes with mocked API (requires npx playwright install chromium)
```

The Makefile explicitly sets `CGO_ENABLED=0` for test targets, overriding the
`export CGO_ENABLED := 1` used for build targets. This ensures tests run in
any environment regardless of C library availability.

The `make test` target list in the Makefile is the authoritative set. The DB-backed tests inside
`task`, `backup` and `tags` `t.Skip` when `TEST_DATABASE_URL` is unset, so each of these packages
runs in this tier and again under `make test-db` / `make test-backup`.

### Database-dependent tests (`make test-db`)

Packages requiring PostgreSQL 16+ (via `TEST_DATABASE_URL`):

```bash
make test-db
```

The `internal/backup` tests also require PostgreSQL (via `database.NewTestDB`);
they run through `make test-backup` with the same `TEST_DATABASE_URL`
environment. The SQL-dump restore tests execute `psql` through the `podman`
runtime against the `edub-test-pg` container (see the AGENTS.md test-database
instructions) instead of requiring `psql` on the host PATH.

### CGo-dependent tests (`make test-cgo`)

These packages' tests require `CGO_ENABLED=1` to compile because their source files
are tagged `//go:build cgo` (or, for `internal/llm/analyzer`,
`internal/ops/cli`, `internal/ops/consume`, `internal/ops/queue`,
`internal/tools`, `internal/enrichment`, `internal/api`,
`internal/api/wizard`, `internal/ops/container`, `internal/tags`,
`internal/tags/admission`, `internal/documents/textextractor` and
`internal/tags/reconcile`, the packages import or link the cgo-gated adapters).
Most use only pure-Go code; only the MuPDF tests and the command handlers
exercise actual CGo function calls.

Requires the C toolchain and built C libraries (Tesseract, Leptonica, MuPDF,
libtokenizers) on the host. Run inside the builder containers to avoid
installing C deps locally:

```bash
make test-cgo          # host, uses Makefile's CGO_ENABLED=1 export
make test-cgo-glibc    # podman: kushim-glibc-builder
make test-cgo-musl     # podman: kushim-musl-builder
```

**Packages in this tier:**

| Package | CGo at runtime? |
|---------|-----------------|
| `internal/cgo` | Yes — MuPDF page render |
| `internal/documents/ocr` | No (pure Go, build-tag gated) |
| `internal/documents/textextractor` | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/llm/analyzer` | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/matcher` | No (pure Go, build-tag gated) |
| `internal/tools` | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/api` | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/api/wizard` | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/ops/container` | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/ops/cli` | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/ops/consume` | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/ops/queue` | No (pure Go tests + DB-gated tests; package links the cgo adapters transitively) |
| `internal/enrichment` | No (pure Go tests + one DB-gated test; package links the cgo adapters transitively) |
| `internal/tags` | No (pure Go tests + DB-gated tests; package links the cgo adapters transitively) |
| `internal/tags/admission` | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/tags/reconcile` | No (pure Go tests + DB-gated tests; package links the cgo adapters transitively) |

`internal/ops/cli` is not part of `make test`: the package only compiles with
CGo (`hugot.go` imports the `//go:build cgo`-gated matcher engine), and
kushim — the only binary importing it — is always built with CGo.
`internal/documents/textextractor` likewise runs only in this tier: it is not
part of `make test`, and its `pdftotext` adapter tests skip when the binary is
absent.
`internal/tools` runs in both tiers: `make test` builds it without CGo, and
`make test-cgo` re-runs it with the cgo adapters linked.
`internal/llm/analyzer` likewise runs in both tiers: `make test` builds it
without CGo, and `make test-cgo` re-runs it with the cgo adapters linked.
`internal/api`, `internal/api/wizard` and `internal/ops/container` also run
in both tiers for the same reason: their packages link the cgo adapters
transitively, so the cgo-linked build is exercised on every push/PR.

### Full CGo+DB run (`make test-cgo-db`)

Runs the consumption pipeline tests with `CGO_ENABLED=1` against PostgreSQL —
the same suite as `make test-db` but with the package linked under CGo (the
cgo-gated adapters resolve at compile time, MuPDF is available). Page-count
assertions remain mode-independent: `setupConsumerTest` stubs `pageCounter` to
return 1 unconditionally, so the assertions are deterministic whether MuPDF
is reachable or not. The tier's value is exercising the full CGo+DB
pipeline (link paths, transactions, future code paths that touch
`countPages`) — not asserting on `countPages` output.

Also runs `internal/ops/configtask` (migrate-db/migrate-storage tests). The
migrate-db tests exec `psql` inside the test DB container via
`database.runtime=podman|docker` — `containerRuntime(t)` picks the first CLI
on PATH that can see the running container (so podman on a runner whose
container runs under docker falls through to docker) — so no host
`postgresql-client` is needed; the container name comes from
`TEST_DATABASE_CONTAINER` (default `edub-test-pg`, CI sets `postgres`):

Also runs `internal/ops/queue` (DB-gated tests in a CGo package — the stale-reclaim
recovery `TestReclaimStaleBatches` and `TestReclaimStaleTasksQuarantinesInboxFiles`).
Those tests `t.Skip` when `TEST_DATABASE_URL`
is unset (as in the `make test-cgo` step), so the package must be listed here
for them to actually exercise the behavior in CI.

Also runs `internal/ops/container` (the schema-role split test
`TestSchemaRoleSplitsTheBootstrap`, DB-gated: a forked-executor container fails
against — and never creates — a database the launcher never bootstrapped, while
the owner container creates it and applies the schema). The package is listed in
this tier so the DB-gated test exercises PostgreSQL under the cgo-linked build.

Also runs `internal/ops/consume` (the executor-token, file-count and
direct-run summary tests — pure Go; the per-batch pool removal left no
DB-gated test in the package, so nothing here skips).

Also runs `internal/ops/dispatch` (the batch dispatcher's launcher and
dispatcher tests). Its launcher tests are DB-gated and `t.Skip` when
`TEST_DATABASE_URL` is unset, so the package is listed in this tier to
exercise PostgreSQL under the cgo-linked build.

Also runs `internal/service` (the batch read model, the CRUD services and the
lifecycle ops the API and CLI build on). Its tests are ungated database tests
requiring `TEST_DATABASE_URL`, so the package is listed only in the DB tier,
like `internal/api/handlers`.

Also runs `internal/tags/reconcile` (DB-gated reconciler tests — the opening-gate
contract `TestRunZeroWorkOnQuietStore`, `TestRunFullAfterStoreMutation`,
`TestRunWorksWhenProvisionalGraceElapsed`, `TestRunBootstrapsWithoutCompletedRun`
and `TestRunRearmsSignalWhenRunFails`, plus `TestApplyKeepFlip_ZeroDocsCreatesNoTag`,
which asserts the keep-flip document-ID ordering fix against a real database).
Those tests also `t.Skip` when `TEST_DATABASE_URL` is unset, so the package is
listed in both tiers.

The `internal/tags/admission` package (identity planning, adjudication,
compound decomposition, verdict-event and tag-quality tests) runs under both
`make test-cgo` and `make test-cgo-db`: its DB-gated tests
(`TestPlanIdentity_LogsPendingPairs`, `TestPlanIdentity_FoldStaysPendingWithoutJudge`,
`TestPlanIdentity_StoreExactAppliesOntoExistingRow`,
`TestPlanIdentity_StoreLoserRegistersAsPairAlias`,
`TestAdjudicateIdentityPairs`, `TestResolvePending_Fold`,
`TestResolvePending_SameIntentPair`, `TestResolvePending_AdjudicatorDisabled`,
`TestResolvePending_FoldTargetDeleted`,
`TestTagQualityEvent_InsertAndCacheRead`, `TestApplyTagQuality_PartialSalvage`,
`TestRunDerivesThenSkipsOnClearSignal`, `TestRunRearmsSignalWhenRunFails`) `t.Skip` when `TEST_DATABASE_URL` is
unset, and the `test-cgo-db` listing is what makes them exercise PostgreSQL in
CI. The parent `internal/enrichment` package also runs under both tiers: its
DB-gated tests (`TestEnrich_SuggestionInjectionBar`,
`TestPromoteEligible_CompositionHold`) `t.Skip` when `TEST_DATABASE_URL` is
unset, and the `test-cgo-db` listing runs them against PostgreSQL.

Also runs `internal/tags` (identity planning, the extension algebra, the
admission and composition rules, the composition promotion hold, and the
store-changed signal protocol `TestOpenStoreSignal`). Its
DB-gated tests `t.Skip` when `TEST_DATABASE_URL` is unset, so the package is
listed in both CGo tiers.

Also runs `internal/tags/curation` (tag CRUD, merge, aliases,
composition-evidence writes). Its tests are ungated database tests, so it is
listed only in the DB tier, like `internal/api/handlers`.

```bash
make test-cgo-db   # requires make build-deps first + TEST_DATABASE_URL
```

### Single package

```bash
# Without DB (CGO_ENABLED=0)
make test-one PKG=./internal/errs/

# Specific test
make test-one PKG=./internal/database/ RUN=TestTaskLifecycle

# CGo-gated package (requires make build-deps first)
make test-cgo
```

### Web UI tests (`make test-web` / `make test-web-e2e`)

**`make test-web`** runs Vitest over two projects defined in `web/vite.config.js`
(`test.projects`, both `extends: true` so they inherit the `sveltekit()` plugin —
which resolves `$app/*` modules — and the `child_process`/`url` stub aliases):

- `unit` (node env) — pure logic and runes stores: `searchFilter.js` (tokenizer,
  query-string parser, size/date parsing, serialize round-trip), `utils/html.js`
  (escaping, size/duration/relative formatting), `filterStore.js`, `toastStore.svelte.js`,
  `confirmStore.svelte.js`.
- `components` (jsdom + `svelteTesting()` from `@testing-library/svelte/vite` for
  auto-cleanup) — `authStore.svelte.js` and `api.js` (both touch `localStorage` at import
  time, so they need the DOM), plus the component tests: `DataTable.svelte` (rows,
  sort refetch, empty/error states, `onTotal` reporting), `FilterPanel.svelte` (filter-store wiring,
  tag autocomplete, failure toast), `UploadModal.svelte` (upload success/error paths),
  `tags/ProvisionalReview.svelte` (verdict labels: recorded outcome preferred, legacy rubric
  fallback, unjudged names, the identity-hold, split-hold and degraded-reason pills, the
  `not_form_genre` criterion chip; filters
  and paging: select → API args and `prov_*` URL sync incl. the tab hash, URL round-trip with
  invalid entries dropped, pager end states, last-page clamp after a reject, and the
  `pending_identity_total`-driven reevaluate button).

Test files are co-located next to their sources as `*.test.js` (mirroring Go's
`_test.go` convention) and import `describe/it/expect/vi` explicitly — no globals,
so `eslint.config.js` needed no changes. Mocking conventions:

- `$app/*` modules are mocked with `vi.mock` where a unit imports them (`$app/navigation`
  `goto`, `$app/paths` `resolve`, `$app/state` `page`).
- `$lib/api` is mocked with `vi.hoisted` factories in component tests (`FilterPanel`,
  `UploadModal`, `ProvisionalReview`) so no network calls escape.
- `api.test.js` stubs the global `fetch` (`vi.stubGlobal`) and asserts the Bearer
  header, 401 → logout + redirect, null propagation, `requestRaw` shape, and the
  `supportedMimeTypes` cache.

**`make test-web-e2e`** runs Playwright against the static SPA build
(`web/playwright.config.js`): the `webServer` runs `npm run build` and then serves
`build/` via `web/scripts/serve-static.mjs` — a tiny zero-dependency static server
that falls back to `index.html` for unknown routes, mirroring adapter-static's
  `fallback` behavior in production (SvelteKit's own `vite preview` cannot be used:
  it runs the SSR server, and `authStore.svelte.js`'s import-time `localStorage` access 500s
  every route). The specs (`e2e/login.spec.js`, `documents.spec.js`,
`upload.spec.js`) share `e2e/helpers.js`, which seeds `localStorage` auth via
`addInitScript` and installs `page.route` mocks for `/api|/wizard|/health` returning
minimal realistic payloads matching `web/src/lib/api.js` expectations. First run
requires `npx playwright install chromium` (CI uses `--with-deps`).

### Vulnerability scanning (`make vuln` / `make vuln-cgo`)

`make vuln` runs govulncheck against the Go vuln DB with `CGO_ENABLED=0`, so no
C toolchain is needed. It scans every package except `cmd/kushim`,
`internal/ops/cli` and `internal/ops/queue` — those reference CGo-gated symbols
and cannot load without CGo, the same split that separates `make test` from
`make test-cgo`.
`make vuln-cgo` is the CGo-enabled variant (full call graph incl. the
gosseract/hugot adapters) and requires `make build-deps` first:

```bash
make vuln          # CGO_ENABLED=0, all loadable packages
make vuln-cgo      # full call graph, requires make build-deps first
```

govulncheck exits non-zero on reachable findings, which fails the CI
`vulncheck` job. Fix findings via `go get <module>@<fixed>` + `go mod tidy`.
The scan covers the standard library of the pinned toolchain (the version
declared in go.mod via the Makefile's GOTOOLCHAIN export), so standard-library
findings are fixed by raising the `go` line in go.mod rather than by upgrading
the locally installed Go.

### Test Helpers

The `internal/testutil` package provides reusable test infrastructure:

| Function | Purpose |
|----------|---------|
| `NewTestLogger()` | Discard logger for silent test output |
| `NewTestConfig(t)` | Config with temp dirs, cleanup function |
| `MinimalTextPDF(content)` | Valid PDF byte slice with text |
| `CreateTestPDF(t, path, content)` | Writes minimal PDF to disk |
| `CreateTestFile(t, path, content)` | Writes arbitrary file to disk |
| `AssertEqual(t, got, want, msg)` | Value equality check |
| `AssertNoError(t, err, msg)` | Nil error check |
| `AssertError(t, err, msg)` | Non-nil error check |

The `internal/database` package also exports:

| Function | Purpose |
|----------|---------|
| `NewTestDB(t)` | PostgreSQL via `TEST_DATABASE_URL` — per-package isolated database (`edub_test_<pkg>`), schema initialized, `t.Cleanup` closes connection + drops database on last reference |
| `NewTestQueries(t)` | Helper returning both `*Queries` and `*sql.DB` |
| `CreateTestDocument(t, queries, title)` | Inserts a document, returns auto-ID and UUID |
| `SeedTagByName(t, queries, name)` | Returns a seeded tag (by name or first) |
| `insertDoc(t, queries, title, md5, sha512)` | Helper for inserting docs with raw checksums (unexported, same-package use) |

### Not wired into any tier

The `internal/documents/converter` and `internal/documents/pdfoptimizer` adapter tests are not
invoked by any Makefile target.

---

## Patterns & Conventions

### Assertion duplication (by design)

The `database` package has local `assertNoError` / `assertEqual`
helpers rather than importing from `testutil`. This is because `testutil` imports
`config`, which imports `database` (through `setup.go`), creating an import cycle
when the `database` test package imports `testutil`. Other packages (search, task,
handlers, consumption) use `testutil` assertions directly.

### Import cycle avoidance

```
database.test → testutil → config → database  (CYCLE)
search.test  → testutil ✓  (no cycle)
```

The `testutil` package avoids importing any `database`-adjacent types to minimize
the cycle surface. `newMockTagService` lives in the handler test file for this reason.

### No CGo in non-CGo tiers

The `make test`/`make test-db` tiers run with `CGO_ENABLED=0`. The CGo-dependent
code paths (gosseract OCR, MuPDF text extraction/optimization) are tested via
mocks at the `runner` interface level and via build-constrained stubs at the
adapter level; the CGo packages themselves run under `make test-cgo` /
`make test-cgo-db` (see above).

---

## Adding Tests

### New database query

1. Add SQL to `internal/database/sql/queries/`
2. Run `sqlc generate`
3. Add a test in `internal/database/integration_test.go`

### New API endpoint

1. Add the handler function
2. Add tests in `internal/api/handlers/handlers_test.go` using `httptest.ResponseRecorder`
   and the `req()` / `rec()` helpers

### New consumption path

1. Implement the adapter
2. For CGo adapters, add a `//go:build cgo` tag and a `//go:build !cgo` stub
3. For new consumer behavior, add mock responses to `integrationTestRunner`
   and test in `internal/documents/consumption/integration_test.go`

### New task type

1. Implement `task.Handler`
2. Add test in `internal/task/task_test.go` using the `mockHandler` utility

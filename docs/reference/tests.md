# Testing Reference

## Overview

The project has **20+ test packages and 375+ unit/integration tests**, with a three-tier
run strategy (Go backend + web UI):

- **`make test`** (12 packages, no database required) — LLM registry, config, API types,
  tools runner, utils (text normalization, logging), tagmatch, storage walk, auth tokens,
  auth middleware + permissions, content analyzer (prompt building, tag filtering, token
  estimation), error classification (`errs`), and worker pool lifecycle.
- **`make test-db`** (6 additional packages, requires PostgreSQL) — database CRUD queries,
  search engine, task system (store/dispatcher/runner/pool), service layer (batch,
  orphaned, errored files, users, enrichment, API keys), API handlers, and consumption
  pipeline.
- **`make test-web`** (vitest, no database) — 63 unit + component tests across 10 files
  in `web/`: pure logic (`searchFilter`, `html` utils), stores (`filterStore`,
  `authStore`, `toastStore`, `confirmStore`), the `api` client (fetch stubbing, auth
  header, 401 handling), and the logic-dense components (`DataTable`, `FilterPanel`,
  `UploadModal`). Component tests run under jsdom with `@testing-library/svelte`.
- **`make test-web-e2e`** (Playwright, no backend) — 4 smoke specs in `web/e2e/`
  (login, documents list + detail, upload) driven against the static SPA build with a
  mocked API (`page.route` intercepts `/api|/wizard|/health`); no `edub`/PostgreSQL
  needed. Requires `npx playwright install chromium` once.

All non-CGo tiers run with `CGO_ENABLED=0` (no Tesseract, MuPDF, or
Ghostscript required); `make test-cgo` and `make test-cgo-db` run with
`CGO_ENABLED=1` and need `make build-deps` first. Every invocation goes through
the Makefile, which exports the `-tags "XLA,ORT"` tags and the CGo environment
— bare `go test` is not supported.

### Quick Start

```bash
make test          # runs all non-CGo tests
make test-verbose  # same with verbose output
make test-db       # database-dependent tests (requires TEST_DATABASE_URL)
make test-cgo      # CGo-gated tests (requires make build-deps first)
make test-cgo-db   # consumption with CGo + DB (requires make build-deps + TEST_DATABASE_URL)
make test-one PKG=./internal/errs/   # single package; add RUN=Name to filter
make test-web      # web/ vitest unit + component tests (no database)
make test-web-e2e  # web/ Playwright smokes with mocked API (requires npx playwright install chromium first)
make vuln          # govulncheck over the Go vuln DB (CGO_ENABLED=0)
make vuln-cgo      # CGo-enabled variant, full call graph (requires make build-deps first)
```

### Continuous Integration

`.github/workflows/ci.yml` runs the Go tiers and the web tests on every push to `dev`/`master` and every PR to `master`:

- `web` job: lints, runs `npm run test` (vitest), and builds the main SPA (`npm ci && npm run lint && npm run test && npm run build` in `web/`), then runs the E2E smokes (`npx playwright install --with-deps chromium && npm run test:e2e` — the E2E `webServer` builds the SPA itself and serves it with an SPA fallback). Then builds the wizard SPA, stages both with `make stage-web`, and uploads them as the `web-assets` artifact. Staging is required because `internal/static/build` and `internal/wizard/static` are gitignored but embedded via `//go:embed` — a fresh checkout has no assets to compile against.
- `test` job (depends on `web`): downloads `web-assets`, stages it with `make stage-web-artifact`, then runs `make test` and `make test-db` against a postgres:17 service container (`TEST_DATABASE_URL` pointing at `localhost:5432`).
- `test-cgo` job (depends on `web`): installs the C build prerequisites, builds the C libraries with `make build-deps TOKENIZERS_ARCH=amd64` (cached under `build/` keyed by `hashFiles('Makefile')`), then runs `make test-cgo` and `make test-cgo-db` against the same postgres:17 service container. This surfaces the CGo-only packages (`internal/commands`, consumption under CGo) on every push/PR.
- `vulncheck` job (depends on `web`): stages the `web-assets` artifact, installs govulncheck (pinned `@v1.7.0`), and runs `make vuln`. Like `make test`, it runs with `CGO_ENABLED=0`, so the CGo-gated packages (`cmd/kushim`, `internal/commands`) are excluded from the CI scan; `make vuln-cgo` covers them locally.

The `global` ruleset requires the `test` and `web` checks to pass before a PR to `master` can merge.

---

## Package Layout

| Package | Tests | What it covers |
|---------|-------|---------------|
| `internal/llm` | 27 | Registry creation (fixture + embedded fallback), model lookup for known/missing providers and models, adapter/provider/model listing, default URL derivation, catalog reload, concurrent access safety |
| `internal/config` | 38 | Default config values, ParseHHMM, polling window validation (valid + invalid), IsWithinActiveWindows, OCR workers, finalizeConfig (reclaim max retries, doc-type refinement non-negative, request-delay bounds, fallback validation: missing adapter/provider/model rejected, negative fallback request_delay rejected, valid enabled fallback accepted, nil/disabled fallback skipped), Load (applies OcrWorkers), SaveMap (merge/no file/invalid file), watcher (fire on change/missing file/invalid YAML) |
| `internal/api/types` | 11 | ConfigResponseFrom: logging defaults/custom, reclaim max retries, prompt template, max batch delete, doc type refinement, pause on credit error, fallback (nil → omitted; enabled fallback maps all llm fields), consumer fields (supported_files, converter), available file types (required flag, extension aliases) |
| `internal/mime` | 9 | IsPDF/IsImage/IsOfficeDoc predicates, ExtensionFromMimeType, MimeTypeFromExtension (canonical + alias extensions, case-insensitive, unknown → octet-stream), ExtensionsFor (canonical + aliases per type, nil for unknown), BuildExtensionSet, Supported constants + slice invariants |
| `internal/tools` | 20 | OptimizePdf (no timeout/with timeout/file not found/cancellation/nil optimizer), ConvertToPdf (nil converter/file not found/with converter/timeout deadline), AnalyzeContent/AnalyzeDocType (timeout deadline, nil analyzer), isProviderError (generic/credits true; content-too-large, token-limit, canceled, deadline-exceeded false — direct and wrapped), fallback paths (provider error → fallback result; ContentTooLargeError, cancellation, and DeadlineExceeded never call the fallback; no fallback configured → error; fallback failure propagates with the fallback provider; doc-type refinement fallback) |
| `internal/utils` | 8 | Truncate (ASCII, CJK, mixed, trim), EstimateTokens (ASCII/CJK/mixed), NormalizeForDB (accent folding, punctuation, spaces), NormalizeForDB_Concurrent (8-goroutine race guard pinning exact outputs), NewLogger level parsing, SetLevel gating (Info/Error/Debug/Warn), REQID prefix formatting, file logging (append/invalid path/levels/source exclusion/level prefix), SlogLogger bridge, LevelPriority/LevelName |
| `internal/tagmatch` | 3 | `MaxMatchBodyBytes` derivation with floor/ceiling clamping; `truncateUTF8` UTF-8-safe truncation with CJK and ASCII boundary tests; idempotency check |
| `internal/storage` | 18 | DetectFileType (uuid/dbid/random), WalkStorageDir (finds PDFs/skips non-PDF/recent/unknown key types/missing dirs), QuarantineFile, RemoveOrphanedFile, CopyToConsumptionDir, TrashDir/DocumentTrashDir, MoveToTrash (happy path, missing original, missing storage), RestoreFromTrash (happy path, missing trash original, both missing, round-trip), RemoveFromTrash (exists, nonexistent) |
| `internal/auth` | 7 | Session secret generation, JWT generation/validation round-trip with role, wrong secret rejection, expired token rejection, malformed token rejection, token part structure, ValidRole cases |
| `internal/api` | 24 | Auth middleware: public path bypass, no-token 401, invalid token 401, wrong secret 401, valid token passes with context injection (includes role), missing bearer prefix, empty authorization header, disabled bypasses all paths, valid API key 200 (includes role), invalid API key 401, wrong prefix falls through to JWT, auth-disabled bypasses API key check, internal error returns 500, valid cookie, invalid cookie, header takes priority over cookie. Permission middleware: RequireRole for admin/editor/viewer (allows+forbids), missing role, invalid role |
| `internal/tools/adapters/contentanalyzer` | 15 | NormalizeTags (transforms, accent folding, dedup/drop), NormalizeCore_Concurrent (8-goroutine race guard pinning exact outputs), BuildPrompt (default/custom/malformed/execution error/whitespace), BuildDocTypePrompt (with/without metadata), ExtractHeadTailWords, FilterTags (18 cases: person token overlap, title overlap, doc-type match, known name subset, >3-word cap, maxTags cap, combined rules), DocMetadata.Format (all fields/partial/zero-valued), checkContentTooLarge (nil caps/zero/negative/within limit/exceeds), parseTokenLimitError (no match/empty/valid OpenAI/zero tokens/partial match) |
| `internal/errs` | 7 | Error constructors preserve kind/op/cause, Error() string formatting, Unwrap(), KindOf through plain/wrapped/nil errors, FromDB (nil→nil, ErrNoRows→NotFound, unique violation→Conflict, other→Internal), PgError predicates (unique/foreign key/deadlock/serialization/plain/wrapped) |
| `internal/pool` | 5 | StartStop (workers call runner, stop terminates), ContextCancellation (cancel stops pool), DoubleStop (sync.Once no panic), PanicRecovery_Restart (worker survives panic and restarts), PanicRecovery_StopDuringRestart (clean shutdown during restart delay) |
| `internal/database` | 37 | sqlc-generated CRUD, task lifecycle, enrich waiting flow, enrich discard/restore/sweep queries (SetEnrichTaskWaiting, RestoreDiscardedEnrichTasks, DiscardWaitingEnrichesOfFailedConsumes), batch ownership, FTS-adjacent operations, document/tag/people/document-type CRUD, saved searches, dashboard analytics queries (empty DB + mixed data), structured search missing filters (MissingLanguage/MissingType/Untagged), structured search people filters (typed relationship scoping, `PersonAnyType` across relationship types, unknown-person miss, count/search parity), structured search sort by `page_count` (asc/desc ordering, unknown sort key falls back to default), WithDocumentCount queries, backup lock lifecycle, gated task claiming, soft delete (TestUpdateDeleteDocument now asserts soft-delete semantics) |
| `internal/search` | 8 | tsvector search with snippets, ranking, pagination; structured search with mime/language/date/missing filters; query sanitization; engine construction |
| `internal/task` | 30 | Store (create/get/claim/complete/fail), dedup key uniqueness, dispatcher enqueue with custom status/ID, runner (complete/fail/no-tasks), panic recovery (panicking handler → task failed with `panic: <text>`, not claimable), nil payload handling, backup lock gating for consume/enrich/config, pool lifecycle, Retry (restores discarded enrich of a retried consume), consume-handler early-exit discards (external `task_test` package exercising `handlers.ConsumeTaskHandler` against a DB-backed store) |
| `internal/service` | 76 | Batch create/get/owner-state/pending/active/cancel/queue, RetryFailed (restores discarded enriches, batch-scoped), ResetStaleProcessingTasks (restore + global sweep), ResetProcessingTasksByBatch restore, orphaned scan/delete/restore/move-to-inbox, errored files list/download/delete/delete-all, user API key create/revoke/rotate/validate, user Create with role defaults/explicit/invalid, UpdateRole (valid/invalid), Update with role change, password validation (12+ rules) |
| `internal/api/handlers` | 65 | Document CRUD, tag/people/DocumentType CRUD, user CRUD (with role), task endpoints, saved searches, concurrent operations, dashboard running tasks + analytics + processing health, analytics error path, config handler get/status, batch delete limits, error helpers, auth login (valid/invalid/empty/claims/role), auth logout, API key generate/revoke/rotate/status/forbidden/invalid-id/not-found + wire-format contract (has_api_key present, has_key absent, api_key_prefix present), MeHandler (valid/missing-id/not-found), self-service API key handlers (MeGenerateKey/MeRevokeKey/MeRotateKey/MeGetKeyStatus/unauthorized + wire-format contract), orphaned handler (list/scan/delete/restore/move-to-inbox/delete-all/move-all), errored handler (list/download/delete/delete-all), logs handler (invalid name/file not found/success/line clamping/large file tail/empty file), people-type reserved-name guard (create/update rejected for `person`, case-insensitive; non-reserved name accepted) |
| `internal/consumption` | 20 | Full consumer pipeline via mock runner (file discovery, DB transaction, file movement, duplicate detection), file I/O helpers (get, move, copy, remove, clean up), checksum calculation, orphaned file management |
| `internal/commands` | 16 | Config handler (help/path/validate valid+invalid/get/missing key/set/invalid set/unset/missing key/unset without key/dump/unknown args), parseValue, deleteNestedKey, highlight snippet ANSI markers. CGo-gated package: compiles only under `CGO_ENABLED=1` (runs via `make test-cgo`) |
| `internal/backup` | 12 | Create (full backup/missing DB/missing storage/no files/SQL dump content), ApplyRetention (delete oldest/keep all/keep 0), ValidateArchive (valid/invalid gzip/missing manifest/missing file), ExtractArchive (valid/path traversal/symlink skip), ReplaceFiles (SQL dump/unknown format), CopyDir |

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
re-runs the seed SQL for `document_type`, `people_type`, and `tag`.

Foreign key constraints are enforced by PostgreSQL just as in production.
The connection pool uses `SetMaxOpenConns(25)`.

### CGo-Free Runner Mock

The consumption pipeline integration tests use a **mock runner** that satisfies the
`runner` interface (defined in `internal/consumption/consumer.go`). This avoids
the need for real Tesseract (gosseract) or MuPDF:

```go
type runner interface {
    ExtractText(ctx context.Context, path string) (*tools.TextExtractionResult, error)
    OCR(ctx context.Context, docId, path string) (*tools.OCRResult, error)
    OptimizePdf(ctx context.Context, docId, path string) (*tools.PdfOptimizationResult, error)
}
```

The mock (`integrationTestRunner` in `internal/consumption/integration_test.go`)
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
make test          # CGO_ENABLED=0, 12 packages, 60s timeout (no DB needed)
make test-verbose  # same with -v output
make test-web      # web/ vitest unit + component tests (no database)
make test-web-e2e  # web/ Playwright smokes with mocked API (requires npx playwright install chromium)
```

The Makefile explicitly sets `CGO_ENABLED=0` for test targets, overriding the
`export CGO_ENABLED := 1` used for build targets. This ensures tests run in
any environment regardless of C library availability.

**12 packages run without a database:** `llm`, `config`, `api/types`, `tools`,
`utils`, `tagmatch`, `storage`, `auth`, `api`, `tools/adapters/contentanalyzer`,
`errs`, `pool`.

### Database-dependent tests (`make test-db`)

Packages requiring PostgreSQL 16+ (via `TEST_DATABASE_URL`):

```bash
make test-db
```

The `internal/backup` tests also require PostgreSQL (via `database.NewTestDB`);
they run through `make test-backup` with the same `TEST_DATABASE_URL`
environment.

### CGo-dependent tests (`make test-cgo`)

These 26 tests require `CGO_ENABLED=1` to compile because their source files
are tagged `//go:build cgo` (or, for `internal/commands`, the package imports
the cgo-gated tagmatcher adapter). Most use only pure-Go code; only the MuPDF
tests and the command handlers exercise actual CGo function calls.

Requires the C toolchain and built C libraries (Tesseract, Leptonica, MuPDF,
libtokenizers) on the host. Run inside the builder containers to avoid
installing C deps locally:

```bash
make test-cgo          # host, uses Makefile's CGO_ENABLED=1 export
make test-cgo-glibc    # podman: kushim-glibc-builder
make test-cgo-musl     # podman: kushim-musl-builder
```

**4 packages, 26 tests:**

| Package | Tests | CGo at runtime? |
|---------|-------|-----------------|
| `internal/tools/adapters` | 2 | Yes — MuPDF page render |
| `internal/tools/adapters/ocr` | 6 | No (pure Go, build-tag gated) |
| `internal/tools/adapters/tagmatcher` | 2 | No (pure Go, build-tag gated) |
| `internal/commands` | 16 | No (pure Go tests; package links the cgo adapters transitively) |

`internal/commands` is not part of `make test`: the package only compiles with
CGo (`hugot.go` imports the `//go:build cgo`-gated tagmatcher adapter), and
kushim — the only binary importing it — is always built with CGo.

### Full CGo+DB run (`make test-cgo-db`)

Runs the consumption pipeline tests with `CGO_ENABLED=1` against PostgreSQL —
the same suite as `make test-db` but with the package linked under CGo (the
cgo-gated adapters resolve at compile time, MuPDF is available). Page-count
assertions remain mode-independent: `setupConsumerTest` stubs `pageCounter` to
return 1 unconditionally, so the assertions are deterministic whether MuPDF
is reachable or not. The tier's value is exercising the full CGo+DB
pipeline (link paths, transactions, future code paths that touch
`countPages`) — not asserting on `countPages` output:

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
  auto-cleanup) — `authStore.js` and `api.js` (both touch `localStorage` at import
  time, so they need the DOM), plus the component tests: `DataTable.svelte` (rows,
  sort refetch, empty/error states), `FilterPanel.svelte` (filter-store wiring,
  tag autocomplete, failure toast), `UploadModal.svelte` (upload success/error paths).

Test files are co-located next to their sources as `*.test.js` (mirroring Go's
`_test.go` convention) and import `describe/it/expect/vi` explicitly — no globals,
so `eslint.config.js` needed no changes. Mocking conventions:

- `$app/*` modules are mocked with `vi.mock` where a unit imports them (`$app/navigation`
  `goto`, `$app/paths` `resolve`, `$app/state` `page`).
- `$lib/api` is mocked with `vi.hoisted` factories in component tests (`FilterPanel`,
  `UploadModal`) so no network calls escape.
- `api.test.js` stubs the global `fetch` (`vi.stubGlobal`) and asserts the Bearer
  header, 401 → logout + redirect, null propagation, `requestRaw` shape, and the
  `supportedMimeTypes` cache.

**`make test-web-e2e`** runs Playwright against the static SPA build
(`web/playwright.config.js`): the `webServer` runs `npm run build` and then serves
`build/` via `web/scripts/serve-static.mjs` — a tiny zero-dependency static server
that falls back to `index.html` for unknown routes, mirroring adapter-static's
`fallback` behavior in production (SvelteKit's own `vite preview` cannot be used:
it runs the SSR server, and `authStore.js`'s import-time `localStorage` access 500s
every route). The three specs (`e2e/login.spec.js`, `documents.spec.js`,
`upload.spec.js`) share `e2e/helpers.js`, which seeds `localStorage` auth via
`addInitScript` and installs `page.route` mocks for `/api|/wizard|/health` returning
minimal realistic payloads matching `web/src/lib/api.js` expectations. First run
requires `npx playwright install chromium` (CI uses `--with-deps`).

### Vulnerability scanning (`make vuln` / `make vuln-cgo`)

`make vuln` runs govulncheck against the Go vuln DB with `CGO_ENABLED=0`, so no
C toolchain is needed. It scans every package except `cmd/kushim` and
`internal/commands` — those reference CGo-gated symbols and cannot load without
CGo, the same split that separates `make test` from `make test-cgo`.
`make vuln-cgo` is the CGo-enabled variant (full call graph incl. the
gosseract/hugot adapters) and requires `make build-deps` first:

```bash
make vuln          # CGO_ENABLED=0, all loadable packages
make vuln-cgo      # full call graph, requires make build-deps first
```

govulncheck exits non-zero on reachable findings, which fails the CI
`vulncheck` job. Fix findings via `go get <module>@<fixed>` + `go mod tidy`.

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
the cycle surface. `NewMockTagService` was moved from `testutil` into the handler
test file for this reason.

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
   and test in `internal/consumption/integration_test.go`

### New task type

1. Implement `task.Handler`
2. Add test in `internal/task/task_test.go` using the `mockHandler` utility

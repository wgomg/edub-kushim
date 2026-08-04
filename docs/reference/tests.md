# Testing Reference

## Overview

The project has **20+ test packages and 375+ unit/integration tests**, with a two-tier
run strategy:

- **`make test`** (12 packages, no database required) — LLM registry, config, API types,
  tools runner, utils (text normalization, logging), tagmatch, storage walk, auth tokens,
  auth middleware + permissions, content analyzer (prompt building, tag filtering, token
  estimation), error classification (`errs`), and worker pool lifecycle.
- **`make test-db`** (6 additional packages, requires PostgreSQL) — database CRUD queries,
  search engine, task system (store/dispatcher/runner/pool), service layer (batch,
  orphaned, errored files, users, enrichment, API keys), API handlers, and consumption
  pipeline.

All non-CGo tiers run with `CGO_ENABLED=0` (no Tesseract, MuPDF, or
Ghostscript required); `make test-cgo` runs with `CGO_ENABLED=1` and needs
`make build-deps` first. Every invocation goes through the Makefile, which
exports the `-tags "XLA,ORT"` tags and the CGo environment — bare `go test`
is not supported.

### Quick Start

```bash
make test          # runs all non-CGo tests
make test-verbose  # same with verbose output
make test-db       # database-dependent tests (requires TEST_DATABASE_URL)
make test-cgo      # CGo-gated adapter tests (requires make build-deps first)
make test-one PKG=./internal/errs/   # single package; add RUN=Name to filter
```

### Continuous Integration

`.github/workflows/ci.yml` runs both tiers on every push to `dev`/`master` and every PR to `master`:

- `web` job: builds both SPAs (`npm ci && npm run build` in `web/` and `web-wizard/`), stages them with `make stage-web`, and uploads them as the `web-assets` artifact. Staging is required because `internal/static/build` and `internal/wizard/static` are gitignored but embedded via `//go:embed` — a fresh checkout has no assets to compile against.
- `test` job (depends on `web`): downloads `web-assets`, stages it with `make stage-web-artifact`, then runs `make test` and `make test-db` against a postgres:17 service container (`TEST_DATABASE_URL` pointing at `localhost:5432`).

The `global` ruleset requires the `test` and `web` checks to pass before a PR to `master` can merge.

---

## Package Layout

| Package | Tests | What it covers |
|---------|-------|---------------|
| `internal/llm` | 27 | Registry creation (fixture + embedded fallback), model lookup for known/missing providers and models, adapter/provider/model listing, default URL derivation, catalog reload, concurrent access safety |
| `internal/config` | 27 | Default config values, ParseHHMM, polling window validation (valid + invalid), IsWithinActiveWindows, OCR workers, finalizeConfig (reclaim max retries), Load (applies OcrWorkers), SaveMap (merge/no file/invalid file), watcher (fire on change/missing file/invalid YAML) |
| `internal/api/types` | 9 | ConfigResponseFrom: logging defaults/custom, reclaim max retries, prompt template, max batch delete, doc type refinement, pause on credit error, consumer fields (supported_files, converter), available file types (required flag, extension aliases) |
| `internal/mime` | 9 | IsPDF/IsImage/IsOfficeDoc predicates, ExtensionFromMimeType, MimeTypeFromExtension (canonical + alias extensions, case-insensitive, unknown → octet-stream), ExtensionsFor (canonical + aliases per type, nil for unknown), BuildExtensionSet, Supported constants + slice invariants |
| `internal/tools` | 6 | OptimizePdf (no timeout/with timeout/file not found/cancellation/nil optimizer), AnalyzeDocType (nil analyzer) |
| `internal/utils` | 7 | Truncate (ASCII, CJK, mixed, trim), EstimateTokens (ASCII/CJK/mixed), NormalizeForDB (accent folding, punctuation, spaces), NewLogger level parsing, SetLevel gating (Info/Error/Debug/Warn), REQID prefix formatting, file logging (append/invalid path/levels/source exclusion/level prefix), SlogLogger bridge, LevelPriority/LevelName |
| `internal/tagmatch` | 3 | `MaxMatchBodyBytes` derivation with floor/ceiling clamping; `truncateUTF8` UTF-8-safe truncation with CJK and ASCII boundary tests; idempotency check |
| `internal/storage` | 18 | DetectFileType (uuid/dbid/random), WalkStorageDir (finds PDFs/skips non-PDF/recent/unknown key types/missing dirs), QuarantineFile, RemoveOrphanedFile, CopyToConsumptionDir, TrashDir/DocumentTrashDir, MoveToTrash (happy path, missing original, missing storage), RestoreFromTrash (happy path, missing trash original, both missing, round-trip), RemoveFromTrash (exists, nonexistent) |
| `internal/auth` | 7 | Session secret generation, JWT generation/validation round-trip with role, wrong secret rejection, expired token rejection, malformed token rejection, token part structure, ValidRole cases |
| `internal/api` | 24 | Auth middleware: public path bypass, no-token 401, invalid token 401, wrong secret 401, valid token passes with context injection (includes role), missing bearer prefix, empty authorization header, disabled bypasses all paths, valid API key 200 (includes role), invalid API key 401, wrong prefix falls through to JWT, auth-disabled bypasses API key check, internal error returns 500, valid cookie, invalid cookie, header takes priority over cookie. Permission middleware: RequireRole for admin/editor/viewer (allows+forbids), missing role, invalid role |
| `internal/tools/adapters/contentanalyzer` | 14 | NormalizeTags (transforms, accent folding, dedup/drop), BuildPrompt (default/custom/malformed/execution error/whitespace), BuildDocTypePrompt (with/without metadata), ExtractHeadTailWords, FilterTags (18 cases: person token overlap, title overlap, doc-type match, known name subset, >3-word cap, maxTags cap, combined rules), DocMetadata.Format (all fields/partial/zero-valued), checkContentTooLarge (nil caps/zero/negative/within limit/exceeds), parseTokenLimitError (no match/empty/valid OpenAI/zero tokens/partial match) |
| `internal/errs` | 7 | Error constructors preserve kind/op/cause, Error() string formatting, Unwrap(), KindOf through plain/wrapped/nil errors, FromDB (nil→nil, ErrNoRows→NotFound, unique violation→Conflict, other→Internal), PgError predicates (unique/foreign key/deadlock/serialization/plain/wrapped) |
| `internal/pool` | 5 | StartStop (workers call runner, stop terminates), ContextCancellation (cancel stops pool), DoubleStop (sync.Once no panic), PanicRecovery_Restart (worker survives panic and restarts), PanicRecovery_StopDuringRestart (clean shutdown during restart delay) |
| `internal/database` | 32 | sqlc-generated CRUD, task lifecycle, enrich waiting flow, enrich discard/restore/sweep queries (SetEnrichTaskWaiting, RestoreDiscardedEnrichTasks, DiscardWaitingEnrichesOfFailedConsumes), batch ownership, FTS-adjacent operations, document/tag/people/document-type CRUD, saved searches, dashboard analytics queries (empty DB + mixed data), structured search missing filters (MissingLanguage/MissingType/Untagged), WithDocumentCount queries, backup lock lifecycle, gated task claiming, soft delete (TestUpdateDeleteDocument now asserts soft-delete semantics) |
| `internal/search` | 8 | tsvector search with snippets, ranking, pagination; structured search with mime/language/date/missing filters; query sanitization; engine construction |
| `internal/task` | 27 | Store (create/get/claim/complete/fail), dedup key uniqueness, dispatcher enqueue with custom status/ID, runner (complete/fail/no-tasks), nil payload handling, backup lock gating for consume/enrich/config, pool lifecycle, Retry (restores discarded enrich of a retried consume), consume-handler early-exit discards (external `task_test` package exercising `handlers.ConsumeTaskHandler` against a DB-backed store) |
| `internal/service` | 76 | Batch create/get/owner-state/pending/active/cancel/queue, RetryFailed (restores discarded enriches, batch-scoped), ResetStaleProcessingTasks (restore + global sweep), ResetProcessingTasksByBatch restore, orphaned scan/delete/restore/move-to-inbox, errored files list/download/delete/delete-all, user API key create/revoke/rotate/validate, user Create with role defaults/explicit/invalid, UpdateRole (valid/invalid), Update with role change, password validation (12+ rules) |
| `internal/api/handlers` | 60 | Document CRUD, tag/people/DocumentType CRUD, user CRUD (with role), task endpoints, saved searches, concurrent operations, dashboard activity + analytics + processing health, analytics error path, config handler get/status, batch delete limits, error helpers, auth login (valid/invalid/empty/claims/role), auth logout, API key generate/revoke/rotate/status/forbidden/invalid-id/not-found, MeHandler (valid/missing-id/not-found), self-service API key handlers (MeGenerateKey/MeRevokeKey/MeRotateKey/MeGetKeyStatus/unauthorized), orphaned handler (list/scan/delete/restore/move-to-inbox/delete-all/move-all), errored handler (list/download/delete/delete-all), logs handler (invalid name/file not found/success/line clamping/large file tail/empty file) |
| `internal/consumption` | 20 | Full consumer pipeline via mock runner (file discovery, DB transaction, file movement, duplicate detection), file I/O helpers (get, move, copy, remove, clean up), checksum calculation, orphaned file management |
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

These 10 tests require `CGO_ENABLED=1` to compile because their source files
are tagged `//go:build cgo`. Most use only pure-Go code; only the MuPDF tests
exercise actual CGo function calls.

Requires the C toolchain and built C libraries (Tesseract, Leptonica, MuPDF,
libtokenizers) on the host. Run inside the builder containers to avoid
installing C deps locally:

```bash
make test-cgo          # host, uses Makefile's CGO_ENABLED=1 export
make test-cgo-glibc    # podman: kushim-glibc-builder
make test-cgo-musl     # podman: kushim-musl-builder
```

**3 packages, 10 tests:**

| Package | Tests | CGo at runtime? |
|---------|-------|-----------------|
| `internal/tools/adapters` | 2 | Yes — MuPDF page render |
| `internal/tools/adapters/ocr` | 6 | No (pure Go, build-tag gated) |
| `internal/tools/adapters/tagmatcher` | 2 | No (pure Go, build-tag gated) |

### Single package

```bash
# Without DB (CGO_ENABLED=0)
make test-one PKG=./internal/errs/

# Specific test
make test-one PKG=./internal/database/ RUN=TestTaskLifecycle

# CGo-gated package (requires make build-deps first)
make test-cgo
```

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

### No CGo in tests

All tests run with `CGO_ENABLED=0`. The CGo-dependent code paths (gosseract OCR,
MuPDF text extraction/optimization) are tested via mocks at the `runner` interface
level and via build-constrained stubs at the adapter level.

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

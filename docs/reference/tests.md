# Testing Reference

## Overview

The project has **90+ integration and unit tests** across ten packages, all runnable
without CGo dependencies (no Tesseract, MuPDF, or Ghostscript required). The test
suite validates database queries, task lifecycle, search, API handlers, auth,
consumption pipeline, tagmatch body cap derivation and UTF-8 truncation, and API key service.

### Quick Start

```bash
make test          # runs all non-CGo tests
make test-verbose  # same with verbose output
CGO_ENABLED=0 go test -tags "XLA,ORT" ./internal/...
```

---

## Package Layout

| Package | Tests | What it covers |
|---------|-------|---------------|
| `internal/tagmatch` | 3 | `MaxMatchBodyBytes` derivation with floor/ceiling clamping; `truncateUTF8` UTF-8-safe truncation with CJK and ASCII boundary tests; idempotency check |
| `internal/database` | 20 | sqlc-generated CRUD, task lifecycle, enrich waiting flow, batch ownership, FTS-adjacent operations, document/tag/people/document-type CRUD, saved searches, dashboard analytics queries (empty DB + mixed data), structured search missing filters (MissingLanguage/MissingType/Untagged), WithDocumentCount queries |
| `internal/search` | 8 | tsvector search with snippets, ranking, pagination; structured search with mime/language/date/missing filters; query sanitization |
| `internal/task` | 14 | Store (create/get/claim/complete/fail), dedup key uniqueness, dispatcher enqueue with custom status/ID, runner (complete/fail/no-tasks), pool lifecycle |
| `internal/api/handlers` | 44 | Document CRUD, tag/people/DocumentType CRUD, user CRUD (with role), task endpoints, saved searches, concurrent operations, dashboard activity + analytics + processing health, analytics error path, config handler get/status, batch delete limits, error helpers, auth login (valid/invalid/empty/claims/role), auth logout, API key generate/revoke/rotate/status/forbidden/invalid-id/not-found, MeHandler (valid/missing-id/not-found), self-service API key handlers (MeGenerateKey/MeRevokeKey/MeRotateKey/MeGetKeyStatus/unauthorized) |
- `internal/auth` | 7 | Session secret generation, JWT generation/validation round-trip with role, wrong secret rejection, expired token rejection, malformed token rejection, token part structure, ValidRole cases |
- `internal/service` | 32 | Batch create/get/owner-state/pending/active/cancel/queue, orphaned scan/delete/restore, errored files list/download/delete/delete-all, user API key create/revoke/rotate/validate, user Create with role defaults/explicit/invalid, UpdateRole (valid/invalid), Update with role change |
| `internal/api` | 13 | Auth middleware: public path bypass, no-token 401, invalid token 401, wrong secret 401, valid token passes with context injection (includes role), missing bearer prefix, empty authorization header, disabled bypasses all paths, valid API key 200 (includes role), invalid API key 401, wrong prefix falls through to JWT, auth-disabled bypasses API key check, internal error returns 500. Permission middleware: RequireRole for admin/editor/viewer (allows+forbids), missing role, invalid role |
| `internal/consumption` | 11 | Full consumer pipeline via mock runner (file discovery, DB transaction, file movement, duplicate detection), file I/O helpers (get, move, copy, remove, clean up), checksum calculation |

---

## How Tests Work

### PostgreSQL via TEST_DATABASE_URL

Every test creates a connection to a PostgreSQL database specified by the `TEST_DATABASE_URL`
environment variable. The schema is initialized via `database.InitializeSchema` on each
connection. Each test starts with a clean slate (all tables truncated, sequences reset,
seed data re-inserted) via `resetDB`:

```go
q, db := database.NewTestQueries(t)
defer db.Close()
resetDB(t, q)
```

The `resetDB` helper deletes all rows from every table, resets identity sequences, and
re-runs the seed SQL for `document_type`, `people_type`, and `tag`.

Key detail: Foreign key constraints are enforced by PostgreSQL just as in production.
The test pool uses `SetMaxOpenConns(5)` — no need for SQLite's single-conn limitation.

> **Phase 2 change**: Tests previously used SQLite in-memory databases (`:memory:`), which
> gave each test an isolated database automatically. With a shared PostgreSQL instance,
> `resetDB` must be called at the start of each test that assumes clean state. Tests are
> run sequentially (`-count=1`) to prevent concurrent writes to the shared test database.

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
make test          # CGO_ENABLED=0, all 5 packages, 60s timeout
make test-verbose  # same with -v output
```

The Makefile explicitly sets `CGO_ENABLED=0` for test targets, overriding the
`export CGO_ENABLED := 1` used for build targets. This ensures tests run in
any environment regardless of C library availability.

### Manual

```bash
# Single package
CGO_ENABLED=0 go test -tags "XLA,ORT" -v ./internal/database/

# Specific test
CGO_ENABLED=0 go test -tags "XLA,ORT" -v -run "TestTaskLifecycle" ./internal/database/

# All non-CGo packages
CGO_ENABLED=0 go test -tags "XLA,ORT" -count=1 -timeout 60s \
    ./internal/database/ \
    ./internal/search/ \
    ./internal/tagmatch/ \
    ./internal/task/ \
    ./internal/auth/ \
    ./internal/api/ \
    ./internal/api/handlers/ \
    ./internal/consumption/

# Full suite (fails if C dependencies are missing)
CGO_ENABLED=1 go test -tags "XLA,ORT" ./internal/...
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
| `NewTestDB(t)` | PostgreSQL via `TEST_DATABASE_URL`, schema initialized, auto-cleanup |
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

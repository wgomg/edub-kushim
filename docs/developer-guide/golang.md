# Developer Guide — The Go of edub-kushim

This guide explains the Go language features used across the edub-kushim codebase
and how they are used *here*, with real snippets and file references. It is aimed
at developers who know how to program but are new to Go (or new to idiomatic Go)
and need to get productive in this repository fast.

It complements the other docs:

| Document | What it answers |
|---|---|
| `architecture.md` | How the system is designed (pipeline, processes, database model) |
| `user-manual.md` | CLI + API usage |
| `docs/reference/*` | Per-package code references (database, search, pipeline, task system, tools, tests) |
| `frontend.md` | The SvelteKit/Svelte 5 side of this repository |
| `postgresql.md` | The PostgreSQL features behind the schema and queries |
| `semantic-matching.md`, `algorithms.md`, `cgo.md`, `ocr-pipeline.md`, `task-system.md`, `llm.md` | Topic deep dives (embeddings, TextRank, C wrappers, OCR, task semantics, LLM integration) |
| **`golang.md` (this)** | *How Go itself is used in this codebase* |

Everything here is a description of code that exists today. If a snippet looks
wrong, trust the code, not the doc.

---

## Table of contents

1. [Codebase map](#1-codebase-map)
2. [Go orientation in ten ideas](#2-go-orientation-in-ten-ideas)
3. [Packages, imports, and the module](#3-packages-imports-and-the-module)
4. [Functions, multiple returns, and `defer`](#4-functions-multiple-returns-and-defer)
5. [Structs, methods, and receivers](#5-structs-methods-and-receivers)
6. [Interfaces: implicit satisfaction and the adapter pattern](#6-interfaces-implicit-satisfaction-and-the-adapter-pattern)
7. [Embedding: composition over inheritance](#7-embedding-composition-over-inheritance)
8. [Error handling](#8-error-handling)
9. [`context.Context`: cancellation and request scopes](#9-contextcontext-cancellation-and-request-scopes)
10. [Goroutines](#10-goroutines)
11. [Channels](#11-channels)
12. [The `sync` package](#12-the-sync-package)
13. [Timers and tickers](#13-timers-and-tickers)
14. [Processes, signals, and sockets](#14-processes-signals-and-sockets)
15. [Database access with sqlc](#15-database-access-with-sqlc)
16. [Configuration](#16-configuration)
17. [`//go:embed`](#17-goembed)
18. [Build tags and cgo](#18-build-tags-and-cgo)
19. [The HTTP layer](#19-the-http-layer)
20. [The CLI layer](#20-the-cli-layer)
21. [Logging](#21-logging)
22. [Testing](#22-testing)
23. [Generics and the modern stdlib](#23-generics-and-the-modern-stdlib)
24. [Feature → file quick reference](#24-feature--file-quick-reference)
25. [Idioms checklist and gotchas](#25-idioms-checklist-and-gotchas)

---

## 1. Codebase map

```
cmd/kushim/main.go          kushim binary: CLI entry, command dispatch, hidden subprocess entry points
cmd/edub/main.go            edub binary: REST API server (+ tiny command dispatch)
internal/
  api/                      HTTP server, middleware, permission gates
  api/handlers/             one struct per resource (Tag, Document, Task, ...)
  api/types/                API DTOs (JSON shapes)
  auth/                     JWT, API keys, roles, context keys
  backup/                   backup/restore + scheduler
  cache/                    in-memory embedding cache (RWMutex-guarded maps)
  commands/                 kushim CLI commands; DI container; flag parser
  config/                   config structs, defaults, validation, polling watcher
  consumption/              the ingestion pipeline (Consumer, scan, storage)
  database/                 sqlc-generated queries + client + migrations + test harness
  enrichment/               async enrich step
  errs/                     the error-kind contract (see §8)
  llm/                      LLM model catalog registry
  mime/                     MIME type detection helpers
  pool/                     generic worker pool (see §10)
  search/                   full-text + structured search
  service/                  business logic over database queries (Batch, Tag, People, ...)
  static/, wizard/          embedded SvelteKit builds
  storage/                  storage dir walker, trash
  tagmatch/                 HTTP-over-Unix-socket client for the hugot matcher daemon
  task/                     task engine: registry, dispatcher, runner, handlers
  testutil/                 shared test helpers and mocks
  tools/                    the tool adapter layer + composition root (Runner)
  tools/adapters/           one package per capability: ocr, textextractor, converter, ...
  utils/                    logger, metrics, text, files, param bag
  version/                  version string (injected via -ldflags)
  types.go                  shared aggregate struct (CrudServices)
```

Two binaries, same module:

- **`kushim`** — CLI, document processing, queue daemon, matcher daemon. Built
  with `CGO_ENABLED=1` (Tesseract, Leptonica, MuPDF, Hugot linked statically).
- **`edub`** — REST API + web UI. Built with `CGO_ENABLED=0`. Pure Go.

Always build through the Makefile — it sets `-tags "XLA,ORT"` (consumed by
 the Hugot/ORT dependency chain) and the CGo environment, e.g. `make build`
 after `make build-deps`. See `AGENTS.md` for the full build story.

---

## 2. Go orientation in ten ideas

If you are new to Go, these ten ideas explain ~90% of what you will see in this
repository. Each is expanded in its own section later.

1. **A Go program is a set of packages.** A package is a directory of `.go` files
   sharing a `package` clause; the import path is the directory path under the
   module root (`internal/database` → `github.com/wgomg/edub-kushim/internal/database`).
   Identifiers starting with a capital letter are exported; lowercase ones are
   package-private. There is no `public`/`private` keyword (§3).

2. **Errors are values, not exceptions.** Functions that can fail return an
   `error` as the last value. There is no `throw`/`catch`; you check and wrap
   (§8). `panic` exists but is used as a last resort (only one production use:
   the worker pool converts panics back into errors, §10).

3. **Interfaces are satisfied implicitly.** A type implements an interface by
   having the right methods — no `implements` keyword, no registration. This is
   the single most important idea in this codebase: the whole tool layer is a
   set of interfaces with swappable implementations (§6).

4. **Pointers are explicit.** `*T` is a pointer, `T` is a value. Methods with
   pointer receivers mutate the receiver and are the norm here. Everything is
   passed by value at the language level; a struct containing a `sync.Mutex`
   must live behind a pointer (§5).

5. **Zero values are real.** Every type has a zero value (`0`, `""`, `nil`,
   `false`, zeroed struct). This codebase uses them heavily: `nil` channels in
   `select`, `sql.NullString{Valid: false}`, `var zero T` in generics.

6. **Goroutines are cheap threads.** `go f()` runs `f` concurrently. The
   codebase coordinates them with channels, `select`, and the `sync` package —
   never with global state (§10–§12).

7. **Composition over inheritance.** Go has no classes and no inheritance.
   Struct *embedding* promotes methods and fields, which this codebase uses to
   build `Client` on top of sqlc's `Queries`, and to reuse a cached store
   (§7).

8. **Struct tags are metadata.** Backtick strings like `` `json:"title"` ``
   drive JSON/YAML/mapstructure serialization, sqlc scanning, and validation
   (§5, §16).

9. **The standard library is the framework.** There is no web framework, no CLI
   framework, no DI framework, no assert library. The code uses `net/http`,
   `os/exec`, `log/slog`, `testing`, plus a handful of well-chosen libraries
   (pgx, sqlc output, goose, viper-for-writes-only, lumberjack). This is
   deliberate: `go.mod` is small, and the project's idioms are hand-rolled and
   consistent.

10. **`gofmt`, `go vet`, and build tags keep the code uniform.** Everything is
    gofmt-formatted; code that needs a C toolchain or a special build is gated
    with `//go:build` lines (§18).

---

## 3. Packages, imports, and the module

`go.mod` declares the module `github.com/wgomg/edub-kushim` and Go 1.26. The
`internal/` directory is a language feature: packages under `internal/` can only
be imported by code inside this module, which is how the project keeps `cmd/` as
the only public face.

Import groups are conventional — stdlib first, then third-party, then the
module's own packages, blank line between groups:

```go
import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wgomg/edub-kushim/internal/config"
)
```

Two import forms worth knowing:

- **Blank import** `_ "github.com/jackc/pgx/v5/stdlib"` — registers the pgx
  database driver as a side effect (used in `internal/database/connection.go`).
- **Embed-only import** `_ "embed"` — needed when `//go:embed` targets a single
  file rather than a directory (§17).

The `internal/...` packages share no cyclic imports: `commands` imports services
and tools; services import `database`; `database` imports nothing internal.

---

## 4. Functions, multiple returns, and `defer`

Go functions return any number of values; the idiomatic shape here is
`(value, error)`. Constructors and services all follow it:

```go
func NewClient(db *sql.DB) *Client                       // database/client.go
func (s *Tag) Create(ctx context.Context, arg ...) (database.Tag, error)  // service/tag.go
```

### Named returns + `defer` for cleanup

`defer` schedules a call to run when the function returns — the project's
most common use is cleanup (rollback, file removal, ticker stop, log close).
A named return value lets a deferred closure inspect or modify the result.
`internal/pool/pool.go:91` converts a worker panic into an error so the pool's
restart loop can recover:

```go
func (p *Pool) runWorker(id int, logPrefix string) (rerr error) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error(nil, "%s panic recovered: %v", logPrefix, r)
			rerr = fmt.Errorf("worker panic: %v", r)
		}
	}()
	// ...
}
```

`internal/consumption/consumer.go:292` rolls the transaction back *only if* the
function is exiting with an error, by capturing the named `err`:

```go
defer func() {
	if err != nil {
		tx.Rollback()
		if file.StorageProcessedPath != nil {
			RemoveFile(*file.StorageProcessedPath)
		}
		// ...
	}
}()
```

And `internal/consumption/consumer.go:116` uses a deferred closure for timing
logs:

```go
start := time.Now()
defer func() {
	elapsed := time.Since(start)
	c.logger.Info(&documentID, "finished consumption %s in %s", ...)
}()
```

### Closures and function values

Functions are first-class values. The codebase threads them through:

- **Middleware**: `RequireRole(allowed ...auth.Role) func(http.Handler) http.Handler`
  returns a middleware function (§19).
- **Dependency injection**: `NewServer(...)` receives closures like
  `getSecret func() string` and `validateAPIKey func(ctx, rawKey) (*database.User, error)`
  (`internal/api/server.go:101-128`) so the HTTP layer never touches config
  internals directly.
- **Factory hooks**: `newGosseract` is a package-level function variable
  overridden in `init()` (§18).

### Variadics

`...` collects trailing arguments into a slice:

```go
func RequireRole(allowed ...auth.Role) func(http.Handler) http.Handler   // api/permission.go
```

Call sites spread a slice back with `...`:
`RequireRole(viewer...)` where `viewer := []auth.Role{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin}`
(`internal/api/server.go:184`).

### `init()` functions

`init()` runs once at package load, before `main`. The project uses it sparingly,
as a test seam and a cgo gate:

```go
// internal/tools/adapters/ocr/gosseract.go:137
func init() {
	newGosseract = func(logger *utils.Logger, cfg config.ToolConfig, optimizerCmd string, languages []string, dataDir string, ocrWorkers int) (OCR, error) {
		return NewGosseract(logger, cfg, optimizerCmd, languages, dataDir, ocrWorkers)
	}
}
```

---

## 5. Structs, methods, and receivers

### Structs

`type X struct { ... }` defines a data record. Fields are `name Type`; tags add
metadata for serializers:

```go
// internal/config/config.go:23 — triple-tagged for the API PATCH path, YAML, JSON
type LoggingConfig struct {
	MaxSize    int  `mapstructure:"max_size" yaml:"max_size" json:"max_size"`
	MaxBackups int  `mapstructure:"max_backups" yaml:"max_backups" json:"max_backups"`
	MaxAge     int  `mapstructure:"max_age" yaml:"max_age" json:"max_age"`
	Compress   bool `mapstructure:"compress" yaml:"compress" json:"compress"`
}
```

`json:"-"` excludes a field from serialization (`internal/tools/runner.go:74`
marks `PassContext` so it never leaks to clients). Anonymous structs appear
inline: `internal/api/server.go:37` nests a typed pool holder:

```go
pools struct {
	config *pool.Pool
}
```

### Methods and receivers

A method is a function with a receiver: `func (s *Tag) Create(...)`. **Every
method in this codebase uses a pointer receiver** (`*Tag`, `*Runner`,
`*Queries`, `*Pool`), for two reasons: methods may mutate state, and structs
containing `sync.Mutex`/`sync.RWMutex`/`atomic` fields must never be copied.
`sync` primitives do not work on copied values — this is why `cache.Cache`,
`pool.Pool`, and `utils.Logger` are always used through `*T`.

### Constructors

There is no `new`-with-arguments; the convention is an exported `NewXxx`
constructor that validates its inputs and returns `(*T, error)`:

```go
// internal/tools/adapters/textextractor/pdftotext.go:22 — validates at startup, with an install hint
func NewPDFToText(logger *utils.Logger, cfg config.ToolConfig) (*PDFToText, error) {
	path, err := exec.LookPath("pdftotext")
	if err != nil {
		return nil, fmt.Errorf("pdftotext not found in PATH (install poppler-utils): %w", err)
	}
	// ...
}
```

Factories for pluggable capabilities return an *interface* instead
(`NewTextExtractor(...) (TextExtractor, error)`); see §6.

### Pointers: when and why

- `*string` / `*int64` in API types mean "optional / not present", distinct from
  zero (`""`, `0`). `ContentAnalysisResult.Title string` vs
  `Stats *json.RawMessage` (`internal/tools/runner.go:66-75`) — nil says "no
  value", which JSON encodes as `null`.
- Pointer fields into aggregates: `Consumer` holds `*config.Config`, not a copy,
  so config hot-reload via `atomic.Pointer` is visible everywhere (§16).

---

## 6. Interfaces: implicit satisfaction and the adapter pattern

This is the heart of the project's design. Go interfaces are *implicit*: a type
satisfies an interface if it has the required methods — nothing declares the
relationship. The tool layer is built entirely on this.

### The adapter family

Each processing capability is one interface plus a factory that switches on the
configured engine:

```go
// internal/tools/adapters/textextractor/adapter.go:11
type TextExtractor interface {
	Extract(ctx context.Context, path string, mimeType string) (*string, error)
	CanHandle(mimeType string) bool
	Name() string
}
```

Implementations: `*PDFToText` (external `pdftotext` binary), `*MuPDF` (in-process
cgo), `*Docx`, `*Odt`, `*Gopdf` — plus `*CompositeExtractor`, which implements
the same interface by delegating to a slice of the same interface
(Chain-of-Responsibility):

```go
// internal/tools/adapters/textextractor/adapter.go:17
type CompositeExtractor struct {
	extractors []TextExtractor
}

func (c *CompositeExtractor) Extract(ctx context.Context, path string, mimeType string) (*string, error) {
	for _, ext := range c.extractors {
		if ext.CanHandle(mimeType) {
			return ext.Extract(ctx, path, mimeType)
		}
	}
	return nil, fmt.Errorf("composite: no extractor found for MIME type %s", mimeType)
}
```

The same pattern repeats for `ocr.OCR`, `converter.DocumentConverter`,
`pdfoptimizer.PdfOptimizer`, `textreducer.TextReducer`, `contentanalyzer.ContentAnalyzer`,
`tagmatcher.Matcher` / `tagmatcher.Embedder`.

Two styles of factory coexist, and the distinction is idiomatic:

- Concrete constructors return `(*T, error)` (`NewOcrMyPdf`, `NewPDFToText`,
  `NewGhostscript`, ...).
- Capability factories return the *interface* (`NewOCR(...) (OCR, error)`,
  `NewTextExtractor(...) (TextExtractor, error)`) — the caller doesn't care
  which engine was configured.

### The composition root: `tools.Runner`

`internal/tools/runner.go:28` is a plain struct holding one adapter per
capability — no interface wrapping it:

```go
type Runner struct {
	logger           *utils.Logger
	config           *config.Config
	textExtractor    textextractor.TextExtractor
	ocr              ocr.OCR
	pdfOptimizer     pdfoptimizer.PdfOptimizer
	documentConverter converter.DocumentConverter
	tagMatcher       tagmatcher.Matcher
	contentAnalyzer  contentanalyzer.ContentAnalyzer
	textReducer      textreducer.TextReducer
}
```

`NewRunner` wires whichever engines the config selects. This is dependency
injection by constructor argument — no framework involved.

### Interface extraction for testability (consumer pays)

`internal/consumption/consumer.go:40` defines a *small, private* interface
covering only the `Runner` methods the consumer uses, so tests can inject a
mock without mocking the whole tool stack:

```go
// runner is an interface covering the subset of *tools.Runner methods that
// Consumer.Process depends on. *tools.Runner satisfies it automatically.
type runner interface {
	ExtractText(ctx context.Context, path string, mimeType string) (*tools.TextExtractionResult, error)
	OCR(ctx context.Context, docId, path string) (*tools.OCRResult, error)
	OptimizePdf(ctx context.Context, docId, path string) (*tools.PdfOptimizationResult, error)
	ConvertToPdf(ctx context.Context, path string, mimeType string) (*string, error)
}

var _ runner = (*tools.Runner)(nil)
```

The `var _ runner = (*tools.Runner)(nil)` line is a **compile-time assertion**:
if `*tools.Runner` ever stops satisfying `runner`, the build fails. The project
uses this trick wherever correctness depends on an implicit relationship (also
in `task/handlers/backup.go:18` and `task/handlers/enrich.go`).

### Optional interface discovery

Interfaces can be *discovered* at runtime with a type assertion. The task
registry checks whether a handler optionally supports deduplication:

```go
// internal/task/registry.go:28
func (r *Registry) DedupKey(taskType string, payload json.RawMessage) string {
	h, err := r.Get(taskType)
	if err != nil {
		return ""
	}
	if dd, ok := h.(Dedupable); ok {
		return dd.DedupKey(payload)
	}
	return ""
}
```

`Dedupable` (defined in `internal/task/handler.go:12`) is implemented by some
handlers and not others; the `ok` form of the assertion handles both.

### One interface, two worlds apart

`tagmatcher.Matcher` has two implementations that are physically different
processes: `*Hugot` (in-process ONNX model, cgo build) and `*MatcherClient`
(HTTP over a Unix socket to the `kushim hugot` daemon). The rest of the system
cannot tell them apart — which is exactly how `edub` (pure Go, no cgo) still
gets embeddings: it uses the client implementation.

### Small dependency interfaces

Services declare the *minimum* interface they need, then accept it:

```go
// internal/service/orphaned.go:19 — Orphaned needs exactly two collaborators
type TaskCreator interface {
	CreateTask(ctx context.Context, taskType, batchID string, payload json.RawMessage,
		taskID, status, dedupKey string) (string, error)
}

type BatchCreator interface {
	Create(ctx context.Context, id, source, status string) error
}
```

Satisfied by `*task.Store` and `*service.Batch` respectively. This is the
"accept interfaces, return structs" rule in practice.

---

## 7. Embedding: composition over inheritance

Go has no inheritance, but struct fields can be *embedded* anonymously (just a
type name, no field name). The embedded type's methods and fields are
*promoted* to the outer type. Three deliberate uses:

### 1. `database.Client` embeds sqlc's `*Queries`

```go
// internal/database/client.go:8
type Client struct {
	*Queries
	db *sql.DB
}

func NewClient(db *sql.DB) *Client {
	return &Client{Queries: New(db), db: db}
}

func (c *Client) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return c.db.BeginTx(ctx, opts)
}
```

Every generated query method (`GetDocument`, `CreateTask`, ...) is promoted, so
`client.GetDocument(...)` works directly — while `Client` adds transaction
support on top. This is the "wrapper with promotions" idiom.

### 2. `cache.EmbeddingStore` embeds `storeBase` for reuse

```go
// internal/cache/embedding_store.go:3
type EmbeddingStore struct {
	storeBase
	entries map[string][]float32
}
```

`storeBase` (`internal/cache/cache.go:16`) owns the `sync.RWMutex` and the
`Attr`/`Attrs` methods; embedding promotes both, so `*EmbeddingStore` satisfies
`cache.Store` without duplicating code.

### 3. `auth.Claims` embeds `jwt.RegisteredClaims`

```go
// internal/auth/claims.go:23
type Claims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}
```

The embedded type brings the standard JWT fields (`exp`, `iat`, ...) and all
their validation logic into the custom claims — the JWT library's parser walks
the promoted methods.

---

## 8. Error handling

Go treats errors as values: functions return them, callers check them. The
codebase has a consistent, layered error strategy.

### Layer 1: sentinel errors

A sentinel is a package-level error used for identity comparison:

```go
// internal/tagmatch/client.go:14
var ErrMatcherUnavailable = fmt.Errorf("matcher unavailable")
```

Callers test with `errors.Is`:

```go
// internal/api/handlers/tag.go:17
func isMatcherUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, tagmatch.ErrMatcherUnavailable)
}
```

Also used: `sql.ErrNoRows` (compared directly with `==` in a few hot paths,
e.g. `internal/task/runner.go:31`).

### Layer 2: wrapping with `%w`

Every error gains context as it travels up; `%w` preserves the chain so
`errors.Is`/`errors.As` still see the original:

```go
if err := tx.Commit(); err != nil {
	return 0, errs.FromDB(err, "commit transaction for reset "+batchID)
}
return fmt.Errorf("text extractor: %w", err)
```

### Layer 3: typed errors with `Unwrap`

A custom error type can carry metadata and still unwrap:

```go
// internal/task/errors.go
type Error struct {
	ReqID      string
	Err        error
	PauseBatch bool
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }
```

`errors.As` extracts it downstream — the runner uses it to recover the request
ID for logging and to pause the batch on LLM credit errors
(`internal/task/runner.go:62`):

```go
var tErr *Error
reqID := (*string)(nil)
if errors.As(err, &tErr) {
	reqID = &tErr.ReqID
	if tErr.PauseBatch && task.BatchID.Valid {
		// pause the batch
	}
}
```

### The project's error contract: `internal/errs`

`errs` defines four kinds and a `*Error` carrying `Kind`, `Op` (the operation),
and `Cause`, with `Unwrap` returning the cause (`internal/errs/errs.go:21`):

```go
type Kind int

const (
	KindNotFound Kind = iota
	KindConflict
	KindInvalid
	KindInternal
)
```

Services translate database errors into this contract at the boundary —
`errs.FromDB` maps `sql.ErrNoRows` → NotFound, unique violations → Conflict,
everything else → Internal (`internal/errs/errs.go:101`):

```go
func FromDB(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ENotFound(op, err)
	}
	if IsConstraint(err) {
		return EConflict(op, err)
	}
	return EInternal(op, err)
}
```

PostgreSQL error codes are inspected through `errors.As` to a `*pgconn.PgError`
(`IsConstraint`, `IsForeignKey`, `IsBusy` — unique violations, FK violations,
deadlock/serialization).

Handlers convert kinds to HTTP statuses (`internal/api/handlers/errors.go:11`):

```go
kind := errs.KindOf(err)
switch kind {
case errs.KindNotFound:
	http.Error(w, op+" not found", http.StatusNotFound)
case errs.KindConflict:
	// 409
case errs.KindInvalid:
	// 400
default:
	// 500, logged with the request ID
}
```

The takeaway: **wrap with `%w`, translate at the service boundary with
`errs.FromDB`, classify with `errs.KindOf`, and never leak raw DB errors to
handlers.**

---

## 9. `context.Context`: cancellation and request scopes

`context.Context` is the project's single mechanism for (a) cancellation,
(b) deadlines, and (c) request-scoped values. It is the first parameter of
almost every function that touches I/O.

### Cancellation: `WithCancel` / `WithTimeout`

Long-running components own a cancelable context and propagate it to all their
goroutines. The queue daemon's lifetime (`internal/commands/queue.go:79`):

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
```

The `kushim consume` command converts OS signals into cancellation
(`internal/commands/consume.go:33` — a reusable pattern):

```go
func watchSignals(ctx context.Context, cancel context.CancelFunc) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()
}
```

Every goroutine checks `ctx.Done()` — see the "double-exit discipline" in §10.
Timeouts appear as `context.WithTimeout(ctx, 5*time.Second)` at operation
boundaries (DB writes, pool shutdown, HTTP shutdown).

### Request-scoped values: `WithValue`

HTTP middleware injects per-request values that handlers read back out:

```go
// internal/api/server.go:320
ctx := context.WithValue(r.Context(), "reqid", reqID)          // request ID
// internal/api/auth_middleware.go:99
ctx = context.WithValue(r.Context(), auth.UserIDKey, user.ID)  // identity
```

Keys are typed to avoid collisions — never raw strings:

```go
// internal/auth/claims.go:30
type contextKey string

const UserIDKey contextKey = "userID"
const RoleKey contextKey = "role"
```

Reading with the two-value type assertion:

```go
role, _ := r.Context().Value(auth.RoleKey).(string)   // api/permission.go:16
```

The param bag rides the same mechanism (`internal/utils/parambag.go:114`): a
private `contextKey` + `WithParamBag`/`GetParamBag` pair.

### The generic timeout complement

Because not every adapter checks `ctx`, the runner wraps calls in a goroutine
and lets the context win (§23, `runWithTimeout`). The two mechanisms — contexts
and the wrapper — make timeout enforcement independent of adapter cooperation.

---

## 10. Goroutines

`go f()` starts `f` on its own goroutine. The project's goroutines always follow
two rules: **listen for an explicit stop channel AND `ctx.Done()`**, and
**never share memory except through channels or guarded fields**.

### The worker pool: `internal/pool`

`pool.Pool` is a generic bounded worker pool — but note the design: workers
*poll* the database on a timer rather than receiving jobs from a channel. The
DB (`ClaimNextPending`) is the queue; the pool just bounds concurrency
(`internal/pool/pool.go:71`):

```go
func (p *Pool) workerLoop(id int) {
	defer p.wg.Done()
	for {
		err := p.runWorker(id, logPrefix)
		if err == nil {
			return
		}
		p.logger.Error(nil, "%s restarting after error: %v", logPrefix, err)
		select {
		case <-p.stopCh:        // explicit stop
			return
		case <-p.ctx.Done():    // parent cancelled
			return
		case <-time.After(p.interval):
		}
	}
}
```

The inner loop shows the canonical multi-case `select` — stop channel, context,
a housekeeping ticker, and the work tick:

```go
for {
	select {
	case <-p.stopCh:
		return nil
	case <-p.ctx.Done():
		return nil
	case <-memTicker.C:
		// periodic memory logging
	case <-time.After(p.interval):
		if err := p.runner.Next(p.ctx, p.taskType); err != nil { ... }
	}
}
```

Workers crash-restart: a panic is converted to an error (§4), which makes the
worker loop log and retry instead of dying. `Stop` is idempotent (`sync.Once`)
and bounded by a timeout (see §12).

### Where goroutines live

| Site | Purpose |
|---|---|
| `pool.go:44` | one goroutine per pool worker |
| `task/heartbeat.go:29` | batch-owner heartbeat ticker |
| `config/watcher.go:45` | config file polling loop |
| `commands/queue.go:111,114` | inbox polling loop + Postgres LISTEN/NOTIFY listener |
| `commands/queue.go:312` | reap the forked `kushim consume` child (`cmd.Wait`) |
| `commands/hugot.go:122` | serve the matcher HTTP server, errors relayed on a channel |
| `commands/setup.go:52` | setup wizard HTTP server |
| `tools/runner.go:87` | `runWithTimeout` executes the wrapped call |
| `tools/adapters/ocr/standalone.go:111` | N worker goroutines for parallel page OCR |
| `storage/orphaned.go:44` | storage-dir walker feeding an unbuffered channel |
| `service/trash.go:217` | hourly trash purge |
| `cmd/edub/main.go:85` | HTTP server; `main` blocks on `<-quit` |

---

## 11. Channels

Channels are typed pipes: `chan T` sends `T` values, `<-chan T` receives.
The project uses all the classic patterns.

### Producer/consumer with a worker fan-out

The OCR engine (`internal/tools/adapters/ocr/standalone.go:107`, cgo build) is
the fullest example — buffered job channel, N workers ranging over it, results
collected by index so page order stays deterministic:

```go
jobs := make(chan pageJob, numWorkers)
results := make(chan pageResult, numPages)
var wg sync.WaitGroup

for range numWorkers {
	wg.Go(func() {
		worker := newOCRClient(languages)
		defer worker.Close()
		for job := range jobs {          // range over a channel = receive until closed
			res, err := computePage(...)
			results <- pageResult{index: job.index, res: res}
		}
	})
}
// producer
for i := range numPages {
	jobs <- pageJob{index: i, ...}
}
close(jobs)                             // producer closes -> workers finish

go func() {
	wg.Wait()
	close(results)                       // closer goroutine closes the out channel
}()

for r := range results {                // main consumer
	resultsByIndex[r.index] = r
}
```

Patterns to note: `for range ch` (receive until `close`), the producer closing
`jobs`, the dedicated goroutine closing `results` after `wg.Wait()`, and
`for range numWorkers` (range-over-int, Go 1.22+).

### Unbuffered channel for backpressure

`internal/storage/orphaned.go:40` returns an *unbuffered* walker channel — the
producer blocks until the consumer takes each item, so a slow consumer throttles
the disk scan naturally. A separate buffered `error` channel carries the first
walk error:

```go
func WalkStorageDir(storageDir string) (<-chan OrphanedFileInfo, <-chan error) {
	infos := make(chan OrphanedFileInfo)  // unbuffered — backpressure
	errs := make(chan error, 1)
	go func() {
		defer close(infos)
		defer close(errs)
		// walk...
	}()
	return infos, errs
}
```

### Non-blocking send (notification coalescing)

`internal/commands/notify.go:56` turns Postgres NOTIFYs into wake-up hints; if
the channel is full the notification is *dropped* — the 30-second safety timer
is the source of truth, so dropping is safe:

```go
select {
case notifyCh <- struct{}{}:
default: // channel full -> drop; the polling loop re-checks anyway
}
```

### Buffered result channel (goroutine-leak prevention)

`runWithTimeout` (`internal/tools/runner.go:81`) uses a **capacity-1** buffer so
that even when the context wins the race, the goroutine's send completes and it
exits instead of blocking forever:

```go
ch := make(chan result, 1)
go func() {
	v, e := fn()
	ch <- result{v, e}
}()
select {
case <-ctx.Done():
	var zero T
	return zero, ctx.Err()
case r := <-ch:
	return r.val, r.err
}
```

### `struct{}` as a signal

Channels that carry no data use `chan struct{}` — zero bytes and impossible to
misuse (`stopCh`, `notifyCh`, `done`).

---

## 12. The `sync` package

### Mutex / RWMutex

Guards are small and scoped. `sync.Mutex` protects short critical sections
(`config/watcher.go:19` `lastMod`; `utils/logger.go:103` log-file writes).
`sync.RWMutex` protects read-heavy caches — reads use `RLock`, writes use `Lock`
(`internal/cache/cache.go:16`). The embedding cache returns a *deep copy* of its
map so callers can't race the lock (`internal/cache/embedding_store.go:49`):

```go
func (e *EmbeddingStore) Entries() map[string][]float32 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[string][]float32, len(e.entries))
	for k, v := range e.entries {
		out[k] = append([]float32(nil), v...)   // copy the slice
	}
	return out
}
```

### WaitGroup

Counts in-flight goroutines; `Wait` blocks until zero. The pool counts workers
(`pool.go:24`) and the OCR pipeline counts page workers. `Stop` wraps `Wait` in
a goroutine and races it against the context — bounded shutdown
(`internal/pool/pool.go:57`):

```go
done := make(chan struct{})
go func() {
	p.wg.Wait()
	close(done)
}()

select {
case <-done:
	// all workers stopped
case <-ctx.Done():
	// shutdown timed out
}
```

The OCR pipeline uses the newer `WaitGroup.Go` (Go 1.25+), which adds the count
and starts the goroutine in one call.

### sync.Once

Runs a function exactly once — used for idempotent cleanup so `Stop` can be
called twice safely (`internal/pool/pool.go:50`):

```go
p.stopOnce.Do(func() {
	close(p.stopCh)
	if p.cancel != nil {
		p.cancel()
	}
})
```

Also: heartbeat stop (`task/heartbeat.go:15`), tessdata download guard
(`ocr/tessdata.go:15`).

### Atomic: lock-free config hot-reload

`atomic.Pointer[T]` (generic, stdlib) provides wait-free reads/writes of a
pointer. The DI container stores the live config this way
(`internal/commands/container.go:27`):

```go
cfg atomic.Pointer[config.Config]
```

Every runtime config read goes through `c.cfg.Load()`; the polling watcher
publishes reloads with `c.cfg.Store(newCfg)` (§16). Readers never block, and
there are no read locks in the hot path. The same pattern powers
`api/server.go:33` and `wizard/server.go:36`.

There is **no `errgroup`** in this codebase — `golang.org/x/sync` is not
imported. The idiomatic alternatives above (WaitGroup + channels, `sync.Once`)
fill the role.

---

## 13. Timers and tickers

### Ticker: periodic work with `defer Stop`

```go
ticker := time.NewTicker(5 * time.Second)
defer ticker.Stop()
for {
	select {
	case <-ticker.C:
		// do periodic work
	case <-ctx.Done():
		return
	}
}
```

Used by the heartbeat (`task/heartbeat.go:33`), config watcher
(`config/watcher.go:66`), trash purge (`service/trash.go:218`), backup drain
(`task/handlers/backup.go:83`), and queue housekeeping (`queue.go:120`).

### Timer with the drain-before-Reset idiom

`time.Timer` fires once; resetting a timer that already fired (or is about to)
is the classic trap. The queue daemon's 30-second safety net shows the correct
dance (`internal/commands/queue.go:116`):

```go
safetyTimer := time.NewTimer(safetyInterval)
defer safetyTimer.Stop()

case <-notifyCh:
	// consume...
	if !safetyTimer.Stop() {          // Stop returned false -> it may have fired
		select {
		case <-safetyTimer.C:         // drain the stale value
		default:
		}
	}
	safetyTimer.Reset(safetyInterval)
```

### `time.After` inside select

A one-shot timer that works inline — used for poll intervals (`pool.go:86,112`)
and backoff sleeps. For *cancellable* backoff the code selects on both the sleep
and the context (`task/runner.go:118`):

```go
select {
case <-time.After(backoff):
case <-ctx.Done():
	return ctx.Err()
}
```

Retries use exponential backoff (50 ms doubling, max 3 attempts) in
`task/runner.go:98` and `consumption/consumer.go:459`.

---

## 14. Processes, signals, and sockets

### Running external tools

`os/exec` with `CommandContext` — the context cancels the child on timeout or
shutdown. Constructors verify the binary exists at startup with `exec.LookPath`
(§5). Examples: `pdftotext`, `ocrmypdf`, `libreoffice --headless`,
`ghostscript`, `tesseract --list-langs`, and the project's own binaries.

The Gosseract OCR adapter doesn't call Tesseract in-process at all — it
**re-executes itself** with a hidden subcommand (`internal/tools/adapters/ocr/gosseract.go:56`):

```go
cmd := exec.CommandContext(ctx, os.Args[0], "internal-ocr",
	"--input", path,
	"--output", outPath,
	"--languages", langStr,
	"--datadir", o.dataDir,
	"--ocr-workers", strconv.Itoa(o.ocrWorkers),
)
cmd.Env = append(os.Environ(), "OMP_THREAD_LIMIT=1")
```

`os.Args[0]` is the running binary; `kushim main.go` special-cases
`internal-ocr` before normal command dispatch. The child's stdout is streamed
into the parent's log through a `bufio.Scanner` goroutine (gosseract.go:80), and
known-harmless stderr noise is filtered (gosseract.go:122).

### Daemonization and PID files

`kushim queue --bg` spawns `exec.Command(os.Args[0], "queue")` with stdio
detached (`internal/commands/queue.go:54`). The daemon writes a PID file and
detects stale entries with a zero signal probe (`queue.go:66`):

```go
if data, err := os.ReadFile(pidFile); err == nil {
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr == nil && syscall.Kill(pid, 0) == nil {
		return fmt.Errorf("queue daemon already running (PID %d)", pid)
	}
}
```

### Signals

`signal.Notify` + a buffered channel, blocking receive in `main`
(`cmd/edub/main.go:82`):

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
...
sig := <-quit
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := srv.Shutdown(ctx); err != nil { ... }
```

Or conversion to context cancellation via `watchSignals` (§9).

### Unix domain sockets

The tag matcher daemon listens on a Unix socket
(`internal/commands/hugot.go:100`) — with stale-socket removal before listen,
`EADDRINUSE` detected via `errors.As` to `syscall.Errno`, and `os.Remove` after
shutdown. The client speaks plain HTTP over the socket by overriding the
transport's dialer (`internal/tagmatch/client.go:49`):

```go
client: &http.Client{
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
		MaxIdleConns:    1,
		IdleConnTimeout: 120 * time.Second,
	},
},
```

The client sets no `Timeout` of its own — the request context deadline
(`enricher.tagmatcher.timeout`, applied by the runner) is the sole bound.

All RPC calls funnel through one `do()` helper that wraps failures in
`ErrMatcherUnavailable` — which is what makes tag endpoints return 503 when the
matcher daemon is down.

---

## 15. Database access with sqlc

SQL is written by hand in `internal/database/sql/queries/`, and **sqlc** generates
type-safe Go from it (`sqlc generate`; see `sqlc.yaml`). Generated files live in
`internal/database/*.sql.go` and are marked `// Code generated by sqlc. DO NOT
EDIT.` — never edit them by hand, regenerate instead.

### The generated shape

Every query becomes a method on `*Queries` with a `Params` struct
(`internal/database/document.sql.go:13`):

```go
const createDocument = `-- name: CreateDocument :one
INSERT INTO document (...) VALUES ($1, ...) RETURNING id
`

type CreateDocumentParams struct {
	DocumentID string
	// ...
	TextContent sql.NullString
}

func (q *Queries) CreateDocument(ctx context.Context, arg CreateDocumentParams) (int64, error) {
	row := q.db.QueryRowContext(ctx, createDocument, arg.DocumentID, ...)
	var id int64
	err := row.Scan(&id)
	return id, err
}
```

`:one` → returns one row, `:many` → returns `([]T, error)`, `:exec` → no rows.
The generated engine is `database/sql` (configured in `sqlc.yaml`), and the
`DBTX` interface (`internal/database/db.go:12`) is satisfied by both `*sql.DB`
and `*sql.Tx`:

```go
type DBTX interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func New(db DBTX) *Queries { return &Queries{db: db} }

func (q *Queries) WithTx(tx *sql.Tx) *Queries { return &Queries{db: tx} }
```

`WithTx` re-binds the same query set to a transaction — a transaction is just a
`DBTX`.

### The client and transactions

`database.Client` (§7) embeds `*Queries` and adds `BeginTx`. The canonical
transaction shape (`internal/service/batch.go:400`):

```go
tx, err := s.client.BeginTx(ctx, nil)
if err != nil {
	return 0, errs.FromDB(err, "begin transaction for reset "+batchID)
}
defer tx.Rollback()                          // no-op after a successful Commit

txQ := s.client.Queries.WithTx(tx)           // queries bound to the tx

quarantined, err := txQ.QuarantineProcessingTasksByBatch(ctx, ...)
if err != nil { ... }

if err := tx.Commit(); err != nil {
	return 0, errs.FromDB(err, "commit transaction for reset "+batchID)
}
```

`defer tx.Rollback()` is the safety net: if anything returns early, the
transaction rolls back; after `Commit` the rollback is a harmless no-op.

### Nullables and `json.RawMessage`

Generated models use `sql.NullString` / `sql.NullTime` for nullable columns
(`internal/database/models.go`). Remember: `NullString{Valid: false}` scans as
`NULL`; a zero `NullString` does **not** write `NULL`. The task table's
`payload`/`result` columns are overridden in `sqlc.yaml` to
`*json.RawMessage` so handlers can pass through arbitrary JSON without parsing
it.

### Connection management

`internal/database/connection.go` uses `database/sql` with the pgx driver
(`sql.Open("pgx", dsn)`) and explicit pool limits:

```go
db.SetMaxOpenConns(25)
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(5 * time.Minute)
```

It also *creates the database if missing* by connecting to the `postgres` admin
DB and running `CREATE DATABASE` with a quoted, injection-safe identifier
(`connection.go:36`).

### Migrations and seeds

`internal/database/schema.go` embeds `sql/schema` (see §17) and runs goose
migrations, then executes idempotent seed SQL for tags, document types, and
people types — all from the embedded FS, so binaries are self-contained.

### The test harness

`internal/database/dbtest.go` gives every test package an isolated database:
name derived from the calling package via `runtime.Caller`
(`edub_test_<pkg_dir>`), ref-counted, auto-dropped with
`DROP DATABASE ... WITH (FORCE)` on cleanup (§22).

---

## 16. Configuration

Config lives in `~/.config/edub-kushim/config.yaml`. The loading philosophy is
**defaults first, overlay the file, then validate**.

### Defaults-first loading (no viper)

`internal/config/config.go:420` reads the YAML with plain `yaml.v3` over a
fully-populated default struct — the file only overrides what it mentions:

```go
func Load(configDir string) (*Config, error) {
	cfg := DefaultConfig(configDir)

	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
	}
	if err := finalizeConfig(cfg, configDir); err != nil {
		return nil, err
	}
	return cfg, nil
}
```

`finalizeConfig` is the validation pass: required fields, `~` expansion, absolute
path enforcement, and derived paths (e.g. the Hugot model dir). Its error
strings are matched by the CLI to print friendly advice
(`cmd/kushim/main.go:134` special-cases "ocr.languages is required").

Struct tags do triple duty — YAML for the file, `mapstructure` for the API's
dot-key PATCH path, JSON for responses (§5).

### viper: writes only

Viper is used exclusively to *write/edit* YAML with dot-notation keys and
permissions — never to load:

```go
// internal/config/setup.go:76
v := viper.New()
v.SetConfigType("yaml")
v.SetConfigPermissions(0600)
// ...
v.Set("consumer.ocr.languages", languages)
v.WriteConfigAs(configPath)
```

Config edits are atomic: write to a temp dir, *validate by loading*, then
`os.Rename` over the real file (`internal/commands/config.go:213`).

### Hot reload

`config.Watcher` polls the file's `ModTime` on a ticker (`internal/config/watcher.go:61`)
and calls an `onReload` callback; the callback does `container.cfg.Store(newCfg)`
(§12). Every consumer reads `c.cfg.Load()`. No restart needed, no locks in the
read path.

---

## 17. `//go:embed`

`//go:embed` bakes files into the binary at compile time — this is how the
project ships self-contained binaries. Five sites:

| Directive | What's embedded | Consumed as |
|---|---|---|
| `internal/static/fs.go:11` `//go:embed build` | SvelteKit SPA build | `embed.FS` → `fs.Sub(buildFS, "build")` → `http.FileServer` |
| `internal/wizard/fs.go:8` `//go:embed static` | setup wizard SPA | same `fs.Sub` pattern |
| `internal/database/schema.go:11` `//go:embed sql/schema` | migrations + seeds | `goose.SetBaseFS(SchemaFS)` + `ReadFile` |
| `internal/llm/registry.go:13` `//go:embed model_catalog.json` | LLM catalog fallback | `[]byte` |
| `internal/tools/adapters/ocr/font_embed.go:11` `//go:embed kushim_font.ttf` | TrueType font for invisible OCR text layer | `[]byte` → `pdf.AddUTF8FontFromBytes` |

Two shapes: a directory becomes an `embed.FS` (plus `fs.Sub` when the embed root
is a parent dir), a single file becomes a `[]byte`. Because `//go:embed build`
requires the SPA to exist at compile time, the Makefile runs `make web-build`
before `make build` (see `AGENTS.md`; CI stages the build with `make stage-web`).

---

## 18. Build tags and cgo

### The two-binary split

The heavy C dependencies (MuPDF, Tesseract/Leptonica, Hugot) are only linked
into `kushim`. `edub` must stay `CGO_ENABLED=0` — so any code touching C is
gated by the implicit `cgo` build tag, and `edub` gets a stub or talks to
`kushim` over the Unix socket instead.

### Stub twins: `cgo` / `!cgo`

Two files declare the same API; exactly one compiles per build:

```go
// internal/tools/adapters/mupdf_nocgo.go:1  (//go:build !cgo)
type MuContext struct{}

func NewMuContext() (*MuContext, error) {
	return nil, errors.New("MuPDF not available without CGo")
}
```

```go
// internal/tools/adapters/mupdf_wrapper.go:1  (//go:build cgo)
/*
#cgo LDFLAGS: -L${SRCDIR}/../../../build/mupdf/local/lib -lmupdf -lmupdf-third -lm
#cgo CFLAGS: -I${SRCDIR}/../../../build/mupdf/local/include

#include <mupdf/fitz.h>
...
*/
import "C"
```

Notes:

- The `//go:build` line must be followed by a blank line before `package`.
- `#cgo` flags use `${SRCDIR}` (the source dir of the file) to reach the
  `build/` tree — why `make build-deps` must run first.
- The `!cgo` twin keeps signatures *relaxed* (`pixmap any`, `opts any`) so the
  non-CGo build doesn't need to import cgo types it can't have.
- Callers degrade gracefully: `countPages()` returns 0 when the stub fails, so
  ingestion is not blocked.

Other cgo-tagged files: `ocr/gosseract.go`, `ocr/standalone.go` (the internal
OCR engine), `ocr/tesseract_link.go` (static linking + a Leptonica stderr
suppression shim), `tagmatcher/hugot.go` (in-process ONNX matcher). The cgo
tests carry the same tags (§22).

### The `init()` factory swap

The OCR factory (`ocr/adapter.go:18`) declares a default that fails, and the
cgo build overrides it in `init()`:

```go
// default (non-cgo): returns an error
var newGosseract = func(...) (OCR, error) {
	return nil, fmt.Errorf("gosseract OCR requires CGo — rebuild with CGO_ENABLED=1 ...")
}
```

```go
// gosseract.go, cgo-only init()
func init() { newGosseract = func(...) (OCR, error) { return NewGosseract(...) } }
```

### C memory discipline

Across the C boundary, memory is converted explicitly and freed with `defer`
(`internal/tools/adapters/mupdf_wrapper.go:200`): `C.CString` + `defer C.free`
for inputs, `C.GoBytes` to copy results out before the C object is dropped.
The wrapper also carries C-level defense in depth: a 100-megapixel render cap
and a 256 MB fz store cap, with a comment explaining the RSS blow-up that
motivated them.

The `-tags "XLA,ORT"` requirement comes from the Hugot dependency chain — no
source file in this repo carries an `XLA`/`ORT` tag itself.

---

## 19. The HTTP layer

`edub` is a plain `net/http` server — no framework.

### Routing: Go 1.22 method patterns

Routes are registered with method + wildcard patterns; `{id}` captures a path
segment (`internal/api/server.go:199`):

```go
mux.Handle("GET /api/v1/documents", RequireRole(viewer...)(http.HandlerFunc(docHandler.ListDocuments)))
mux.Handle("GET /api/v1/documents/{id}", RequireRole(viewer...)(http.HandlerFunc(docHandler.GetDocument)))
mux.Handle("PUT /api/v1/documents/{id}", RequireRole(editor...)(http.HandlerFunc(docHandler.UpdateDocument)))
```

Handlers read the wildcard with `r.PathValue("id")` (e.g.
`internal/api/handlers/tag.go:141`). The SPA fallback route uses the catch-all
`GET /{path...}` (`server.go:146`), serving the embedded build and falling back
to `/` for client-side routing.

### Handler structs

Each resource is a struct holding its dependencies; methods are the handlers
(`internal/api/handlers/tag.go:32`):

```go
type TagHandler struct {
	services *itypes.CrudServices
	logger   *utils.Logger
}
func (h *TagHandler) List(w http.ResponseWriter, r *http.Request) { ... }
```

Dependencies are shared through `types.CrudServices` (`internal/types.go:10`),
a plain struct of service pointers — assembled once in `NewServer`.

### Middleware: closures composing handlers

Middleware is `func(http.Handler) http.Handler`. Role gating is a middleware
*factory* — it takes the allowed roles and returns the middleware
(`internal/api/permission.go:9`):

```go
func RequireRole(allowed ...auth.Role) func(http.Handler) http.Handler {
	allowedSet := make(map[auth.Role]bool, len(allowed))
	for _, r := range allowed {
		allowedSet[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := r.Context().Value(auth.RoleKey).(string)
			if role == "" || !allowedSet[auth.Role(role)] {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

The chain is built inside-out (`internal/api/server.go:316`):

```go
func chainMiddleware(..., h http.Handler) http.Handler {
	return requestMiddleware(logger,
		AuthMiddleware(parambagMiddleware(h), getSecret, getAuthEnabled, validateAPIKey, getUserByID))
}
```

`requestMiddleware` injects a request ID and logs each request
(`server.go:320`):

```go
reqID := uuid.New().String()
ctx := context.WithValue(r.Context(), "reqid", reqID)
logger.Info(nil, "%s %s REQID=%s", r.Method, r.URL.Path, reqID)
next.ServeHTTP(w, r.WithContext(ctx))
```

Auth middleware (Bearer token, `edub_token` cookie, or `ek_`-prefixed API key)
injects the identity into the context (§9), and the "auth disabled" mode injects
a synthetic admin — the whole server still works for local setups.

### Server lifecycle

`http.Server` with explicit timeouts (`ReadTimeout`, `WriteTimeout`,
`IdleTimeout`), started in a goroutine, shut down gracefully via
`server.Shutdown(ctx)` with a 30-second deadline and `srv.services.Close()` —
which uses reflection to close every service implementing `io.Closer`
(`internal/types.go:23`):

```go
func (s *CrudServices) Close() {
	v := reflect.ValueOf(s).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		if closer, ok := f.Interface().(io.Closer); ok && !f.IsNil() {
			closer.Close()
		}
	}
}
```

This is the project's single use of the `reflect` package — a miniature
dependency-closing loop.

---

## 20. The CLI layer

There is no cobra/urfave: the CLI is hand-rolled and deliberately small.

### Command registry

A `Command` is a struct with a name, description, and handler
(`internal/commands/commands.go:10`):

```go
type Command struct {
	Name        string
	Description string
	Handler     func(container *Container, args []string) error
}
```

Commands are registered in a map per command set (`"cli"` for `kushim`,
`"server"` for `edub`), and dispatched by first argument
(`commands.go:109`):

```go
func (r *CommandRunner) ExecuteCommand(name string, args []string) error {
	cmd, exists := r.commands[name]
	if !exists {
		return fmt.Errorf("unknown command: %s", name)
	}
	return cmd.Handler(r.container, args)
}
```

Errors bubble to `main`, which prints and exits 1
(`cmd/kushim/main.go:158`):

```go
if err := runner.ExecuteCommand(commandName, args); err != nil {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
```

Subcommands (`kushim task list|status|retry`) are plain `switch args[0]`
dispatch (`internal/commands/task.go:28`).

### The DI container

`commands.Container` (`internal/commands/container.go`) is the composition root
for the CLI: it lazily builds the logger, DB client, services, and tools, and
holds the `atomic.Pointer[config.Config]` for hot reload (§12).

### Flags: a tiny parser

`FlagParser` (`internal/commands/flags.go:8`) walks `[]string`, tracks consumed
indices so positional args survive, and offers typed getters with range checks.
Usage looks like (`internal/commands/search.go:21`):

```go
limit := 20
offset := 0
rebuild := false

if err := p.Int("--limit", &limit, 1, 100); err != nil {
	return err
}
if err := p.Int("--offset", &offset, 0, 1<<31); err != nil {
	return err
}
if err := p.Bool("--rebuild-index", &rebuild); err != nil {
	return err
}
args = p.Rest()
```

Unknown flags are rejected explicitly (`if rest := p.Rest(); len(rest) > 0 { ... }`).

### Hidden subprocess entry points

`internal-ocr` and `internal-mupdf-clean` are commands no human runs; they exist
so the process can re-execute itself for isolation (§14). `main` parses their
arguments with a raw `switch` loop before normal dispatch.

---

## 21. Logging

Logging is `log/slog` wrapped in a project logger (`internal/utils/logger.go`).
No logrus/zap.

- **Custom level enum** mirrors syslog priorities (`LevelSilent` … `LevelDebug`,
  plus a `Fatal` at level 12 that also calls `os.Exit(1)`).
- **Two custom `slog.Handler` implementations**: a console handler that
  resolves the call site via `runtime.CallersFrames(record.PC)` and prints
  syslog-style lines (`<6>INFO  : 2006/01/02 15:04:05 file.go:42: message`),
  routing WARN+ to stderr and INFO- to stdout; and a file handler, mutex-guarded
  and without call sites.
- **Rotation** via lumberjack when `MaxSize > 0` (`SetLogFile`,
  logger.go:175).
- **Request IDs**: logging methods take an optional `reqID *string` that
  renders as `REQID=...` — handlers pass `&documentID` or the context's reqid.
- **Test seams**: `NewDiscardLogger()` (slog's `DiscardHandler` at a level above
  Fatal) and `NewLoggerWithWriter(w)` are the standard test loggers.

Example call sites: `c.logger.Info(&documentID, "starting consumption for file %s", ...)`,
`p.logger.Error(nil, "%s: %v", logPrefix, err)`.

---

## 22. Testing

All tests use the stdlib `testing` package — no testify, no gomock, no
testcontainers. Idioms are consistent:

### Table-driven tests with subtests

```go
// internal/utils/logger_test.go:13
tests := []struct {
	input string
	want  LogLevel
}{
	{"debug", LevelDebug},
	{"info", LevelInfo},
	{"unknown", LevelInfo},
}
for _, tt := range tests {
	t.Run(tt.input, func(t *testing.T) {
		l := NewLogger(tt.input)
		if l.Level() != tt.want {
			t.Errorf("NewLogger(%q).Level() = %d, want %d", tt.input, l.Level(), tt.want)
		}
	})
}
```

### Helpers with structural typing

Test helpers accept the smallest interface they need instead of `*testing.T`
(`internal/testutil/testutil.go:20`):

```go
func NewTestConfig(t interface{ Fatalf(format string, args ...any) }) (*config.Config, func()) {
	configDir, err := os.MkdirTemp("", "edub-kushim-test-*")
	if err != nil {
		t.Fatalf("create temp config dir: %v", err)
	}
	// ...
}
```

`*testing.T` and `*testing.B` both satisfy it. Helpers call `t.Helper()` so
failure lines point at the caller.

### Hand-rolled mocks

Mocks are structs implementing the real interfaces, often with injectable
function fields:

```go
// internal/tools/runner_test.go:17
type mockPdfOptimizer struct {
	optimizeFn func(ctx context.Context, docId, path string) (*string, error)
}

func (m *mockPdfOptimizer) Optimize(ctx context.Context, docId, path string) (*string, error) {
	if m.optimizeFn != nil {
		return m.optimizeFn(ctx, docId, path)
	}
	out := path + ".opt"
	return &out, nil
}
```

Shared test doubles live in `internal/testutil/` — including `MockEmbedder`, a
fake implementation of the real `tagmatcher.Embedder` interface used by API
handler tests.

### HTTP handler tests

`httptest.NewRequest` + `httptest.NewRecorder`, driving handler structs
directly (`internal/api/handlers/handlers_test.go:122`):

```go
r := httptest.NewRequest(method, path, bytes.NewReader(body))
r = utils.WithParamBag(r, utils.NewParamBag(r))
r = r.WithContext(context.WithValue(r.Context(), "reqid", "test-req"))
```

### Isolated databases

`database.NewTestDB(t)` (`internal/database/dbtest.go:16`) requires
`TEST_DATABASE_URL` and creates a per-package database (`edub_test_<pkg_dir>`,
named by walking `runtime.Caller`), seeds it from the embedded schema, registers
`t.Cleanup` to close and drop it (`DROP DATABASE ... WITH (FORCE)`, so even live
connections are killed). Tests in one package share the database, and the suite
runs without `t.Parallel()` on purpose. `ResetTestDatabase` truncates tables in
FK-safe order and re-seeds.

### Build-tagged tests

Tests that need the C toolchain carry `//go:build cgo`
(`mupdf_wrapper_test.go`, `gosseract_test.go`, `hugot_test.go`) — they are
skipped by `make test` (which runs `CGO_ENABLED=0`).

The golden rule for new tests: stdlib only, table-driven where possible, mocks
by hand, `testutil` for shared fixtures.

---

## 23. Generics and the modern stdlib

Generics appear in exactly the places where they earn their keep — no
over-engineering:

### `runWithTimeout[T any]` — generic timeout wrapper

`internal/tools/runner.go:81`, used for every tool call regardless of return
type (instantiated with `*string`, `[]string`, `string`, `*AnalysisResult`, ...):

```go
func runWithTimeout[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, e := fn()
		ch <- result{v, e}
	}()
	select {
	case <-ctx.Done():
		var zero T                      // zero-value idiom for any type T
		return zero, ctx.Err()
	case r := <-ch:
		return r.val, r.err
	}
}
```

### Generic result envelopes

`internal/service/result.go:3`:

```go
type CreateResult[T any] struct {
	Entity T
	Status CreateStatus
}
```

One type serves every resource instead of ten near-identical structs.

### Generic stdlib types

`atomic.Pointer[config.Config]` for lock-free config hot-swap (§12) is the
flagship. Beyond that, modern stdlib generics are everywhere:

```go
slices.Sort(items)                       // internal/backup/backup.go:519
slices.Contains(tools, "ocr")            // internal/tools/runner.go:158
slices.SortFunc(..., cmp.Compare)        // textreducer/textrank.go
slices.Collect(maps.Keys(set))           // textreducer/textrank.go
min(a, b) / max(a, b)                    // builtin, Go 1.21+
for range numWorkers                     // range-over-int, Go 1.22+
wg.Go(func() { ... })                    // WaitGroup.Go, Go 1.25+
strings.SplitSeq(...)                    // Go 1.24+ (logger_test.go:221)
```

There are no custom type constraints (`constraints`/`~` are not imported) — the
project sticks to `any`.

---

## 24. Feature → file quick reference

| I want to see... | Go to |
|---|---|
| A goroutine + select loop | `internal/pool/pool.go` (worker), `internal/commands/queue.go` (daemon), `internal/task/heartbeat.go` |
| Channels (producer/consumer, fan-out) | `internal/tools/adapters/ocr/standalone.go`, `internal/storage/orphaned.go` |
| Non-blocking send | `internal/commands/notify.go:56` |
| `sync.Once` idempotent stop | `internal/pool/pool.go:50`, `internal/task/heartbeat.go:15` |
| `atomic.Pointer` hot reload | `internal/commands/container.go:27`, `internal/api/server.go:33`, `internal/config/watcher.go` |
| Timer drain/reset | `internal/commands/queue.go:116` |
| Signals → cancellation | `internal/commands/consume.go:33` (`watchSignals`), `cmd/edub/main.go:82` |
| `os/exec` + self-reexec | `internal/commands/queue.go:54`, `internal/tools/adapters/ocr/gosseract.go:56` |
| Unix socket HTTP client/server | `internal/tagmatch/client.go:49`, `internal/commands/hugot.go:100` |
| Interfaces + composite | `internal/tools/adapters/textextractor/adapter.go` |
| Interface extraction for tests | `internal/consumption/consumer.go:40` |
| Optional interface discovery | `internal/task/registry.go:28` |
| Struct embedding | `internal/database/client.go`, `internal/cache/embedding_store.go`, `internal/auth/claims.go` |
| Typed errors + `Unwrap` | `internal/errs/errs.go`, `internal/task/errors.go` |
| `errors.Is`/`errors.As` | `internal/api/handlers/tag.go:17`, `internal/task/runner.go:62` |
| Context values (typed keys) | `internal/auth/claims.go:30`, `internal/utils/parambag.go:114`, `internal/api/server.go:320` |
| Transactions (defer rollback) | `internal/service/batch.go:400`, `internal/consumption/consumer.go:292` |
| sqlc generated queries | `internal/database/document.sql.go`, `db.go`, `models.go` |
| Migrations/seeds via embed | `internal/database/schema.go` |
| Defaults-first config load | `internal/config/config.go:420` |
| Viper writes-only | `internal/config/setup.go:76` |
| Middleware chain | `internal/api/permission.go`, `internal/api/server.go:316` |
| Go 1.22 routing + `PathValue` | `internal/api/server.go:199`, handlers |
| `go:embed` assets | `internal/static/fs.go`, `internal/database/schema.go`, `internal/llm/registry.go`, `ocr/font_embed.go` |
| cgo preamble + `#cgo` flags | `internal/tools/adapters/mupdf_wrapper.go`, `ocr/tesseract_link.go` |
| Stub twin (`!cgo`) | `internal/tools/adapters/mupdf_nocgo.go` |
| `init()` factory swap | `internal/tools/adapters/ocr/adapter.go` + `gosseract.go:137` |
| CLI registry + flag parser | `internal/commands/commands.go`, `internal/commands/flags.go` |
| slog custom handlers | `internal/utils/logger.go` |
| Table-driven tests | `internal/utils/logger_test.go:13` |
| DB test harness | `internal/database/dbtest.go`, `internal/testutil/testutil.go` |
| Generics | `internal/tools/runner.go:81`, `internal/service/result.go` |
| Reflection | `internal/types.go:23` |

---

## 25. Idioms checklist and gotchas

### Do

- **Return `(value, error)` and wrap with `%w`.** Every `return nil, err` should
  add context: `fmt.Errorf("get tag: %w", err)`.
- **Translate at the boundary.** Services end with `errs.FromDB(err, op)`;
  handlers map kinds with `writeServiceError`.
- **Accept interfaces, return structs.** Declare the smallest interface you need
  (`internal/service/orphaned.go:19`) and add a `var _` compile-time assertion
  where it matters.
- **Pass `ctx` first** and select on `ctx.Done()` in every long-running
  goroutine.
- **Pointer receivers** for anything with mutable state or a mutex.
- **`defer` for cleanup** — rollback, ticker stop, file close.
- **`defer ticker.Stop()`** and the drain-before-`Reset` dance for `time.Timer`.
- **Guard shared state with `sync` primitives or channels** — never globals.
- **Regenerate after SQL edits**: `sqlc generate`; never hand-edit `*.sql.go`.
- **`gofmt`/`go vet` before committing**; build through the Makefile
  (`make build`), which sets `-tags "XLA,ORT"` and the CGo environment.

### Gotchas that bit people before

- **`sync.Mutex` doesn't move.** Copying a struct that contains one (e.g.
  returning it by value) silently breaks the lock. Use pointers.
- **`NullString` semantics**: `Valid: false` scans to `NULL`; a bare zero value
  does not write `NULL`. Check `Valid` before reading `.String`.
- **Map iteration order is random** — don't rely on it (the cache's `Entries()`
  deep-copy exists because of this).
- **`time.Timer` reset without drain** can fire immediately after `Reset`
  (`queue.go:116` shows the correct pattern).
- **Buffered channel capacity matters.** Capacity 1 in `runWithTimeout`
  prevents goroutine leaks; `errs` channel in `WalkStorageDir` has capacity 1 so
  the first error survives.
- **`errors.Is` vs `errors.As`**: `Is` compares against sentinels (or
  `Unwrap` chains), `As` extracts typed values (`*errs.Error`,
  `*pgconn.PgError`, `*exec.ExitError`).
- **Don't compare errors with `==`** unless you mean it — sentinels like
  `sql.ErrNoRows` are fine, wrapped errors are not.
- **`for range ch` blocks** until the channel is closed. The producer must
  `close(jobs)` or workers hang forever.
- **Build tags need a blank line** after the `//go:build` line.
- **`go:embed` paths are relative to the file** and can't use `..` or absolute
  paths; embed a directory and `fs.Sub` when needed.
- **The worker pool polls; it is not a job queue.** Concurrency is bounded by
  worker count, work is claimed from the DB. Adding work to a channel would
  break the design.

---

*Last verified against the tree: 2026-08-03. If code and doc disagree, code wins.*

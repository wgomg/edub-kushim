# Implementation Reference

## Project Structure

```
internal/
├── api/                    # HTTP handlers, middleware, types
│   ├── handlers/
│   │   ├── document.go    # Document API handlers
│   │   └── health.go      # Health check handler
│   ├── server.go          # HTTP server setup and middleware
│   └── types/
│       └── document.go    # API request/response types
├── commands/              # CLI command framework
│   ├── commands.go        # Command definitions and runner
│   ├── consume.go         # Document consumption command
│   └── container.go       # Dependency injection container
├── config/                # Configuration parsing
│   └── config.go          # Configuration structs and loading
├── consumption/           # Document processing engine
│   ├── consumer.go        # Main consumer logic and processing
│   └── storage.go         # File operations and storage utilities
├── database/              # Database layer (sqlc-generated + manual)
│   ├── connection.go      # Database connection and schema setup
│   ├── models.go          # Generated data models
│   ├── *.sql.go           # Generated query implementations
│   ├── fts5.go            # Manual FTS5 query implementation
│   └── db.go              # Database interface
├── tools/                 # External tool integration
│   ├── adapters/
│   │   ├── ocr/
│   │   │   ├── adapter.go      # OCR interface and factory
│   │   │   ├── gosseract.go    # Go‑native gosseract implementation
│   │   │   ├── ocrmypdf.go     # Legacy ocrmypdf implementation
│   │   │   ├── tesseract_link.go  # CGo linker flags for static Tesseract
│   │   │   └── tessdata_embed.go  # Embedded eng.traineddata
│   │   ├── textextractor/
│   │   │   ├── adapter.go      # Text extractor interface
│   │   │   ├── fitz.go         # Go‑native go‑fitz implementation
│   │   │   └── pdftotext.go    # Legacy pdftotext implementation
│   │   └── pdfoptimizer/
│   │       ├── adapter.go      # PDF optimizer interface
│   │       └── ghostscript.go  # Ghostscript implementation
│   └── runner.go          # Unified tool runner
└── utils/                 # Utilities
    ├── logger.go          # Structured logging
    └── parambag.go        # HTTP parameter parsing

sql/
├── schema.sql            # Database schema definitions
└── queries/              # SQL queries for sqlc

cmd/
└── kushim/               # CLI entry point
    └── main.go
```

---

## File Reference

### API Layer (`internal/api/`)

#### `server.go`

**Struct:**

- `Server`
  - **Fields**: `httpServer *http.Server`, `logger *utils.Logger`, `addr string`
  - **Methods**:
    - `NewServer(cfg config.ServerConfig, logger *utils.Logger, db *sql.DB) *Server`
    - `Start() error`
    - `Shutdown(ctx context.Context) error`
    - `Addr() string`

**Functions:**

- `registerRoutes(mux *http.ServeMux, logger *utils.Logger, queries *database.Queries)`
- `chainMiddleware(logger *utils.Logger, h http.Handler) http.Handler`
- `requestMiddleware(logger *utils.Logger, next http.Handler) http.Handler`
- `parambagMiddleware(next http.Handler) http.Handler`

#### `handlers/document.go`

**Struct:**

- `DocumentHandler`
  - **Fields**: `queries *database.Queries`, `logger *utils.Logger`
  - **Methods**:
    - `NewDocumentHandler(queries *database.Queries, logger *utils.Logger) *DocumentHandler`
    - `ListDocuments(w http.ResponseWriter, r *http.Request)`
    - `GetDocument(w http.ResponseWriter, r *http.Request)`

#### `handlers/health.go`

**Struct:**

- `HealthResponse`
  - **Fields**: `Status string`, `Version string`, `Time string`

**Function:**

- `HealthHandler(w http.ResponseWriter, r *http.Request, logger *utils.Logger)`

#### `types/document.go`

**Structs:**

- `CreateDocumentRequest`
  - **Fields**: `Title string`, `Checksum string`, `Filename string`, `MimeType string`, `FileSize int64`, `SourcePath string`
- `DocumentResponse`
  - **Fields**: `ID int64`, `Title string`, `Checksum string`, `Filename string`, `MimeType string`, `FileSize int64`, `CreatedAt string`, `ModifiedAt string`, `SourcePath string`

---

### CLI Commands (`internal/commands/`)

#### `commands.go`

**Structs:**

- `Command`
  - **Fields**: `Name string`, `Description string`, `Handler func(container *Container, args []string) error`
- `CommandRunner`
  - **Fields**: `container *Container`, `commands map[string]Command`
  - **Methods**:
    - `NewCommandRunner(container *Container) *CommandRunner`
    - `ExecuteCommand(name string, args []string) error`

**Functions:**

- `ListCommands() []Command`
- `PrintUsage()`
- `versionHandler(container *Container, args []string) error`

#### `consume.go`

**Function:**

- `consumeHandler(c *Container, args []string) error`

#### `container.go`

**Struct:**

- `Container`
  - **Fields**: `config *config.Config`, `logger *utils.Logger`, `db *sql.DB`, `consumer *consumption.Consumer`
  - **Methods**:
    - `NewContainer(cfg *config.Config, logger *utils.Logger) *Container`
    - `GetDB() (*sql.DB, error)`
    - `GetConsumer() (*consumption.Consumer, error)`
    - `Close()`

---

### Configuration (`internal/config/config.go`)

**Structs:**

- `Config`
  - **Fields**: `App AppConfig`, `Srv ServerConfig`, `Db DatabaseConfig`, `Storage StorageConfig`, `Consumer ConsumerConfig`
- `AppConfig`: `Env Environment`, `LogLevel string`
- `ServerConfig`: `Host string`, `Port int`, `ReadTimeout time.Duration`, `WriteTimeout time.Duration`, `IdleTimeout time.Duration`
- `DatabaseConfig`: `Type string`, `Path string`, `Name string`
- `StorageConfig`: `ConsumptionDir string`, `StorageDir string`
- `ConsumerConfig`: `SupportedFiles []string`, `TextExtractor string`, `PdfOptimizer string`, `OCR string`, `DeleteOriginal bool`
- `ToolConfig`: `Command string`, `Timeout time.Duration`

**Constants:** `Environment` (`Development`, `Production`)

**Function:**

- `Load(path string) (*Config, error)` — Loads YAML config with defaults, creates directories.

---

### Consumption Engine (`internal/consumption/`)

#### `consumer.go`

**Structs:**

- `Consumer`
  - **Fields**: `config *config.Config`, `logger *utils.Logger`, `db *sql.DB`
  - **Methods**:
    - `NewConsumer(cfg *config.Config, logger *utils.Logger, db *sql.DB) *Consumer`
    - `Consume(reqID *string) error` — Full pipeline: scan → process → store
    - `Process(file File) (File, error)` — Processes a single file
    - `extractText(file File) (File, error)` — Text extraction with OCR fallback
    - `isDuplicate(path string) (bool, error)` — MD5 + SHA512 duplicate check

- `File`
  - **Fields**: `Name string`, `OriginalPath string`, `OCRTmpPath *string`, `OptimizedPdfTmpPath *string`, `StorageProcessedPath *string`, `StorageOriginalPath *string`, `MD5Checksum string`, `SHA512Checksum string`, `Text sql.NullString`, `MimeType string`, `Date time.Time`, `FileSize int64`

**Functions:**

- `calculateMD5(path string) (string, error)`
- `calculateSHA512(path string) (string, error)`

#### `storage.go`

**Functions:**

- `GetFiles(src string, exts []string) ([]File, error)` — Scan directory for supported files
- `RemoveFile(path string) error`
- `MoveFile(src, dst string) error` — Atomic rename with cross‑device fallback
- `CopyFile(src, dst string) error`
- `moveFileCrossDevice(src, dst string, srcInfo os.FileInfo) error` — Copy + remove
- `calculateChecksums(filePath string) (md5Hash string, sha512Hash string, err error)` — Single‑pass
- `CleanUp(path string) error` — Safe temp file removal

---

### Database Layer (`internal/database/`)

#### `connection.go`

**Functions:**

- `NewSQLiteDB(cfg config.DatabaseConfig) (*sql.DB, error)`
- `NewQueries(db *sql.DB) *Queries`
- `createSchema(db *sql.DB) error`

#### `models.go` (sqlc-generated)

Key struct:

- `Document`: `ID int64`, `Title string`, `Md5Checksum string`, `Sha512Checksum string`, `MimeType string`, `FileSize int64`, `CreatedAt sql.NullTime`, `ModifiedAt sql.NullTime`, `DocumentTypeID sql.NullInt64`, `OriginalPath string`, `StoragePath string`, `TextContent sql.NullString`

Supporting: `Author`, `DocumentAuthor`, `DocumentTag`, `DocumentType`, `Tag`, `Task`, `User`.

#### Query methods (sqlc-generated, `*.sql.go`)

- `CreateDocument(ctx context.Context, arg CreateDocumentParams) (sql.Result, error)` — Returns `LastInsertId`
- `GetDocument(ctx context.Context, id int64) (Document, error)`
- `ListDocuments(ctx context.Context, arg ListDocumentsParams) ([]Document, error)`
- `UpdateDocumentPaths(ctx context.Context, arg UpdateDocumentPathsParams) error`
- `GetDocumentByMD5Checksum(ctx context.Context, md5Checksum string) ([]Document, error)`
- `GetDocumentBySHA512Checksum(ctx context.Context, sha512Checksum string) (Document, error)`

#### `fts5.go`

**Struct:**

- `FTSDocumentRow` — Document fields + `Rank float64`, `Snippet string`

**Function:**

- `SearchDocumentsFTS(ctx context.Context, searchQuery string, limit, offset int32) ([]FTSDocumentRow, error)` — FTS5 search with BM25 ranking and snippet highlighting

#### `db.go` (sqlc-generated)

- `DBTX` interface: `ExecContext`, `PrepareContext`, `QueryContext`, `QueryRowContext`
- `Queries` struct: `New(db DBTX) *Queries`, `WithTx(tx *sql.Tx) *Queries`

---

### Tools Framework (`internal/tools/`)

#### `runner.go`

**Struct:**

- `Runner`
  - **Fields**: `logger *utils.Logger`, `config *config.ConsumerConfig`
  - **Methods**:
    - `NewRunner(logger *utils.Logger, cfg *config.ConsumerConfig) *Runner`
    - `ExtractText(path string) (*TextExtractionResult, error)`
    - `OCR(path string) (*OCRResult, error)`
    - `OptimizePdf(path string) (*PdfOptimizationResult, error)`

**Result structs:**

- `TextExtractionResult`: `Text *string`, `Metadata map[string]interface{}`
- `OCRResult`: `Success bool`, `TmpPath *string`, `Confidence *float64`
- `PdfOptimizationResult`: `Success bool`, `TmpPath *string`

#### `adapters/ocr/adapter.go`

**Interface:**

```go
type OCR interface {
    Process(path string) (*string, error)
    CanHandle(mimeType string) bool
    Name() string
}
```

**Factory:** `NewOCR(logger *utils.Logger, cfg config.ToolConfig) (OCR, error)`

#### `adapters/ocr/gosseract.go`

**Struct:** `Gosseract`

- `NewGosseract(logger *utils.Logger, cfg config.ToolConfig) (*Gosseract, error)`
- `Process(path string) (*string, error)` — Renders pages at 300 DPI via go‑fitz, OCRs with gosseract (PPM input), builds searchable PDF with fpdf using text rendering mode 3.
- `CanHandle(mimeType string) bool`
- `Name() string` — returns `"gosseract"`

**Details:**

- Tesseract/Leptonica statically linked via CGo (`tesseract_link.go`)
- `eng.traineddata` embedded (`tessdata_embed.go`), extracted to temp dir at runtime
- PPM encoding for OCR (Leptonica handles PPM natively; avoids libjpeg conflict with MuPDF)
- JPEG encoding for fpdf background (pure Go, separate from OCR path)
- Raw PDF operators (`3 Tr`) injected via `RawWriteStr`
- Coordinate conversion: 300 DPI pixels → 72 DPI PDF points, Y flipped top‑left → bottom‑left

#### `adapters/textextractor/adapter.go`

**Interface:**

```go
type TextExtractor interface {
    Extract(path string) (*string, error)
    CanHandle(mimeType string) bool
    Name() string
}
```

**Factory:** `NewTextExtractor(logger *utils.Logger, cfg config.ToolConfig) (TextExtractor, error)`

#### `adapters/textextractor/fitz.go`

**Struct:** `Fitz`

- `NewFitz(logger *utils.Logger, config config.ToolConfig) (*Fitz, error)`
- `Extract(path string) (*string, error)` — Uses go‑fitz (statically‑linked MuPDF)
- `CanHandle(mimeType string) bool`
- `Name() string` — returns `"go-fitz"`

#### `adapters/pdfoptimizer/adapter.go`

**Interface:**

```go
type PdfOptimizer interface {
    Optimize(path string) (*string, error)
    Name() string
}
```

**Factory:** `NewPdfOptimizer(logger *utils.Logger, cfg config.ToolConfig) (PdfOptimizer, error)`

#### `adapters/pdfoptimizer/ghostscript.go`

**Struct:** `Ghostscript`

- `NewGhostscript(logger *utils.Logger, cfg config.ToolConfig) (*Ghostscript, error)`
- `Optimize(path string) (*string, error)` — Rewrites PDF via `gs`
- `Name() string` — returns `"ghostscript"`

---

### Utilities (`internal/utils/`)

#### `logger.go`

**Struct:** `Logger`

- `NewLogger(level string) *Logger`
- `NewDiscardLogger() *Logger`
- `Info(reqID *string, format string, v ...any)`
- `Error(reqID *string, format string, v ...any)`
- `Debug(reqID *string, format string, v ...any)`
- `Fatal(v ...any)` — exits

**Constants:** `LevelDebug`, `LevelInfo`, `LevelError`

#### `parambag.go`

**Struct:** `ParamBag`

- `NewParamBag(r *http.Request) *ParamBag`
- `Get(key, defaultValue string) string`
- `GetInt(key string, defaultValue, min, max int) int`
- `GetInt64(key string, defaultValue, min, max int64) int64`
- `GetBool(key string, defaultValue bool) bool`
- `GetStrings(key string) []string`
- `SetPathValue(key, value string)`

Context helpers: `WithParamBag(r *http.Request, pb *ParamBag) *http.Request`, `GetParamBag(r *http.Request) *ParamBag`

---

## API Reference

### Health Check

```
GET /health
Response: { "status": "healthy", "version": "0.1.0", "time": "..." }
```

### List Documents

```
GET /api/v1/documents?limit=50&offset=0
Response: Array of DocumentResponse
```

### Get Document

```
GET /api/v1/documents/{id}
Response: Single DocumentResponse
```

### Search Documents

```
GET /api/v1/documents/search?q=terms&limit=50&offset=0
Query:
  q      FTS5 query (supports AND, OR, NOT, "phrase", prefix*)
  limit  1-100, default 50
  offset default 0
Response: Array of FTSDocumentResponse
```

### DocumentResponse

```json
{
  "id": 1,
  "title": "document.pdf",
  "checksum": "sha256:abc123...",
  "filename": "document.pdf",
  "mime_type": "application/pdf",
  "file_size": 102400,
  "created_at": "2024-03-19T10:30:00Z",
  "modified_at": "2024-03-19T10:30:00Z",
  "source_path": "/storage/2024/03/19/1.pdf"
}
```

`FTSDocumentResponse` adds `text_content`, `rank` (BM25 score), and `snippet` (highlighted context).

---

## Database Schema

### Core Tables

- `document` — Main storage: `md5_checksum`, `sha512_checksum` (UNIQUE), `text_content`, paths
- `author`, `tag`, `document_type` — Classification
- `task` — Async processing tracking
- `user` — Future authentication

### Junction Tables

- `document_author` (document_id, author_id)
- `document_tag` (document_id, tag_id)

### FTS5 Virtual Table

```sql
CREATE VIRTUAL TABLE document_fts USING fts5(
    title,
    content,
    document_id UNINDEXED,
    tokenize = 'unicode61'
);
```

### Triggers

- `document_ai` — INSERT: auto‑adds to `document_fts`
- `document_au` — UPDATE: syncs FTS index
- `document_ad` — DELETE: removes from FTS index

### Key Indexes

- `idx_document_md5` — Fast MD5 lookups
- `idx_document_sha512` — SHA512 lookups (UNIQUE constraint)
- `idx_document_created` — Date‑based queries
- `idx_task_status` — Task monitoring

---

## Full Configuration Reference

```yaml
app:
  environment: development # development | production
  log_level: debug # debug | info | error

server:
  host: localhost
  port: 3000
  read_timeout: 60 # seconds
  write_timeout: 60
  idle_timeout: 60

database:
  type: sqlite
  path: ./data # directory for .db file

storage:
  consumption_dir: ./inbox # scan target
  storage_dir: ./storage # processed files root

consumer:
  supported_files: ['.pdf']
  textextractor: 'go-fitz' # go‑fitz (statically‑linked MuPDF)
  pdfoptimizer: 'gs' # Ghostscript (pending MuPDF replacement)
  ocr: 'gosseract' # gosseract (statically‑linked Tesseract)
  delete_original: false # remove inbox file after processing?
```

---

## Build System

```bash
make             # build binary → ./dev/bin/kushim
make build-deps  # one‑time: compile Leptonica + Tesseract static libs
make consume     # build + run pipeline
make clean       # remove binary
```

The Makefile exports `CGO_ENABLED=1` and `CGO_CPPFLAGS` automatically.
CGo linker flags are in `internal/tools/adapters/ocr/tesseract_link.go`.

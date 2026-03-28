> **AI-Generated Documentation Notice**: This documentation was generated with AI assistance and may contain inaccuracies or omissions. Always verify against the actual source code for precise implementation details. The documentation serves as a reference guide but should not be considered authoritative.

---

# Document Management System - Core Components

## Overview

High-performance, Go-based document management system optimized for large collections (tens of thousands of documents including full books). Headless REST API + CLI first, web UI later.

**Current Status**: MVP API foundation complete. Document consumption pipeline with OCR/text extraction capabilities implemented. Core processing workflow functional.

## Architecture

### Core Design Principles

- **Headless First**: API-driven with CLI interface, web UI as optional layer
- **Tool Agnostic**: Adapter pattern for external tools (pdftotext, ocrmypdf)
- **SQLite First**: Development-friendly with migration path to production databases
- **Fallback Processing**: Text extraction → OCR → text extraction pattern
- **Date-based Organization**: Temporal storage structure for scalability

### Current Implementation Status

#### ✅ Complete

- HTTP Server with middleware (request ID, parameter parsing)
- Database layer with sqlc-generated type-safe queries
- Configuration system with YAML support
- Structured logging with request correlation
- Document API (list, get by ID)
- CLI framework with dependency injection
- Tools framework (pdftotext, ocrmypdf adapters)
- Document consumption pipeline (scan → extract → OCR → store)
- Database schema with text content storage and FTS5 virtual tables
- File movement and cleanup operations with transaction safety
- Full-text search implementation with manual FTS5 queries

#### 🚧 In Progress

- API endpoints for FTS search
- Integration testing of FTS triggers
- Performance optimization for large collections

#### 📋 Planned

- Async task processing with queue
- ZincSearch integration for full-text search
- LLM/semantic processing for tags/classification
- Authentication and user management

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
├── database/              # Database layer (sqlc-generated)
│   ├── connection.go      # Database connection and schema setup
│   ├── models.go          # Generated data models
│   ├── *.sql.go           # Generated query implementations
│   └── db.go              # Database interface
├── tools/                 # External tool integration
│   ├── adapters/
│   │   ├── ocr/
│   │   │   ├── adapter.go # OCR interface and factory
│   │   │   └── ocrmypdf.go # ocrmypdf implementation
│   │   └── textextractor/
│   │       ├── adapter.go # Text extractor interface
│   │       └── pdftotext.go # pdftotext implementation
│   └── runner.go          # Unified tool runner
└── utils/                 # Utilities
    ├── logger.go          # Structured logging
    └── parambag.go        # HTTP parameter parsing

sql/
├── schema.sql            # Database schema definitions
└── queries/              # SQL queries for sqlc
    ├── author.sql
    ├── document_author.sql
    ├── document.sql
    ├── document_tag.sql
    ├── document_type.sql
    ├── tag.sql
    ├── task.sql
    └── user.sql

cmd/
├── edub/                 # Main application entry point
│   └── main.go
└── kushim/               # CLI entry point
    └── main.go
```

## Database Full-Text Search

### Overview

The system implements full-text search using SQLite's FTS5 (Full-Text Search version 5) virtual tables. This provides fast, relevance-ranked search across document titles and extracted text content without requiring external search services.

### Architecture

#### FTS5 Implementation

The system uses SQLite's built-in FTS5 extension for full-text search capabilities. FTS5 virtual tables are created alongside regular database tables to enable efficient text searching with relevance ranking.

#### Key Components

1. **Virtual Table Structure**: A dedicated FTS5 virtual table stores searchable text fields (title and content) with document references
2. **Automatic Indexing**: SQLite automatically creates and maintains inverted indexes for fast text search
3. **Relevance Ranking**: Built-in ranking algorithms (BM25) provide relevance-based result ordering
4. **Snippet Generation**: Context highlighting shows search term matches within results

#### Integration Points

- **Text Storage**: Extracted text from documents is stored in both the main document table and the FTS virtual table
- **Search Queries**: Specialized queries use the `MATCH` operator for full-text search
- **Result Ranking**: Search results are ordered by relevance score for optimal user experience

#### Performance Characteristics

- **In-Memory Indexes**: FTS5 maintains search indexes in memory for fast query performance
- **Efficient Updates**: Triggers or application logic keep FTS data synchronized with document changes
- **Scalable Design**: The architecture supports tens of thousands of documents with responsive search

#### Search Features

- **Phrase Search**: Exact phrase matching with quotation marks
- **Boolean Operators**: AND, OR, NOT operators for complex queries
- **Prefix Search**: Wildcard matching for partial terms
- **Relevance Scoring**: Results ranked by term frequency and document structure

#### Current FTS5 Implementation

**Schema Features:**

- `document_fts` virtual table with `unicode61` tokenizer (multi-language support)
- Automatic synchronization via triggers (`document_ai`, `document_au`, `document_ad`)
- `UNINDEXED` document_id column for efficient JOINs
- Universal Unicode support for all scripts/languages

**Manual Implementation (sqlc workaround):**
Since sqlc doesn't support FTS5 syntax, FTS queries are implemented manually in:

- `internal/database/fts5.go` - Raw SQL queries with `bm25()` ranking and `snippet()` highlighting
- Triggers automatically maintain FTS table synchronization

**Search Capabilities:**

- Relevance ranking using BM25 algorithm
- Snippet generation with highlighted matches
- Multi-language support via `unicode61` tokenizer
- Boolean operators, phrase search, wildcard matching

### Future Considerations

- **Alternative Databases**: PostgreSQL and MariaDB full-text search support planned for future abstraction
- **Hybrid Search**: Potential for combining FTS with vector/semantic search
- **Performance Optimization**: Query optimization and indexing strategies for large document collections

## File Reference

### API Layer (`internal/api/`)

#### `server.go`

**Functions:**

- `NewServer(cfg config.ServerConfig, logger *utils.Logger, db *sql.DB) *Server`
  - **Params**: `cfg` (server config), `logger` (logger instance), `db` (database connection)
  - **Returns**: `*Server` (configured HTTP server)
- `registerRoutes(mux *http.ServeMux, logger *utils.Logger, queries *database.Queries)`
  - **Params**: `mux` (HTTP mux), `logger` (logger), `queries` (database queries)
  - **Returns**: `void`
- `chainMiddleware(logger *utils.Logger, h http.Handler) http.Handler`
  - **Params**: `logger` (logger), `h` (next handler)
  - **Returns**: `http.Handler` (chained middleware)
- `requestMiddleware(logger *utils.Logger, next http.Handler) http.Handler`
  - **Params**: `logger` (logger), `next` (next handler)
  - **Returns**: `http.Handler` (request middleware)
- `parambagMiddleware(next http.Handler) http.Handler`
  - **Params**: `next` (next handler)
  - **Returns**: `http.Handler` (parameter bag middleware)

**Struct:**

- `Server`
  - **Fields**: `httpServer *http.Server`, `logger *utils.Logger`, `addr string`
  - **Methods**:
    - `Start() error` - Start HTTP server
    - `Shutdown(ctx context.Context) error` - Graceful shutdown
    - `Addr() string` - Get server address

#### `handlers/document.go`

**Struct:**

- `DocumentHandler`
  - **Fields**: `queries *database.Queries`, `logger *utils.Logger`
  - **Methods**:
    - `NewDocumentHandler(queries *database.Queries, logger *utils.Logger) *DocumentHandler`
      - **Params**: `queries` (database queries), `logger` (logger)
      - **Returns**: `*DocumentHandler` (new handler instance)
    - `ListDocuments(w http.ResponseWriter, r *http.Request)`
      - **Params**: `w` (HTTP response writer), `r` (HTTP request)
      - **Returns**: `void`
    - `GetDocument(w http.ResponseWriter, r *http.Request)`
      - **Params**: `w` (HTTP response writer), `r` (HTTP request)
      - **Returns**: `void`

#### `handlers/health.go`

**Struct:**

- `HealthResponse`
  - **Fields**: `Status string`, `Version string`, `Time string`

**Function:**

- `HealthHandler(w http.ResponseWriter, r *http.Request, logger *utils.Logger)`
  - **Params**: `w` (HTTP response writer), `r` (HTTP request), `logger` (logger)
  - **Returns**: `void`

#### `types/document.go`

**Structs:**

- `CreateDocumentRequest`
  - **Fields**: `Title string`, `Checksum string`, `Filename string`, `MimeType string`, `FileSize int64`, `SourcePath string`
- `DocumentResponse`
  - **Fields**: `ID int64`, `Title string`, `Checksum string`, `Filename string`, `MimeType string`, `FileSize int64`, `CreatedAt string`, `ModifiedAt string`, `SourcePath string`

### CLI Commands (`internal/commands/`)

#### `commands.go`

**Structs:**

- `Command`
  - **Fields**: `Name string`, `Description string`, `Handler func(container *Container, args []string) error`
- `CommandRunner`
  - **Fields**: `container *Container`, `commands map[string]Command`
  - **Methods**:
    - `NewCommandRunner(container *Container) *CommandRunner`
      - **Params**: `container` (dependency container)
      - **Returns**: `*CommandRunner` (new command runner)
    - `ExecuteCommand(name string, args []string) error`
      - **Params**: `name` (command name), `args` (command arguments)
      - **Returns**: `error` (execution error)

**Functions:**

- `ListCommands() []Command`
  - **Returns**: `[]Command` (list of available commands)
- `PrintUsage()`
  - **Returns**: `void` (prints usage and exits)
- `versionHandler(container *Container, args []string) error`
  - **Params**: `container` (dependency container), `args` (command arguments)
  - **Returns**: `error` (execution error)

#### `consume.go`

**Function:**

- `consumeHandler(c *Container, args []string) error`
  - **Params**: `c` (dependency container), `args` (command arguments)
  - **Returns**: `error` (execution error)

#### `container.go`

**Struct:**

- `Container`
  - **Fields**: `config *config.Config`, `logger *utils.Logger`, `db *sql.DB`, `consumer *consumption.Consumer`
  - **Methods**:
    - `NewContainer(cfg *config.Config, logger *utils.Logger) *Container`
      - **Params**: `cfg` (configuration), `logger` (logger)
      - **Returns**: `*Container` (new container)
    - `GetDB() (*sql.DB, error)`
      - **Returns**: `*sql.DB` (database connection), `error` (connection error)
    - `GetConsumer() (*consumption.Consumer, error)`
      - **Returns**: `*consumption.Consumer` (consumer instance), `error` (initialization error)
    - `Close()`
      - **Returns**: `void` (cleanup resources)

### Configuration (`internal/config/config.go`)

**Structs:**

- `AppConfig`
  - **Fields**: `Env Environment`, `LogLevel string`
- `ServerConfig`
  - **Fields**: `Host string`, `Port int`, `ReadTimeout time.Duration`, `WriteTimeout time.Duration`, `IdleTimeout time.Duration`
- `DatabaseConfig`
  - **Fields**: `Type string`, `Path string`, `Name string`
- `StorageConfig`
  - **Fields**: `ConsumptionDir string`, `StorageDir string`
- `ConsumerConfig`
  - **Fields**: `SupportedFiles []string`, `TextExtractor string`, `OCR string`, `DeleteOriginal bool`
- `ToolConfig`
  - **Fields**: `Command string`, `Timeout time.Duration`
- `Config`
  - **Fields**: `App AppConfig`, `Srv ServerConfig`, `Db DatabaseConfig`, `Storage StorageConfig`, `Consumer ConsumerConfig`

**Constants:**

- `Environment`: `Development`, `Production`

**Function:**

- `Load(path string) (*Config, error)`
  - **Params**: `path` (config file path)
  - **Returns**: `*Config` (loaded configuration), `error` (loading error)
  - **Description**: Loads configuration from YAML file with sensible defaults. Creates required directories if they don't exist. Supports `delete_original` configuration option to remove original files after successful processing.

### Consumption Engine (`internal/consumption/`)

#### `consumer.go`

**Structs:**

- `Consumer`
  - **Fields**: `config *config.Config`, `logger *utils.Logger`, `db *sql.DB`
  - **Methods**:
    - `NewConsumer(cfg *config.Config, logger *utils.Logger, db *sql.DB) *Consumer`
      - **Params**: `cfg` (configuration), `logger` (logger), `db` (database connection)
      - **Returns**: `*Consumer` (new consumer)
    - `Consume(reqID *string) error`
      - **Params**: `reqID` (optional request ID)
      - **Returns**: `error` (consumption error)
    - `Process(file File) (File, error)`
      - **Params**: `file` (file to process)
      - **Returns**: `File` (processed file), `error` (processing error)
    - `extractText(file File) (File, error)`
      - **Params**: `file` (file to extract text from)
      - **Returns**: `File` (file with extracted text), `error` (extraction error)
    - `isDuplicate(path string) (bool, error)`
      - **Params**: `path` (file path to check)
      - **Returns**: `bool` (is duplicate), `error` (check error)
      - **Description**: Checks if a file is a duplicate by computing MD5 and SHA512 checksums and comparing with database records. Uses MD5 for fast filtering and SHA512 for secure verification.

- `File`
  - **Fields**: `Name string`, `OriginalPath string`, `OCRTmpPath *string`, `StorageProcessedPath *string`, `StorageOriginalPath *string`, `MD5Checksum string`, `SHA512Checksum string`, `Text *string`, `MimeType string`, `Date time.Time`, `FileSize int64`

**Functions:**

- `calculateMD5(path string) (string, error)`
  - **Params**: `path` (file path)
  - **Returns**: `string` (MD5 checksum), `error` (calculation error)
- `calculateSHA512(path string) (string, error)`
  - **Params**: `path` (file path)
  - **Returns**: `string` (SHA512 checksum), `error` (calculation error)

#### `storage.go`

**Functions:**

- `GetFiles(src string, exts []string) ([]File, error)`
  - **Params**: `src` (source directory), `exts` (supported extensions)
  - **Returns**: `[]File` (list of files), `error` (directory reading error)
- `RemoveFile(path string) error`
  - **Params**: `path` (file path to remove)
  - **Returns**: `error` (removal error)
- `MoveFile(src, dst string) error`
  - **Params**: `src` (source file path), `dst` (destination file path)
  - **Returns**: `error` (move operation error)
  - **Description**: Moves a file from source to destination. First attempts atomic rename within same filesystem, falls back to copy+remove for cross-device moves. Creates destination directory if needed. Returns error if destination already exists.
- `CopyFile(src, dst string) error`
  - **Params**: `src` (source file path), `dst` (destination file path)
  - **Returns**: `error` (copy operation error)
  - **Description**: Copies a file from source to destination. Creates destination directory if needed. Returns error if destination already exists.
- `moveFileCrossDevice(src, dst string, srcInfo os.FileInfo) error`
  - **Params**: `src` (source file path), `dst` (destination file path), `srcInfo` (source file metadata)
  - **Returns**: `error` (copy operation error)
  - **Description**: Internal helper function for cross-device file moves. Copies file contents, preserves permissions, and removes source after successful copy.
- `calculateChecksums(filePath string) (md5Hash string, sha512Hash string, err error)`
  - **Params**: `filePath` (path to file)
  - **Returns**: `md5Hash` (MD5 checksum), `sha512Hash` (SHA512 checksum), `error` (calculation error)
  - **Description**: Calculates both MD5 and SHA512 checksums for a file in a single pass.

### Database Layer (`internal/database/`)

#### `connection.go`

**Functions:**

- `NewSQLiteDB(cfg config.DatabaseConfig) (*sql.DB, error)`
  - **Params**: `cfg` (database configuration)
  - **Returns**: `*sql.DB` (database connection), `error` (connection error)
- `NewQueries(db *sql.DB) *Queries`
  - **Params**: `db` (database connection)
  - **Returns**: `*Queries` (query interface)
- `createSchema(db *sql.DB) error`
  - **Params**: `db` (database connection)
  - **Returns**: `error` (schema creation error)

#### `models.go` (sqlc-generated)

**Structs:**

- `Author`: `ID int64`, `Name string`, `CreatedAt sql.NullTime`
- `Document`: `ID int64`, `Title string`, `Md5Checksum string`, `Sha512Checksum string`, `MimeType string`, `FileSize int64`, `CreatedAt sql.NullTime`, `ModifiedAt sql.NullTime`, `DocumentTypeID sql.NullInt64`, `OriginalPath string`, `StoragePath string`
- `DocumentAuthor`: `DocumentID int64`, `AuthorID int64`
- `DocumentTag`: `DocumentID int64`, `TagID int64`
- `DocumentType`: `ID int64`, `Name string`, `CreatedAt sql.NullTime`
- `Tag`: `ID int64`, `Name string`, `CreatedAt sql.NullTime`
- `Task`: `ID int64`, `TaskID string`, `TaskName string`, `Status string`, `DocumentID sql.NullInt64`, `CreatedAt sql.NullTime`, `StartedAt sql.NullTime`, `CompletedAt sql.NullTime`, `Error sql.NullString`
- `User`: `ID int64`, `Username string`, `PasswordHash sql.NullString`, `ApiKey interface{}`, `CreatedAt sql.NullTime`

#### `*.sql.go` (sqlc-generated)

**Key Query Methods:**

- `CreateDocument(ctx context.Context, arg CreateDocumentParams) (sql.Result, error)`
  - **Params**: `ctx` (context), `arg` (document parameters)
  - **Returns**: `sql.Result` (database result with LastInsertId), `error` (creation error)
- `GetDocument(ctx context.Context, id int64) (Document, error)`
  - **Params**: `ctx` (context), `id` (document ID)
  - **Returns**: `Document` (document), `error` (query error)
- `ListDocuments(ctx context.Context, arg ListDocumentsParams) ([]Document, error)`
  - **Params**: `ctx` (context), `arg` (list parameters)
  - **Returns**: `[]Document` (documents), `error` (query error)
- `UpdateDocumentPaths(ctx context.Context, arg UpdateDocumentPathsParams) error`
  - **Params**: `ctx` (context), `arg` (update parameters)
  - **Returns**: `error` (update error)
  - **Description**: Updates both original_path and storage_path for a document in a single operation.
- `GetDocumentByMD5Checksum(ctx context.Context, md5Checksum string) ([]Document, error)`
  - **Params**: `ctx` (context), `md5Checksum` (MD5 checksum)
  - **Returns**: `[]Document` (matching documents), `error` (query error)
- `GetDocumentBySHA512Checksum(ctx context.Context, sha512Checksum string) (Document, error)`
  - **Params**: `ctx` (context), `sha512Checksum` (SHA512 checksum)
  - **Returns**: `Document` (matching document), `error` (query error)

#### `db.go` (sqlc-generated)

**Interface:**

- `DBTX`
  - **Methods**: `ExecContext()`, `PrepareContext()`, `QueryContext()`, `QueryRowContext()`

**Struct:**

- `Queries`
  - **Fields**: `db DBTX`
  - **Methods**:
    - `New(db DBTX) *Queries`
      - **Params**: `db` (database interface)
      - **Returns**: `*Queries` (new queries instance)
    - `WithTx(tx *sql.Tx) *Queries`
      - **Params**: `tx` (database transaction)
      - **Returns**: `*Queries` (queries with transaction)

### Tools Framework (`internal/tools/`)

#### `runner.go`

**Structs:**

- `Runner`
  - **Fields**: `logger *utils.Logger`, `config *config.ConsumerConfig`
  - **Methods**:
    - `NewRunner(logger *utils.Logger, cfg *config.ConsumerConfig) *Runner`
      - **Params**: `logger` (logger), `cfg` (consumer config)
      - **Returns**: `*Runner` (new runner)
    - `ExtractText(path string) (*TextExtractionResult, error)`
      - **Params**: `path` (file path)
      - **Returns**: `*TextExtractionResult` (extraction result), `error` (extraction error)
    - `OCR(path string) (*OCRResult, error)`
      - **Params**: `path` (file path)
      - **Returns**: `*OCRResult` (OCR result), `error` (OCR error)

- `TextExtractionResult`
  - **Fields**: `Text *string`, `Metadata map[string]interface{}`
- `OCRResult`
  - **Fields**: `Success bool`, `TmpPath *string`, `Confidence *float64`

#### `adapters/ocr/adapter.go`

**Interface:**

- `OCR`
  - **Methods**:
    - `Process(path string) (*string, error)`
      - **Params**: `path` (file path)
      - **Returns**: `*string` (output path), `error` (processing error)
    - `CanHandle(mimeType string) bool`
      - **Params**: `mimeType` (MIME type)
      - **Returns**: `bool` (can handle)
    - `Name() string`
      - **Returns**: `string` (adapter name)

**Function:**

- `NewOCR(logger *utils.Logger, cfg config.ToolConfig) (OCR, error)`
  - **Params**: `logger` (logger), `cfg` (tool config)
  - **Returns**: `OCR` (OCRadapter), `error` (initialization error)

#### `adapters/ocr/ocrmypdf.go`

**Struct:**

- `OcrMyPdf`
  - **Fields**: `logger *utils.Logger`, `config config.ToolConfig`
  - **Methods**:
    - `NewOcrMyPdf(logger *utils.Logger, cfg config.ToolConfig) (*OcrMyPdf, error)`
      - **Params**: `logger` (logger), `cfg` (tool config)
      - **Returns**: `*OcrMyPdf` (new ocrmypdf adapter), `error` (initialization error)
    - `Process(path string) (*string, error)`
      - **Params**: `path` (file path)
      - **Returns**: `*string` (output path), `error` (processing error)
    - `CanHandle(mimeType string) bool`
      - **Params**: `mimeType` (MIME type)
      - **Returns**: `bool` (can handle PDF)
    - `Name() string`
      - **Returns**: `string` ("ocrmypdf")

#### `adapters/textextractor/adapter.go`

**Interface:**

- `TextExtractor`
  - **Methods**:
    - `Extract(path string) (*string, error)`
      - **Params**: `path` (file path)
      - **Returns**: `*string` (extracted text), `error` (extraction error)
    - `CanHandle(mimeType string) bool`
      - **Params**: `mimeType` (MIME type)
      - **Returns**: `bool` (can handle)
    - `Name() string`
      - **Returns**: `string` (adapter name)

**Function:**

- `NewTextExtractor(logger *utils.Logger, cfg config.ToolConfig) (TextExtractor, error)`
  - **Params**: `logger` (logger), `cfg` (tool config)
  - **Returns**: `TextExtractor` (text extractor adapter), `error` (initialization error)

#### `adapters/textextractor/pdftotext.go`

**Struct:**

- `PDFToText`
  - **Fields**: `logger *utils.Logger`, `config config.ToolConfig`
  - **Methods**:
    - `NewPDFToText(logger *utils.Logger, cfg config.ToolConfig) (*PDFToText, error)`
      - **Params**: `logger` (logger), `cfg` (tool config)
      - **Returns**: `*PDFToText` (new pdftotext adapter), `error` (initialization error)
    - `Extract(path string) (*string, error)`
      - **Params**: `path` (file path)
      - **Returns**: `*string` (extracted text), `error` (extraction error)
    - `CanHandle(mimeType string) bool`
      - **Params**: `mimeType` (MIME type)
      - **Returns**: `bool` (can handle PDF)
    - `Name() string`
      - **Returns**: `string` ("pdftotext")

### Utilities (`internal/utils/`)

#### `logger.go`

**Struct:**

- `Logger`
  - **Fields**: `level LogLevel`, `infoLogger *log.Logger`, `errorLogger *log.Logger`, `debugLogger *log.Logger`, `fatalLogger *log.Logger`, `RawBodyLog bool`
  - **Methods**:
    - `NewLogger(level string) *Logger`
      - **Params**: `level` (log level string)
      - **Returns**: `*Logger` (new logger)
    - `NewDiscardLogger() *Logger`
      - **Returns**: `*Logger` (discard logger)
    - `Info(reqID *string, format string, v ...any)`
      - **Params**: `reqID` (optional request ID), `format` (format string), `v` (format arguments)
      - **Returns**: `void`
    - `Error(reqID *string, format string, v ...any)`
      - **Params**: `reqID` (optional request ID), `format` (format string), `v` (format arguments)
      - **Returns**: `void`
    - `Debug(reqID *string, format string, v ...any)`
      - **Params**: `reqID` (optional request ID), `format` (format string), `v` (format arguments)
      - **Returns**: `void`
    - `Fatal(v ...any)`
      - **Params**: `v` (arguments)
      - **Returns**: `void` (exits program)

**Constants:**

- `LogLevel`: `LevelDebug`, `LevelInfo`, `LevelError`

#### `parambag.go`

**Struct:**

- `ParamBag`
  - **Fields**: `queryValues map[string]string`, `pathValues map[string]string`
  - **Methods**:
    - `NewParamBag(r *http.Request) *ParamBag`
      - **Params**: `r` (HTTP request)
      - **Returns**: `*ParamBag` (new parameter bag)
    - `Get(key, defaultValue string) string`
      - **Params**: `key` (parameter key), `defaultValue` (default value)
      - **Returns**: `string` (parameter value)
    - `GetInt(key string, defaultValue, min, max int) int`
      - **Params**: `key` (parameter key), `defaultValue` (default), `min` (minimum), `max` (maximum)
      - **Returns**: `int` (parsed integer)
    - `GetInt64(key string, defaultValue, min, max int64) int64`
      - **Params**: `key` (parameter key), `defaultValue` (default), `min` (minimum), `max` (maximum)
      - **Returns**: `int64` (parsed int64)
    - `GetBool(key string, defaultValue bool) bool`
      - **Params**: `key` (parameter key), `defaultValue` (default)
      - **Returns**: `bool` (parsed boolean)
    - `GetStrings(key string) []string`
      - **Params**: `key` (parameter key)
      - **Returns**: `[]string` (string slice)
    - `SetPathValue(key, value string)`
      - **Params**: `key` (path key), `value` (path value)
      - **Returns**: `void`

**Functions:**

- `WithParamBag(r *http.Request, pb *ParamBag) *http.Request`
  - **Params**: `r` (HTTP request), `pb` (parameter bag)
  - **Returns**: `*http.Request` (request with context)
- `GetParamBag(r *http.Request) *ParamBag`
  - **Params**: `r` (HTTP request)
  - **Returns**: `*ParamBag` (parameter bag from context)

## Data Models

### Document (Primary Entity)

```go
type Document struct {
    ID             int64          // Primary key
    Title          string         // Document title (filename)
    Md5Checksum    string         // MD5 checksum (fast lookup)
    Sha512Checksum string         // SHA512 checksum (secure, unique)
    MimeType       string         // MIME type
    FileSize       int64          // File size in bytes
    CreatedAt      sql.NullTime   // Creation timestamp
    ModifiedAt     sql.NullTime   // Last modification timestamp
    DocumentTypeID sql.NullInt64  // Optional document type reference
    OriginalPath   string         // Path to original file
    StoragePath    string         // Path to processed/stored file
    TextContent    sql.NullString // Extracted text content (NEW)
}
```

### File Processing Model

```go
type File struct {
    Name                 string     // Original filename
    OriginalPath         string     // Full path to original file
    OCRTmpPath           *string    // Path to OCR-processed temporary file
    StorageProcessedPath *string    // Final storage path for processed file
    StorageOriginalPath  *string    // Storage path for original file
    MD5Checksum          string     // MD5 checksum (fast lookup)
    SHA512Checksum       string     // SHA512 checksum (secure verification)
    Text                 *string    // Extracted text content
    MimeType             string     // MIME type
    Date                 time.Time  // Processing date for temporal organization
    FileSize             int64      // File size in bytes
}
```

### Supporting Entities

- **Author**: Document authors (many-to-many via `document_author`)
- **Tag**: Document tags (many-to-many via `document_tag`)
- **DocumentType**: Document categorization (invoice, contract, book, etc.)
- **Task**: Processing task tracking for async operations
- **User**: User accounts (for future authentication)

## Processing Pipeline

### 1. File Discovery (`GetFiles()` in `storage.go`)

- Scans configured `consumption_dir` for files with supported extensions
- Detects MIME type using `mimetype` library
- Calculates MD5 and SHA512 checksums in single pass
- Filters by supported file types
- Returns `File` structs with metadata

### 2. Duplicate Detection (`isDuplicate()` in `consumer.go`)

- Computes MD5 checksum for fast filtering
- Queries database for MD5 matches
- If MD5 matches found, computes SHA512 for secure verification
- Queries database for SHA512 exact match
- Skips processing if exact duplicate found

### 3. Text Extraction (`extractText()` in `consumer.go`)

```go
// Primary extraction attempt
extractResult, err := runner.ExtractText(file.OriginalPath)
if extractResult.Text != nil && *extractResult.Text != "" {
    file.Text = extractResult.Text
    return file, nil  // Success - text found
}

// OCR fallback
ocrResult, err := runner.OCR(file.OriginalPath)
extractResult, err = runner.ExtractText(*ocrResult.TmpPath)
file.Text = extractResult.Text
file.OCRTmpPath = ocrResult.TmpPath
```

### 4. Database Integration (`Process()` in `consumer.go`)

- Creates document record via `CreateDocument` query (returns `sql.Result`)
- Gets inserted document ID via `LastInsertId()`
- Generates date-based storage paths: `storage/originals/{year}/{month}/{day}/{documentID}.pdf` and `storage/{year}/{month}/{day}/{documentID}.pdf`
- Updates document with final storage paths using `UpdateDocumentPaths`
- Uses database transactions with rollback support

### 5. File Movement and Copy Operations

- **OCR Case**: Moves OCR temp file to processed storage, copies original to original storage
- **No OCR Case**: Copies original file to both original and processed storage
- **Transaction Safety**: File operations are rolled back if database transaction fails
- **Cleanup**: Original file removed if `delete_original` configuration is enabled

### 6. Transaction Management

- Uses `BeginTx()` for database transaction
- Automatic rollback on error via defer function
- File operations coordinated with transaction commit/rollback
- Cleanup of partially moved files on failure

### 7. FTS Indexing (Automatic via Triggers)

- **INSERT Trigger**: Automatically adds new document text to `document_fts`
- **UPDATE Trigger**: Updates FTS index when document changes
- **DELETE Trigger**: Removes document from FTS index
- **Multi-language**: `unicode61` tokenizer supports all Unicode scripts
- **Real-time**: Index updates happen immediately via database triggers

## Storage Organization

### Directory Structure

```
storage/
├── originals/                    # Original files (copied)
│   ├── 2024/
│   │   ├── 03/
│   │   │   ├── 19/
│   │   │   │   ├── 1.pdf
│   │   │   │   ├── 2.pdf
│   │   │   │   └── ...
│   │   │   └── 20/
│   │   │       └── ...
│   │   └── 04/
│   │       └── ...
│   └── 2025/
│       └── ...
└── 2024/                        # Processed files (OCR'd if needed)
    ├── 03/
    │   ├── 19/
    │   │   ├── 1.pdf
    │   │   ├── 2.pdf
    │   │   └── ...
    │   └── 20/
    │       └── ...
    └── 04/
        └── ...
```

### Key Features

- **Date-based organization**: Files organized by year/month/day for scalability
- **Document ID filenames**: All files use document ID as filename (e.g., `1.pdf`)
- **Dual storage**: Original files preserved, processed files stored separately
- **Scalable**: Avoids "too many files in one directory" problem

## API Reference

### Health Check

```
GET /health
Response: { "status": "healthy", "version": "0.1.0", "time": "2024-03-19T10:30:00Z" }
```

### List Documents

```
GET /api/v1/documents?limit=50&offset=0
Query Parameters:
  limit:  Number of documents to return (1-100, default: 50)
  offset: Pagination offset (default: 0)

Response: Array of DocumentResponse
```

### Get Document

```
GET /api/v1/documents/{id}
Path Parameters:
  id: Document ID (integer)

Response: Single DocumentResponse
```

### DocumentResponse Structure

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
  "source_path": "/storage/2024/03/19/pdf/1.pdf"
}
```

### Search Documents (FTS)

```
GET /api/v1/documents/search?q=search+terms&limit=50&offset=0
Query Parameters:
  q:      Search query (supports FTS5 operators: AND, OR, NOT, "phrase")
  limit:  Number of results (1-100, default: 50)
  offset: Pagination offset (default: 0)

Response: Array of FTSDocumentResponse
```

### FTSDocumentResponse Structure

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
  "source_path": "/storage/2024/03/19/pdf/1.pdf",
  "text_content": "Extracted document text...",
  "rank": 0.85,
  "snippet": "...showing <b>search</b> terms in context..."
}
```

## CLI Commands

### Version

```bash
kushim version
# Output: Document Management System v0.1.0
```

### Consume Documents

```bash
kushim consume
# Scans consumption_dir, processes PDFs
# Checks for duplicates using MD5/SHA512 checksums
# Extracts text, runs OCR if needed
# Moves files to storage_dir with date-based organization
# Creates database records for processed documents
# Optionally deletes original files based on configuration
```

## Database Schema

### Core Tables

- `document`: Main document storage with metadata
  - `md5_checksum`: MD5 checksum for fast duplicate detection
  - `sha512_checksum`: SHA512 checksum for secure uniqueness (UNIQUE constraint)
- `author`: Document authors
- `tag`: Document tags
- `document_type`: Document categorization
- `task`: Processing task tracking
- `user`: User accounts (future use)

### Junction Tables

- `document_author`: Many-to-many document-author relationships
- `document_tag`: Many-to-many document-tag relationships

### FTS5 Virtual Tables

- `document_fts`: Full-text search virtual table
  - `document_id UNINDEXED`: Reference to main document table
  - `title`: Searchable document title
  - `content`: Searchable extracted text content
  - `tokenize = 'unicode61'`: Multi-language tokenizer

### Triggers for Automatic Synchronization

- `document_ai`: Inserts new documents into FTS table
- `document_au`: Updates FTS table on document changes
- `document_ad`: Removes documents from FTS table on deletion

### Key Indexes

- `idx_document_md5`: Fast MD5 checksum lookups
- `idx_document_sha512`: SHA512 checksum lookups (also UNIQUE constraint)
- `idx_document_created`: Date-based queries
- `idx_task_status`: Task monitoring
- Foreign key indexes for junction tables

## Configuration Reference

### Default Configuration (`config.example.yaml`)

```yaml
app:
  environment: development # development|production
  log_level: debug # debug|info|error

server:
  host: localhost # Server hostname
  port: 3000 # Server port
  read_timeout: 60 # Request read timeout (seconds)
  write_timeout: 60 # Response write timeout (seconds)
  idle_timeout: 60 # Connection idle timeout (seconds)

database:
  type: sqlite # Database type
  path: ./data # Database directory

storage:
  consumption_dir: ./inbox # Directory to scan for new documents
  storage_dir: ./storage # Directory for processed documents

consumer:
  supported_files: ['.pdf'] # File extensions to process
  textextractor: 'pdftotext' # Text extraction command
  ocr: 'ocrmypdf' # OCR command
  delete_original: false # Whether to delete original files after processing
```

## Development Workflow

### Setup

```bash
# Install dependencies
go mod download

# Install external tools
brew install poppler    # pdftotext
brew install ocrmypdf   # ocrmypdf (requires tesseract)

# Create config file
cp config.example.yaml config.yaml

# Create directories
mkdir -p inbox storage data
```

### Database Management

```bash
# Generate sqlc code (after schema changes)
sqlc generate

# Database auto-created on first run with schema
```

### Running

```bash
# Start HTTP server
go run cmd/cli/main.go server

# Process documents
go run cmd/cli/main.go consume

# Test API
curl http://localhost:3000/health
curl http://localhost:3000/api/v1/documents
```

## Known Limitations

### FTS5 Implementation

- **sqlc incompatibility**: FTS5 queries implemented manually due to sqlc limitations
- **CJK languages**: Limited tokenization for Chinese/Japanese/Korean (no word segmentation)
- **No stemming**: Using `unicode61` alone for universal support (no language-specific stemming)

### Performance Considerations

- **FTS index size**: Text content duplicated in both `document` and `document_fts` tables
- **Trigger overhead**: Three triggers per document operation
- **Memory usage**: FTS5 indexes maintained in memory for fast search

---

**Last Updated**: 2024-03-28

**Current Version**: 0.1.0 (Development)

**Focus Area**: Full-text search implementation with multi-language support and FTS5 integration

**Next Milestone**: FTS search API endpoints and performance testing

_This document serves as the primary reference for the Document Management System architecture and implementation details._

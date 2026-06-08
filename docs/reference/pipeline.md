# Consumption Engine (`internal/consumption/`)

## `consumer.go`

### Structs

- `Consumer`
  - **Fields**: `config *config.Config`, `logger *utils.Logger`, `db *sql.DB`, `runner *tools.Runner`
  - **Methods**:
    - `NewConsumer(cfg, logger, db) (*Consumer, error)` — Validates `PdfOptimizer.Fallback` at startup
    - `NewConsumerWithRunner(cfg, logger, db, runner) (*Consumer, error)` — DI variant
    - `Process(ctx, file File) (File, error)` — Extract → OCR fallback → optimize → store (creates document with PageCount, WordCount, CharCount)
    - `extractText(ctx, file File) (File, error)` — Uses `minTextDensityRatio` (0.001) to decide OCR vs text
    - `isDuplicate(ctx, path) (bool, error)` — MD5 → SHA512 two-step duplicate check

- `File`
  - **Fields**: `Name`, `OriginalPath`, `OCRTmpPath *string`, `OptimizedPdfTmpPath *string`, `StorageProcessedPath *string`, `StorageOriginalPath *string`, `DocumentID sql.NullInt64`, `MD5Checksum`, `SHA512Checksum`, `Text sql.NullString`, `MimeType`, `Date time.Time`, `FileSize int64`, `PageCount int`

### Functions

`calculateMD5`, `calculateSHA512`, `countPages(path) int` — Uses MuPDF to count PDF pages

---

## `storage.go`

### Functions

`GetFiles(src, exts) ([]File, error)`, `FileFromPath(path) (File, error)`, `RemoveFile`, `MoveFile`, `CopyFile`, `CleanUp`, `moveFileCrossDevice`, `calculateChecksums`, `FilePaths`

- `GetFiles` uses `gabriel-vasile/mimetype` for MIME detection, filters by extension
- `FileFromPath` builds a `File` from a single path with checksums, MIME detection, file info

---

# Enrichment Engine (`internal/enrichment/`)

## `enricher.go`

### Struct

- `Enricher` — `config`, `logger`, `db`, `runner`, `cache *cache.Cache`
  - **Methods**:
    - `NewEnricher(cfg, logger, db, embeddingCache *cache.Cache) (*Enricher, error)` — Creates runner with textreducer, contentanalyzer, tagmatcher
    - `Enrich(ctx, document) (*json.RawMessage, error)` — Full pipeline:
      1. Dual text reduction: LLM-targeted and tag-matching-targeted (via `targetWordCount`)
      2. Fetch doc types, people types, all tags from DB
      3. Retrieve tag embeddings from cache; on nil/missing store or wrong type, logs errors but continues
      4. Semantic tag matching against cached tag embeddings (falls back to all tags on failure)
      5. LLM content analysis (title, doc type, tags, people, language)
      6. Post-LLM tag consolidation via `MatchEach` (creates new tags if needed)
      7. Update document metadata (title, doc_type, language)
      8. Manage document_tag junction (clear + add, create new tags as needed)
      9. Manage document_people junction (clear + add, create new people as needed; unknown types default to `"unknown"`)
    - `GetDb() *sql.DB`

### Helpers

- `targetWordCount(contentWC, targetWC int) int` — Returns `max(2000, targetWC)` or `max(2000, contentWC / -targetWC)` if targetWC is negative

---

# Search Engine (`internal/search/`)

## `search.go`

### Struct

- `Engine` — `logger`, `queries *database.Queries`
  - **Methods**: `NewEngine(logger, db) *Engine`, `Search(ctx, query, limit, offset) ([]Result, error)`

- `Result`
  - **Fields**: `DocumentID`, `Title`, `MD5Checksum`, `SHA512Checksum`, `MimeType`, `FileSize`, `Language`, `DocumentTypeID`, `CreatedAt`, `ModifiedAt`, `OriginalPath`, `StoragePath`, `Snippet`, `Rank`

### Function

- `sanitizeQuery(q string) string` — Wraps query in double quotes for FTS5 (escapes internal quotes)

---

## See Also

- [Tools](tools.md) — Adapter framework (TextReducer, OCR, TextExtractor, PdfOptimizer)
- [Database](database.md) — FTS5 queries, document storage
- [Config & Utils](config-and-utils.md) — Consumer and Enricher configuration
- [Task System](task-system.md) — Task handlers that invoke consumer/enricher

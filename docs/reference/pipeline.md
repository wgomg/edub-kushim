# Consumption Engine (`internal/consumption/`)

## `consumer.go`

### Structs

- `Consumer`
  - **Fields**: `config *config.Config`, `logger *utils.Logger`, `db *sql.DB`, `runner *tools.Runner`
  - **Methods**:
    - `NewConsumer(cfg, logger, db) (*Consumer, error)` — Validates `PdfOptimizer.Fallback` at startup
    - `NewConsumerWithRunner(cfg, logger, db, runner) (*Consumer, error)` — DI variant
    - `Process(ctx, file File, documentID string) (File, error)` — Extract → OCR fallback → optimize → store (creates document with PageCount, WordCount, CharCount)
    - `extractText(ctx, file File, documentID string) (File, error)` — Uses `minTextDensityRatio` (0.001) to decide OCR vs text
    - `isDuplicate(ctx, path) (bool, error)` — MD5 → SHA512 two-step duplicate check

- `File`
  - **Fields**: `Name`, `OriginalPath`, `OCRTmpPath *string`, `OptimizedPdfTmpPath *string`, `StorageProcessedPath *string`, `StorageOriginalPath *string`, `DocumentID string` (UUID), `DocumentDbId sql.NullInt64` (DB auto-increment ID), `MD5Checksum`, `SHA512Checksum`, `Text sql.NullString`, `MimeType`, `Date time.Time`, `FileSize int64`, `PageCount int`

### Functions

`calculateSHA512`, `countPages(path) int` — Uses MuPDF to count PDF pages

`MoveFailedFile(storageDir, originalPath, errType, logger, docID)` — Moves a failed file from the inbox to `storage/errors/<uuid>-<filename>.pdf`. If `errType` is `"duplicate"`, moves to `storage/errors/duplicated/`. Creates destination directories as needed. Handles non-existent source files gracefully (logs error, no-op).

`QuarantineFailedFiles(ctx, queries, storageDir, logger, batchID) error` — For a batch with recently-quarantined consume tasks (status=`failed`, error `LIKE 'Max retries exceeded%'`), moves each task's inbox file to `storage/errors/` via `MoveFailedFile` and discards orphaned waiting enrich tasks via `DiscardEnrichTaskByTaskID`. Called from `reclaimStaleBatches` (queue daemon) and `consumeHandler` (CLI resume path). Returns the first error encountered (continues processing remaining tasks on error).

- MD5 checksum computation is in `utils.CalculateMD5` — see [Config & Utils](config-and-utils.md)

---

## `storage.go`

### Functions

`FileFromPath(path) (File, error)`, `RemoveFile`, `MoveFile`, `CopyFile`, `CleanUp`, `moveFileCrossDevice`, `calculateChecksums`

- `FileFromPath` builds a `File` from a single path with checksums, MIME detection, file info

> **Note**: The file-scanning functionality was consolidated into `utils.ListFilePaths` (`internal/utils/files.go`) for use by the consume handler, replacing the former `internal/fileresolver/` package.

---

## `scan.go`

### Functions

- `ScanAndEnqueue(ctx, cfg, client, logger) (batchID string, enqueued int, err error)` — Shared inbox scan → dedup → batch creation function. Scans the consumption directory via `utils.ListFilePaths`, computes MD5 for each file, batch-deduplicates against existing documents via `queryDuplicatesByMD5`, and creates consume+enrich task pairs. The batch record is created **after** all tasks are committed (with status `queued`) to prevent the queue consumer from picking up an incomplete or empty batch. Returns an empty batch ID when no files or all duplicates are found.
- `queryDuplicatesByMD5(ctx, client, hashes) (map[string]string, error)` — Single SQL query (`SELECT md5_checksum, document_id FROM document WHERE md5_checksum IN (...)`) for all MD5 hashes, returns a map for O(1) lookups in the dedup loop.

---

# Enrichment Engine (`internal/enrichment/`)

## `enricher.go`

### Struct

- `Enricher` — `config`, `logger`, `db`, `runner`, `services *types.CrudServices`
  - **Methods**:
    - `NewEnricher(cfg, logger, db, services, matcher tagmatcher.Matcher) (*Enricher, error)` — Creates runner via `NewRunnerWithMatcher` with textreducer, contentanalyzer, tagmatcher tools; matcher is either a direct `*Hugot` (in `kushim` CLI) or a `*tagmatch.MatcherClient` (in `edub` API server) — both satisfy the same `tagmatcher.Matcher` interface.
    - `Enrich(ctx, document) (*json.RawMessage, error)` — Full pipeline:
      1. Dual text reduction: LLM-targeted and tag-matching-targeted (via `targetWordCount`)
      2. Fetch doc types, people types from DB; all tags via `services.Tag.ListAll`
      3. Semantic tag matching: passes tag names to `Runner.MatchTags`. In `kushim`, embeddings are resolved via the local Hugot store (cache-miss encoded on the fly). In `edub`, the `MatcherClient` forwards requests over the Unix socket to the external matcher process. Falls back to all tags on failure.
       4. LLM content analysis (title, doc type, tags, people, language). If the result is entirely empty (all fields blank/default), enrichment retries once; if still empty, the task is marked `failed` for visibility and manual retry.
        5. **Tag normalization** — LLM-extracted tags are run through `NormalizeTags` (accent-folds accented characters to ASCII base letters, hyphens/underscores→spaces, strips non-alpha, collapses whitespace, deduplicates)
        6. **Tag filtering** — `FilterTags` drops deterministic garbage tags: tags with >3 tokens, tags that share any token with a person's name (from the current document), multi-word tags (≥2 tokens) whose tokens form a strict subset of any known person in the full `people` table, and tags whose token set is a subset of the title's token set. Caps survivors at `maxTags` (5).
       7. Post-LLM tag consolidation via `services.Tag.Consolidate` (delegates to `Embedder.Consolidate` over the matcher interface)
       8. New tags created via a single batch call `services.Tag.Create(ctx, analysis.Tags)` — returns per-index results with `Created`/`Conflict`/`Invalid` statuses. The service delegates store management to the matcher via `AddToStore`.
        9. Update document metadata — title is first rune-capped at 127 characters via `Truncate` to enforce the server-side limit (independent of LLM prompt compliance), then persisted alongside doc_type and language
        10. Auto-detect OCR language: `ensureOCRLanguage` cross-checks LLM-detected language against configured OCR languages. If missing and the engine is gosseract, the tessdata download runs first (inside a background goroutine). The language is only appended to the in-memory config list and persisted to `config.yaml` on download success. A failed download leaves config untouched, so the next document detecting the same language retries the download naturally. A `sync.Mutex` protects the shared `Languages` slice against concurrent mutations from multiple enrich workers. For non-gosseract engines, the language is appended and persisted immediately (no tessdata needed).
       11. Manage document_tag junction (clear + add)
       12. Manage document_people junction (clear + add) — for each person:
          - Determines canonical name via `canonicalPersonName`: uses LLM's `name_romanized` if provided (for non-Latin names), falls back to AnyAscii transliteration
          - Normalizes the canonical name via `utils.NormalizeForDB` (NFKC, lowercase, accent-folding, alpha-only) for normalized-name dedup lookup
          - Creates new people with `name` (canonical) + `name_native` (original non-Latin script) + `normalized_name` (computed from canonical) when no match found
          - Updates `name_native` on existing people if currently empty
           - Unknown people types default to `"unknown"`

### Helpers

- `targetWordCount(contentWC, targetWC int) int` — Returns `max(2000, targetWC)` or `max(2000, contentWC / -targetWC)` if targetWC is negative

---

# Search Engine (`internal/search/`)

## `search.go`

### Struct

- `Engine` — `logger`, `queries *database.Queries`
  - **Methods**: `NewEngine(logger, queries) *Engine`, `Search(ctx, query, limit, offset) ([]Result, error)`

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

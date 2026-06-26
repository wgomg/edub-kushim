# Database Layer (`internal/database/`)

## `connection.go`

### Functions

`NewSQLiteDB(cfg) (*sql.DB, error)`, `NewClient(db) *Client`

- `NewSQLiteDB` sets `PRAGMA foreign_keys = ON`, `journal_mode = WAL`, `synchronous = NORMAL`, max 1 connection
- `NewClient` wraps a `*sql.DB` and an embedded `*Queries` (sqlc-generated). Exposes `BeginTx(ctx, opts)` and `DB()` for direct `*sql.DB` access. All query methods on `*Queries` are promoted to `*Client`.

---

## `schema.go`

### Functions

`InitializeSchema(db) error` — Configures goose (`SetBaseFS`, `SetDialect`), runs pending migrations via `goose.Up()` from the embedded `sql/schema/migrations/` directory, then runs seeders: `tags`, `document-types`, `people-types`. On a fresh database, the baseline migration (`00001_baseline.sql`) creates all tables, indexes, triggers, and the FTS5 virtual table. On existing databases, only unapplied migrations are executed.

`ResetDatabase(db) error` — Rolls back all migrations via `goose.Reset()` (down → up), then re-runs seeders. Used by `kushim setup --reset-database`.

### Migration files

Numbered SQL files in `internal/database/sql/schema/migrations/`. Each file uses goose annotations:

- `-- +goose Up` — applied when migrating forward
- `-- +goose Down` — applied when rolling back
- `-- +goose StatementBegin` / `-- +goose StatementEnd` — wraps multi-statement SQL (triggers, functions) so goose's semicolon-based parser doesn't split them prematurely

### sqlc integration

sqlc is configured in `sqlc.yaml` to read its schema from the same `migrations/` directory, not from a separate `schema.sql` file:

```yaml
schema: 'internal/database/sql/schema/migrations'
```

sqlc natively understands goose annotations — it recognises `-- +goose Up`/`-- +goose Down` boundaries and ignores down migrations when building its schema model. This ensures the generated Go code always matches the actual database schema without duplication.

When adding a new migration:

1. Write `00002_description.sql` in `migrations/` with `-- +goose Up` / `-- +goose Down` sections
2. Run `sqlc generate` — sqlc picks up the new file from the same directory
3. No separate `schema.sql` update is needed

### Migration auto-apply

Migrations run automatically on startup (no manual CLI command needed):

- **Server** (`edub`) — called in `cmd/edub/main.go` after `NewSQLiteDB()`
- **CLI commands** (`kushim consume`, `search`, `task`) — called in `internal/commands/container.go` `GetDB()` when the connection is first acquired

---

## `models.go` (sqlc-generated)

### Key structs

- `Document` — 17 fields: `ID`, `DocumentID` (UUID string), `Title`, `Md5Checksum`, `Sha512Checksum`, `MimeType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `CreatedAt`, `ModifiedAt`, `DocumentTypeID`, `OriginalPath`, `StoragePath`, `TextContent`
- `DocumentFt` — `Title`, `Content`, `DocumentID`
- `Task` — 12 fields: `ID`, `TaskID`, `TaskType`, `Status`, `BatchID sql.NullString`, `Payload json.RawMessage`, `Result *json.RawMessage`, `DedupKey sql.NullString`, `CreatedAt`, `StartedAt`, `CompletedAt`, `Error`
- `Tag` — `ID`, `Name`, `CreatedAt`
- `DocumentType` — `ID`, `Name`, `Description`, `CreatedAt`
- `DocumentTag` — `DocumentID`, `TagID`
- `DocumentPeople` — `DocumentID`, `PeopleID`, `PeopleTypeID`
- `People` — `ID`, `Name`, `NameNative sql.NullString`, `CreatedAt`
- `PeopleType` — `ID`, `Name`, `Description`, `CreatedAt`
- `User` — `ID`, `Username`, `PasswordHash sql.NullString`, `ApiKey sql.NullString`, `CreatedAt`
- `SavedSearch` — `ID`, `Name`, `FilterJson string`, `CreatedAt string`

---

## `db.go`

- `DBTX` interface
- `Queries` struct with `New(db DBTX)`, `WithTx(tx *sql.Tx) *Queries`

---

## `document_sort.go`

### Struct

`ListDocumentsWithSortParams` — `Limit`, `Offset`, `SortBy`, `SortOrder`

### Function

`ListDocumentsWithSort(ctx, params)` — Dynamic ORDER BY with whitelisted columns (`title`, `mime_type`, `file_size`, `created_at`); now selects `document_id` (UUID) in addition to the numeric `id`

---

## Query methods (sqlc-generated)

### Document

`CreateDocument` (with `DocumentID` UUID, WordCount, CharCount, Language, PageCount), `GetDocument` (by `document_id`), `GetDocumentById`, `ListDocuments`, `UpdateDocumentPaths` (by `document_id`), `UpdateDocumentPathsById`, `UpdateDocumentMetadata` (by `document_id`), `UpdateDocumentMetadataById`, `GetDocumentByMD5Checksum`, `GetDocumentBySHA512Checksum`, `GetDocumentWithDetails` (by `document_id`), `GetDocumentWithDetailsById`, `GetDocumentWithText` (by `document_id`), `GetDocumentWithTextById`, `SearchDocumentsByTitle`, `SumDocumentFileSizes`, `DeleteDocument` (by `document_id`), `DeleteDocumentById`

### Tag

`CreateTag` (`INSERT OR IGNORE`, `:execresult`), `GetTag`, `GetTagByName`, `ListTags`, `ListAllTags`, `ListAllTagsNames`, `SearchTagsByName` (prefix search with `LIKE ?` + `LIMIT`), `UpdateTag`, `DeleteTag`

### Document tag

`AddDocumentTag`, `RemoveDocumentTag`, `GetDocumentTags`, `ClearDocumentTags`, `GetTagDocuments`

### Document type

`CreateDocumentType` (name only), `CreateDocumentTypeFull` (name + description), `GetDocumentType`, `GetDocumentTypeByName`, `ListDocumentTypes`, `ListAllDocumentTypes`, `ListAllDocumentTypesNames`, `SearchDocumentTypeByName` (prefix search with `LIKE ?` + `LIMIT`), `UpdateDocumentType` (name only), `UpdateDocumentTypeFull` (name + description), `DeleteDocumentType`

### People

`CreatePeople` (`INSERT OR IGNORE` with `Name` + `NameNative`), `GetPeople`, `GetPeopleByName`, `ListPeople`, `ListAllPeople`, `SearchPeopleByName` (prefix search with `LIKE ?` + `LIMIT`), `UpdatePeople` (name only), `UpdatePeopleFull` (name + name_native), `UpdatePeopleNative` (fills `name_native` only if currently NULL), `DeletePeople`

### People type

`CreatePeopleType`, `GetPeopleType`, `GetPeopleTypeByName`, `ListPeopleTypes`, `ListAllPeopleTypes`, `ListAllPeopleTypesNames`, `SearchPeopleTypeByName` (prefix search with `LIKE ?` + `LIMIT`), `UpdatePeopleType`, `DeletePeopleType`

### Document people

`AddDocumentPeople`, `RemoveDocumentPeople`, `ClearDocumentPeople`, `GetDocumentPeople`, `GetDocumentPeopleWithType`, `GetPeopleDocuments`

### Task

`CreateTask`, `GetTask`, `GetTaskByTaskID`, `GetTaskByBatchID`, `ListTasks`, `ListAllTasks`, `ListTasksByBatch`, `ListAllTasksByBatch`, `ListTasksByBatchAndStatus`, `ListAllTasksByBatchAndStatus`, `ListTasksByBatchAndStatusAndType`, `ListAllTasksByBatchAndStatusAndType`, `ListTasksByBatchAndType`, `ListAllTasksByBatchAndType`, `ListTasksByStatus`, `ListAllTasksByStatus`, `ListTasksByStatusAndType`, `ListAllTasksByStatusAndType`, `ListTasksByType`, `ListAllTasksByType`, `CountTasksByBatchAndStatus`, `GetNextPendingTask`, `GetNextPendingTaskOfType`, `ClaimTask`, `CompleteTask`, `FailTask`, `RetryTask`, `DeleteTask`, `CancelPendingTasksByBatch`, `CancelProcessingTasksByBatch`, `SetEnrichTaskPending`, `DiscardEnrichTask`, `ListDistinctBatchIDs`, `ListDistinctBatchIDsByStatus`, `CountDistinctBatches`, `CountAllTasks`, `CountTasksByStatus`

### User

`CreateUser`, `GetUser`, `GetUserByUsername`, `GetUserByAPIKey`, `ListUsers`, `UpdateUser`, `UpdateUserPassword`, `DeleteUser`

### Saved search

`CreateSavedSearch`, `ListSavedSearches`, `DeleteSavedSearch`

---

## `fts5.go`

### Struct

`FTSDocumentRow` — `ID`, `DocumentID` (UUID string), `Title`, `Md5Checksum`, `Sha512Checksum`, `MimeType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `CreatedAt`, `ModifiedAt`, `DocumentTypeID`, `OriginalPath`, `StoragePath`, `TextContent`, `Rank`, `Snippet`

### Functions

`SearchDocumentsFTS`, `SearchDocumentsFTSWithFilters`, `GetDocumentFTSContent`, `UpdateDocumentFTS`, `DeleteDocumentFTS`, `RebuildDocumentFTS`

---

## `structured_search.go`

### Struct

`SearchFilter` — `Query`, `Tags []string`, `People []struct{ Name, Type string }`, `DocumentType`, `Language`, `MimeType`, `DateCreated *struct{ From, To *string }`, `DateModified *struct{ From, To *string }`, `FileSize *struct{ Min, Max *int64 }`, `SortBy`, `SortOrder`, `Limit`, `Offset`

### Internal: `queryBuilder`

A flexible SQL query builder that composes `WHERE` clauses dynamically:

- `add(clause, args...)` — Appends raw clause with positional parameters
- `eq(col, val)` — Adds `AND d.col = ?` if val is non-empty
- `subqueryIn(col, subquery, values)` — Adds `AND d.col IN (SELECT ... WHERE t.name IN (?,?...))`
- `rangeClause(col, min, max)` — Adds `AND d.col >= ?` / `AND d.col <= ?`
- `dateRange(col, range)` — Adds date range filters with optional from/to

### Functions

- `SearchDocumentsStructured(ctx, filter) ([]FTSDocumentRow, error)` — Dynamically builds a SELECT query:
  - If `query` is non-empty: joins `document_fts`, adds `MATCH ?`, `bm25()` rank, `snippet()` highlighting
  - Applies tag subquery, people subqueries, document type subquery, language/MIME equality, date ranges, file size ranges
  - When FTS query present: ordered by `rank`; otherwise ordered by requested `sort_by`/`sort_order`
  - Uses `LIMIT ? OFFSET ?` for pagination
- `CountDocumentsStructured(ctx, filter) (int64, error)` — Same filters but `SELECT COUNT(*)` for total count

---

# Database Schema

## Core Tables

- `document` — Main storage: `document_id` (UUID, UNIQUE), `md5_checksum`, `sha512_checksum` (UNIQUE), `page_count`, `word_count`, `char_count`, `language` (all int64/text defaults), `text_content`, file paths
- `saved_search` — Saved search configurations: `id`, `name`, `filter_json` (JSON), `created_at`
- `task` — Async processing: `task_id` (UUID), `batch_id` (nullable), `task_type`, `payload` (JSON), `result` (JSON), `dedup_key` (nullable), `status`, timestamps, `error`
- `tag` — Classification tags (seeded with 110+ Dewey Decimal tags)
- `document_type` — Document type classification (seeded with types like `article`, `book`, `report`, `letter`, etc.)
- `people` — People/entities associated with documents (`name` UNIQUE, `name_native` nullable for original non-Latin script)
- `people_type` — Roles for people (e.g., `author`, `editor`, `translator`, `subject`)
- `user` — Authentication (username, password_hash, api_key)

## Junction Tables

- `document_author` — (document_id, author_id) — legacy (see document_people)
- `document_tag` — (document_id, tag_id)
- `document_people` — (document_id, people_id, people_type_id) — replaces document_author

## FTS5 Virtual Table

```sql
CREATE VIRTUAL TABLE document_fts USING fts5(
    title,
    content,
    document_id UNINDEXED,
    tokenize = 'unicode61'
);
```

## Triggers

- `document_ai` — INSERT: auto-adds to `document_fts`
- `document_au` — UPDATE: syncs FTS index
- `document_ad` — DELETE: removes from FTS index

## Schema Idempotency

All `CREATE TABLE`, `CREATE INDEX`, and `CREATE TRIGGER` statements use `IF NOT EXISTS`
to allow safe re-runs of the baseline migration. Junction table inserts (`document_tag`, `document_people`)
use `INSERT OR IGNORE` instead of plain `INSERT` to avoid duplicate-key errors on
re-enrichment.

New schema changes after the baseline are written as numbered migration files with
`IF NOT EXISTS` not strictly required (goose tracks which versions have been applied),
but using it is still recommended for idempotent re-runs during development.

## Migration Version Table

A `goose_db_version` table (managed by goose) tracks applied migrations with columns: `version_id`, `is_applied`, `tstamp`. Created automatically on first `goose.Up()` call.

## Key Indexes

- `idx_document_md5`, `idx_document_sha512` (UNIQUE), `idx_document_created`
- `idx_task_status`, `idx_task_type`, `idx_task_batch`, `idx_task_batch_status`
- `idx_task_pending` — partial index on `task(created_at)` where `status = 'pending'`
- `idx_task_dedup` — unique partial index on `task(task_type, dedup_key)` where
  status is `pending` or `processing` and `dedup_key IS NOT NULL`. Prevents duplicate
  active tasks for the same (type, key).

## MySQL / MariaDB compatibility notes

The dedup index uses a feature not available in MySQL/MariaDB (`WHERE` on
`CREATE INDEX`). The equivalent when adding MySQL/MariaDB support:

```sql
ALTER TABLE task ADD COLUMN active_token VARCHAR(1) GENERATED ALWAYS AS
  (CASE WHEN status IN ('pending', 'processing') THEN '1' END) VIRTUAL;

CREATE UNIQUE INDEX idx_task_dedup ON task(task_type, dedup_key, active_token);
```

---

## See Also

- [Search](search.md) — Search engine, structured search queries, autocomplete queries
- [API](api.md) — Document and task response types that map to DB models
- [Task System](task-system.md) — Task CRUD operations
- [Pipeline](pipeline.md) — Consumer and enrichment engines that read/write documents

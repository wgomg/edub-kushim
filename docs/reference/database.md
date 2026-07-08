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

1. Write `00005_description.sql` in `migrations/` with `-- +goose Up` / `-- +goose Down` sections
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
- `People` — `ID`, `Name`, `NameNative sql.NullString`, `NormalizedName string`, `CreatedAt`
- `PeopleType` — `ID`, `Name`, `Description`, `CreatedAt`
- `User` — `ID`, `Username`, `PasswordHash sql.NullString`, `ApiKeyHash sql.NullString`, `ApiKeyPrefix sql.NullString`, `ApiKeyCreatedAt sql.NullTime`, `CreatedAt sql.NullTime`, `Role` — roles: `"admin"`, `"editor"`, `"viewer"` (default). Role is validated at the application layer (no CHECK constraint in SQLite ALTER TABLE).
- `SavedSearch` — `ID`, `Name`, `FilterJson string`, `CreatedAt string`
- `Batch` — 4 fields: `ID`, `Source`, `CreatedAt sql.NullTime`, `Status` (queued/processing/completed/failed/cancelled)
- `BatchOwner` — `BatchID`, `OwnerID`, `Pid`, `AcquiredAt`, `LastHeartbeat`
- `OrphanedFile` — `ID`, `DocumentKey`, `DocumentKeyType` (uuid/dbid), `FilePath`, `OriginalPath`, `SourceDir` (originals/processed), `FileSize`, `MimeType`, `DetectedAt`, `Status` (pending/deleted/restored/reingested), `ActionAt`, `ActionType`

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

`CreatePeople` (`INSERT OR IGNORE` with `Name`, `NameNative`, `NormalizedName`), `GetPeople`, `GetPeopleByName`, `ListPeople`, `ListAllPeople`, `SearchPeopleByName` (prefix search with `LIKE ?` + `LIMIT`), `UpdatePeople` (name + normalized_name), `UpdatePeopleFull` (name + name_native + normalized_name), `UpdatePeopleNative` (fills `name_native` only if currently NULL), `DeletePeople`

### People type

`CreatePeopleType`, `GetPeopleType`, `GetPeopleTypeByName`, `ListPeopleTypes`, `ListAllPeopleTypes`, `ListAllPeopleTypesNames`, `SearchPeopleTypeByName` (prefix search with `LIKE ?` + `LIMIT`), `UpdatePeopleType`, `DeletePeopleType`

### Document people

`AddDocumentPeople`, `RemoveDocumentPeople`, `ClearDocumentPeople`, `GetDocumentPeople`, `GetDocumentPeopleWithType`, `GetPeopleDocuments`

### Task

`CreateTask`, `GetTask`, `GetTaskByTaskID`, `GetTaskByBatchID`, `ListTasks`, `ListAllTasks`, `ListTasksByBatch`, `ListAllTasksByBatch`, `ListTasksByBatchAndStatus`, `ListAllTasksByBatchAndStatus`, `ListTasksByBatchAndStatusAndType`, `ListAllTasksByBatchAndStatusAndType`, `ListTasksByBatchAndType`, `ListAllTasksByBatchAndType`, `ListTasksByStatus`, `ListAllTasksByStatus`, `ListTasksByStatusAndType`, `ListAllTasksByStatusAndType`, `ListTasksByType`, `ListAllTasksByType`, `CountTasksByBatchAndStatus`, `GetNextPendingTask`, `GetNextPendingTaskOfType`, `ClaimTask`, `CompleteTask`, `FailTask`, `RetryTask`, `DeleteTask`, `CancelPendingTasksByBatch`, `CancelProcessingTasksByBatch`, `SetEnrichTaskPending`, `DiscardEnrichTask`, `DiscardEnrichTaskByTaskID`, `ListDistinctBatchIDs`, `ListDistinctBatchIDsByStatus`, `CountDistinctBatches`, `CountAllTasks`, `CountTasksByStatus`

### Batch

`CreateBatch`, `GetBatch`, `SetBatchProcessing`, `SetBatchCompleted`, `SetBatchFailed`, `SetBatchCancelled`, `RequeueBatch`, `CountQueuedBatches`, `GetNextQueuedBatch`, `CountLiveBatches`, `ListStaleBatchOwners`, `CleanupCompletedBatches`, `QuarantineProcessingTasksByBatch`, `ResetProcessingTasksByBatch`, `GetQuarantinedConsumeTaskPayloads`, `TryInsertBatchOwner`, `UpdateBatchOwnerIfStale`, `AcquireBatchOwnerForce`, `HeartbeatBatchOwner`, `ReleaseBatchOwner`, `DeleteBatchOwnerByBatchID`, `ListBatchOverviews`

### User

`CreateUser` (with `role` column), `GetUser`, `GetUserByUsername`, `GetUserByAPIKeyHash`, `ListUsers` (returns `ListUsersRow` without `password_hash`, includes `role`), `UpdateUser`, `UpdateUserPassword`, `UpdateUserCredentials` (single `UPDATE` for username + password_hash), `UpdateUserRole` (sets role by user ID), `CountUsers`, `DeleteUser`

### Saved search

`CreateSavedSearch`, `ListSavedSearches`, `DeleteSavedSearch`

### Orphaned file

`CreateOrphanedFile`, `GetOrphanedFile`, `ListOrphanedFiles` (pending only, ordered by detected_at DESC), `MarkOrphanedFileDeleted`, `MarkOrphanedFileRestored`, `MarkOrphanedFileReingested`, `MarkAllOrphanedFilesDeleted` (bulk UPDATE pending→deleted)

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

## `dashboard.go`

### Query methods (raw SQL, not sqlc-generated)

These methods are written manually (no sqlc) and follow a consistent pattern:
`QueryContext` → `defer Close` → scan loop → `Close` → `Err`, with error-wrapped scan errors.

| Method | SQL | Returns |
|--------|-----|---------|
| `MimeTypeBreakdown` | `SELECT mime_type, COUNT(*), SUM(file_size) FROM document GROUP BY mime_type ORDER BY total_bytes DESC` | `[]MimeTypeBreakdownRow` |
| `StorageTrendDaily` | `SELECT date(created_at), COUNT(*), SUM(file_size) FROM document GROUP BY day ORDER BY day` | `[]StorageTrendDailyRow` |
| `ListActivityTimeline` | `UNION ALL` of document/task/batch events, ordered by time DESC, limit 30 | `[]ActivityEventRow` |
| `DocumentAggregates` | `SELECT COUNT(*), SUM(file_size), SUM(page_count), SUM(word_count) FROM document` | `DocumentAggregatesRow` |
| `LanguageDistribution` | `SELECT language, COUNT(*) FROM document WHERE language != 'und' AND language != '' GROUP BY language ORDER BY count DESC` | `[]DistributionRow` |
| `DocumentTypeDistribution` | `SELECT dt.name, COUNT(*) FROM document d JOIN document_type dt ON d.document_type_id = dt.id WHERE d.document_type_id != 1 GROUP BY dt.id, dt.name ORDER BY count DESC` | `[]DistributionRow` |
| `TagFrequency` | `SELECT t.name, COUNT(*) FROM document_tag dt JOIN tag t ON dt.tag_id = t.id GROUP BY t.id, t.name ORDER BY count DESC LIMIT 10` | `[]DistributionRow` |
| `MissingCounts` | Single-row SELECT with 3 correlated subqueries: documents with `language = 'und' OR ''`, documents with `document_type_id = 1`, documents with no `document_tag` rows | `MissingCountsRow` |

| `TaskSuccessRate` | `SELECT SUM(CASE status='completed'), SUM(CASE status='failed') FROM task WHERE completed_at >= datetime('now', '-7 days')` | `TaskSuccessRateRow` |
| `AvgTaskDurationMs` | `SELECT AVG(julianday(completed_at) - julianday(started_at)) * 86400000 FROM task WHERE status='completed' AND started_at IS NOT NULL AND completed_at >= datetime('now', '-7 days')` | `AvgTaskDurationMsRow` |
| `ActiveBatchIDs` | `SELECT DISTINCT batch_id FROM task WHERE batch_id IS NOT NULL AND status IN ('pending', 'processing')` | `[]string` |

These three methods feed the dashboard processing health panel. `ActiveBatchIDs` identifies which batches still have work, then the handler checks each batch's owner state to determine orphaned count.

The last 4 methods back the dashboard analytics panel. `LanguageDistribution` and `DocumentTypeDistribution` exclude undetermined values (language `'und'`/`''` and `document_type_id = 1`) to avoid double-counting with the `MissingCounts` cards.

---

# Database Schema

## Core Tables

- `document` — Main storage: `document_id` (UUID, UNIQUE), `md5_checksum`, `sha512_checksum` (UNIQUE), `page_count`, `word_count`, `char_count`, `language` (all int64/text defaults), `text_content`, file paths
- `saved_search` — Saved search configurations: `id`, `name`, `filter_json` (JSON), `created_at`
- `task` — Async processing: `task_id` (UUID), `batch_id` (nullable), `task_type`, `payload` (JSON), `result` (JSON), `dedup_key` (nullable), `status`, timestamps, `error`
- `tag` — Classification tags (seeded with 110+ Dewey Decimal tags)
- `document_type` — Document type classification (seeded with types like `article`, `book`, `report`, `letter`, etc.)
- `people` — People/entities associated with documents (`name` UNIQUE, `name_native` nullable for original non-Latin script, `normalized_name` NOT NULL UNIQUE for accent-folded matcher key)
- `people_type` — Roles for people (e.g., `author`, `editor`, `translator`, `subject`)
- `user` — Authentication (username, password_hash, role, api_key_hash, api_key_prefix, api_key_created_at)
- `batch` — Batch processing units: `id`, `source`, `created_at`, `status` (queued/processing/completed/failed/cancelled). The `status` column was added in migration `00005` to support queue-based processing.
- `batch_owner` — Batch ownership: `batch_id`, `owner_id`, `pid`, `acquired_at`, `last_heartbeat`. Each processing batch is claimed by one owner (CLI or queue daemon) with a heartbeat.
- `orphaned_file` — Detected orphaned files: `document_key`, `document_key_type` (uuid/dbid), `source_dir` (originals/processed), `file_path` (inside quarantined/orphaned/), `status` (pending/deleted/restored/reingested), `action_type`, `action_at`

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
- `idx_batch_status` — on `batch(status)`. Added in migration `00005` to support queue queries (`CountQueuedBatches`, `GetNextQueuedBatch`).
- `idx_people_normalized_name` — UNIQUE index on `people(normalized_name)`. Added in migration `00011` alongside the NOT NULL constraint.
- `idx_task_pending` — partial index on `task(created_at)` where `status = 'pending'`
- `idx_task_dedup` — unique partial index on `task(task_type, dedup_key)` where
  status is `pending` or `processing` and `dedup_key IS NOT NULL`. Prevents duplicate
  active tasks for the same (type, key).
- `idx_orphaned_status` — on `orphaned_file(status)`
- `idx_orphaned_detected` — on `orphaned_file(detected_at)`

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

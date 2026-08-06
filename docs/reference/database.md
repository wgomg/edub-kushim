# Database Layer (`internal/database/`)

## `connection.go`

### Functions

`NewPostgresDB(dsn string) (*sql.DB, error)`, `NewClient(db) *Client`

- `NewPostgresDB` opens a PostgreSQL connection pool via `pgx/stdlib` (25 max open, 5 max idle, 5 min lifetime). Before opening the target database it calls `ensureDatabaseExists` which connects to the `postgres` bootstrap database, checks existence via `pg_database`, and auto-creates the target if missing.
- `NewClient` wraps a `*sql.DB` and an embedded `*Queries` (sqlc-generated). Exposes `BeginTx(ctx, opts)` and `DB()` for direct `*sql.DB` access. All query methods on `*Queries` are promoted to `*Client`.

---

## `schema.go`

### Functions

`InitializeSchema(db) error` — Configures goose (`SetBaseFS`, `SetDialect("postgres")`), runs pending migrations via `goose.Up()` from the embedded `sql/schema/migrations/` directory, then runs seeders: `tags`, `document-types`, `people-types`. On a fresh database, the baseline migration (`00001_baseline.sql`) creates all tables and indexes (PostgreSQL syntax). On existing databases, only unapplied migrations are executed.

> The baseline uses PostgreSQL DDL (`GENERATED ALWAYS AS IDENTITY`, `TIMESTAMPTZ`, `JSONB`). Connection and goose dialect are PostgreSQL.

`ResetDatabase(db) error` — Rolls back all migrations via `goose.Reset()` (down → up), then re-runs seeders. Used by `kushim setup --reset-database`.

### Migration files

Numbered SQL files in `internal/database/sql/schema/migrations/`. Each file uses goose annotations:

- `-- +goose Up` — applied when migrating forward
- `-- +goose Down` — applied when rolling back
- `-- +goose StatementBegin` / `-- +goose StatementEnd` — wraps multi-statement SQL (triggers, functions) so goose's semicolon-based parser doesn't split them prematurely

> **Note**: The current baseline has no triggers or multi-statement blocks.

### sqlc integration

sqlc is configured in `sqlc.yaml` to read its schema from the same `migrations/` directory, not from a separate `schema.sql` file:

```yaml
schema: 'internal/database/sql/schema/migrations'
```

sqlc natively understands goose annotations — it recognises `-- +goose Up`/`-- +goose Down` boundaries and ignores down migrations when building its schema model.

> Generated code uses `$1, $2` PostgreSQL-style placeholders.
> Types changed accordingly: `INTEGER` → `int32`, `BIGINT` → `int64`, `TIMESTAMPTZ` nullable → `sql.NullTime`, `TIMESTAMPTZ NOT NULL` → `time.Time`.

When adding a new migration:

1. Write `00002_description.sql` in `migrations/` with `-- +goose Up` / `-- +goose Down` sections (PostgreSQL syntax)
2. Run `sqlc generate` — sqlc picks up the new file from the same directory
3. No separate `schema.sql` update is needed

### Migration auto-apply

Migrations run automatically on startup (no manual CLI command needed):

- **Server** (`edub`) — called in `cmd/edub/main.go` after `NewPostgresDB()`
- **CLI commands** (`kushim consume`, `search`, `task`) — called in `internal/commands/container.go` `GetDB()` when the connection is first acquired
- **Reconnect** (`kushim queue`) — `Container.reconnectClient()` re-runs `InitializeSchema` as a no-op safety net after a DB config change

---

## `dump.go` / `migrate.go`

The SQL dump/restore machinery lives in `internal/database/` (shared by `kushim backup`, `kushim restore`, and the `migrate-db` config task):

- `DumpSchemaAndData(ctx, db, schemaFS, w)` — Writes a complete SQL dump to `w`: a single `BEGIN...COMMIT` transaction that recreates the schema from the embedded migrations (preamble drops the public schema), recreates the `goose_db_version` table from the live schema (introspection-based DDL) and carries its rows across the dump so a restored database can be reconnected without goose re-applying migrations, then batch-inserts all table data (excluding `goose_db_version` and `backup_lock` — the latter is recreated by its own migration).
- `SQLDumpToFile(ctx, db, schemaFS, destPath)` — Gzipped variant (the gzip footer is flushed before the file is closed); used for the pre-migration safety snapshot.
- `ExecuteDumpFile(ctx, db, dumpPath)` — Runs the dump's statements one at a time via a quote-aware splitter (single-quote literals, `''` escapes, dollar-quoted strings, comments) so memory stays bounded regardless of dump size; the dump's own transaction keeps execution atomic.
- `ValidateMigrationDestination(ctx, db)` — Refuses the destructive restore when the destination holds tables without edub migration history (goose version table present with rows), so a mistyped/reused database name is never silently wiped.
- `RewriteStoragePaths(ctx, db, oldDir, newDir)` — Rewrites `document.storage_path`/`original_path` and `orphaned_file.file_path` from `oldDir` to `newDir` in a single transaction (LIKE-escaped prefix match); used by restore and the `migrate-db` task.
- `WaitForTaskDrain(ctx, queries, logger, what)` — Polls `CountProcessingTasks` (5s ticker) until no consume/enrich task is processing; the caller must hold the backup lock, which blocks new claims, so it converges. Shared by `BackupTaskHandler` and the `migrate-db` handler.
- `WithConnectTimeout(dsn, seconds)` — Appends `connect_timeout` to a DSN so connecting to an unreachable host fails within the given seconds (used by the migration's destination connect).

---

## `models.go` (sqlc-generated)

### Key structs

- `Document` — 18 fields: `ID`, `DocumentID` (UUID string), `Title`, `Md5Checksum`, `Sha512Checksum`, `OriginalType`, `FileSize`, `PageCount` (`int32`), `WordCount` (`int32`), `CharCount` (`int32`), `Language`, `CreatedAt`, `ModifiedAt`, `DocumentTypeID`, `OriginalPath`, `StoragePath`, `TextContent`, `DeletedAt sql.NullTime`
- `Task` — 13 fields: `ID`, `TaskID`, `TaskType`, `Status`, `BatchID sql.NullString`, `Payload *json.RawMessage`, `Result *json.RawMessage`, `DedupKey sql.NullString`, `CreatedAt`, `StartedAt`, `CompletedAt`, `Error`, `Attempts int32`
- `Tag` — `ID`, `Name`, `CreatedAt`
- `DocumentType` — `ID`, `Name`, `Description`, `CreatedAt`
- `DocumentTag` — `DocumentID`, `TagID`
- `DocumentPeople` — `DocumentID`, `PeopleID`, `PeopleTypeID`
- `People` — `ID`, `Name`, `NameNative sql.NullString`, `NormalizedName string`, `CreatedAt`
- `PeopleType` — `ID`, `Name`, `Description`, `CreatedAt`
- `User` — `ID`, `Username`, `PasswordHash sql.NullString`, `ApiKeyHash sql.NullString`, `ApiKeyPrefix sql.NullString`, `ApiKeyCreatedAt sql.NullTime`, `Role` (`"admin"`, `"editor"`, `"viewer"`, default `'viewer'`), `CreatedAt sql.NullTime`
- `SavedSearch` — `ID`, `Name`, `FilterJson string`, `CreatedAt time.Time`
- `Batch` — `ID`, `Source`, `Status` (queued/processing/completed/failed/cancelled), `CreatedAt sql.NullTime`
- `BatchOwner` — `BatchID`, `OwnerID`, `Pid`, `AcquiredAt`, `LastHeartbeat`
- `OrphanedFile` — `ID`, `DocumentKey`, `DocumentKeyType` (uuid/dbid), `FilePath`, `OriginalPath`, `SourceDir` (originals/processed), `FileSize`, `OriginalType`, `DetectedAt`, `Status` (pending/deleted/restored/reingested), `ActionAt`, `ActionType`

---

## `db.go`

- `DBTX` interface
- `Queries` struct with `New(db DBTX)`, `WithTx(tx *sql.Tx) *Queries`

---

## `document_sort.go`

### Struct

`ListDocumentsWithSortParams` — `Limit`, `Offset`, `SortBy`, `SortOrder`

### Function

`ListDocumentsWithSort(ctx, params)` — Dynamic ORDER BY with whitelisted columns (`title`, `file_size`, `created_at`); now selects `document_id` (UUID) in addition to the numeric `id`

---

## Query methods (sqlc-generated)

### Document

`CreateDocument` (with `DocumentID` UUID, WordCount, CharCount, Language, PageCount), `GetDocument` (by `document_id`), `GetDocumentById`, `ListDocuments`, `UpdateDocumentPaths` (by `document_id`), `UpdateDocumentPathsById`, `UpdateDocumentMetadata` (by `document_id`), `UpdateDocumentMetadataById`, `GetDocumentByMD5Checksum`, `GetDocumentBySHA512Checksum`, `GetDocumentWithDetails` (by `document_id`), `GetDocumentWithDetailsById`, `GetDocumentWithText` (by `document_id`), `GetDocumentWithTextById`, `SearchDocumentsByTitle`, `SumDocumentFileSizes`, `SoftDeleteDocument` (by `document_id`), `GetTrashDocument` (by `document_id`), `ListTrashDocuments`, `CountTrashDocuments`, `RestoreDocument` (by `document_id`), `PermanentlyDeleteDocument` (by `document_id`), `PurgeExpiredDocuments` (`:execrows`, `$1::text || ' days'` interval)

> All read queries filter `deleted_at IS NULL`; trash queries filter `deleted_at IS NOT NULL`. `SoftDeleteDocument`/`RestoreDocument`/`PermanentlyDeleteDocument` guard on `deleted_at IS NULL`/`IS NOT NULL` so they are no-ops on rows in the wrong state.

### Tag

`CreateTag` (`INSERT ... ON CONFLICT (name) DO NOTHING`, `:execresult`), `GetTag`, `GetTagByName`, `ListTags`, `ListAllTags`, `ListAllTagsNames`, `SearchTagsByName` (prefix search with `LIKE $1` + `LIMIT $2`), `UpdateTag`, `DeleteTag`

### Document tag

`AddDocumentTag`, `RemoveDocumentTag`, `GetDocumentTags`, `ClearDocumentTags`, `GetTagDocuments`

### Document type

`CreateDocumentType` (name only — description defaults to `''`), `CreateDocumentTypeFull` (name + description), `GetDocumentType`, `GetDocumentTypeByName`, `ListDocumentTypes`, `ListAllDocumentTypes`, `ListAllDocumentTypesNames`, `SearchDocumentTypeByName` (prefix search with `LIKE $1` + `LIMIT $2`), `UpdateDocumentType` (name only), `UpdateDocumentTypeFull` (name + description), `DeleteDocumentType`

### People

`CreatePeople` (`INSERT ... ON CONFLICT (name) DO NOTHING` with `Name`, `NameNative`, `NormalizedName`), `GetPeople`, `GetPeopleByName`, `ListPeople`, `ListAllPeople`, `SearchPeopleByName` (prefix search with `LIKE $1` + `LIMIT $2`), `UpdatePeople` (name + normalized_name), `UpdatePeopleFull` (name + name_native + normalized_name), `UpdatePeopleNative` (fills `name_native` only if currently NULL), `DeletePeople`

### People type

`CreatePeopleType`, `GetPeopleType`, `GetPeopleTypeByName`, `ListPeopleTypes`, `ListAllPeopleTypes`, `ListAllPeopleTypesNames`, `SearchPeopleTypeByName` (prefix search with `LIKE ?` + `LIMIT`), `UpdatePeopleType`, `DeletePeopleType`

### Document people

`AddDocumentPeople`, `RemoveDocumentPeople`, `ClearDocumentPeople`, `GetDocumentPeople`, `GetDocumentPeopleWithType`, `GetPeopleDocuments`

### Task

`CreateTask`, `GetTask`, `GetTaskByTaskID`, `GetTaskByBatchID`, `ListTasks`, `ListAllTasks`, `ListTasksByBatch`, `ListAllTasksByBatch`, `ListTasksByBatchAndStatus`, `ListAllTasksByBatchAndStatus`, `ListTasksByBatchAndStatusAndType`, `ListAllTasksByBatchAndStatusAndType`, `ListTasksByBatchAndType`, `ListAllTasksByBatchAndType`, `ListTasksByStatus`, `ListAllTasksByStatus`, `ListTasksByStatusAndType`, `ListAllTasksByStatusAndType`, `ListTasksByType`, `ListAllTasksByType`, `CountTasksByBatchAndStatus`, `GetNextPendingTask`, `GetNextPendingTaskOfType`, `ClaimTask`, `CompleteTask` (now `:execrows` with `AND status = 'processing'` status guard), `FailTask`, `RetryTask`, `DeleteTask`, `CancelPendingTasksByBatch`, `CancelProcessingTasksByBatch`, `SetEnrichTaskPending`, `DiscardEnrichTask`, `DiscardEnrichTaskByTaskID`, `ListDistinctBatchIDs`, `ListDistinctBatchIDsByStatus`, `CountDistinctBatches`, `CountAllTasks`, `CountTasksByStatus`, `QuarantineStaleProcessingTasks`, `ResetStaleProcessingTasks`

### Batch

`CreateBatch`, `GetBatch`, `SetBatchProcessing`, `SetBatchCompleted`, `SetBatchFailed`, `SetBatchCancelled`, `SetBatchPaused`, `CountQueuedBatches`, `CountPausedBatches`, `GetNextQueuedBatch`, `ListPausedBatches`, `CountLiveBatches`, `ListStaleBatchOwners`, `CleanupCompletedBatches`, `QuarantineProcessingTasksByBatch`, `ResetProcessingTasksByBatch`, `GetQuarantinedConsumeTaskPayloads`, `TryInsertBatchOwner`, `UpdateBatchOwnerIfStale`, `AcquireBatchOwnerForce`, `HeartbeatBatchOwner`, `ReleaseBatchOwner`, `DeleteBatchOwnerByBatchID`, `ListBatchOverviews`

### User

`CreateUser` (with `role` column), `GetUser`, `GetUserByUsername`, `GetUserByAPIKeyHash`, `ListUsers` (returns `ListUsersRow` without `password_hash`, includes `role`), `UpdateUser`, `UpdateUserPassword`, `UpdateUserCredentials` (single `UPDATE` for username + password_hash), `UpdateUserRole` (sets role by user ID), `CountUsers`, `DeleteUser`

### Saved search

`CreateSavedSearch`, `ListSavedSearches`, `DeleteSavedSearch`

### Orphaned file

`CreateOrphanedFile`, `GetOrphanedFile`, `ListOrphanedFiles` (pending only, ordered by detected_at DESC), `MarkOrphanedFileDeleted`, `MarkOrphanedFileRestored`, `MarkOrphanedFileReingested`, `MarkAllOrphanedFilesDeleted` (bulk UPDATE pending→deleted)

---

## `Document` — tsvector generated column

The `document` table has a `text_search_vector` column of type `tsvector` that is
`GENERATED ALWAYS AS (to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(text_content, ''))) STORED`.
This means:

- PostgreSQL automatically populates the column when `title` or `text_content` changes
- No application-level write coordination is needed
- Queries use `@@` operator with `plainto_tsquery('simple', $1)` for matching
- The `'simple'` config provides exact-match semantics (no stemming)

The column is backed by a GIN index:

```sql
CREATE INDEX idx_document_tsv ON document USING GIN (text_search_vector);
```

`FTSDocumentRow` (in `structured_search.go`) maps query results with `Rank` (`float64`, from `ts_rank`) and `Snippet` (`string`, from `ts_headline`).

---

## `structured_search.go`

### Struct

`SearchFilter` — `Query`, `Tags []string`, `People []struct{ Name, Type string }`, `DocumentType`, `Language`, `DateCreated *struct{ From, To *string }`, `DateModified *struct{ From, To *string }`, `FileSize *struct{ Min, Max *int64 }`, `SortBy`, `SortOrder`, `Limit`, `Offset`

### Internal: `queryBuilder`

A flexible SQL query builder that composes `WHERE` clauses dynamically:

- `add(clause, args...)` — Appends raw clause with positional parameters
- `eq(col, val)` — Adds `AND d.col = $N` if val is non-empty (`nextIndex()` tracks the `$N` counter)
- `subqueryIn(col, subquery, values)` — Adds `AND d.col IN (SELECT ... WHERE t.name IN ($1,$2,...))`
- `rangeClause(col, min, max)` — Adds `AND d.col >= $N` / `AND d.col <= $N`
- `dateRange(col, range)` — Adds date range filters with optional from/to

### Functions

- `SearchDocumentsStructured(ctx, filter) ([]FTSDocumentRow, error)` — Dynamically builds a SELECT query:
  - If `query` is non-empty: adds `WHERE d.text_search_vector @@ plainto_tsquery('simple', $N)`, `ts_rank()` rank, `ts_headline()` snippet
  - Applies tag subquery, people subqueries, document type subquery, language equality, date ranges, file size ranges
  - When tsquery present: ordered by `rank`; otherwise ordered by requested `sort_by`/`sort_order`
  - Uses `LIMIT $N OFFSET $N` for pagination
- `CountDocumentsStructured(ctx, filter) (int64, error)` — Same filters but `SELECT COUNT(*)` for total count

---

## `dashboard.go`

### Query methods (raw SQL, not sqlc-generated)

These methods are written manually (no sqlc) and follow a consistent pattern:
`QueryContext` → `defer Close` → scan loop → `Close` → `Err`, with error-wrapped scan errors.

| Method | SQL | Returns |
|--------|-----|---------|
| `OriginalTypeBreakdown` | `SELECT original_type, COUNT(*), SUM(file_size) FROM document GROUP BY original_type ORDER BY total_bytes DESC` | `[]OriginalTypeBreakdownRow` |
| `StorageTrendDaily` | `SELECT date(created_at), COUNT(*), SUM(file_size) FROM document GROUP BY day ORDER BY day` | `[]StorageTrendDailyRow` |
| `ListActivityTimeline` | `UNION ALL` of document/task/batch events, ordered by time DESC, limit 30 | `[]ActivityEventRow` |
| `DocumentAggregates` | `SELECT COUNT(*), SUM(file_size), SUM(page_count), SUM(word_count) FROM document` | `DocumentAggregatesRow` |
| `LanguageDistribution` | `SELECT language, COUNT(*) FROM document WHERE language != 'und' AND language != '' GROUP BY language ORDER BY count DESC` | `[]DistributionRow` |
| `DocumentTypeDistribution` | `SELECT dt.name, COUNT(*) FROM document d JOIN document_type dt ON d.document_type_id = dt.id WHERE d.document_type_id != 1 GROUP BY dt.id, dt.name ORDER BY count DESC` | `[]DistributionRow` |
| `TagFrequency` | `SELECT t.name, COUNT(*) FROM document_tag dt JOIN tag t ON dt.tag_id = t.id GROUP BY t.id, t.name ORDER BY count DESC LIMIT 10` | `[]DistributionRow` |
| `MissingCounts` | Single-row SELECT with 3 correlated subqueries: documents with `language = 'und' OR ''`, documents with `document_type_id = 1`, documents with no `document_tag` rows | `MissingCountsRow` |

| `TaskSuccessRate` | `SELECT SUM(CASE status='completed'), SUM(CASE status='failed') FROM task WHERE completed_at >= CURRENT_TIMESTAMP - INTERVAL '7 days'` | `TaskSuccessRateRow` |
| `AvgTaskDurationMs` | `SELECT AVG(EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000) FROM task WHERE status='completed' AND started_at IS NOT NULL AND completed_at >= CURRENT_TIMESTAMP - INTERVAL '7 days'` | `AvgTaskDurationMsRow` |
| `ActiveBatchIDs` | `SELECT DISTINCT batch_id FROM task WHERE batch_id IS NOT NULL AND status IN ('pending', 'processing')` | `[]string` |

These three methods feed the dashboard processing health panel. `ActiveBatchIDs` identifies which batches still have work, then the handler checks each batch's owner state to determine orphaned count.

The last 4 methods back the dashboard analytics panel. `LanguageDistribution` and `DocumentTypeDistribution` exclude undetermined values (language `'und'`/`''` and `document_type_id = 1`) to avoid double-counting with the `MissingCounts` cards.

---

# Database Schema

## Core Tables

- `document` — Main storage: `document_id` (UUID, UNIQUE), `md5_checksum`, `sha512_checksum` (UNIQUE), `file_size` (`BIGINT`), `page_count` (`INTEGER`, Go: `int32`), `word_count` (`int32`), `char_count` (`int32`), `language`, `text_content`, file paths, `deleted_at` (`TIMESTAMPTZ`, nullable — soft-delete marker, NULL = active). Primary key: `id BIGINT GENERATED ALWAYS AS IDENTITY`.
- `saved_search` — Saved search configurations: `id`, `name`, `filter_json` (JSON), `created_at TIMESTAMPTZ NOT NULL` (Go: `time.Time`)
- `task` — Async processing: `task_id` (UUID), `batch_id` (nullable), `task_type`, `payload` (`JSONB`), `result` (`JSONB`), `dedup_key` (nullable), `status`, timestamps, `error`, `attempts int32`
- `tag` — Classification tags (seeded with 110+ Dewey Decimal tags)
- `document_type` — Document type classification (seeded with types like `article`, `book`, `report`). `description` defaults to `''`.
- `people` — People/entities (`name` UNIQUE, `name_native` nullable, `normalized_name` NOT NULL UNIQUE)
- `people_type` — Roles for people
- `user` — Authentication (username, password_hash, role default `'viewer'`, api_key_hash UNIQUE, api_key_prefix, api_key_created_at)
- `batch` — Batch processing units: `id`, `source`, `status` (queued/processing/completed/failed/cancelled), `created_at`
- `batch_owner` — Batch ownership: `batch_id` (PK, FK to batch), `owner_id`, `pid` (`BIGINT`), `acquired_at`, `last_heartbeat`
- `orphaned_file` — Detected orphaned files: `document_key`, `document_key_type` (uuid/dbid), `source_dir` (originals/processed), `file_path`, `file_size` (`BIGINT`), `original_type`, `detected_at`, `status` (pending/deleted/restored/reingested), `action_at`, `action_type`. CHECK constraints on `document_key_type`, `source_dir`, `status`, `action_type`.

## Junction Tables

- `document_tag` — (document_id, tag_id), PK(document_id, tag_id), FK ON DELETE CASCADE
- `document_people` — (document_id, people_id, people_type_id), PK(document_id, people_id, people_type_id), FK ON DELETE CASCADE


## Key Indexes

- `task`: `status`, `task_type`, `batch_id`, `(batch_id, status)`, partial `(created_at WHERE status = 'pending')`, partial unique `(task_type, dedup_key) WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL`, partial `(task_type, created_at) WHERE status = 'pending'`
- `document`: `md5_checksum`, `sha512_checksum`, `created_at`, `document_type_id`, GIN `text_search_vector` (`idx_document_tsv`), partial `idx_document_deleted_at` (`deleted_at WHERE deleted_at IS NOT NULL`)
- `document_people`: `document_id`, `people_id`
- `document_tag`: `document_id`, `tag_id`
- `people`: `normalized_name` (UNIQUE)
- `batch`: `status`
- `batch_owner`: `owner_id`, `last_heartbeat`
- `orphaned_file`: `status`, `detected_at`

## Schema Idempotency

Junction table inserts (`document_tag`, `document_people`) use `INSERT ... ON CONFLICT ... DO NOTHING`
instead of plain `INSERT` to avoid duplicate-key errors on re-enrichment.

The consolidated baseline (`00001_baseline.sql`) creates the initial schema. New schema changes
after the baseline are written as numbered migration files (starting at `00002`). Current
migrations: `00001_baseline.sql`, `00002_tsvector.sql`, `00003_tsvector_index.sql`,
`00004_listen_notify.sql`, `00005_backup_lock.sql`, `00006_rename_mime_type.sql`
(`document.mime_type` / `orphaned_file.mime_type` → `original_type`), `00007_trash_soft_delete.sql`
(adds nullable `document.deleted_at` + partial index `idx_document_deleted_at`). Goose tracks
which versions have been applied in the `goose_db_version` table.

## Migration Version Table

A `goose_db_version` table (managed by goose) tracks applied migrations with columns: `version_id`, `is_applied`, `tstamp`. Created automatically on first `goose.Up()` call.

---

## See Also

- [Search](search.md) — Search engine, structured search queries, autocomplete queries
- [API](api.md) — Document and task response types that map to DB models
- [Task System](task-system.md) — Task CRUD operations
- [Pipeline](pipeline.md) — Consumer and enrichment engines that read/write documents

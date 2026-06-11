# Database Layer (`internal/database/`)

## `connection.go`

### Functions

`NewSQLiteDB(cfg) (*sql.DB, error)`, `NewQueries(db) *Queries`

- `NewSQLiteDB` sets `PRAGMA foreign_keys = ON`, `journal_mode = WAL`, `synchronous = NORMAL`, max 1 connection

---

## `schema.go`

### Functions

`InitializeSchema(db) error` — Reads embedded schema from `sql/schema/schema.sql`, runs seeders: `tags`, `document-types`, `people-types`

`ResetDatabase(db) error` — Drops all non-system tables via `DROP TABLE IF EXISTS` (disables foreign keys first) and re-runs `InitializeSchema`. Used by `kushim setup --reset-database`.

---

## `models.go` (sqlc-generated)

### Key structs

- `Document` — 17 fields: `ID`, `DocumentID` (UUID string), `Title`, `Md5Checksum`, `Sha512Checksum`, `MimeType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `CreatedAt`, `ModifiedAt`, `DocumentTypeID`, `OriginalPath`, `StoragePath`, `TextContent`
- `DocumentFt` — `DocumentID`, `Title`, `Content`
- `Task` — 12 fields: `ID`, `TaskID`, `TaskType`, `Status`, `BatchID sql.NullString`, `Payload json.RawMessage`, `Result *json.RawMessage`, `DedupKey sql.NullString`, `CreatedAt`, `StartedAt`, `CompletedAt`, `Error`
- `Tag` — `ID`, `Name`, `CreatedAt`
- `DocumentType` — `ID`, `Name`, `Description`, `CreatedAt`
- `DocumentTag` — `DocumentID`, `TagID`
- `DocumentPeople` — `DocumentID`, `PeopleID`, `PeopleTypeID`
- `People` — `ID`, `Name`, `CreatedAt`
- `PeopleType` — `ID`, `Name`, `Description`, `CreatedAt`
- `User` — `ID`, `Username`, `PasswordHash sql.NullString`, `ApiKey interface{}`, `CreatedAt`

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

`CreateTag`, `GetTag`, `ListTags`, `ListAllTags`, `ListAllTagsNames`, `UpdateTag`, `DeleteTag`

### Document tag

`AddDocumentTag`, `RemoveDocumentTag`, `GetDocumentTags`, `ClearDocumentTags`, `GetTagDocuments`

### Document type

`CreateDocumentType`, `GetDocumentType`, `ListDocumentTypes`, `ListAllDocumentTypes`, `ListAllDocumentTypesNames`, `UpdateDocumentType`, `DeleteDocumentType`

### People

`CreatePeople`, `GetPeople`, `ListPeople`, `ListAllPeople`, `UpdatePeople`, `DeletePeople`

### People type

`CreatePeopleType`, `GetPeopleType`, `ListPeopleTypes`, `ListAllPeopleTypes`, `ListAllPeopleTypesNames`, `UpdatePeopleType`, `DeletePeopleType`

### Document people

`AddDocumentPeople`, `RemoveDocumentPeople`, `ClearDocumentPeople`, `GetDocumentPeople`, `GetDocumentPeopleWithType`, `GetPeopleDocuments`

### Task

`CreateTask`, `GetTask`, `GetTaskByTaskID`, `GetTaskByBatchID`, `ListTasks`, `ListAllTasks`, `ListTasksByBatch`, `ListAllTasksByBatch`, `ListTasksByBatchAndStatus`, `ListAllTasksByBatchAndStatus`, `ListTasksByBatchAndStatusAndType`, `ListAllTasksByBatchAndStatusAndType`, `ListTasksByBatchAndType`, `ListAllTasksByBatchAndType`, `ListTasksByStatus`, `ListAllTasksByStatus`, `ListTasksByStatusAndType`, `ListAllTasksByStatusAndType`, `ListTasksByType`, `ListAllTasksByType`, `CountTasksByBatchAndStatus`, `GetNextPendingTask`, `GetNextPendingTaskOfType`, `ClaimTask`, `CompleteTask`, `FailTask`, `RetryTask`, `DeleteTask`, `CancelPendingTasksByBatch`, `CancelProcessingTasksByBatch`, `SetEnrichTaskPending`, `ListDistinctBatchIDs`, `ListDistinctBatchIDsByStatus`, `CountDistinctBatches`, `CountAllTasks`, `CountTasksByStatus`

### User

`CreateUser`, `GetUser`, `GetUserByUsername`, `GetUserByAPIKey`, `ListUsers`, `UpdateUser`, `UpdateUserPassword`, `DeleteUser`

---

## `fts5.go`

### Struct

`FTSDocumentRow` — `ID`, `DocumentID` (UUID string), `Title`, `Md5Checksum`, `Sha512Checksum`, `MimeType`, `FileSize`, `PageCount`, `WordCount`, `CharCount`, `Language`, `CreatedAt`, `ModifiedAt`, `DocumentTypeID`, `OriginalPath`, `StoragePath`, `TextContent`, `Rank`, `Snippet`

### Functions

`SearchDocumentsFTS`, `SearchDocumentsFTSWithFilters`, `GetDocumentFTSContent`, `UpdateDocumentFTS`, `DeleteDocumentFTS`, `RebuildDocumentFTS`

---

# Database Schema

## Core Tables

- `document` — Main storage: `document_id` (UUID, UNIQUE), `md5_checksum`, `sha512_checksum` (UNIQUE), `page_count`, `word_count`, `char_count`, `language` (all int64/text defaults), `text_content`, file paths
- `task` — Async processing: `task_id` (UUID), `batch_id` (nullable), `task_type`, `payload` (JSON), `result` (JSON), `dedup_key` (nullable), `status`, timestamps, `error`
- `tag` — Classification tags (seeded with 110+ Dewey Decimal tags)
- `document_type` — Document type classification (seeded with types like `article`, `book`, `report`, `letter`, etc.)
- `people` — People/entities associated with documents
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
to allow safe re-runs of the schema. Junction table inserts (`document_tag`, `document_people`)
use `INSERT OR IGNORE` instead of plain `INSERT` to avoid duplicate-key errors on
re-enrichment.

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

- [API](api.md) — Document and task response types that map to DB models
- [Task System](task-system.md) — Task CRUD operations
- [Pipeline](pipeline.md) — Consumer and enrichment engines that read/write documents

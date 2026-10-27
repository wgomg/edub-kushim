# Database Layer (`internal/database/`)

## `connection.go`

### Functions

`NewPostgresDB(dsn string) (*sql.DB, error)`, `NewClient(db) *Client`

- `NewPostgresDB` opens a PostgreSQL connection pool via `pgx/stdlib` (25 max open, 5 max idle, 5 min lifetime). Before opening the target database it calls `ensureDatabaseExists` which connects to the `postgres` bootstrap database, checks existence via `pg_database`, and auto-creates the target if missing.
- `WithConnectTimeout(dsn, seconds) string` — Appends the `connect_timeout` parameter to a URL-shaped DSN (returned unchanged otherwise).
- `PrepareFreshDatabase(dsn string) error` — Setup-time database claim: connects to the `postgres` bootstrap database (10s connect timeout), creates the target database when missing, then verifies the target is reachable and accepts it only when it has no tables in any non-system schema; a target holding tables fails with `ErrDatabaseNotEmpty` ("drop it or choose another database name"). Opens no schema — callers run `InitializeSchema` after persisting their configuration. Used by `kushim setup` (wizard and CLI) before any settings are persisted.
- `NewClient` wraps a `*sql.DB` and an embedded `*Queries` (sqlc-generated). Exposes `BeginTx(ctx, opts)` and `DB()` for direct `*sql.DB` access. All query methods on `*Queries` are promoted to `*Client`.

---

## `schema.go`

### Functions

`InitializeSchema(ctx, db) error` — Configures goose (`SetBaseFS`, `SetDialect("postgres")`), runs pending migrations via `goose.UpContext(ctx)` from the embedded `sql/schema/migrations/` directory, then runs seeders: `tags`, `tag-aliases`, `document-types`, `people-types`. The context bounds the whole sequence — the backfill passes derive their budgets from it — and migrations are transactional per file, so a cancelled run resumes on the next attempt. On a fresh database, the baseline migration (`00001_baseline.sql`) creates all tables and indexes (PostgreSQL syntax). On existing databases, only unapplied migrations are executed.

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
- **CLI commands** (`kushim consume`, `search`, `task`) — called in `internal/ops/container/container.go` `GetDB()` when the connection is first acquired
- **Reconnect** (`kushim queue`) — `Container.ReconnectClient()` re-runs `InitializeSchema` as a no-op safety net after a DB config change

---

## `dump.go` / `psqlrestore.go` / `migrate.go`

The SQL dump/restore machinery lives in `internal/database/` (shared by `kushim backup`, `kushim restore`, and the queued `migrate-db`/`migrate-storage` tasks through `configtask.MigrateDatabase`/`MigrateStorage`):

- `DumpSchemaAndData(ctx, db, schemaFS, w)` — Writes a complete SQL dump to `w`: a single `BEGIN...COMMIT` transaction that recreates the schema from the embedded migrations (preamble drops the public schema), recreates the `goose_db_version` table from the live schema (introspection-based DDL) and carries its rows across the dump so a restored database can be reconnected without goose re-applying migrations, then batch-inserts all table data (excluding the application-managed tables — `goose_db_version` and `system_signal`, whose seeded signal rows the migration replay recreates, and `matcher_text_embedding`, a rebuildable derived cache whose `bytea` values cannot round-trip the plain-literal value formatter and would yield an unrestorable dump).
- `SQLDumpToFile(ctx, db, schemaFS, destPath)` — Gzipped variant (the gzip footer is flushed before the file is closed); used for the pre-migration safety snapshot.
- `CheckRestoreTooling(runtime, container)` — Verifies the external tooling for a restore under the configured `database.runtime`: `psql` on PATH (`host`), the `docker`/`podman` binary plus a non-empty container name, or refuses outright for `remote` with manual-restore guidance. Runs before any destructive step.
- `RestoreDumpViaPSQL(ctx, runtime, container, dsn, dumpPath)` — Pipes a plain SQL dump file into `psql`, executed on the host (`psql -X -v ON_ERROR_STOP=1 -f <dump> <dsn>`, password via `PGPASSWORD` so it never appears on argv) or inside the container (`<runtime> exec -i -e PGPASSWORD=<pw> <container> psql -X -v ON_ERROR_STOP=1 -h localhost -U <user> -d <db>`, dump piped to stdin; `-h localhost` forces TCP + password auth inside the container). The dump's own `BEGIN/COMMIT` keeps the restore atomic and `ON_ERROR_STOP=1` fails fast; stderr is captured into the error message.
- `ValidateMigrationDestination(ctx, db)` — Refuses the destructive restore when the destination holds tables without edub migration history (goose version table present with rows); the application-managed tables `goose_db_version`, `system_signal`, and `matcher_text_embedding` are ignored (the shared `appManagedTables` list), so a mistyped/reused database name is never silently wiped.
- `RewriteStoragePaths(ctx, db, oldDir, newDir)` — Rewrites `document.storage_path`/`original_path` and `orphaned_file.file_path` from `oldDir` to `newDir` in a single transaction (LIKE-escaped prefix match); used by restore and the `migrate-storage` task.
- `RewriteTaskPayloadPaths(ctx, db, oldConsumptionDir, newConsumptionDir)` — Rewrites `file_path` inside pending `consume` task payloads (JSON) from the old consumption dir to the new one via `jsonb_set` with a strict-prefix match; runs before files move so queued work references the new inbox paths.
- `WaitForTaskDrain(ctx, queries, logger, what, drainTypes []types.TaskType, onWait ...func(count int64))` — Polls `CountProcessingTasks` over the given task types (5s ticker) until none is processing; the caller holds locks the types wait on, which blocks new claims, so it converges. Callers derive the set with `types.TaskTypesWaitingOn(holds...)`. `onWait`, when non-nil, observes the in-flight count on each poll.
- `StartLockHeartbeat(ctx, queries, logger, interval, names []types.LockName, token uuid.NullUUID) func()` — Refreshes the heartbeat timestamps of the named locks held under the token (a NULL token refreshes the manual holder's locks) on the given interval until the context ends; the returned stop function ends the refresh early. Lock acquisition, release and claims live in `internal/task` (locks.go / store.go / direct.go) on top of the `lock.sql` queries.
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
- `SystemSignal` — `ID int16`, `Name`, `Type`, `Value string`, `Data *json.RawMessage` (JSONB), `ModifiedAt time.Time`
- `MatcherTextEmbedding` — `Fingerprint`, `InputHash []byte` (bytea), `Embedding []byte` (bytea), `CreatedAt time.Time`
- `OrphanedFile` — `ID`, `DocumentKey`, `DocumentKeyType` (uuid/dbid), `FilePath`, `OriginalPath`, `SourceDir` (originals/processed), `FileSize`, `OriginalType`, `DetectedAt`, `Status` (pending/deleted/restored/reingested), `ActionAt`, `ActionType`
- `TagVerdictEvent` — `ID`, `DocumentID int64` (FK → `document.id`, ON DELETE CASCADE), `Query`, `Target`, `Sim float64`, `Verdict types.TagVerdict` (`same`/`variant`/`related`), `VerdictModel sql.NullString` (NULL for cache-applied rows), `PolicyVersion`, `VerdictAt time.Time`
- `TagQualityEvent` — `ID`, `DocumentID int64` (FK → `document.id`, ON DELETE CASCADE), `TagName`, `Criteria json.RawMessage` (JSONB, the rubric booleans — the judge's four answers plus the `dimension` stamped from the parts pass), `Pass bool`, `VerdictModel sql.NullString` (NULL for cache-applied rows), `PolicyVersion`, `VerdictAt time.Time`

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

`CreateDocument` (with `DocumentID` UUID, WordCount, CharCount, Language, PageCount), `GetDocument` (by `document_id`), `GetDocumentById`, `ListDocuments`, `ListLiveDocumentIDs` (`:many` — `document_id` for live rows ordered by `id` ascending; the bulk re-enrich selection), `UpdateDocumentPaths` (by `document_id`), `UpdateDocumentPathsById`, `UpdateDocumentMetadata` (by `document_id`), `UpdateDocumentMetadataById`, `GetDocumentByMD5Checksum`, `GetDocumentBySHA512Checksum`, `GetDocumentWithDetails` (by `document_id`), `GetDocumentWithDetailsById`, `GetDocumentWithText` (by `document_id`), `GetDocumentWithTextById`, `GetDocumentTextForTagQuality` (`:one` — `text_content` + `word_count` for an internal document id, live rows only; the provisional sweep's judge-input fetch, `ErrNoRows` when the document is trashed), `SearchDocumentsByTitle`, `SumDocumentFileSizes`, `SoftDeleteDocument` (by `document_id`), `GetTrashDocument` (by `document_id`), `ListTrashDocuments`, `CountTrashDocuments`, `RestoreDocument` (by `document_id`), `PermanentlyDeleteDocument` (by `document_id`), `PurgeExpiredDocuments` (`:execrows`, `$1::text || ' days'` interval)

> All read queries filter `deleted_at IS NULL`; trash queries filter `deleted_at IS NOT NULL`. `SoftDeleteDocument`/`RestoreDocument`/`PermanentlyDeleteDocument` guard on `deleted_at IS NULL`/`IS NOT NULL` so they are no-ops on rows in the wrong state.

### Tag

`CreateTag` (`INSERT ... ON CONFLICT (name) DO NOTHING`, `:execresult`), `GetTag`, `GetTagForUpdate` (`SELECT ... FOR UPDATE` — row lock used by the tag-merge transaction), `GetTagForUpdateByName` (`SELECT ... FOR UPDATE` by name — the alias action's loser lock), `GetTagByName`, `ListTags`, `ListAllTags` (unfiltered — the enricher's novel-set derivation needs every row), `ListPromotedTagNames` (`promoted_at IS NOT NULL`, `created_at DESC` — the matcher store build), `ListStoreTagKeys` (`:many` — the union of `tag.name` and `tag_alias.alias_name`; the enricher house-forms the rows into the store key set the decomposition gate skips before judgment), `PromoteTags` (`:execrows` — sets `promoted_at = now()` for the given ids where still NULL, so repeat/concurrent promotions are no-ops, and stamps `modified_at`), `SearchTagsByName` (prefix search with `LIKE $1` + `LIMIT $2`), `UpdateTag` (rename; stamps `modified_at`), `DeleteTag`, `MarkOrphanTags` (`:execrows` — sets `orphaned_since = now()` on document-less tags without a mark), `ClearOrphanTagMarks` (`:execrows` — clears marks on tags that currently have documents), `ClearOrphanTagMarksByTagIDs` (array of tag ids — called by every assignment path so the sweep grace tracks real last usage), `ListOrphanTags` (`:many` — marked for longer than a cutoff and still document-less, ordered by name), `DeleteOrphanTags` (`:many` — deletes the same set, returning the names; both queries re-check the `NOT EXISTS (document_tag)` guard so a tag re-assigned mid-run is never listed or deleted), `ListProvisionalTagsWithDocumentCount` (`:many` — `promoted_at IS NULL` with at least one assignment, newest first by `coalesce(created_at, now())` then `id`, `document_count` from a scalar subquery over `document_tag` joined to `document` with `deleted_at IS NULL` so only live documents count; `limit`/`offset` — the review queue page), `CountProvisionalTags` (`:one` — the same lateral join and filter predicate's total, so the queue page reports the filtered total beyond its first page). Both queries `LEFT JOIN LATERAL` the latest `tag_quality_event` row per name at the given policy version (`verdict_at DESC LIMIT 1`, served by `idx_tag_quality_event_name`) and take seven optional filters where an empty text skips the filter: the six rubric criteria compare `coalesce(criteria ->> key, 'absent')` against `true`/`false`/`absent`, and the verdict filter matches `none` when the lateral missed (`pass IS NULL`), otherwise the recorded outcome or the UI-equivalent fallback (`pass` → `admit`, the `defer` criterion → `defer`, else `reject`) guarded by `pass IS NOT NULL` so unjudged names stay in the `none` class, `ListProvisionalTagsOlderThan` (`:many` — provisional rows older than a cutoff, oldest first; the reconciliation sweep's provisional worklist), `FirstDocumentIDForTag` (`:one` — the lowest `document_id` carrying the tag among live documents (`document.deleted_at IS NULL`); the sweep's judge-input picker, returning `ErrNoRows` when every assignment is on a trashed document)

### Document tag

`AddDocumentTag` (`source` parameter, `INSERT ... ON CONFLICT (document_id, tag_id) DO NOTHING` — used by enrichment with `source = 'enrich'`; an existing row is never downgraded), `UpsertManualDocumentTag` (`INSERT ... ON CONFLICT (document_id, tag_id) DO UPDATE SET source = 'manual'` — operator writes pin the row so re-enrichment keeps it), `RemoveDocumentTag`, `GetDocumentTags`, `ClearDocumentTags`, `ClearEnrichDocumentTags` (`source = 'enrich'` only — re-enrichment keeps manual assignments), `GetTagDocuments`, `CountTagDocumentsByTagIDs` (`:many` — `tag_id` + `COUNT(*)` per requested id; the `(document_id, tag_id)` PK makes the count a distinct-document count; used by the reconciliation sweep to tell zero-assignment rows, which the orphan sweep owns, from rows with assignments), `CountLiveTagDocumentsByTagIDs` (`:many` — the same count restricted to live documents by joining `document` with `deleted_at IS NULL`; a NULL `tag_ids` reads the whole store's per-tag mass, which is how the measurement pass loads its snapshot, while a non-empty array serves the promotion-floor gate (`tags.RecurrenceFloor`, 10 live documents) in both the enricher and the reconciliation sweep, so trashed carriers are not evidence), `ReassignDocumentTags` (`:execrows` — re-points `document_tag` rows from a source tag to a target tag, skipping documents that already carry the target via a `NOT EXISTS` guard), `DeleteDocumentTagsForTag` (`:execrows` — removes leftover source rows before the tag row is deleted)

### Deleted tag

`ListAllDeletedTags` (`:many` — `name`, `kind`, newest first; the enricher and reconciler load this once per run as the blocklist), `ListDeletedTags` (`:many` — `name`, `kind`, `deleted_at` with an optional `name_prefix` filter, paged by `page_limit`/`page_offset`), `CountDeletedTags`, `UpsertDeletedTag` (`INSERT ... ON CONFLICT (name) DO UPDATE` — refreshes `kind`/`deleted_at`; written by tag delete), `GetDeletedTag`, `DeleteDeletedTag`

### Document type

`CreateDocumentType` (name only — description defaults to `''`), `CreateDocumentTypeFull` (name + description), `GetDocumentType`, `GetDocumentTypeByName`, `ListDocumentTypes`, `ListAllDocumentTypes`, `ListAllDocumentTypesNames`, `SearchDocumentTypeByName` (prefix search with `LIKE $1` + `LIMIT $2`), `UpdateDocumentType` (name only), `UpdateDocumentTypeFull` (name + description), `DeleteDocumentType`

### People

`CreatePeople` (`INSERT ... ON CONFLICT (name) DO NOTHING` with `Name`, `NameNative`, `NormalizedName`), `GetPeople`, `GetPeopleByName`, `ListPeople`, `ListAllPeople`, `SearchPeopleByName` (prefix search with `LIKE $1` + `LIMIT $2`), `UpdatePeople` (name + normalized_name), `UpdatePeopleFull` (name + name_native + normalized_name), `UpdatePeopleNative` (fills `name_native` only if currently NULL), `DeletePeople`

### People type

`CreatePeopleType`, `GetPeopleType`, `GetPeopleTypeByName`, `ListPeopleTypes`, `ListAllPeopleTypes`, `ListAllPeopleTypesNames`, `SearchPeopleTypeByName` (prefix search with `LIKE ?` + `LIMIT`), `UpdatePeopleType`, `DeletePeopleType`

### Document people

`AddDocumentPeople`, `RemoveDocumentPeople`, `ClearDocumentPeople`, `GetDocumentPeople`, `GetDocumentPeopleWithType`, `GetPeopleDocuments`

### Task

`CreateTask`, `GetTask`, `GetTaskByTaskID`, `GetTaskByBatchID`, `ListTasks`, `ListTasksByBatch`, `ListAllTasksByBatch` (one batch's whole task set, unpaginated — the enrich bookend's read), `ListTasksByBatchAndStatus`, `ListTasksByBatchAndStatusAndType`, `ListTasksByBatchAndType`, `ListTasksByStatus`, `ListTasksByStatusAndType`, `ListTasksByType`, `ListActiveTasks` (non-terminal `pending`/`processing`/`waiting`, processing-first then oldest-queued, `LIMIT/OFFSET`), `CountTasksByBatchAndStatus`, `BatchTaskStatusCounts` (one row of per-status counts via `COUNT(*) FILTER`, `:one`; the batch settle's task-outcome accounting), `GetNextPendingTask` / `GetNextPendingTaskOfTypeForOwner` (the claim selection: `GetNextPendingTaskOfTypeForOwner` is the store's claim query — pending tasks of `queued`/`processing` batches owned by the store's execution lease, with the waits-waiter `NOT EXISTS`) , `ListBatchTaskTypes` (distinct task types of one batch — the batch-level hold derivation), `ListBatchTaskProgress` (the display projection of one batch's tasks: `task_id, task_type, status, payload, error, progress` — the progress reporter never transfers the result blobs), `ClaimTask` (`:execrows` — `pending → processing` under the batch's lease owner, clearing `progress`; the guard is the owner the transitions authenticate against), `CompleteBlockingTask` (guarded completion: `AND status = 'processing' AND batch_leased_by(task.batch_id, $owner)`, `:execrows`), `FailBlockingTask` (guarded failure, same guard), `RetryFailedTasksByBatch` (`:execrows` — failed batch tasks back to pending), `DeleteTask`, `CancelPendingTasksByBatch` (`pending` and `waiting` together), `CancelProcessingTasksByBatch`, `ListWaitingSuccessorsByParent` (`:many` — one parent's waiting successors in enqueue order), `SetWaitingSuccessorPending` (`:execrows` — the activation flip over a `waiting` row, `payload` and `dedup_key` written atomically), `DiscardWaitingSuccessor`, `DiscardWaitingSuccessorBranch` (`:execrows` — the whole waiting branch below one parent, recursive CTE over `parent_task_id`), `RestoreDiscardedSuccessors` / `RestoreDiscardedSuccessorsByBatch` (`:execrows` — recursive re-arm of discarded successors of `pending`/`waiting` parents), `DiscardOrphanedWaitingSuccessors` / `DiscardOrphanedWaitingSuccessorsGlobal` (`:execrows` — waiting successors under dead parents (`completed`/`failed`/`cancelled`/`discarded`), the root error copied down the branch; a live parent (`pending`/`processing`/`waiting`) keeps its branch), `ListWaitingSuccessorsWithParentStatus` (`:many` — the settle-refusal diagnostic), `CountAllTasks`, `CountTasksByStatus`, `QuarantineStaleProcessingTasks` (`:many`, `RETURNING task_id, task_type` — the stale-attempts blanket for non-holding types, cutoff by the database clock), `ResetStaleProcessingTasks` (`:execrows`, same clock and exclusion) (the stale blockers and the batch-id exemption live in the lock queries), `GetConsumeTaskPayloadsByIDs` (`:many` — payload rows for a quarantined id set), `GetPermanentlyFailedConsumeTaskPayloads` (`:many` — a batch's attempts-exhausted failed consume rows: the crash residue). The successor link is the `task.parent_task_id` column (migration `00056`, backfilled from the legacy `waiting_for` payloads and indexed by `idx_task_parent`; a soft reference — the enqueue validates that the parent is an earlier spec in the same enqueue).

### Resource locks

One `system_signal` row per lock name (`maintenance`, `storage` — `value` is the held flag, JSONB `data` carries `owner_token`, `batch_id` and the heartbeat timestamp; `lock_is_held(name)` is held-and-fresh under a 30-minute window). In `sql/queries/lock.sql`: `IsLockHeld` (the gate for scheduling and polling checks), `LockRowsForUpdate` (canonical-name-order row locks for acquisition), `CountHeldLocks`, `AcquireLocks` (guarded multi-row hold overwrite: free or past the window; `owner_token`/`batch_id` recorded per acquisition — narg NULL matches the manual holder's absent key via `IS NOT DISTINCT FROM`), `ReleaseLocksForToken` / `ReleaseLocksForBatch` (all `RETURNING name`, matching token- or batch-ownership with `IS NOT DISTINCT FROM`), `TouchLocksForToken` (heartbeat refresh), `NotifyLocksReleased` (`pg_notify('lock_released', ...)`, called in the release transaction so waiters wake at commit), and `QuarantineStaleBlockingTasks` / `ResetStaleBlockingTasks` (stale `processing` blocking tasks, exempted while a fresh lock row belongs to their batch id). Acquisition and the manual holder path live in `internal/task` (locks.go / direct.go).

### Store-change record

The three `system_signal` store-changed flags (`tag_measure_changed`, `tag_reconcile_changed`, `tag_audit_changed`): `GetTagStoreSignal` (`:one` — the flag's `value` by name; the measurement's, reconcile's, and audit's opening gates read it and treat `ErrNoRows` as changed) and `SetTagStoreSignal` (`:exec` — writes `value` and bumps `modified_at`; an opening gate clears its own flag, and a failed run re-arms it). The gate protocol is shared, not per-pass: `tags.OpenStoreSignal`/`tags.RearmStoreSignal` (`internal/tags/store_signal.go`) wrap the read-then-clear in one committed transaction and the failure re-arm, so each pass owns only its flag name and the absent-row-counts-as-changed rule lives in one place. Setting is table-owned, not query-owned: the `tag_store_changed()` trigger function, fired by row triggers on `tag`, `document_tag`, `tag_alias`, and `deleted_tag` (migration `00044`, narrowed to the three flags by `00045`), upserts all three flags to `'true'` inside the mutating statement's own transaction, so every writer is covered — the 23 vocabulary queries regardless of caller, the seeders, and manual SQL. The WHEN guards on the UPDATE triggers compare the semantic columns (`tag`: name, promoted_at, demoted_at, demoted_reason; `document_tag`: tag_id, source; `tag_alias`: alias_name, canonical_tag_id), so orphan-mark bookkeeping and the seeder's no-op `ON CONFLICT` rewrites stay silent; the INSERT/DELETE triggers carry no guard (a `WHEN` referencing `OLD` is illegal on a trigger that can fire for INSERT), and `deleted_tag` needs none because both of its write shapes always change the row. The identity pass holds no flag: its work set is the pending-event count, already its first read, so a store-changed record would have nothing to gate.

### Matcher text embedding cache

`matcher_text_embedding` (migration `00046`) — the matcher daemon's memo of one pooled BGE-M3 vector per distinct chunked input text, keyed `(fingerprint, input_hash)`: `GetMatcherTextEmbedding` (`:one` — the row's `embedding` bytea; `ErrNoRows` is the ordinary miss), `InsertMatcherTextEmbedding` (`:exec` — `ON CONFLICT (fingerprint, input_hash) DO NOTHING`, so concurrent encoders of the same text converge on the first write), `GetMatcherTextEmbeddingGeneration` and `SetMatcherTextEmbeddingGeneration` (read and upsert of the `matcher_text_embedding_generation` marker row's value in `system_signal` — the embedding-stack fingerprint the stored rows were produced under), and `TruncateMatcherTextEmbedding` (`:exec`). The daemon consults the cache before tokenizing and writes the pooled vector on a miss; the fingerprint absorbs every embedding-stack change, so a changed stack invalidates every vector at once and the daemon clears the table through the marker at startup instead of scanning it (mechanics in `docs/developer-guide/semantic-matching.md`). Rows are rebuildable derived data — the table is excluded from dumps and deleted, not re-encoded, when the stack changes.

### Batch

`CreateBatch`, `GetBatch`, `LockBatchForClaim` (`:one`, `SELECT ... FOR UPDATE` — the claim path's identity row lock), `MarkBatchClaimed` (`:execrows`, guarded `queued → processing`, the claim transaction's batch flip; zero rows refuses the claim), `SettleBatch` (`:one RETURNING status`, guarded terminal write — `status = 'processing'`, no active task, no foreign lease; the statement computes `failed` when any failed task exists, `completed` otherwise), `SetBatchPaused` (`:execrows`, guarded `processing → paused` by the batch lease owner), `SetBatchCancelled` (`:execrows`, guarded out of `queued`/`processing`/`paused` — a terminal batch is immutable), `SetBatchFailed` (`:execrows`, guarded `queued|processing → failed`; the launch-failure terminal write behind `task.FailUnstartedBatch`), `RequeueProcessingBatch` (`:execrows`, guarded `processing → queued`), `ResumeBatch` (`:execrows`, guarded `failed|completed|cancelled|paused → queued`), `ListQueuedBatchesByAge` (`:many`, queued batches oldest-first with their task-type set — the dispatcher's candidate list; eligibility and budget filter in Go over the catalog), `CountPausedBatches`, `ListPausedBatches`, `CountLiveNoHoldsBatches` (one dispatcher's live leases on batches holding no resource lock — the per-process slot budget; `dispatcher_id` scopes it), `CountActiveBatchesForSchedule` (queued or processing batches of one schedule name — the scheduler's active check), `GetLastTerminalBatchCreatedAtForSchedule` (the most recent `completed`/`failed`/`cancelled` batch of a schedule name by `created_at` — the scheduler's due-ness anchor, served by the partial `idx_batch_schedule_terminal`), `CountActiveTagAuditBatches` (queued or processing `tagaudit` batches; the reevaluate endpoint's active-run gate), `CountActiveTagIdentityBatches` (queued or processing `tagidentity` batches; the reevaluate endpoint's active-run gate), `ListStaleBatchOwners`, `QuarantineProcessingTasksByBatch` (`:many`, `RETURNING task_id, task_type`) / `ResetProcessingTasksByBatch` (the per-batch attempts split, both keyed by `attempts` against the retry bound), `TryInsertBatchOwner` (with the claiming dispatcher's identity; NULL for manual runs), `GetBatchOwner` (`:one` with the lease `stale` decision computed by the database clock), `AdoptBatchOwner` (`:execrows`, one-time: sets the child's pid only while the lease is still unadopted under the same owner token), `ReleaseStaleBatchOwner` (`:execrows`, the reaper's fenced release: the listed `(batch_id, owner_id, pid)` and still stale), `HeartbeatBatchOwner`, `ReleaseBatchOwner`, `DeleteBatchOwnerByBatchID`, `DeleteBatch`, `ListBatchOverviews` (the batch table's aggregate row — `batch LEFT JOIN LATERAL` per-status task counts `LEFT JOIN batch_owner` — serving the batch list, the single-batch summary, the CLI and the dashboard: optional `batch_id`/`status` filters, the owner's `last_heartbeat`, `owner_pid` and clock-computed `owner_stale` flag, and the `orphaned` flag computed by `batch_orphaned`), `CountBatches` (every batch row), `CountActiveBatches` (non-terminal `queued`/`processing`/`paused` rows), `CountOrphanedBatches` (`batch_orphaned` behind a `status = 'processing'` pre-filter)

### User

`CreateUser` (with `role` column), `GetUser`, `GetUserByUsername`, `GetUserByAPIKeyHash`, `ListUsers` (returns `ListUsersRow` without `password_hash`, includes `role`), `UpdateUser`, `UpdateUserPassword`, `UpdateUserCredentials` (single `UPDATE` for username + password_hash), `UpdateUserRole` (sets role by user ID), `CountUsers`, `DeleteUser`

### Saved search

`CreateSavedSearch`, `ListSavedSearches`, `DeleteSavedSearch`

### Orphaned file

`CreateOrphanedFile`, `GetOrphanedFile`, `ListOrphanedFiles` (pending only, ordered by detected_at DESC), `MarkOrphanedFileDeleted`, `MarkOrphanedFileRestored`, `MarkOrphanedFileReingested`, `MarkAllOrphanedFilesDeleted` (bulk UPDATE pending→deleted)

### Tag verdict event

`InsertTagVerdictEvent` (`:execrows` — `ON CONFLICT (query, target, policy_version) WHERE verdict_model IS NOT NULL DO NOTHING`, returns 0 when a concurrent worker already judged the pair fresh), `LatestTagVerdictsForPairs` (`DISTINCT ON (query, target)` latest verdict per pair at a given `policy_version`, served by `idx_tag_verdict_event_pair`), `TagPairEvidenceStats` (per-pair population and verdict aggregates across all policy versions — `COUNT(DISTINCT document_id)`, event count, and `same`/`variant`/`related` counts — restricted to the requested pair list; backs `tags.Store.PairVerdictStats`, the reconciler's pair evidence)

### Tag identity event

`InsertTagIdentityEvent` (`:execrows` — `ON CONFLICT (kind, candidate, COALESCE(partner, ''), rule_set_version) WHERE verdict IS NULL DO NOTHING`, so concurrent workers leave one pending row per claim), `LatestTagIdentityEventsForCandidates` (`DISTINCT ON (kind, candidate, COALESCE(partner, ''))` latest judged row per claim at a given `rule_set_version` — the identity verdict cache; `same_intent` rows carry the direction — or the variant class for a mechanically resolved pair — in their `criteria` payload), `ListPendingTagIdentityEvents` (`:many` — verdict-less rows oldest first with `page_limit`/`page_offset`; the identity pass reads its backlog through it, served by the partial `created_at` index), `CountPendingTagIdentityEvents` (`:one` — the backlog count reported when the adjudicator is disabled), `ResolveTagIdentityEvent` (`:execrows` — writes `verdict`/`model`/`criteria` onto rows that are still pending, so concurrent identity passes converge on the first verdict), `PendingTagIdentityCountsByNames` (`:many` — per-name pending counts over candidate and partner sides; the review queue's pending-identity badge), `ListIdentityHeldNames` (`:many` — the latest decided `base_form`/`fold_gate` row per candidate at the current rule set whose `proposed_fold` is a live tag, `DISTINCT ON (candidate, kind)`; backs the evidence-derived promotion hold)

### Tag alias

`InsertTagAlias` (`:execrows` — `ON CONFLICT (alias_name) DO NOTHING`, returns 0 when the alias already exists, in which case the caller adopts the existing row), `GetTagAliasByName` (`:one` — idempotence check and read path), `DeleteTagAliasByName` (`:execrows` — removes the alias row; zero rows when the name is not an alias, the withdraw step of a keep flip), `ResolveTagAliasNames` (`:many` — one joined query returning `alias_name` plus the canonical tag `name` for the requested names; backs the shared `tags.ResolveAliases` used by registration, suggestions, structured search and the enricher), `RetargetTagAliases` (`:execrows` — re-points a losing tag's own aliases onto the canonical, so alias chains never form)

### Tag quality event

`InsertTagQualityEvent` (`:execrows` — `ON CONFLICT (tag_name, policy_version) WHERE verdict_model IS NOT NULL DO NOTHING`, returns 0 when a concurrent worker already cached the name fresh; the enricher writes fresh _and_ cache-applied rows with it, and the reconciliation sweep reuses it for a re-judged provisional name, keeping whichever fresh verdict landed first and reading its `pass`/`outcome`/`verdict_model` back through `LatestTagQualityEventsForNames`), `LatestTagQualityEventsForNames` (`DISTINCT ON (tag_name)` latest row per requested name at a given `policy_version`, served by `idx_tag_quality_event_name`; returns only the names that have rows, so the caller's absent names go to the judge — read by the enricher's cache/conflict path, the enricher's promotion fallback, the provisional review queue and the sweep's promote-backstop check)

### Tag composition event

`InsertTagCompositionEvent` (`:exec` — appends one decomposition verdict per judged compound: `document_id`, `candidate`, `parts` JSONB (the validated or resolved parts for split and `part_rejected` drop rows; the surviving parts for a `partial_parts` keep; empty for a plain keep with no alongside parts), `disposition` (`split`/`keep`/`drop`), `reason`, `criteria_version`, the nullable judge `model` — NULL for cached verdicts and the conjunction fallback — the nullable `reconstructible` store signal, and the `reading` evidence JSONB (`{}` when no reading was established)), `LatestCompositionReasonsForNames` (`:many` — `DISTINCT ON (candidate)` latest row per requested candidate at a given `criteria_version`, returning `candidate` and `reason`; the promotion hold reads it and applies the held-reason set in Go), `LatestCompositionEventsForNames` (`:many` — `DISTINCT ON (candidate)` latest row per requested candidate regardless of `criteria_version`, returning the full row; the composition review surface reads it)

### Tag part dimension event

`InsertTagPartDimensionEvent` (`:execrows` — `ON CONFLICT (value, policy_version) WHERE verdict_model IS NOT NULL DO NOTHING`, returns 0 when a concurrent worker already judged the part fresh), `LatestTagPartVerdictsForValues` (`DISTINCT ON (value)` latest row per part value at a given `policy_version`, served by `idx_tag_part_dimension_event_value`)

### Tag compound status event

`InsertTagCompoundStatusEvent` (`:execrows` — `ON CONFLICT (candidate, split_criteria_version) WHERE verdict_model IS NOT NULL DO NOTHING`, returns 0 when a concurrent worker already judged the candidate fresh), `LatestTagCompoundStatusForCandidates` (`DISTINCT ON (candidate)` latest row per candidate at a given `split_criteria_version`, served by `idx_tag_compound_status_event_candidate`)

---

### Tag measurement

`SnapshotNow` (`:one` — the transaction timestamp the pass stamps every record with, so `snapshot_at` is the same instant the repeatable-read snapshot was established), `ListTagsForMeasure` (`:many` — every tag with its `promoted_at`, so provisional anchors stay flagged), `TagLivePairOverlaps` (`:many` — exhaustive pairwise co-occurrence over live documents: `tag_a`/`tag_b` with `COUNT(*)`, a self-join on `document_tag` restricted to `b.tag_id > a.tag_id` so each unordered pair appears once and the `(document_id, tag_id)` primary key keeps each document counted once per tag), `TagCompoundReconstructibility` (`:many` — for the given compound/part id arrays, per compound the live documents carrying every part and those that also carry the compound, so the pass derives `E`/`D` exactly; compounds whose parts are missing from the store read as zero and are left out), and `InsertTagMeasurements` (`:exec` — appends a run's records from a `jsonb_to_recordset` payload: `run_id`, `snapshot_at`, `rule_set_version`, `operator`, `operands`, `numbers`, `state`, `caveats`)

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

`SearchFilter` — `Query`, `Tags []string`, `People []struct{ Name, Type string }`, `DocumentType`, `Language`, `DateCreated *struct{ From, To *string }`, `DateModified *struct{ From, To *string }`, `FileSize *struct{ Min, Max *int64 }`, `SortBy`, `SortOrder`, `Limit`, `Offset`. `PersonAnyType` (`"person"`) is a reserved people-filter type that matches across all relationship types by omitting the `people_type` clause.

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
| `ListActiveTasks` | `SELECT ... FROM task WHERE status IN ('pending','processing','waiting') ORDER BY CASE status (processing→pending→waiting), started_at ASC NULLS LAST, created_at ASC LIMIT/OFFSET` | `[]Task` |
| `DocumentAggregates` | `SELECT COUNT(*), SUM(file_size), SUM(GREATEST(processed_size,0)), SUM(page_count), SUM(word_count) FROM document WHERE deleted_at IS NULL` | `DocumentAggregatesRow` (adds `ProcessedBytes`; `GREATEST` excludes the backfill `-1` sentinel) |
| `LanguageDistribution` | `SELECT language, COUNT(*) FROM document WHERE language != 'und' AND language != '' GROUP BY language ORDER BY count DESC` | `[]DistributionRow` |
| `DocumentTypeDistribution` | `SELECT dt.name, COUNT(*) FROM document d JOIN document_type dt ON d.document_type_id = dt.id WHERE d.document_type_id != 1 GROUP BY dt.id, dt.name ORDER BY count DESC` | `[]DistributionRow` |
| `TagFrequency` | `SELECT t.name, COUNT(*) FROM document_tag dt JOIN tag t ON dt.tag_id = t.id GROUP BY t.id, t.name ORDER BY count DESC LIMIT 10` | `[]DistributionRow` |
| `MissingCounts` | Single-row SELECT with 3 correlated subqueries: documents with `language = 'und' OR ''`, documents with `document_type_id = 1`, documents with no `document_tag` rows | `MissingCountsRow` |

| `TaskSuccessRate` | `SELECT SUM(CASE status='completed'), SUM(CASE status='failed') FROM task WHERE completed_at >= CURRENT_TIMESTAMP - INTERVAL '7 days'` | `TaskSuccessRateRow` |
| `AvgTaskDurationMs` | `SELECT AVG(EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000) FROM task WHERE status='completed' AND started_at IS NOT NULL AND completed_at >= CURRENT_TIMESTAMP - INTERVAL '7 days'` | `AvgTaskDurationMsRow` |

The last 4 methods back the dashboard analytics panel. `LanguageDistribution` and `DocumentTypeDistribution` exclude undetermined values (language `'und'`/`''` and `document_type_id = 1`) to avoid double-counting with the `MissingCounts` cards.

The dashboard's batch health numbers (`active_batches`, `orphaned_batches`) come from the batch-table count queries (`CountActiveBatches`, `CountOrphanedBatches`) documented with the batch queries above.

---

# Database Schema

## Core Tables

- `document` — Main storage: `document_id` (UUID, UNIQUE), `md5_checksum`, `sha512_checksum` (UNIQUE), `file_size` (`BIGINT`, original inbox file size), `processed_size` (`BIGINT`, processed file size; `0` = unknown, `-1` = backfill sentinel for missing files), `page_count` (`INTEGER`, Go: `int32`), `word_count` (`int32`), `char_count` (`int32`), `language`, `text_content`, file paths, `deleted_at` (`TIMESTAMPTZ`, nullable — soft-delete marker, NULL = active). Primary key: `id BIGINT GENERATED ALWAYS AS IDENTITY`.
- `saved_search` — Saved search configurations: `id`, `name`, `filter_json` (JSON), `created_at TIMESTAMPTZ NOT NULL` (Go: `time.Time`)
- `task` — Async processing: `task_id` (UUID), `batch_id` (nullable), `task_type`, `payload` (`JSONB`), `result` (`JSONB`), `dedup_key` (nullable), `status`, timestamps, `error`, `attempts int32`
- `tag` — Classification tags (seeded with 402 values: the re-derived subject inventory plus the country and continent place list, all inserted promoted). `name` is UNIQUE and stored in canonical form: `curation.Tag.Create`/`Update` normalize every incoming name through `textnorm.NormalizeTag` (lowercase, accent-folded, dash family → space, keeps `+ # .`, digits, and letters of any Unicode script) before insert. `promoted_at TIMESTAMPTZ` (nullable) is the trust marker: NULL = provisional (quarantined — no embedding-store membership, so the name is never suggested nor used as a consolidation target), set = trusted. The quality layer promotes a provisional name inline when the admission decision admits it and it has assignments on ≥10 distinct live documents (trashed carriers do not count; the same rule backs the reconciliation sweep's promote-backstop); every other creation path (operator API, deleted-tag restore, reconciler, seeded vocabulary) is born promoted. `orphaned_since TIMESTAMPTZ` (nullable) marks the last reconciliation run that observed the tag without documents; the sweep deletes tags whose mark is older than `enricher.tag_reconcile.dead_tag_grace_days` (seeded values are exempt), and assignment paths clear the mark. `demoted_at TIMESTAMPTZ` and `demoted_reason TEXT` (both nullable) record a revoked standing: demotion clears `promoted_at`, writes the trigger (`mass_decay`, `operator`, or the verdict/composition reason the decision produced) and removes the name from the embedding store while the row and its assignments stay; re-admission through `PromoteTags` clears both. `seeded BOOLEAN NOT NULL DEFAULT false` marks the operator-owned inventory `seed-tags.sql` inserts, which no demotion path and no dead-tag sweep removes. `modified_at TIMESTAMPTZ NOT NULL DEFAULT now()` records the row's last vocabulary change: a rename (`UpdateTag`), a promotion (`PromoteTags`), and a demotion (`DemoteTags`) stamp it. Orphan-mark bookkeeping (`MarkOrphanTags` and the `ClearOrphanTagMarks*` queries) deliberately does not, and a row-removing mutation leaves nothing to stamp.
- `tag_alias` — Alias names that resolve to a canonical tag: `alias_name` (UNIQUE, canonical form), `canonical_tag_id` (FK → `tag.id`, ON DELETE CASCADE, indexed by `idx_tag_alias_canonical_tag`), `rule_set_version`, `created_at`. Aliases resolve to the canonical label in matching, suggestions, emitted names, and query input, and never surface as values themselves. `seed-tag-aliases.sql` seeds the recognized alternate spellings (country and continent alternates plus `christianism`), inserting a row only when no tag row already bears the alias name.
- `document_type` — Document type classification (seeded with types like `article`, `book`, `report`). `description` defaults to `''`.
- `people` — People/entities (`name` UNIQUE, `name_native` nullable, `normalized_name` NOT NULL UNIQUE)
- `people_type` — Roles for people
- `user` — Authentication (username, password_hash, role default `'viewer'`, api_key_hash UNIQUE, api_key_prefix, api_key_created_at)
- `batch` — Batch processing units: `id`, `source`, `status` (queued/processing/completed/failed/cancelled), `created_at`
- `batch_owner` — Batch ownership: `batch_id` (PK, FK to batch), `owner_id`, `pid` (`BIGINT`), `acquired_at`, `last_heartbeat`
- `orphaned_file` — Detected orphaned files: `document_key`, `document_key_type` (uuid/dbid), `source_dir` (originals/processed), `file_path`, `file_size` (`BIGINT`), `original_type`, `detected_at`, `status` (pending/deleted/restored/reingested), `action_at`, `action_type`. CHECK constraints on `document_key_type`, `source_dir`, `status`, `action_type`.
- `deleted_tag` — Tag blocklist: `name` (PK, canonical form), `kind` (`blocked` only, CHECK constrained), `deleted_at TIMESTAMPTZ NOT NULL DEFAULT now()`. Written when an operator deletes a tag and read by the enricher/reconciler to keep enrichment from resurrecting the name.
- `tag_verdict_event` — Append-only tag-adjudication verdicts: `document_id` (FK → `document.id`, ON DELETE CASCADE), `query`, `target`, `sim`, `verdict` (`same`/`variant`/`related`), `verdict_model` (nullable — NULL for cache-applied rows), `policy_version`, `verdict_at`. A partial unique index on `(query, target, policy_version) WHERE verdict_model IS NOT NULL` deduplicates fresh verdicts across concurrent workers.
- `tag_quality_event` — Append-only tag-quality rubric verdicts: `document_id` (FK → `document.id`, ON DELETE CASCADE), `tag_name` (canonical form), `criteria` (JSONB — the judge's four rubric answers `subject_matter`, `not_person`, `atomic`, `not_form_genre` plus the optional `defer` abstention, with `dimension` stamped from the parts pass's shared verdict), `pass` (whether the decision admitted the name), `outcome` (nullable — `reject`/`defer`/`admit`, the recorded disposition; NULL on rows predating the column and on cache-applied copies of those), `verdict_model` (nullable — NULL for cache-applied rows), `policy_version`, `verdict_at`. A partial unique index on `(tag_name, policy_version) WHERE verdict_model IS NOT NULL` deduplicates fresh verdicts across concurrent workers; the newest row per name is the cache, so a re-emitted name resolves without an LLM call.
- `tag_composition_event` — Append-only decomposition verdicts: `document_id` (FK → `document.id`, ON DELETE CASCADE; nullable — NULL on a row the stored-compound sweep journals with no emitting document, migration `00036`), `candidate` (canonical form), `parts` JSONB (the validated segments for split rows and the segments applied alongside for keep rows; `[]` when a row has no parts, including `part_rejected` drops that keep the parts they resolved and keep rows pruned of every rejected part), `disposition` (`split`/`keep`/`drop`, CHECK constrained), `reason` (`judge`/`cached`/`conjunction`/`invalid_components`/`judge_unavailable`; the decision reasons `named_unit`, `named_unit_reconstructible`, `relational_keep`, `no_dimension`, `split_defer`, `conflicting_evidence`, `sample_disagreement`; or `part_rejected` when a split lost every admitted part and degrades to `drop`, or `partial_parts` when it lost some but not all and keeps the compound whole), `criteria_version` (the split criteria the verdict was read under), `model` (nullable — the split judge's model for fresh verdicts), `reconstructible` (nullable — the substrate reconstructibility signal the parts test read: true on the reading's firm or gray band, false when it is far, and NULL on rows with no parts reading), `reading` (JSONB, non-NULL — the evidence behind that signal: `source` (`substrate`, `proxy`, or `absent`) plus, for a substrate reading, `band`, `state`, `mass`, `parts_intersection`, `both`, `excess`, `deficit`, `excess_ratio`, and `deficit_ratio`; `{}` when no reading was established), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`. One row per judged compound per document, serving the composition review as evidence only; a store-present compound skips judgment only once a current-version row decides it, so a missing or degraded row means the compound is re-judged on the next emission.
- `tag_part_dimension_event` — Append-only part-dimension verdicts: `document_id` (FK → `document.id`, ON DELETE CASCADE; nullable — NULL on a row written with no emitting document, such as one the stored-compound sweep produces), `value` (canonical form), `criteria` (JSONB — the dimension reading; `defer` lives in the payload, never as a column), `dimensional` (whether the part names a retrievable dimension), `verdict_model` (nullable — NULL for cache-applied rows), `policy_version`, `verdict_at`. A partial unique index on `(value, policy_version) WHERE verdict_model IS NOT NULL` deduplicates fresh verdicts across concurrent workers.
- `tag_compound_status_event` — Append-only compound-status verdicts: `document_id` (FK → `document.id`, ON DELETE CASCADE; nullable — NULL on a row written with no emitting document, such as one the stored-compound sweep produces), `candidate` (canonical form), `status` (`named_unit`/`composition`/`no_dimension`/`defer`, CHECK constrained), `criteria` (JSONB), `verdict_model` (nullable — NULL for cache-applied rows), `split_criteria_version`, `verdict_at`. A partial unique index on `(candidate, split_criteria_version) WHERE verdict_model IS NOT NULL` deduplicates fresh verdicts across concurrent workers.
- `tag_phrase_fold_event` — Append-only residual phrase-context fold verdicts (migration `00034`): `document_id` (nullable FK → `document.id`, ON DELETE CASCADE), `phrase`, `part`, `anchor`, `criteria` (JSONB — the fold/abstention reading), `fold` (whether the part denotes the anchor in the phrase), `verdict_model` (nullable — NULL for cache-applied rows), `criteria_version` (`analyzer.TagPhraseFoldVersion`), `verdict_at`. A partial unique index on `(phrase, part, anchor, criteria_version) WHERE verdict_model IS NOT NULL` deduplicates fresh verdicts across concurrent workers.
- `matcher_text_embedding` — The matcher daemon's text-embedding cache (migration `00046`): `fingerprint text + input_hash bytea` (composite PK; sha256 of the exact input text × a fingerprint of the embedding stack), `embedding bytea NOT NULL` (the pooled BGE-M3 vector for one chunked input text, little-endian float32), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`. One row per distinct long input text per embedding stack — written by the daemon on an encode miss, read instead of a re-embed when the same text is matched again; only texts over the chunk threshold land here (tag names and rank queries ride the batched short path). Rebuildable derived data: excluded from dumps, truncated when the generation marker in `system_signal` records a changed embedding stack.

## Junction Tables

- `document_tag` — (document_id, tag_id, `source` `'enrich'|'manual'` NOT NULL DEFAULT `'enrich'`, `created_at` TIMESTAMPTZ NOT NULL DEFAULT now()), PK(document_id, tag_id), FK ON DELETE CASCADE
- `document_people` — (document_id, people_id, people_type_id, `created_at` TIMESTAMPTZ NOT NULL DEFAULT now()), PK(document_id, people_id, people_type_id), FK ON DELETE CASCADE


## Key Indexes

- `task`: `status`, `task_type`, `batch_id`, `(batch_id, status)`, partial `(created_at WHERE status = 'pending')`, partial unique `(task_type, dedup_key) WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL`, partial `(task_type, created_at) WHERE status = 'pending'`
- `document`: `md5_checksum`, `sha512_checksum`, `created_at`, `document_type_id`, GIN `text_search_vector` (`idx_document_tsv`), partial `idx_document_deleted_at` (`deleted_at WHERE deleted_at IS NOT NULL`)
- `document_people`: `document_id`, `people_id`
- `document_tag`: `document_id`, `tag_id`
- `people`: `normalized_name` (UNIQUE)
- `tag`: partial `idx_tag_orphaned_since` (`orphaned_since WHERE orphaned_since IS NOT NULL`), partial `idx_tag_provisional` (`created_at WHERE promoted_at IS NULL` — leads with `created_at` because the useful scans filter the provisional set by age or list it wholesale)
- `tag_verdict_event`: `idx_tag_verdict_event_pair` (`query, target, policy_version, verdict_at DESC`), `idx_tag_verdict_event_stale` (`policy_version, verdict_at`), partial unique `idx_tag_verdict_event_fresh_pair` (`(query, target, policy_version) WHERE verdict_model IS NOT NULL`)
- `tag_quality_event`: `idx_tag_quality_event_name` (`tag_name, policy_version, verdict_at DESC`), partial unique `idx_tag_quality_event_fresh_name` (`(tag_name, policy_version) WHERE verdict_model IS NOT NULL`)
- `tag_composition_event`: `idx_tag_composition_event_candidate` (`candidate, criteria_version, created_at DESC`), `idx_tag_composition_event_document` (`document_id` — the document-delete cascade)
- `tag_part_dimension_event`: `idx_tag_part_dimension_event_value` (`value, policy_version, verdict_at DESC`), partial unique `idx_tag_part_dimension_event_fresh_value` (`(value, policy_version) WHERE verdict_model IS NOT NULL`)
- `tag_compound_status_event`: `idx_tag_compound_status_event_candidate` (`candidate, split_criteria_version, verdict_at DESC`), partial unique `idx_tag_compound_status_event_fresh_candidate` (`(candidate, split_criteria_version) WHERE verdict_model IS NOT NULL`)
- `tag_phrase_fold_event`: `idx_tag_phrase_fold_event_key` (`phrase, part, anchor, criteria_version, verdict_at DESC`), partial unique `idx_tag_phrase_fold_event_fresh_key` (`(phrase, part, anchor, criteria_version) WHERE verdict_model IS NOT NULL`)
- `batch`: `status`
- `batch_owner`: `owner_id`, `last_heartbeat`
- `orphaned_file`: `status`, `detected_at`
- `matcher_text_embedding`: PK `(fingerprint, input_hash)` — both match-path lookups and writes are indexed point reads

## Schema Idempotency

Junction table inserts (`document_tag`, `document_people`) use `INSERT ... ON CONFLICT ... DO NOTHING`
instead of plain `INSERT` to avoid duplicate-key errors on re-enrichment; the operator
tag-write query (`UpsertManualDocumentTag`) uses `DO UPDATE SET source = 'manual'` so an
existing enrichment row is upgraded instead of ignored.

The consolidated baseline (`00001_baseline.sql`) creates the initial schema. New schema changes
after the baseline are written as numbered migration files (starting at `00002`). Current
migrations: `00001_baseline.sql`, `00002_tsvector.sql`, `00003_tsvector_index.sql`,
`00004_listen_notify.sql`, `00005_backup_lock.sql`, `00006_rename_mime_type.sql`
(`document.mime_type` / `orphaned_file.mime_type` → `original_type`), `00007_trash_soft_delete.sql`
(adds nullable `document.deleted_at` + partial index `idx_document_deleted_at`),
`00008_thumbnail.sql`, `00009_thumbnail_backfill_index.sql`, `00010_processed_size.sql`,
`00011_task_progress.sql`, `00012_text_hash.sql`, `00013_task_vocabulary_enums.sql`,
`00014_maintenance_lock_predicate.sql` (renames the gate predicate `is_backup_running()` →
`is_maintenance_lock_held()`), `00015_claim_token_lock.sql` (adds `backup_lock.owner_token`
and `task.claim_token` for token-guarded lock acquisition/release),
`00016_tag_symbol_forms.sql` (collapses legacy spelled-out tag forms into their symbol
forms: `c plus plus` → `c++`, `c sharp` → `c#`, `dot net` → `.net`, `asp net` → `asp.net`,
`vbnet` → `vb.net`, deduping `document_tag` rows and deleting the obsolete spelled rows),
`00017_tag_verdict_events.sql` (adds the `tag_verdict` enum — `same`/`variant`/`related` —
and the append-only `tag_verdict_event` table with a partial unique index
`idx_tag_verdict_event_fresh_pair` on `(query, target, policy_version) WHERE verdict_model IS NOT NULL`
so concurrent workers deduplicate fresh LLM verdicts per pair/version),
`00018_tag_reconcile_vocabulary.sql` (adds `tagreconcile` to the `task_type` and
`batch_source` enums), `00019_thumbbackfill_task_type.sql` (adds the `thumbbackfill`
task type), `00020_deleted_tag_table.sql` (creates the `deleted_tag` blocklist with
`name` as PK and the `kind` CHECK), `00021_document_tag_source.sql` (adds
`document_tag.source` — `enrich`/`manual` — so re-enrichment never clears operator
assignments), `00022_tag_orphaned_since.sql` (adds the nullable `tag.orphaned_since`
mark) with `00023_tag_orphaned_since_index.sql` (partial
`idx_tag_orphaned_since WHERE orphaned_since IS NOT NULL`),
`00024_tag_quality_events.sql` (adds the append-only `tag_quality_event` table —
`tag_name`, JSONB `criteria`, `pass`, nullable `verdict_model`, `policy_version` —
with the partial unique index `idx_tag_quality_event_fresh_name` on
`(tag_name, policy_version) WHERE verdict_model IS NOT NULL` for fresh-verdict
deduplication, mirroring the verdict table), `00025_tag_promotion.sql` (adds the
nullable `tag.promoted_at` trust marker with the partial index
`idx_tag_provisional` on `(created_at) WHERE promoted_at IS NULL`, then backfills
`promoted_at = created_at` for every tag with at least one `document_tag`
assignment — the `EXISTS` predicate is the one `MarkOrphanTags` uses, so
`orphaned_since IS NOT NULL ⇒ promoted_at IS NULL` holds for legacy rows, and
zero-assignment rows stay provisional), and
`00026_tag_promotion_correction.sql` (demotes the singleton tags that the
blanket backfill grandfathered — rows whose `promoted_at` still equals
`created_at`, the backfill signature, and that hold fewer than two distinct
documents — so the reconciliation sweep re-judges them; every other grant path
stamps its own `now()` and survives), `00027_tag_merge_dismissal.sql` (creates
the `tag_merge_dismissal` table — `tag_a`/`tag_b` in canonical order with a
`created_at` — recording operator dismissals so merge suggestions stay hidden
until restored), `00028_tag_measure.sql` (adds `tagmeasure` to the
`task_type` and `batch_source` enums and creates the append-only
`tag_measurement` evidence table — `run_id`, `snapshot_at`, `rule_set_version`,
`operator` and `state` CHECK constraints, `operands text[]`, `numbers jsonb`,
`caveats text[]` — with `idx_tag_measurement_run`, `idx_tag_measurement_operator`,
`idx_tag_measurement_snapshot` (on `(snapshot_at DESC, id DESC)`, serving the
reconciler's latest-run read),
and a GIN index on `operands`), `00029_tag_identity.sql` (adds `tagidentity` to
the `task_type` and `batch_source` enums and creates `tag_alias` — canonical
name mappings with `idx_tag_alias_canonical_tag` — plus the append-only
`tag_identity_event` table for the canonical-form criteria, same-intent
claims and confirmed folds, with candidate/partner/version indexes, a partial
`idx_tag_identity_event_pending_created` over the unjudged backlog, and the
partial unique `idx_tag_identity_event_pending` on
`(kind, candidate, COALESCE(partner, ''), rule_set_version) WHERE verdict IS NULL`
so concurrent workers do not duplicate pending claims), and
`00030_tag_quality_outcome.sql` (adds the nullable `tag_quality_event.outcome`
column recording the admission decision's disposition — `reject`/`defer`/`admit` —
so the review queue can label verdicts without re-deriving them), and
`00031_tag_composition.sql` (creates the append-only `tag_composition_event`
table — one decomposition verdict per judged compound with `parts` JSONB, a
`split`/`keep`/`drop` CHECK, `reason`, `criteria_version`, nullable `model` —
with `idx_tag_composition_event_candidate` and
`idx_tag_composition_event_document`, the latter serving the document-delete
cascade), `00032_tag_split_verdicts.sql` (creates the append-only
`tag_part_dimension_event` part-dimension verdict table and the append-only
`tag_compound_status_event` compound-status verdict table, each with a partial
unique fresh-row index mirroring the quality gate, and adds the nullable
`tag_composition_event.reconstructible` column recording the store
reconstructibility signal), and `00033_tag_split_verdict_provenance.sql`
(drops `NOT NULL` from `tag_part_dimension_event.document_id` and
`tag_compound_status_event.document_id`, so a verdict row written with no
emitting document — a sweep-originated row — is storable),
`00034_tag_phrase_fold.sql` (creates the append-only `tag_phrase_fold_event`
residual phrase-context fold verdict table with the partial unique
`idx_tag_phrase_fold_event_fresh_key` on
`(phrase, part, anchor, criteria_version) WHERE verdict_model IS NOT NULL`),
and `00035_retire_merge_review.sql` (drops the `tag_merge_dismissal` table,
deletes the `deleted_tag` rows of kind `merged`, drops the
`deleted_tag.target_name` column, and tightens the `deleted_tag` kind CHECK
to `('blocked')`), `00036_tag_composition_provenance.sql` (drops `NOT NULL`
from `tag_composition_event.document_id`, so the stored-compound sweep can
journal a decision with no emitting document), and
`00037_tag_measurement_snapshot_index.sql` (adds
`idx_tag_measurement_snapshot` on `tag_measurement (snapshot_at DESC, id DESC)`
so the reconciler's latest-run read is index-first), and
`00038_tag_audit.sql` (adds `tagaudit` to the `task_type` and `batch_source`
enums, widens the `tag_measurement.operator` CHECK with `coverage`, and creates
the coverage-audit tables: `tag_coverage_event` — the append-only per
(document, dimension) coverage reading with `class`, `source`, `verdict`,
nullable `verdict_model`, `text_hash`, `criteria_version`, `action`,
`review_outcome`, `provisional` and `rule_set_version` CHECKs, plus
`idx_tag_coverage_event_fresh` (partial unique on
`(document_id, tag_name, text_hash, criteria_version) WHERE verdict_model IS NOT NULL`,
the judged-verdict cache), `idx_tag_coverage_event_open` (partial on
`(created_at DESC) WHERE action IN ('flag','proposal') AND review_outcome IS NULL`,
serving the review list), `idx_tag_coverage_event_class`,
`idx_tag_coverage_event_document` and `idx_tag_coverage_event_tag` — and the
sweep register: `tag_audit_register` (per-document `last_audited_sweep` — the sweep
the document was last audited in, read as `stamp < current sweep` for eligibility,
cascading on document delete) and the `tag_audit_sweep` counter, whose row lives in
`system_signal` since `00041_system_signal.sql`).
`system_signal` holds the migration-seeded signal rows (the sweep counter, the
two resource lock rows, and the three store-changed flags; the matcher daemon also
records its `matcher_text_embedding_generation` marker there at runtime), so
the table is excluded from the
schema-and-data dump and the migration replay recreates its seeded rows on restore. `00039_tag_demotion.sql` adds the demotion record and the seeded marker
(`tag.demoted_at`, `tag.demoted_reason`, `tag.seeded`) plus the partial index
`idx_tag_promoted_demotable` on `(promoted_at) WHERE promoted_at IS NOT NULL AND NOT
seeded`, serving the demotion worklist. `00040_tag_composition_reading.sql` adds the
`tag_composition_event.reading` evidence column (`JSONB NOT NULL DEFAULT '{}'`), which
the insert fills with `COALESCE(@reading, '{}'::jsonb)`. `00041_system_signal.sql`
consolidates the singleton state into one `system_signal` table: it creates the
signal store (`name` unique, text `value`, optional JSONB `data`), carries the
maintenance lock and the sweep counter into it, redefines
`is_maintenance_lock_held()` over the new table, and drops `backup_lock` and
`tag_audit_sweep`. `00042_tag_audit_epoch_register.sql` renames
`tag_audit_register.audit_count` to `last_audited_sweep` and stamps existing rows with
the current sweep; `00043_deleted_tag_deleted_at.sql` renames `deleted_tag.created_at`
to `deleted_at`. `00044_tag_store_change_record.sql` adds the timestamp coverage
(`document_tag.created_at`, `document_people.created_at`, `tag.modified_at` — existing
rows take the migration instant) and the store-changed record: it seeds the four
`system_signal` flags (`tag_measure_changed`, `tag_reconcile_changed`,
`tag_identity_changed`, `tag_audit_changed`) and creates the `tag_store_changed()`
trigger function with its row triggers on `tag`, `document_tag`, `tag_alias`, and
`deleted_tag`. `00045_drop_tag_identity_changed.sql` deletes the
`tag_identity_changed` flag and redefines `tag_store_changed()` over the three
remaining flags — the identity pass has no store-wide read for a signal to gate.
`00046_matcher_text_embedding.sql` creates the matcher daemon's text-embedding
cache — `matcher_text_embedding` (`fingerprint text`, `input_hash bytea`,
`embedding bytea NOT NULL`, PK `(fingerprint, input_hash)`) — one pooled BGE-M3
vector per distinct chunked input text, written by the daemon on an encode miss
and read in place of a re-embed when the same text is matched again; its Down
section deletes the `matcher_text_embedding_generation` marker row it may have
written into `system_signal`. `00047_batch_origin_schedule.sql` replaces
`batch.source` (the `batch_source` enum) with `batch.origin` — the `batch_origin`
enum `cli`/`api`/`polling`/`schedule`/`setup`/`wizard` — and adds the nullable
`batch.schedule` text column: existing rows are mapped deterministically
(`upload`/`reenrich`/`orphaned-restore` → `api`, the scheduled types →
`schedule`, `config` → `setup`), `migrate-db` and `migrate-storage` are added
to the `task_type` enum, and the Down mapping is deliberately lossy (the
schedule origin is approximated from the batch's first task type).
`00048_rekey_migrate_tasks.sql` re-keys pending/failed `config` migration tasks
onto the new `migrate-db`/`migrate-storage` task types and dedup keys (Down
restores the `op` payload field and the `config:` key prefix). `00049_named_resource_locks.sql`
renames the `maintenance_lock` signal row to `maintenance` (carrying `value`,
`owner_token` and the heartbeat timestamp so a live holder survives the
migration), seeds the `maintenance` and `storage` lock rows, and replaces the
gate: `lock_is_held(lock_name)` reads the named row's held flag plus a
30-minute window over the `data` timestamp, and `is_maintenance_lock_held()`
is dropped (its Down restores both for the rollback path; the `storage` row's
state has no legacy counterpart). `00050_batch_owner_dispatcher_id.sql` adds
the `batch_owner.dispatcher_id` column so a dispatcher counts only its own
executions against the slot budget. `00051_drop_task_claim_token.sql` drops
`task.claim_token` — the execution guard is the batch lease owner, so the
per-task token guarded nothing anymore (Down re-adds the column). `00052_remove_config_tasks.sql`
cancels non-terminal `config` tasks and their config-only batches (the type
was removed; tessdata/hugot downloads are direct config updates now), resets
in-flight tasks of lease-free `processing` batches back to `pending` and
requeues those batches (pre-upgrade pool-era work is claimable again; Down is
a documented no-op). `00053_lease_authorization.sql` adds the
`batch_leased_by(batch_id, owner_id)` SQL function the guarded task
transitions and the pause write authorize through (Down drops it).
`00054_batch_schedule_terminal_index.sql` adds the partial
`idx_batch_schedule_terminal` on `batch (schedule, created_at DESC)` over the
terminal statuses — the scheduler's per-tick due-ness query reads one
schedule's last terminal batch instead of scanning the whole unpruned table
(Down drops the index). `00055_batch_orphaned.sql` adds the
`batch_orphaned(batch_id, stale_after_seconds)` SQL function — a `processing`
batch whose lease is missing or heartbeat-stale — that the batch list, the
single-batch summary and the orphan count all compute their orphan flag
through (Down drops it).
Goose tracks
which versions have been applied in the `goose_db_version` table.

## Migration Version Table

A `goose_db_version` table (managed by goose) tracks applied migrations with columns: `version_id`, `is_applied`, `tstamp`. Created automatically on first `goose.Up()` call.

---

## See Also

- [Search](search.md) — Search engine, structured search queries, autocomplete queries
- [API](api.md) — Document and task response types that map to DB models
- [Task System](task-system.md) — Task CRUD operations
- [Pipeline](pipeline.md) — Consumer and enrichment engines that read/write documents

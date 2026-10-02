# Database Layer (`internal/database/`)

## `connection.go`

### Functions

`NewPostgresDB(dsn string) (*sql.DB, error)`, `NewClient(db) *Client`

- `NewPostgresDB` opens a PostgreSQL connection pool via `pgx/stdlib` (25 max open, 5 max idle, 5 min lifetime). Before opening the target database it calls `ensureDatabaseExists` which connects to the `postgres` bootstrap database, checks existence via `pg_database`, and auto-creates the target if missing.
- `NewClient` wraps a `*sql.DB` and an embedded `*Queries` (sqlc-generated). Exposes `BeginTx(ctx, opts)` and `DB()` for direct `*sql.DB` access. All query methods on `*Queries` are promoted to `*Client`.

---

## `schema.go`

### Functions

`InitializeSchema(db) error` — Configures goose (`SetBaseFS`, `SetDialect("postgres")`), runs pending migrations via `goose.Up()` from the embedded `sql/schema/migrations/` directory, then runs seeders: `tags`, `tag-aliases`, `document-types`, `people-types`. On a fresh database, the baseline migration (`00001_baseline.sql`) creates all tables and indexes (PostgreSQL syntax). On existing databases, only unapplied migrations are executed.

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

The SQL dump/restore machinery lives in `internal/database/` (shared by `kushim backup`, `kushim restore`, the `migrate-db` config task, and the `migrate-storage` config task):

- `DumpSchemaAndData(ctx, db, schemaFS, w)` — Writes a complete SQL dump to `w`: a single `BEGIN...COMMIT` transaction that recreates the schema from the embedded migrations (preamble drops the public schema), recreates the `goose_db_version` table from the live schema (introspection-based DDL) and carries its rows across the dump so a restored database can be reconnected without goose re-applying migrations, then batch-inserts all table data (excluding `goose_db_version` and `backup_lock` — the latter is recreated by its own migration).
- `SQLDumpToFile(ctx, db, schemaFS, destPath)` — Gzipped variant (the gzip footer is flushed before the file is closed); used for the pre-migration safety snapshot.
- `CheckRestoreTooling(runtime, container)` — Verifies the external tooling for a restore under the configured `database.runtime`: `psql` on PATH (`host`), the `docker`/`podman` binary plus a non-empty container name, or refuses outright for `remote` with manual-restore guidance. Runs before any destructive step.
- `RestoreDumpViaPSQL(ctx, runtime, container, dsn, dumpPath)` — Pipes a plain SQL dump file into `psql`, executed on the host (`psql -X -v ON_ERROR_STOP=1 -f <dump> <dsn>`, password via `PGPASSWORD` so it never appears on argv) or inside the container (`<runtime> exec -i -e PGPASSWORD=<pw> <container> psql -X -v ON_ERROR_STOP=1 -h localhost -U <user> -d <db>`, dump piped to stdin; `-h localhost` forces TCP + password auth inside the container). The dump's own `BEGIN/COMMIT` keeps the restore atomic and `ON_ERROR_STOP=1` fails fast; stderr is captured into the error message.
- `ValidateMigrationDestination(ctx, db)` — Refuses the destructive restore when the destination holds tables without edub migration history (goose version table present with rows), so a mistyped/reused database name is never silently wiped.
- `RewriteStoragePaths(ctx, db, oldDir, newDir)` — Rewrites `document.storage_path`/`original_path` and `orphaned_file.file_path` from `oldDir` to `newDir` in a single transaction (LIKE-escaped prefix match); used by restore and the `migrate-storage` task.
- `RewriteTaskPayloadPaths(ctx, db, oldConsumptionDir, newConsumptionDir)` — Rewrites `file_path` inside pending `consume` task payloads (JSON) from the old consumption dir to the new one via `jsonb_set` with a strict-prefix match; runs before files move so queued work references the new inbox paths.
- `WaitForTaskDrain(ctx, queries, logger, what, onWait ...func(count int64))` — Polls `CountProcessingTasks` (5s ticker) until no consume/enrich task is processing; the caller must hold the maintenance lock, which blocks new claims, so it converges. `onWait`, when non-nil, observes the in-flight count on each poll.
- `AcquireMaintenanceLock(ctx, queries, logger, what)` — Waits until the maintenance lock is free, polling every 5s and logging each wait; the context ends the wait. The `kushim mirror` CLI uses it so a running backup never blocks the mirror forever.
- `HoldMaintenanceLock(ctx, queries, logger, what, token, onWait)` — Runs the full lock protocol: acquires the lock (or adopts the lock named by a valid claim token without taking ownership of it), starts the 5-minute heartbeat, drains in-flight tasks, and returns the release function that stops the heartbeat and releases an acquired lock. Composed by `task.RunBlocking` and the manual `kushim tagmeasure` run.
- `TryHoldMaintenanceLock(...)` — `HoldMaintenanceLock` without the wait: a lock held by another process returns an error instead. The `migrate-db` and `migrate-storage` handlers refuse with it while another process holds the lock.
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
- `TagVerdictEvent` — `ID`, `DocumentID int64` (FK → `document.id`, ON DELETE CASCADE), `Query`, `Target`, `Sim float64`, `Verdict types.TagVerdict` (`same`/`variant`/`related`), `VerdictModel sql.NullString` (NULL for cache-applied rows), `PolicyVersion`, `VerdictAt time.Time`
- `TagQualityEvent` — `ID`, `DocumentID int64` (FK → `document.id`, ON DELETE CASCADE), `TagName`, `Criteria json.RawMessage` (JSONB, the five rubric booleans), `Pass bool`, `VerdictModel sql.NullString` (NULL for cache-applied rows), `PolicyVersion`, `VerdictAt time.Time`

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

`CreateTag` (`INSERT ... ON CONFLICT (name) DO NOTHING`, `:execresult`), `GetTag`, `GetTagForUpdate` (`SELECT ... FOR UPDATE` — row lock used by the tag-merge transaction), `GetTagForUpdateByName` (`SELECT ... FOR UPDATE` by name — the alias action's loser lock), `GetTagByName`, `ListTags`, `ListAllTags` (unfiltered — the enricher's novel-set derivation needs every row), `ListPromotedTagNames` (`promoted_at IS NOT NULL`, `created_at DESC` — the matcher store build), `ListStoreTagKeys` (`:many` — the union of `tag.name` and `tag_alias.alias_name`; the enricher house-forms the rows into the store key set the decomposition gate skips before judgment), `PromoteTags` (`:execrows` — sets `promoted_at = now()` for the given ids where still NULL, so repeat/concurrent promotions are no-ops), `SearchTagsByName` (prefix search with `LIKE $1` + `LIMIT $2`), `UpdateTag`, `DeleteTag`, `MarkOrphanTags` (`:execrows` — sets `orphaned_since = now()` on document-less tags without a mark), `ClearOrphanTagMarks` (`:execrows` — clears marks on tags that currently have documents), `ClearOrphanTagMarksByTagIDs` (array of tag ids — called by every assignment path so the sweep grace tracks real last usage), `ListOrphanTags` (`:many` — marked for longer than a cutoff and still document-less, ordered by name), `DeleteOrphanTags` (`:many` — deletes the same set, returning the names; both queries re-check the `NOT EXISTS (document_tag)` guard so a tag re-assigned mid-run is never listed or deleted), `ListProvisionalTagsWithDocumentCount` (`:many` — `promoted_at IS NULL` with at least one assignment, newest first by `coalesce(created_at, now())`, `document_count` from a scalar subquery over `document_tag` joined to `document` with `deleted_at IS NULL` so only live documents count; `limit`/`offset` — the review queue page), `CountProvisionalTags` (`:one` — the same predicate's total, so the queue page reports a total beyond its first page), `ListProvisionalTagsOlderThan` (`:many` — provisional rows older than a cutoff, oldest first; the reconciliation sweep's provisional worklist), `FirstDocumentIDForTag` (`:one` — the lowest `document_id` carrying the tag among live documents (`document.deleted_at IS NULL`); the sweep's judge-input picker, returning `ErrNoRows` when every assignment is on a trashed document)

### Document tag

`AddDocumentTag` (`source` parameter, `INSERT ... ON CONFLICT (document_id, tag_id) DO NOTHING` — used by enrichment with `source = 'enrich'`; an existing row is never downgraded), `UpsertManualDocumentTag` (`INSERT ... ON CONFLICT (document_id, tag_id) DO UPDATE SET source = 'manual'` — operator writes pin the row so re-enrichment keeps it), `RemoveDocumentTag`, `GetDocumentTags`, `ClearDocumentTags`, `ClearEnrichDocumentTags` (`source = 'enrich'` only — re-enrichment keeps manual assignments), `GetTagDocuments`, `CountTagDocumentsByTagIDs` (`:many` — `tag_id` + `COUNT(*)` per requested id; the `(document_id, tag_id)` PK makes the count a distinct-document count; used by the reconciliation sweep to tell zero-assignment rows, which the orphan sweep owns, from rows with assignments), `CountLiveTagDocumentsByTagIDs` (`:many` — the same count restricted to live documents by joining `document` with `deleted_at IS NULL`; a NULL `tag_ids` reads the whole store's per-tag mass, which is how the measurement pass loads its snapshot, while a non-empty array serves the promotion-floor gate (`tags.RecurrenceFloor`, 10 live documents) in both the enricher and the reconciliation sweep, so trashed carriers are not evidence), `ReassignDocumentTags` (`:execrows` — re-points `document_tag` rows from a source tag to a target tag, skipping documents that already carry the target via a `NOT EXISTS` guard), `DeleteDocumentTagsForTag` (`:execrows` — removes leftover source rows before the tag row is deleted)

### Deleted tag

`ListAllDeletedTags` (`:many` — `name`, `kind`, `target_name`, newest first; the enricher and reconciler load this once per run as the blocklist), `ListDeletedTags` (`:many` — `name`, `kind`, `target_name`, `created_at` with an optional `name_prefix` filter, paged by `page_limit`/`page_offset`), `CountDeletedTags`, `UpsertDeletedTag` (`INSERT ... ON CONFLICT (name) DO UPDATE` — refreshes `kind`/`target_name`/`created_at`; written by tag delete and inside the merge transaction), `GetDeletedTag`, `DeleteDeletedTag`

### Tag merge dismissal

`ListTagMergeDismissals` (`:many` — `tag_a`, `tag_b`, `created_at`, newest first; loaded into a canonical-name set by every merge-suggestions scan), `UpsertTagMergeDismissal` (`:exec` — `INSERT ... ON CONFLICT (tag_a, tag_b) DO NOTHING`, so dismissing a pair twice is a no-op), `DeleteTagMergeDismissal` (`:execrows` — restore; zero rows when the pair was not dismissed)

### Document type

`CreateDocumentType` (name only — description defaults to `''`), `CreateDocumentTypeFull` (name + description), `GetDocumentType`, `GetDocumentTypeByName`, `ListDocumentTypes`, `ListAllDocumentTypes`, `ListAllDocumentTypesNames`, `SearchDocumentTypeByName` (prefix search with `LIKE $1` + `LIMIT $2`), `UpdateDocumentType` (name only), `UpdateDocumentTypeFull` (name + description), `DeleteDocumentType`

### People

`CreatePeople` (`INSERT ... ON CONFLICT (name) DO NOTHING` with `Name`, `NameNative`, `NormalizedName`), `GetPeople`, `GetPeopleByName`, `ListPeople`, `ListAllPeople`, `SearchPeopleByName` (prefix search with `LIKE $1` + `LIMIT $2`), `UpdatePeople` (name + normalized_name), `UpdatePeopleFull` (name + name_native + normalized_name), `UpdatePeopleNative` (fills `name_native` only if currently NULL), `DeletePeople`

### People type

`CreatePeopleType`, `GetPeopleType`, `GetPeopleTypeByName`, `ListPeopleTypes`, `ListAllPeopleTypes`, `ListAllPeopleTypesNames`, `SearchPeopleTypeByName` (prefix search with `LIKE ?` + `LIMIT`), `UpdatePeopleType`, `DeletePeopleType`

### Document people

`AddDocumentPeople`, `RemoveDocumentPeople`, `ClearDocumentPeople`, `GetDocumentPeople`, `GetDocumentPeopleWithType`, `GetPeopleDocuments`

### Task

`CreateTask`, `GetTask`, `GetTaskByTaskID`, `GetTaskByBatchID`, `ListTasks`, `ListAllTasks`, `ListTasksByBatch`, `ListAllTasksByBatch`, `ListTasksByBatchAndStatus`, `ListAllTasksByBatchAndStatus`, `ListTasksByBatchAndStatusAndType`, `ListAllTasksByBatchAndStatusAndType`, `ListTasksByBatchAndType`, `ListAllTasksByBatchAndType`, `ListTasksByStatus`, `ListAllTasksByStatus`, `ListTasksByStatusAndType`, `ListAllTasksByStatusAndType`, `ListTasksByType`, `ListAllTasksByType`, `ListActiveTasks` (non-terminal `pending`/`processing`/`waiting`, processing-first then oldest-queued, `LIMIT/OFFSET`), `CountTasksByBatchAndStatus`, `GetNextPendingTask`, `GetNextPendingTaskOfType`, `ClaimTask`, `CompleteTask` (now `:execrows` with `AND status = 'processing'` status guard), `FailTask`, `RetryTask`, `DeleteTask`, `CancelPendingTasksByBatch`, `CancelProcessingTasksByBatch`, `SetEnrichTaskPending`, `DiscardEnrichTask`, `DiscardEnrichTaskByTaskID`, `ListDistinctBatchIDs`, `ListDistinctBatchIDsByStatus`, `CountDistinctBatches`, `CountAllTasks`, `CountTasksByStatus`, `QuarantineStaleProcessingTasks`, `ResetStaleProcessingTasks` (both gated-class only, blocking types excluded), `QuarantineStaleBlockingTasks`, `ResetStaleBlockingTasks` (ownership-gated on `backup_lock`), `ReleaseMaintenanceLockForStaleHolders` (releases only past the 30-minute window, token-membership scoped), `ReleaseMaintenanceLockForBatch`

### Batch

`CreateBatch`, `GetBatch`, `SetBatchProcessing`, `SetBatchCompleted`, `SetBatchFailed`, `SetBatchCancelled`, `SetBatchPaused`, `CountQueuedConsumeBatches` (queued batches that occupy a consume slot; excludes `config`/`backup`/`mirror`/`tagreconcile`/`tagmeasure`), `CountPausedBatches`, `GetNextQueuedBatch`, `ListPausedBatches`, `CountLiveBatches`, `CountActiveTagMeasureBatches` (queued or processing `tagmeasure` batches; the scheduler's active-run gate), `GetLastTagMeasureBatchCreatedAt` (most recent **completed** `tagmeasure` batch's `created_at`; the scheduler's anchor, so a failed or orphaned batch never defers the next scheduled run), `ListStaleBatchOwners`, `CleanupCompletedBatches`, `QuarantineProcessingTasksByBatch`, `ResetProcessingTasksByBatch`, `GetQuarantinedConsumeTaskPayloads`, `TryInsertBatchOwner`, `UpdateBatchOwnerIfStale`, `AcquireBatchOwnerForce`, `HeartbeatBatchOwner`, `ReleaseBatchOwner`, `DeleteBatchOwnerByBatchID`, `DeleteBatch`, `ListEmptyQueuedBatches` (queued batches with zero tasks older than N minutes), `ListBatchOverviews`

### User

`CreateUser` (with `role` column), `GetUser`, `GetUserByUsername`, `GetUserByAPIKeyHash`, `ListUsers` (returns `ListUsersRow` without `password_hash`, includes `role`), `UpdateUser`, `UpdateUserPassword`, `UpdateUserCredentials` (single `UPDATE` for username + password_hash), `UpdateUserRole` (sets role by user ID), `CountUsers`, `DeleteUser`

### Saved search

`CreateSavedSearch`, `ListSavedSearches`, `DeleteSavedSearch`

### Orphaned file

`CreateOrphanedFile`, `GetOrphanedFile`, `ListOrphanedFiles` (pending only, ordered by detected_at DESC), `MarkOrphanedFileDeleted`, `MarkOrphanedFileRestored`, `MarkOrphanedFileReingested`, `MarkAllOrphanedFilesDeleted` (bulk UPDATE pending→deleted)

### Tag verdict event

`InsertTagVerdictEvent` (`:execrows` — `ON CONFLICT (query, target, policy_version) WHERE verdict_model IS NOT NULL DO NOTHING`, returns 0 when a concurrent worker already judged the pair fresh), `LatestTagVerdictsForPairs` (`DISTINCT ON (query, target)` latest verdict per pair at a given `policy_version`, served by `idx_tag_verdict_event_pair`), `TagPairEvidenceStats` (per-pair population and verdict aggregates across all policy versions — `COUNT(DISTINCT document_id)`, event count, and `same`/`variant`/`related` counts — restricted to the requested pair list; feeds the merge-suggestions scan)

### Tag identity event

`InsertTagIdentityEvent` (`:execrows` — `ON CONFLICT (kind, candidate, COALESCE(partner, ''), rule_set_version) WHERE verdict IS NULL DO NOTHING`, so concurrent workers leave one pending row per claim), `LatestTagIdentityEventsForCandidates` (`DISTINCT ON (kind, candidate, COALESCE(partner, ''))` latest judged row per claim at a given `rule_set_version` — the identity verdict cache; `same_intent` rows carry the direction in their `criteria` payload), `ListPendingTagIdentityEvents` (`:many` — verdict-less rows oldest first with `page_limit`/`page_offset`; the triggered run and the sweep read their backlog through it, served by the partial `created_at` index), `CountPendingTagIdentityEvents` (`:one` — the backlog count reported when the adjudicator is disabled), `ResolveTagIdentityEvent` (`:execrows` — writes `verdict`/`model`/`criteria` onto rows that are still pending, so the run and the sweep converge on the first verdict), `PendingTagIdentityCountsByNames` (`:many` — per-name pending counts over candidate and partner sides; the review queue's pending-identity badge), `ListIdentityHeldNames` (`:many` — the latest decided `base_form`/`fold_gate` row per candidate at the current rule set whose `proposed_fold` is a live tag, `DISTINCT ON (candidate, kind)`; backs the evidence-derived promotion hold)

### Tag alias

`InsertTagAlias` (`:execrows` — `ON CONFLICT (alias_name) DO NOTHING`, returns 0 when the alias already exists, in which case the caller adopts the existing row), `GetTagAliasByName` (`:one` — idempotence check and read path), `ResolveTagAliasNames` (`:many` — one joined query returning `alias_name` plus the canonical tag `name` for the requested names; backs the shared `tags.ResolveAliases` used by registration, suggestions, structured search and the enricher), `RetargetTagAliases` (`:execrows` — re-points a losing tag's own aliases onto the canonical, so alias chains never form)

### Tag quality event

`InsertTagQualityEvent` (`:execrows` — `ON CONFLICT (tag_name, policy_version) WHERE verdict_model IS NOT NULL DO NOTHING`, returns 0 when a concurrent worker already cached the name fresh; the enricher writes fresh _and_ cache-applied rows with it, and the reconciliation sweep reuses it for a re-judged provisional name, keeping whichever fresh verdict landed first and reading its `pass`/`outcome`/`verdict_model` back through `LatestTagQualityEventsForNames`), `LatestTagQualityEventsForNames` (`DISTINCT ON (tag_name)` latest row per requested name at a given `policy_version`, served by `idx_tag_quality_event_name`; returns only the names that have rows, so the caller's absent names go to the judge — read by the enricher's cache/conflict path, the enricher's promotion fallback, the provisional review queue and the sweep's promote-backstop check)

### Tag composition event

`InsertTagCompositionEvent` (`:exec` — appends one decomposition verdict per judged compound: `document_id`, `candidate`, `parts` JSONB (`[]` for keep/drop rows), `disposition` (`split`/`keep`/`drop`), `reason`, `criteria_version`, and the nullable judge `model` — NULL for cached verdicts and the conjunction fallback)

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
| `ActiveBatchIDs` | `SELECT DISTINCT batch_id FROM task WHERE batch_id IS NOT NULL AND status IN ('pending', 'processing')` | `[]string` |

These three methods feed the dashboard processing health panel. `ActiveBatchIDs` identifies which batches still have work, then the handler checks each batch's owner state to determine orphaned count.

The last 4 methods back the dashboard analytics panel. `LanguageDistribution` and `DocumentTypeDistribution` exclude undetermined values (language `'und'`/`''` and `document_type_id = 1`) to avoid double-counting with the `MissingCounts` cards.

---

# Database Schema

## Core Tables

- `document` — Main storage: `document_id` (UUID, UNIQUE), `md5_checksum`, `sha512_checksum` (UNIQUE), `file_size` (`BIGINT`, original inbox file size), `processed_size` (`BIGINT`, processed file size; `0` = unknown, `-1` = backfill sentinel for missing files), `page_count` (`INTEGER`, Go: `int32`), `word_count` (`int32`), `char_count` (`int32`), `language`, `text_content`, file paths, `deleted_at` (`TIMESTAMPTZ`, nullable — soft-delete marker, NULL = active). Primary key: `id BIGINT GENERATED ALWAYS AS IDENTITY`.
- `saved_search` — Saved search configurations: `id`, `name`, `filter_json` (JSON), `created_at TIMESTAMPTZ NOT NULL` (Go: `time.Time`)
- `task` — Async processing: `task_id` (UUID), `batch_id` (nullable), `task_type`, `payload` (`JSONB`), `result` (`JSONB`), `dedup_key` (nullable), `status`, timestamps, `error`, `attempts int32`
- `tag` — Classification tags (seeded with 402 values: the re-derived subject inventory plus the country and continent place list, all inserted promoted). `name` is UNIQUE and stored in canonical form: `curation.Tag.Create`/`Update` normalize every incoming name through `textnorm.NormalizeTag` (lowercase, accent-folded, dash family → space, keeps `+ # .`, digits, and letters of any Unicode script) before insert. `promoted_at TIMESTAMPTZ` (nullable) is the trust marker: NULL = provisional (quarantined — no embedding-store membership, so the name is never suggested nor used as a consolidation target), set = trusted. The quality layer promotes a provisional name inline when the admission decision admits it and it has assignments on ≥10 distinct live documents (trashed carriers do not count; the same rule backs the reconciliation sweep's promote-backstop); every other creation path (operator API, deleted-tag restore, reconciler, seeded vocabulary) is born promoted. `orphaned_since TIMESTAMPTZ` (nullable) marks the last reconciliation run that observed the tag without documents; the sweep deletes tags whose mark is older than `enricher.tag_reconcile.dead_tag_grace_days`, and assignment paths clear the mark.
- `tag_alias` — Alias names that resolve to a canonical tag: `alias_name` (UNIQUE, canonical form), `canonical_tag_id` (FK → `tag.id`, ON DELETE CASCADE, indexed by `idx_tag_alias_canonical_tag`), `rule_set_version`, `created_at`. Aliases resolve to the canonical label in matching, suggestions, emitted names, and query input, and never surface as values themselves. `seed-tag-aliases.sql` seeds the recognized alternate spellings (country and continent alternates plus `christianism`), inserting a row only when no tag row already bears the alias name.
- `document_type` — Document type classification (seeded with types like `article`, `book`, `report`). `description` defaults to `''`.
- `people` — People/entities (`name` UNIQUE, `name_native` nullable, `normalized_name` NOT NULL UNIQUE)
- `people_type` — Roles for people
- `user` — Authentication (username, password_hash, role default `'viewer'`, api_key_hash UNIQUE, api_key_prefix, api_key_created_at)
- `batch` — Batch processing units: `id`, `source`, `status` (queued/processing/completed/failed/cancelled), `created_at`
- `batch_owner` — Batch ownership: `batch_id` (PK, FK to batch), `owner_id`, `pid` (`BIGINT`), `acquired_at`, `last_heartbeat`
- `orphaned_file` — Detected orphaned files: `document_key`, `document_key_type` (uuid/dbid), `source_dir` (originals/processed), `file_path`, `file_size` (`BIGINT`), `original_type`, `detected_at`, `status` (pending/deleted/restored/reingested), `action_at`, `action_type`. CHECK constraints on `document_key_type`, `source_dir`, `status`, `action_type`.
- `deleted_tag` — Tag blocklist: `name` (PK, canonical form), `kind` (`blocked`|`merged`, CHECK constrained), `target_name` (nullable, set for merged rows), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`. Written when an operator deletes a tag (`blocked`) or merges one away (`merged` + target) and read by the enricher/reconciler to keep enrichment from resurrecting the name.
- `tag_merge_dismissal` — Merge-suggestion pairs the operator dismissed: `tag_a`, `tag_b` (canonical normalized names, PK over the pair, written with `CHECK (tag_a COLLATE "C" < tag_b COLLATE "C")` so the stored order matches the service's byte-order canonicalization), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`. Global across users; rows are never pruned, so a dismissed pair stays hidden by intent even across a delete-and-recreate of either name.
- `tag_verdict_event` — Append-only tag-adjudication verdicts: `document_id` (FK → `document.id`, ON DELETE CASCADE), `query`, `target`, `sim`, `verdict` (`same`/`variant`/`related`), `verdict_model` (nullable — NULL for cache-applied rows), `policy_version`, `verdict_at`. A partial unique index on `(query, target, policy_version) WHERE verdict_model IS NOT NULL` deduplicates fresh verdicts across concurrent workers.
- `tag_quality_event` — Append-only tag-quality rubric verdicts: `document_id` (FK → `document.id`, ON DELETE CASCADE), `tag_name` (canonical form), `criteria` (JSONB — the five rubric answers `subject_matter`, `dimension`, `not_person_place_work`, `atomic`, `not_form_genre`, plus the optional `defer` abstention), `pass` (whether the decision admitted the name), `outcome` (nullable — `reject`/`defer`/`admit`, the recorded disposition; NULL on rows predating the column and on cache-applied copies of those), `verdict_model` (nullable — NULL for cache-applied rows), `policy_version`, `verdict_at`. A partial unique index on `(tag_name, policy_version) WHERE verdict_model IS NOT NULL` deduplicates fresh verdicts across concurrent workers; the newest row per name is the cache, so a re-emitted name resolves without an LLM call.
- `tag_composition_event` — Append-only decomposition verdicts: `document_id` (FK → `document.id`, ON DELETE CASCADE), `candidate` (canonical form), `parts` JSONB (`[]` for keep/drop rows, the validated components for split rows), `disposition` (`split`/`keep`/`drop`, CHECK constrained), `reason` (`judge`/`cached`/`conjunction`/`invalid_components`/`judge_unavailable`, or `part_rejected` when an admitted part was rejected downstream and the split degrades to `drop`), `criteria_version` (the split criteria the verdict was read under), `model` (nullable — the split judge's model for fresh verdicts), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`. One row per judged compound per document, serving the composition review as evidence only.

## Junction Tables

- `document_tag` — (document_id, tag_id, `source` `'enrich'|'manual'` NOT NULL DEFAULT `'enrich'`), PK(document_id, tag_id), FK ON DELETE CASCADE
- `document_people` — (document_id, people_id, people_type_id), PK(document_id, people_id, people_type_id), FK ON DELETE CASCADE


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
- `batch`: `status`
- `batch_owner`: `owner_id`, `last_heartbeat`
- `orphaned_file`: `status`, `detected_at`

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
cascade). Goose tracks
which versions have been applied in the `goose_db_version` table.

## Migration Version Table

A `goose_db_version` table (managed by goose) tracks applied migrations with columns: `version_id`, `is_applied`, `tstamp`. Created automatically on first `goose.Up()` call.

---

## See Also

- [Search](search.md) — Search engine, structured search queries, autocomplete queries
- [API](api.md) — Document and task response types that map to DB models
- [Task System](task-system.md) — Task CRUD operations
- [Pipeline](pipeline.md) — Consumer and enrichment engines that read/write documents

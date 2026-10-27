# Project Overview

## Project Structure

```
internal/
├── api/                    # HTTP server, middleware, permission gates
│   ├── handlers/           # One file per resource: handler + its API DTOs
│   │   ├── api_key.go      # API key handlers (admin per-user + self-service /me)
│   │   ├── auth.go         # Login/logout/me handlers (JWT + HttpOnly cookie)
│   │   ├── autocomplete.go # Lightweight person/document-type/people-type reference responses
│   │   ├── config.go       # Config handler (GET/PUT /wizard/config, GET /wizard/config/status) + ConfigResponse/ConfigStatusResponse types
│   │   ├── consume.go      # Consume handler (async, creates paired consume+enrich tasks, returns batch ID)
│   │   ├── document.go     # Document API handlers (list, get, search, structured search, get file) + response types (tags, people, language, doc type, SearchResponse)
│   │   ├── document_type.go # Document type CRUD handlers + request/response types
│   │   ├── errored.go      # Errored file management handlers (list, download, delete, delete-all)
│   │   ├── errors.go       # Shared error writing helpers
│   │   ├── health.go       # Health check handler
│   │   ├── json.go         # Shared JSON response writer (writeJSON)
│   │   ├── llm.go          # LLM model catalog handler: discovery view + catalog CRUD/reset (503 with live recovery when the startup load failed)
│   │   ├── logs.go         # Log viewer handler
│   │   ├── orphaned.go     # Orphaned file management handlers (list, scan, delete, restore, move-to-inbox)
│   │   ├── people.go       # People + people-type CRUD handlers + request/response types
│   │   ├── saved_search.go # Saved search CRUD handlers + request/response types
│   │   ├── static.go       # RegisterStaticRoutes — embedded SPA serving with index fallback (shared with the wizard)
│   │   ├── tag.go          # Tag CRUD handlers (list, create, update, delete, provisional queue, promote) + request/response types
│   │   ├── task.go         # Task API handlers (list, get, batch summary, global summary with waiting status) + response types
│   │   ├── trash.go        # Trash/soft-delete handlers (list, get, restore, permanent delete, batch, purge)
│   │   └── user.go         # User CRUD handlers + request/response types
│   ├── wizard/             # Setup wizard HTTP server + embedded SPA
│   │   ├── server.go       # Standalone HTTP server on :8420, serves wizard SPA + config API
│   │   └── fs.go           # Embedded SvelteKit wizard build (static/ via //go:embed)
│   └── server.go           # HTTP server setup, middleware, route registration, static SPA (Go 1.22+ patterns)
├── auth/                   # JWT tokens, claims, session secrets, API key helpers
│   ├── token.go            # GenerateToken/ValidateToken (HS256)
│   ├── claims.go           # Claims struct + roles (admin/editor/viewer)
│   └── api_key.go          # API key generation and hashing (ek_ prefix, SHA-256)
├── backup/                 # Backup, restore, scheduling, and the rsync document mirror
│   ├── backup.go           # Create backup (SQL dump, tar.gz, manifest, retention)
│   ├── backup_task.go      # BackupTaskHandler (drain-wait → snapshot → retention, batch lifecycle)
│   ├── restore.go          # Validate, extract, and replace files from backup archive
│   ├── scheduler.go        # Scheduling timing helpers: ScheduleTiming, NextBackupTime, NextOccurrence (the first-occurrence slot)
│   ├── mirror.go           # Run (rsync -a --delete, stats parsing), WriteState (.edub-mirror.json), RunMirrorAndRecord (the queued handler's action; the runner owns the lock heartbeat and drain)
│   └── mirror_task.go      # MirrorTaskHandler (pure work function → RunMirrorAndRecord)
├── cgo/                    # MuPDF CGo glue
│   ├── mupdf_wrapper.go    # MuPDF CGo wrapper (6 C helpers + Go API)
│   └── mupdf_nocgo.go      # Non-CGo stub (build-tag gated)
├── config/                 # Configuration parsing
│   ├── config.go           # Configuration structs and loading (IdentityCandidateSimilarity, default thresholds, engine identifier constants, AvailableEngines map)
│   ├── configdir.go        # ConfigDir() — returns ~/.config/edub-kushim
│   ├── setup.go            # Bootstrap config, SaveMap, tessdata/Hugot model download helpers
│   ├── servicefiles.go     # Generates systemd service files during setup
│   ├── tools.go            # External tool availability checks (MissingExternalTools)
│   └── watcher.go          # File-based config watcher (hot-reload on disk change)
├── database/               # Database layer (sqlc-generated + manual)
│   ├── connection.go       # DB connection (PostgreSQL via pgx, 25 max conn, auto-create database, WithConnectTimeout)
│   ├── client.go           # Client wrapper — embeds *Queries, exposes BeginTx/DB()
│   ├── schema.go           # InitializeSchema — goose migrations + seeders (tags, tag-aliases, doc-types, people-types)
│   ├── dump.go             # SQL dump: DumpSchemaAndData, SQLDumpToFile, goose version preservation
│   ├── psqlrestore.go      # Restore via psql: CheckRestoreTooling, RestoreDumpViaPSQL (host or docker/podman exec)
│   ├── migrate.go          # RewriteStoragePaths, ValidateMigrationDestination, WaitForTaskDrain, StartLockHeartbeat (lock acquisition/release stay in internal/task)
│   ├── models.go           # Generated data models (Document now has PageCount, WordCount, CharCount, Language; added SavedSearch)
│   ├── db.go               # Database interface (Queries, WithTx)
│   ├── document_sort.go    # ListDocumentsWithSort (whitelisted sort columns)
│   ├── structured_search.go # Dynamic SQL query builder for structured search (SearchDocumentsStructured, CountDocumentsStructured)
│   ├── dashboard.go        # Dashboard analytics/processing-health queries (raw SQL)
│   ├── document.sql.go     # Generated document queries (with WordCount, CharCount, Language)
│   ├── document_tag.sql.go # Generated document_tag junction queries
│   ├── document_people.sql.go # Generated document_people junction queries
│   ├── document_type.sql.go # Generated document type queries (includes UpdateDocumentType)
│   ├── tag.sql.go          # Generated tag queries (includes SearchTagsByName)
│   ├── people.sql.go       # Generated people queries (CreatePeople with name_native, ListAllPeople, SearchPeopleByName, UpdatePeopleNative, GetPeopleByName)
│   ├── people_type.sql.go  # Generated people type queries
│   ├── task.sql.go         # Generated task queries (includes SetEnrichTaskPending, DiscardEnrichTask)
│   ├── batch.sql.go        # Generated batch + batch_owner queries (queued/processing, ownership, stale owners)
│   ├── lock.sql.go          # Generated resource-lock queries (acquire/release/touch/sweeps over the system_signal lock rows)
│   ├── user.sql.go         # Generated user queries
│   ├── saved_search.sql.go # Generated saved search queries (CreateSavedSearch, ListSavedSearches, DeleteSavedSearch)
│   └── sql/                # Embedded SQL assets
│       ├── schema/         # Seed SQL files (seed-tags.sql, seed-tag-aliases.sql, seed-document-types.sql, seed-people-types.sql)
│       │   └── migrations/ # goose versioned migrations (00001_baseline.sql, ...) — also read by sqlc
│       └── queries/        # SQL queries for sqlc
│           ├── document.sql
│           ├── document_tag.sql
│           ├── document_people.sql
│           ├── document_type.sql
│           ├── saved_search.sql
│           ├── tag.sql
│           ├── people.sql
│           ├── people_type.sql
│           ├── task.sql
│           ├── lock.sql
│           ├── user.sql
│           └── orphaned.sql
├── documents/              # Document domain: ingestion, storage, MIME, and processing adapters
│   ├── consumption/        # Document processing engine + its task handlers
│   │   ├── consumer.go     # Main consumer (extract → OCR → optimize → store), duplicate detection (MD5+SHA512)
│   │   ├── consume.go      # ConsumeTaskHandler (uses FileFromPath)
│   │   ├── scan.go         # ScanAndEnqueue — shared inbox scan → dedup → batch creation
│   │   ├── storage.go      # File operations, checksums, FileFromPath, MIME detection via mimetype lib
│   │   └── thumbnail.go    # ThumbnailTaskHandler (render + save page thumbnail)
│   ├── storage/            # Filesystem operations for documents and orphaned/trashed files
│   │   ├── dirs.go         # Canonical storage subdirectory name constants (Dir*), part of the persisted data contract
│   │   ├── paths.go        # Path derivation helpers (ThumbnailPath)
│   │   ├── files.go        # Inbox scanning (ListFilePaths/CountFilePaths), CalculateMD5, SameDevice
│   │   ├── hash.go         # SHA256Hex — document text hash
│   │   ├── copy.go         # CopyTree/copyFile — directory-tree copying for backup staging
│   │   ├── orphaned.go     # Walk originals/processed, quarantine, remove, copy to inbox
│   │   └── trash.go        # Move files to/from trash dir, remove trash dirs (soft-delete file operations)
│   ├── mime/               # MIME type predicates, extension mapping, supported-types constants
│   ├── ocr/                # OCR engines (gosseract, ocrmypdf, standalone pipeline)
│   │   ├── adapter.go      # OCR interface and factory
│   │   ├── gosseract.go    # gosseract OCR with subprocess fork
│   │   ├── standalone.go   # RunStandalone OCR pipeline (subprocess entry)
│   │   ├── ocrmypdf.go     # ocrmypdf external tool
│   │   ├── tesseract_link.go # CGo linker flags for static Tesseract
│   │   ├── tessdata.go     # Embedded traineddata + download
│   │   ├── font_embed.go   # LiberationSans TTF embedding
│   │   └── kushim_font.ttf # Embedded font file
│   ├── textextractor/      # Text extraction (PDF, DOCX, ODT)
│   │   ├── adapter.go      # Text extractor interface + CompositeExtractor (PDF + DOCX + ODT)
│   │   ├── mupdf.go        # MuPDF text extractor (default, CGo, streams)
│   │   ├── gopdf.go        # gopdf text extractor (pure Go)
│   │   ├── pdftotext.go    # pdftotext external tool
│   │   ├── docx.go         # Pure Go DOCX text extraction
│   │   └── odt.go          # Pure Go ODT text extraction
│   ├── converter/          # LibreOffice headless DOCX/ODT → PDF conversion
│   │   ├── adapter.go      # Converter interface + factory
│   │   └── libreoffice.go  # LibreOffice CLI wrapper
│   ├── pdfoptimizer/       # PDF optimization
│   │   ├── adapter.go      # PDF optimizer interface
│   │   ├── mupdf.go        # MuPDF optimizer (default, CGo wrapper)
│   │   └── ghostscript.go  # Ghostscript external tool
│   └── thumbnail/          # Page thumbnail rendering
│       ├── adapter.go      # Thumbnailer interface + factory
│       ├── mupdf.go        # MuPDF page renderer (CGo)
│       └── standalone.go   # RunStandalone thumbnail pipeline (subprocess entry)
├── enrichment/             # Enrichment engine (LLM pipeline) + its task handler
│   ├── enricher.go         # Enricher: dual text reduction → tag matching → token budget pre-check + retry loop → LLM → identity → admission seams → people/tag/doc type with romanization + normalization; truncate (rune-aware title truncation with the "Unknown" sentinel)
│   └── enrich.go           # EnrichTaskHandler (fetches document, calls Enricher.Enrich)
├── errs/                   # Error classification (kind, wrapping, FromDB/PgError mapping)
├── llm/                    # LLM capability registry
│   ├── analyzer/           # LLM classification adapters (capability-based)
│   │   ├── adapter.go      # ContentAnalyzer interface + TypeRef + factory (adapter-based dispatch)
│   │   ├── gates.go        # content/token/credit gates, per-call output budgets (analysis and judged), their error types, token-usage stats, request pacing
│   │   ├── prompt.go       # SystemMessage, BuildPrompt + template, DocMetadata, BuildDocTypePrompt, ExtractHeadTailWords
│   │   ├── tagfilter.go    # NormalizeTags, FilterTags, TagFilter, SplitComponents
│   │   ├── facets.go       # TagDetail + categories, ParseAnalysisResponse, SeparateFacets
│   │   ├── adjudication.go # same-intent pair adjudication: prompt + parser
│   │   ├── tag_split.go    # compound split gate: prompt + parser
│   │   ├── tag_quality.go  # quality rubric judge: prompt + parser
│   │   ├── tag_translation.go # language guard: translation prompt + parser
│   │   ├── tag_phrase_fold.go # phrase-context fold judge: prompt + parser
│   │   ├── tag_identity.go # canonical-form judge: prompt + parser
│   │   ├── tag_audit.go    # completeness audit: applicability prompt + parser
│   │   ├── text.go         # estimateTokens, cleanCodeBlock, ContainsNonLatin
│   │   ├── openai_compatible.go # OpenAI-compatible API (absorbs former OpenAI, DeepSeek, Mistral, etc.)
│   │   └── llm_anthropic.go # Anthropic Messages API with capability flags
│   ├── provider_catalog.json # Default provider entries (adapter, official_url, session_header); seeded into the config dir on first load
│   ├── model_catalog.json  # Default model entries: capabilities per provider/model; seeded into the config dir on first load
│   ├── registry.go         # Registry: resolves the config-dir catalogs (seeds missing files from the defaults), Lookup, Adapters, ProvidersForAdapter, ModelsForProvider
│   └── catalog_edit.go     # Catalog mutation API (Add/Set/Remove provider and model, ResetCatalogs, Catalog snapshot): the in-use guard, the cross-process catalog lock, the staged two-file write with rollback, and the atomic single-file write
├── log/                    # Structured logging and memory metrics
│   ├── logger.go           # Logger (slog-based console/file handlers, numeric level filtering, reqID) + WithReqID/ReqIDFrom context accessors
│   └── metrics.go          # Memory metrics (HeapInUse, RSS, NumGC), HumanDuration, FormatMemDelta, FormatBytes
├── matcher/                # Tag matcher: Hugot engine, RPC client, embedding cache
│   ├── adapter.go          # Matcher, Embedder, EmbeddingStore interfaces
│   ├── hugot.go            # Hugot session (Go or ORT backend), cosine similarity, chunked encoding
│   ├── client.go           # MatcherClient — HTTP client over Unix socket to the matcher daemon
│   ├── bootstrap.go        # BuildTagCache — pre-compute tag embeddings at startup
│   └── embedding_store.go  # TagEmbeddingStore (map[string][]float32 with thread-safe ops)
├── ops/                    # Process layer: CLI verbs, queue daemon, dispatcher, consume child, setup, shared-services container
│   ├── cli/                # Command table and the `kushim` verbs
│   │   ├── commands.go     # Command definitions and runner
 │   │   ├── backup.go       # Backup and restore commands
 │   │   ├── batch.go        # `kushim batch` queue surface: create/run/stop/cancel/resume/restart/list/status
 │   │   ├── config.go       # `kushim config` command (dump/get/set/unset/validate/path)
│   │   ├── enrich.go       # Enrich command (single document, --limit N, or --all)
│   │   ├── hugot.go        # Matcher RPC server over Unix socket (encode, match, rank, store ops)
│   │   ├── migrate.go      # `kushim migrate` database/storage migration commands
│   │   ├── mirror.go       # `kushim mirror` command (rsync --delete, waits for the backup lock)
│   │   ├── search.go       # Search command (CLI)
│   │   ├── storage.go      # Storage commands: `kushim storage orphans` (list, scan, delete, restore, move-to-inbox)
│   │   ├── tagaudit.go    # `kushim tagaudit` (direct run / `--from-batch` batch mode) coverage-audit pass
│   │   ├── tagidentity.go  # `kushim tagidentity` (direct run / `--from-batch` batch mode) identity adjudication
│   │   ├── tagmeasure.go   # `kushim tagmeasure` (direct run / `--from-batch` batch mode) measurement pass
│   │   ├── tagpass.go      # Shared tag-verb plumbing (batch-mode selector validation, manual lock holder, batch executor)
│   │   ├── tagreconcile.go # `kushim tagreconcile` (direct run / `--from-batch` batch mode) reconciliation pass
│   │   ├── task.go         # Task commands (list, status)
 │   │   ├── thumbnails.go   # `kushim thumbnails` — direct thumbnail runs (--all/--document) plus the --from-batch child
│   │   └── user.go         # User commands (create)
 │   ├── container/          # Shared-services lazy assembler (config, DB reconnect, tag service, search, matcher client, tag tools)
 │   │   └── container.go    # Config/logger/database accessors and reconnect protocol; builds no task wiring
 │   ├── dispatch/           # Batch dispatcher and launcher (explicit dependencies)
 │   │   ├── dispatcher.go   # Dispatcher: claims queued batches (status, lease, holds in one tx) and forks the task CLI; optional in-process registration executes covered types in-process
 │   │   ├── inprocess.go    # InProcessRun — hosted execution of a claimed batch (pid-0 lease, launch-failure recovery)
 │   │   ├── notify.go       # Postgres LISTEN/NOTIFY goroutine shared by the daemon and the wizard
 │   │   └── launcher.go     # Shared claim/fork + LaunchQueuedBatch (kushim batch run) + launch-failure backoff
│   ├── queue/              # Queue daemon for background batch consumption (Postgres LISTEN/NOTIFY via dispatch)
│   │   ├── queue.go        # Daemon loop: daemon task catalog, dispatcher wiring, config hot-reload
│   │   ├── scheduler.go    # Declarative scheduler: schedule definitions (name, task type, enabled predicate, timing, producer), central due/active checks, first-run anchor
│   │   └── reaper.go       # Stale batch/task reclamation (fenced release, then signal, reset, settle/requeue)
│   ├── consume/            # `kushim consume` child process
 │   │   ├── consume.go      # Direct inbox run + batch mode through the shared library, cancel subcommand
│   │   └── advisories.go   # Tool-block and ocrmypdf advisories printed before a batch starts
│   ├── setup/              # Setup command — launches web wizard by default, --cli for terminal mode
│   │   └── setup.go
│   ├── flags/              # CLI flag parser (shared by the ops packages)
│   │   └── flags.go
 │   └── configtask/         # Migration task handlers
 │       └── configtask.go   # MigrateDBTaskHandler/MigrateStorageTaskHandler — separate task types that copy the database or relocate files when connection/storage settings change; asset downloads (tessdata/hugot) are direct config updates, not tasks
├── search/                 # Full-text search engine
│   └── search.go           # Engine, Result (with Language, DocumentTypeID, checksums), Filter (structured search), sanitizeQuery, SearchStructured (returns results + total count)
├── service/                # Shared domain service package (people, document types, batches, trash, users)
│   ├── people.go           # People service (NewPeople, batch CRUD)
│   ├── peopletype.go       # PeopleType service (NewPeopleType, batch CRUD)
│   ├── document_type.go    # DocumentType service (NewDocumentType, batch CRUD)
 │   ├── batch.go            # Batch service (NewBatch, GetOverview, ListOverviews, CountBatches, CountActiveBatches, CountOrphaned, HasPendingWork, CountPausedBatches, ListPausedBatches, ListQueuedBatchesByAge, CountLiveNoHoldsBatches, ListStaleBatchOwners, DeleteBatchOwnerByBatchID, ResetProcessingTasksByBatch, ResetStaleProcessingTasks); BatchOverview read model — one aggregate row the list, dashboard, summary and CLI share
│   ├── orphaned.go         # Orphaned file service (scan, list, delete, restore, move-to-inbox, orphaned-thumbnail sweep)
│   ├── trash.go            # Trash service (soft delete, restore, permanent delete, purge + hourly background purge)
│   ├── errored.go          # Errored file service (list on disk, get path, delete, delete-all)
 │   ├── enrich.go           # Re-enrich service (single document and bulk, dedup via active task check)
 │   ├── thumbnail_backfill.go # ThumbnailBackfill (batch/document enqueue paths) + ThumbnailBackfillSpecs (all-documents scan) + ThumbnailDedupKey
│   ├── password.go         # Password validation rules (12+ chars, complexity)
│   ├── user.go             # User service (CRUD, roles, API key hashing)
│   └── crud.go             # CrudServices aggregate + Close() (explicit member list)
├── static/                 # Embedded web UI (main app)
│   └── fs.go               # Embedded SvelteKit build (build/ directory via //go:embed)
├── tags/                   # Tag subsystem (store, measurement, identity, policy, reconciliation)
│   ├── store.go            # Store wrapper over tag queries (NewStore, LiveMass, CompoundIntersections, PairNames, PairVerdictStats, LatestWorklists, ReconstructibilityBatch, SiblingWorklist)
│   ├── measure.go          # Measurement pass (NewRunner, Report, WorklistCounts)
│   ├── tagmeasure.go       # TagMeasureDedupKey — the measurement dedup key shared by the enqueue path and task definition
│   ├── evidence.go         # Evidence records and JSON encoding/decoding (RuleSetVersion, Record, EncodeRows, DecodeRows)
│   ├── identity.go         # Identity rules and planning (IdentityRuleSetVersion, HouseForm, VariantClass, Plan/PlanDeterministic, Disposition, AliasAction, Registration, CompositionRow/CompositionReading)
│   ├── decision.go         # Pinned decision rule sets (DecideAdmission, DecideComposition, DecidePromotion, DecideMassDemotion, DecisionRuleSetVersion, RecurrenceFloor)
│   ├── worklist.go         # Rule-set v1 worklist cuts and reconstructibility bands (DeriveWorklists, Twin/Sibling/CompoundSplit, ClassifyReconstructibility)
│   ├── algebra.go          # Extension algebra readings (Set, NewSet, MassOf, EvidenceState)
│   ├── extension.go        # Read-only tag-store snapshot (NewSnapshot, Tag, Pair, AliasResolver, SelfAliasResolver)
│   ├── atomicity.go        # Compound shape classification (ClassifyShape, DeriveParts, MaxCompoundWords)
│   ├── completeness.go     # Coverage classes and derivations (CoverageClass/CoverageSource/CoverageAction, CoverageRecord, ResolvedContent/ResolvedSegments/CoversDimension/PendingCoverageSegments, TwinPartner)
│   ├── curation/           # Tag vocabulary services (operator and adapter-visible tag operations)
│   │   ├── tag.go          # Tag service (NewTag, query delegates, rename/delete/promote/demote, deleted-tag records, the read-only overlap surface's ListSiblings/SiblingSnapshot, embedder integration)
│   │   ├── registration.go # Planned tag registration (Create/CreateAndUnblock, PlanRegistration, CreatePlannedTx)
│   │   ├── alias.go        # Alias-plan application (ApplyAliasPlan, applyAliasAction) + identity-evidence recording
│   │   ├── tagidentity.go  # TagIdentity service (NewTagIdentity — enqueues the triggered identity runs)
│   │   ├── tagreconcile.go # ReconcileSpecs + TagReconcileDedupKey (the enqueue surface's reconciliation task set)
│   │   ├── tagmeasure.go   # MeasureSpecs (the enqueue surface's measurement task set)
│   │   └── tagaudit.go     # TagAudit service (NewTagAudit — enqueues the coverage-audit runs, serves the coverage review surface)
│   ├── admission/          # Tag admission (LLM adjudication seams + identity resolution)
│   │   ├── adjudicator.go  # Adjudicator (NewAdjudicator — canonical suggestions, compound decomposition incl. JudgeCompounds over the substrate reading, deleted-tag policy, identity planning, tag-quality gating)
│   │   ├── identity.go     # Canonical-form identity mechanics (house form, key equality, tie-break)
│   │   ├── identity_resolve.go # Pending identity-evidence resolver (NewIdentityResolver, IdentityReport)
│   │   ├── audit.go        # AuditRunner (NewAuditRunner — the coverage-verification pass: sweep worklist, trigger/lexical narrowing, judged applicability, dispositions, pending-coverage accounting)
│   │   ├── tagidentity.go  # TagIdentityTaskHandler (runs the pass and stores its report as the task result)
│   │   └── tagaudit.go     # TagAuditTaskHandler (same shape over the audit runner)
│   ├── policy/             # Pure candidate generation (identity + band subjects)
│   │   └── policy.go       # Evaluate, Decision, Version — no logging, no config, no I/O
│   └── reconcile/          # Scheduled re-adjudication
│       ├── reconciler.go   # Reconciler orchestrator (NewReconciler, Run, Report, ProvisionalCounts)
│       ├── worklist.go     # Worklist build (buildWorklist, bootstrap, category ordering)
│       ├── evidence.go     # Evidence pass (re-rank, band adjudication, hysteresis confirmation)
│       ├── apply.go        # Apply pass (alias/keep flips, verdict upsert)
│       ├── sweeps.go       # Twin canonicalization, orphan and provisional sweeps
│       ├── tagreconcile.go # ReconcileTaskHandler — the batch-mode pass handler (lock-holding lifecycle inside the child's RunBatch)
│       ├── bootstrap.go    # Log bootstrap parser (absorbed pairs and band-keep lines from the daemon's log rotations)
│       └── compounds.go    # Stored-compound decomposition scan (JudgeCompounds-driven over the substrate reconstructibility reading)
├── task/                   # Generic task system
 │   ├── batch.go            # Batch ownership (Owner.Adopt/Release/Heartbeat) + the guarded lifecycle ops: ClaimBatch (atomic dispatcher claim), SettleBatch (normal settlement writer), FailUnstartedBatch (launch-failure terminal write), PauseBatch (lease-only), CancelBatch (signal + counts + release), ResumeBatch (guarded recovery with in-tx child restore and optional failed-task retry); Execution guard, ClaimRequest, ResetResult/CancelResult/ResumeResult, TaskIdentity; LeaseStaleSeconds, IsAlive; ResetProcessingTasksByBatch/ForClaim
 │   ├── crud.go             # Task CRUD (Get, ListFiltered — batch/status/type paging over the per-shape queries, positive limit required)
 │   ├── direct.go           # RunDirect — the manual holder path for direct CLI commands (NULL-token locks, wait/--no-wait)
 │   ├── enqueue.go          # BatchSpec/TaskSpec + Enqueuer — the single transactional creation path; refuses a pending task whose type carries no dedup hook in the boundary's catalog
 │   ├── catalog.go          # Catalog — one boundary's task type table: static policy (locks, direct locks, CLI surface, hosts) merged with boundary-supplied wiring (TypeWiring: dedup hooks, worker counts, preconditions)
 │   ├── type_definition.go  # TypeDefinition contract + DedupKeyFunc
 │   ├── errors.go           # Error wrapper carrying ReqID for log correlation
 │   ├── handler.go          # Handler interface — pure work functions
 │   ├── execute.go          # Runner — claim under the batch lease, dispatch, guarded complete/fail, settle after every transition; RunBatch — the leased child (adopt, repair, batch holds with heartbeat and drain, per-type workers, InProcess-hosted runs keep a pid-0 lease)
 │   ├── heartbeat.go        # Heartbeat goroutine — periodic Owner.Heartbeat every 5s
 │   ├── locks.go            # Resource-lock lifecycle: ErrLockBusy, AcquireLocks (all-or-nothing, canonical order), ReleaseLocksForToken/Batch, UnionLockNames, LockHeartbeatInterval
 │   ├── payload.go          # TaskPayload + ReadPayload — display fields decoded from a task payload
 │   ├── progress.go         # Progress tracking (ProgressFunc, ProgressSnapshot, ProgressTracker)
 │   ├── registry.go         # Task type → handler registry (assembled at each command boundary)
 │   ├── store.go            # Task persistence (ClaimNextPending — owner-scoped claim + batch flip + wait verification, CompleteTask/FailTask lease-guarded transitions, ActivateChild activation with dedup discard, Discard)
 │   └── types.go            # Task domain struct
├── testutil/               # Test helpers (config fixtures, minimal PDFs, assertions)
├── textnorm/               # Tag and DB-key canonicalization
│   ├── tagname.go          # NormalizeTag — canonical tag names
│   ├── tagjoint.go         # SplitJointTag — conjunction-based joint tag splitting
│   └── normalize.go        # NormalizeForDB, FoldAccents — DB key normalization
├── tools/                  # Tool adapter framework + composition root
│   ├── runner.go           # Unified tool runner (all adapters), runWithTimeout generic wrapper
│   └── textreducer/        # Text summarization
│       ├── adapter.go      # TextReducer interface + factory
│       └── textrank.go     # TextRank: chunking, TF-IDF, weighted PageRank, diversity penalty
 ├── types/                  # Closed task-system vocabulary registry
 │   ├── registry.go         # Batch origins, task types, statuses, validity/parse helpers
 │   ├── status.go           # Shared CRUD enums (CreateStatus, UpdateStatus, DeleteStatus, PromoteStatus)
 │   └── result.go           # Generic result types (CreateResult[T], UpdateResult[T], DeleteResult)
└── version/                # Application version
    └── version.go          # var Version = "3.0.0"

cmd/
├── kushim/               # CLI entry point
│   └── main.go           # Also dispatches internal-ocr / internal-mupdf-clean subprocess commands
└── edub/                 # API server entry point
    ├── main.go           # Default: start server; version command
    └── runner.go         # Standalone command runner (version only)

web/                      # SvelteKit SPA frontend (main UI)
├── src/
│   ├── app.html          # HTML shell
│   ├── lib/
│   │   ├── api.js        # API client (fetch helper) — documents, tasks, autocomplete, saved searches, config, auth, logs, orphaned, me
│   │   ├── index.js
│   │   ├── assets/
│   │   │   └── favicon.svg
│   │   ├── stores/
│   │   │   ├── authStore.js      # Auth state (token/user/role) from localStorage
│   │   │   ├── filterStore.js   # Reactive filter state store (setPartial, reset, fromQueryString)
│   │   │   ├── searchFilter.js  # Query parser, tokenizer, size/date formatting utilities
│   │   │   ├── confirmStore.svelte.js  # Imperative confirm-dialog promise store
│   │   │   ├── toastStore.svelte.js    # Global toast stack store
│   │   │   └── settingsStore.svelte.js # Settings state — config snapshot/diff, staged catalog op queue, detail level
│   │   └── components/
│   │       ├── DataTable.svelte   # Reusable data table (sortable, paginated, total count, refreshKey)
│   │       ├── FilterPanel.svelte # Collapsible advanced filter panel (tags, people, dates, size, missing flags)
│   │       ├── SearchBar.svelte   # Rich search input with chips, autocomplete, keyboard navigation
│   │       ├── Modal.svelte       # Reusable overlay modal
│   │       ├── ConfirmDialog.svelte # Themed confirmation dialog
│   │       ├── Toast.svelte       # Global toast stack
│   │       ├── UploadModal.svelte # Upload progress modal
│   │       ├── StoragePanel.svelte # Dashboard storage panel
│   │       ├── BatchOverviewPanel.svelte # Dashboard batch overview panel
│   │       ├── ActiveTasksStrip.svelte # Dashboard active tasks strip (pending/processing/waiting cards)
│   │       ├── DocumentAnalyticsPanel.svelte # Dashboard analytics panel
│   │       └── ProcessingHealthPanel.svelte # Dashboard processing health panel
│   └── routes/
│       ├── +layout.svelte        # App layout shell — auth guard, role-gated nav, logout
│       ├── +page.svelte          # Home/dashboard page
│       ├── layout.css            # Global styles (CSS custom properties)
│       ├── documents/
│       │   ├── +page.svelte      # Document list with search bar, filters, saved searches, DataTable
│       │   └── [id]/
│       │       └── +page.svelte  # Single document detail page
│       ├── documents/orphaned/
│       │   └── +page.svelte      # Orphaned + errored file management page
│       ├── settings/
│       │   ├── +layout.svelte     # Settings shell — admin guard, breadcrumb, detail level, staged-changes tray, palette
│       │   ├── +page.svelte       # Settings hub — health summary, pipeline map, destination rows
│       │   ├── intake/+page.svelte     # Intake settings page
│       │   ├── processing/+page.svelte # Processing settings page
│       │   ├── enrichment/+page.svelte # Enrichment settings page
│       │   ├── models/+page.svelte     # AI providers & models page (master–detail catalog editor)
│       │   ├── storage/+page.svelte    # Storage settings page
│       │   ├── safety/+page.svelte     # Safety settings page
│       │   ├── access/+page.svelte     # Access settings page
│       │   └── system/+page.svelte     # System settings page
│       ├── tags/
│       │   └── +page.svelte      # Tag management page
│       ├── people/
│       │   └── +page.svelte      # People + person types management
│       ├── document-types/
│       │   └── +page.svelte      # Document type management
│       ├── tasks/
│       │   ├── +page.svelte      # Task/batch monitoring page
│       │   └── [id]/
│       │       └── +page.svelte  # Single task detail page
│       ├── login/
│       │   └── +page.svelte      # Login page
│       ├── profile/
│       │   └── +page.svelte      # Self-service profile + API key management
│       └── logs/
│           └── +page.svelte      # Log viewer (kushim/edub/hugot/queue tabs)
├── static/
│   └── robots.txt
├── package.json
├── svelte.config.js
├── vite.config.js
├── eslint.config.js
└── .npmrc

web-wizard/               # SvelteKit SPA setup wizard (embedded in kushim binary)
├── src/
│   ├── app.html          # HTML shell
│   ├── app.css           # Tailwind styles (clay/gold/lapis/parchment palette)
│   ├── lib/
│   │   └── api.js        # API client for /wizard/config endpoints
│   └── routes/
│       ├── +layout.svelte  # Wizard layout shell
│       └── +page.svelte   # Six-step setup wizard flow
├── package.json
├── svelte.config.js
├── vite.config.js
├── eslint.config.js
├── .prettierrc
├── .npmrc
└── .gitignore
```

## Architecture Overview

This system is a document management pipeline with three main stages:

1. **Consumption** — Scans an inbox directory, extracts text (via MuPDF/gopdf/pdftotext), optionally OCRs (via Tesseract), optimizes PDFs, stores files with checksums
2. **Enrichment** — Applies LLM-based classification (title, doc type, tags, people, language) with pre-request token budget check and proportional re-reduction retry, plus semantic tag matching via embeddings
3. **Search & API** — Full-text search via PostgreSQL tsvector, REST API with SvelteKit SPA frontend

Processing is async via a task queue with batch tracking. The `kushim queue`
daemon consumes queued batches (Postgres LISTEN/NOTIFY + 30s fallback timer),
forks processing workers, polls the inbox on a schedule, and reclaims stale
batches and tasks.

### Process Architecture

The system uses two linked binaries with **worker forking**:

- **`edub`** (API server, `CGO_ENABLED=0`) — Pure Go binary handling HTTP requests. When consume/upload is called, it enqueues tasks with `status='queued'`; the `kushim queue` daemon picks up the batch and forks the batch's task CLI. It never claims a batch.
- **`kushim`** (CLI, CGo) — Handles all document processing (consumption, enrichment). Also hosts the **matcher RPC server** via `kushim hugot` and the queue daemon.
- **Matcher process** — A standalone `kushim hugot` process provides semantic tag matching (Hugot embeddings) over a Unix domain socket (`kushim-hugot.sock`). Both `edub` and `kushim` workers communicate with it via `internal/matcher.MatcherClient`.

## See Also

- [API Layer](api.md) — HTTP handlers, routes, response types
- [CLI Commands](cli.md) — Command definitions, container, flags
- [Pipeline](pipeline.md) — Consumption and enrichment engines
- [Task System](task-system.md) — Pool, transactional enqueue, CRUD, task handlers
- [Database](database.md) — Schema, queries, tsvector
- [Search](search.md) — Search engine, structured search, autocomplete
- [Tags](tags.md) — Tag subsystem: measurement substrate, identity, decisions, store reads
- [Tools](tools.md) — Adapter framework for all processing tools
- [Config & Utils](config-and-utils.md) — Configuration, cache, logging, utilities
- [Frontend](frontend.md) — SvelteKit UI and build system

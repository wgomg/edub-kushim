# Project tree

Directory layout derived from the tracked file set (`git ls-files`), so it cannot list paths that
are absent from the repository. Regenerate with `make docs-tree`; the one-line descriptions live in
`scripts/generate-project-tree.sh`.

```
edub-kushim/
├── .github  # GitHub configuration
│   └── workflows  # ci.yml (tests, CGo, web, vulncheck) and release.yml
├── build  # build root for the static C dependencies (contents gitignored)
├── cmd  # binary entry points
│   ├── apidocs  # renders the committed OpenAPI spec into docs/api.md
│   ├── edub  # API server and web UI host, CGO_ENABLED=0
│   └── kushim  # CLI, document processing, matcher and queue daemons, CGo
├── deploy  # deployment assets
│   └── systemd  # systemd unit and target templates
├── docker  # container files: glibc and musl builders, deployment entrypoint
├── docs  # project documentation
│   ├── developer-guide  # language and feature onboarding guides (Go, frontend, PostgreSQL, cgo, OCR, tasks, LLM, semantic matching, algorithms)
│   ├── research  # design notes and investigation reports
│   └── tags  # tag subsystem design, entry point overview.md
├── internal  # Go packages
│   ├── api  # HTTP server, middleware, permission gates, wizard server, embedded SPA
│   │   ├── handlers  # one file per resource: handlers with their API DTOs
│   │   ├── spec  # generated OpenAPI spec (make api-spec), embedded by edub
│   │   └── wizard  # setup wizard HTTP server and embedded SPA
│   ├── auth  # JWT tokens, claims, roles, API key hashing
│   ├── backup  # backup, restore, scheduling, rsync document mirror
│   ├── cgo  # MuPDF CGo glue and its non-CGo stub
│   ├── config  # configuration structs, loading, defaults, watcher, service files
│   ├── database  # pgx connection, sqlc queries, schema bootstrap, dumps, restore and migration helpers
│   │   └── sql  # embedded SQL assets
│   │       ├── queries  # sqlc query sources, regenerated into *.sql.go
│   │       └── schema  # seed SQL files
│   │           └── migrations  # goose migrations, the schema source of truth
│   ├── documents  # document domain: ingestion, storage, MIME, processing adapters
│   │   ├── consumption  # processing engine and its task handlers
│   │   ├── converter  # LibreOffice headless DOCX/ODT to PDF conversion
│   │   ├── mime  # MIME predicates, extension mapping, supported types
│   │   ├── ocr  # OCR engines: gosseract, ocrmypdf, standalone pipeline
│   │   ├── pdfoptimizer  # PDF optimization adapters
│   │   ├── storage  # storage layout and document, orphaned and trash file operations
│   │   ├── textextractor  # text extraction adapters (MuPDF, gopdf, pdftotext, DOCX, ODT)
│   │   └── thumbnail  # page thumbnail rendering
│   ├── enrichment  # LLM enrichment engine and its task handler
│   ├── errs  # error classification and database error mapping
│   ├── llm  # LLM capability registry and provider/model catalogs
│   │   ├── analyzer  # LLM classification adapters, prompts, gates and judges
│   │   └── testdata  # analyzer test fixtures
│   ├── log  # structured logging and memory metrics
│   ├── matcher  # tag matcher: Hugot engine, RPC client, embedding store
│   ├── ops  # process layer: CLI verbs, queue daemon, dispatcher, consume child, setup
│   │   ├── cli  # kushim command table and verbs
│   │   ├── configtask  # migrate-db and migrate-storage task handlers
│   │   ├── consume  # consume child process and tool advisories
│   │   ├── container  # shared-services container: config, database, matcher, tag tools
│   │   ├── dispatch  # batch dispatcher and launcher: claim, fork, in-process runs
│   │   ├── flags  # CLI flag parser
│   │   ├── queue  # queue daemon: scheduler, reaper, hot reload
│   │   ├── setup  # setup command, web wizard and terminal mode
│   │   └── tagmaintenance  # tag maintenance routine task handler
│   ├── search  # full-text and structured search engine
│   ├── service  # shared domain services: people, document types, batches, trash, users
│   ├── static  # embedded main SPA build
│   ├── tags  # tag subsystem: store, measurement, identity, decisions, worklists
│   │   ├── admission  # tag admission: adjudicator, identity resolver, audit runner
│   │   ├── curation  # tag vocabulary services for operators and adapters
│   │   ├── policy  # pure candidate generation: identity and band subjects
│   │   └── reconcile  # scheduled re-adjudication: worklist, evidence, apply, sweeps
│   ├── task  # task system: batches, enqueue, catalog, runner, locks
│   ├── testutil  # test helpers: config fixtures, minimal PDFs, assertions
│   ├── textnorm  # tag and database-key canonicalization
│   ├── tools  # tool adapter runner and text reduction
│   │   └── textreducer  # TextRank summarization
│   ├── types  # closed task-system vocabulary registry
│   └── version  # application version
├── scripts  # repository maintenance scripts
├── web  # main SvelteKit SPA, embedded into edub
└── web-wizard  # setup wizard SPA, embedded into kushim
    └── src  # wizard source
        ├── lib  # wizard API client
        └── routes  # six-step setup flow
│   ├── e2e  # Playwright smoke specs against the static build with a mocked API
│   │   └── fixtures  # E2E fixtures
│   ├── scripts  # SPA helper scripts
│   ├── src  # SPA source
│   │   ├── lib  # shared SPA modules
│   │   │   ├── assets  # static assets
│   │   │   ├── components  # reusable Svelte components
│   │   │   │   ├── settings  # settings-area section components
│   │   │   │   └── tags  # tag-review components
│   │   │   ├── pdf  # self-hosted pdf.js loader, worker wrapper, polyfills
│   │   │   ├── stores  # runes-based state stores
│   │   │   ├── stubs  # build stubs for optional modules
│   │   │   └── utils  # formatting, status-chip and tag-report helpers
│   │   └── routes  # SvelteKit routes
│   │       ├── docs  # generated API reference rendered by Scalar
│   │       ├── document-types  # document type management
│   │       ├── documents  # document list: search, filters, saved searches
│   │       │   ├── [id]  # document detail: PDF viewer and metadata editor
│   │       │   └── orphaned  # orphaned and errored file management
│   │       ├── login  # login page
│   │       ├── logs  # log viewer with per-daemon tabs
│   │       ├── people  # people and person-type management
│   │       ├── profile  # self-service profile and API keys
│   │       ├── settings  # settings hub
│   │       │   ├── access  # settings page: access and authentication
│   │       │   ├── enrichment  # settings page: enrichment
│   │       │   ├── intake  # settings page: intake and polling
│   │       │   ├── models  # settings page: AI providers and models
│   │       │   ├── processing  # settings page: processing pipeline
│   │       │   ├── safety  # settings page: backups, mirror and trash safety
│   │       │   ├── storage  # settings page: storage
│   │       │   └── system  # settings page: system
│   │       ├── tags  # tag workspace: vocabulary, new tags, coverage, overlap, deleted
│   │       ├── tasks  # batch and task monitoring
│   │       │   └── [id]  # task detail page
│   │       └── trash  # trash: soft-deleted documents
│   └── static  # SPA static files
```

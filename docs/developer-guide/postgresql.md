# Developer Guide — The PostgreSQL of edub-kushim

This guide explains how the database layer uses PostgreSQL — the design
decisions and the less common features, with real snippets and file
references. It is aimed at developers familiar with SQL and PostgreSQL, new to
this codebase; it does not explain SQL or PostgreSQL features themselves.

It complements the other docs:

| Document | What it answers |
|---|---|
| `docs/reference/database.md` | The schema and query inventory |
| `golang.md` | The Go side (including the sqlc layer) |
| `frontend.md` | The SvelteKit side |
| `task-system.md` | How the schema powers task claims, leases, and dedup |
| **`postgresql.md` (this)** | *The PostgreSQL features themselves* |

**Where the SQL lives**: migrations and seeds in
`internal/database/sql/schema/`, queries in `internal/database/sql/queries/`
(hand-written, compiled to Go by sqlc), plus hand-written dynamic SQL in
`internal/database/structured_search.go` and `dashboard.go`, and DDL-ish
operations in `connection.go` / `dbtest.go`.

---

## Table of contents

1. [Orientation](#1-orientation)
2. [Keys: identity vs TEXT primary keys](#2-keys-identity-vs-text-primary-keys)
3. [Partial indexes](#3-partial-indexes)
4. [Generated columns and full-text search](#4-generated-columns-and-full-text-search)
5. [Full-text search queries](#5-full-text-search-queries)
6. [Upserts: `ON CONFLICT` and `excluded`](#6-upserts-on-conflict-and-excluded)
7. [The optimistic claim pattern](#7-the-optimistic-claim-pattern)
8. [`LATERAL` joins and conditional aggregation](#8-lateral-joins-and-conditional-aggregation)
9. [`UNION ALL` with literal rows](#9-union-all-with-literal-rows)
10. [JSONB: documents-in-a-column](#10-jsonb-documents-in-a-column)
11. [SQL functions as gates](#11-sql-functions-as-gates)
12. [LISTEN/NOTIFY: the database as message bus](#12-listennotify-the-database-as-message-bus)
13. [Time arithmetic and dynamic intervals](#13-time-arithmetic-and-dynamic-intervals)
14. [Migrations with goose](#14-migrations-with-goose)
15. [Admin operations from application code](#15-admin-operations-from-application-code)
16. [Notably absent features (and why)](#16-notably-absent-features-and-why)
17. [Feature → file quick reference](#17-feature--file-quick-reference)
18. [Gotchas](#18-gotchas)

---

## 1. Orientation

- **PostgreSQL 16+** (test containers use `postgres:17`).
- **Driver**: `pgx` v5, used through the `database/sql` interface
  (`_ "github.com/jackc/pgx/v5/stdlib"`) for the app, and directly
  (`pgxpool`) for the LISTEN/NOTIFY listener.
- **sqlc** compiles the query files into type-safe Go (`sqlc generate` after
  editing any `*.sql` under `internal/database/sql/queries/`). Query files use
  `$1`, `$2`, ... placeholders; sqlc annotations (`:one`, `:many`, `:exec`,
  `:execrows`) appear in a comment above each query.
- **Migrations** are SQL files run by goose
  (`-- +goose Up` / `-- +goose Down` markers).
- **Every table has `TIMESTAMPTZ` timestamps** (timezone-aware, stored as UTC)
  defaulting to `CURRENT_TIMESTAMP` — never `TIMESTAMP` (which silently drops
  the timezone) and never `now()` as a string.

---

## 2. Keys: identity vs TEXT primary keys

Every surrogate key in the schema is an identity column
(`internal/database/sql/schema/migrations/00001_baseline.sql:3`):

```sql
id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
```

Business keys that come from outside (Go-generated UUIDs) are plain `TEXT`
primary keys instead (`batch.id`, `00001_baseline.sql:101`). Rule of thumb in
this schema: **database-invented IDs are identity, externally-invented IDs
are TEXT PKs.**

Shared singleton state — the maintenance lock, the tag-audit sweep counter, and
the three tag store-changed flags — lives in the `system_signal` table
(`00041_system_signal.sql`, extended by `00044_tag_store_change_record.sql` and
narrowed by `00045_drop_tag_identity_changed.sql`) —
the **signal store** pattern: one row per signal keyed by a unique `name`,
carrying its value as text plus an optional JSONB `data` object for structured
fields. Adding a signal is a seed row and its queries, not a new table; the
lock's use as a gate is covered in §11, and the flags' trigger-owned setting is
covered in §11's closing note.

---

## 3. Partial indexes

The codebase's favorite trick, used three ways.

### 1. Indexing only the rows queries actually touch

The task table can hold millions of rows, but workers only ever look at
`status = 'pending'`:

```sql
CREATE INDEX idx_task_pending ON task(created_at) WHERE status = 'pending';
CREATE INDEX idx_task_pending_type ON task(task_type, created_at) WHERE status = 'pending';
```

(00001_baseline.sql:125-126). The index is a fraction of the table's size, and
the planner uses it precisely when the query has the same `WHERE status =
'pending'` predicate.

### 2. A partial index for soft-delete

```sql
CREATE INDEX idx_document_deleted_at ON document(deleted_at) WHERE deleted_at IS NOT NULL;
```

(00007_trash_soft_delete.sql:3). Queries on live documents add
`deleted_at IS NULL` and never touch the trash rows in this index.

### 3. A partial UNIQUE index for conditional deduplication

The interesting one (`00001_baseline.sql:127-128`):

```sql
CREATE UNIQUE INDEX idx_task_dedup ON task(task_type, dedup_key)
    WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL;
```

Uniqueness applies **only while the task is still live**. Once a task
completes or fails, the row drops out of the index and the same `dedup_key`
becomes insertable again — so "re-run this document" creates a new task while
"queue this document twice right now" collapses to one. Dedup is enforced by
the database, not by application logic; the task-system side is in
`task-system.md` §9.

---

## 4. Generated columns and full-text search

`00002_tsvector.sql` adds a **stored generated column** that keeps the search
vector in sync automatically — the app never writes it:

```sql
ALTER TABLE document ADD COLUMN text_search_vector tsvector
  GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(text_content, ''))
  ) STORED;
```

- **`'simple'` is deliberate**: no stemming, no stopwords — documents arrive
  in many languages (the pipeline detects the language per document), and
  language-specific dictionaries would mangle everything else.
- Full-text search is fed the concatenation of title + body.
- If the formula changes, a new migration must rewrite the column
  (`ALTER TABLE ... ADD COLUMN ... GENERATED ALWAYS AS ...` in a fresh
  migration recomputes existing rows).

The index comes separately, in its own migration
(`00003_tsvector_index.sql`):

```sql
-- +goose NO TRANSACTION
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_document_tsv
  ON document USING GIN (text_search_vector);
```

`CONCURRENTLY` builds the GIN index without locking the table against writes —
but it **cannot run inside a transaction**, hence the
`-- +goose NO TRANSACTION` marker (goose otherwise wraps each migration in
one).

---

## 5. Full-text search queries

The dynamic search (`internal/database/structured_search.go:126-133`) is where
the FTS features come together:

```sql
ts_rank(d.text_search_vector, plainto_tsquery('simple', $1)) as rank,
COALESCE(ts_headline('simple', d.text_content, plainto_tsquery('simple', $1),
    'StartSel=<b>, StopSel=</b>, MaxWords=64, MinWords=32'), '') as snippet
FROM document d
WHERE d.text_search_vector @@ plainto_tsquery('simple', $1) AND d.deleted_at IS NULL
```

- `plainto_tsquery` (not `to_tsquery`, which expects operators in the input
  and would break on user text) applies the same `'simple'` config as the
  column — `@@` only matches if both sides used the *same* config. All three
  `$1` references reuse one parameter.
- `ts_rank` drives `ORDER BY rank` (`structured_search.go:171`);
  `ts_headline` produces the snippet the UI shows; `COALESCE(..., '')` guards
  against NULL `text_content`.

The generated column guarantees `text_search_vector` is never out of sync with
the text, so the query can be pure and index-friendly.

---

## 6. Upserts: `ON CONFLICT` and `excluded`

The "insert, but only if absent" pattern appears in every creation path —
seeds, tags, people, document tags, batch owners. The two forms:

**Do nothing** (`internal/database/sql/queries/tag.sql:17-20`):

```sql
INSERT INTO tag (name)
VALUES ($1)
ON CONFLICT (name) DO NOTHING
RETURNING id;
```

`RETURNING id` still fires on the *conflict* path — but returns no rows, which
surfaces as `sql.ErrNoRows` on the Go side. That's the documented way to
detect "already existed" on a `:one` upsert.

**Do update** — the batch-owner takeover (`batch.sql:24-31`):

```sql
INSERT INTO batch_owner (batch_id, owner_id, pid, acquired_at, last_heartbeat)
VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(batch_id) DO UPDATE SET
  owner_id = excluded.owner_id,
  pid = excluded.pid,
  acquired_at = CURRENT_TIMESTAMP,
  last_heartbeat = CURRENT_TIMESTAMP;
```

The multi-column composite key `(document_id, people_id, people_type_id)`
(`document_people.sql:24`) shows `ON CONFLICT` on composite keys works the
same way.

The seeds are the canonical idempotency pattern: `INSERT ... VALUES (…),
(…), … ON CONFLICT (name) DO NOTHING`, safe to run on every boot (they are,
via `InitializeSchema`). `seed-tags.sql` carries 402 values and marks them
operator-owned (`seeded`), re-promoting only rows still provisional
(`ON CONFLICT (name) DO UPDATE SET seeded = true, promoted_at = coalesce(tag.promoted_at, now()) WHERE NOT tag.seeded OR tag.promoted_at IS NULL`), giving each row a distinct `created_at`
so tag listings order deterministically; `seed-tag-aliases.sql` carries 39
aliases and skips any alias whose name is already a tag row; 9 document types
and 15 people types round out the seeds.

---

## 7. The optimistic claim pattern

Multiple worker processes claim tasks without locks, with a **guarded UPDATE**
that is atomic at the row level (`task.sql:99-104`):

```sql
-- name: ClaimTask :execrows
UPDATE task SET
    status = 'processing',
    started_at = CURRENT_TIMESTAMP
WHERE id = $1 AND status = 'pending';
```

The `WHERE ... AND status = 'pending'` is the lock: exactly one of N racing
workers flips `pending → processing`; the others affect 0 rows, and the Go
side checks `RowsAffected` (that's what sqlc's `:execrows` annotation is for)
and treats 0 as "someone else took it". The same rows-affected discipline
drives the backup lock (§11).

The full concurrency story — batch ownership, heartbeats, stale sweeps, and
why `SELECT ... FOR UPDATE SKIP LOCKED` was not used — is in
`task-system.md` §3–§5 and §10.

---

## 8. `LATERAL` joins and conditional aggregation

The batch overview query (`batch.sql:137-166`) is the most advanced statement
in the codebase. It computes per-batch task statistics with a `LATERAL`
subquery:

```sql
SELECT
    b.id AS batch_id,
    b.source,
    ...
    COALESCE(sub.total, 0) AS total,
    COALESCE(sub.waiting, 0) AS waiting,
    ...
    bo.last_heartbeat AS owner_last_heartbeat,
    bo.pid AS owner_pid
FROM batch b
LEFT JOIN LATERAL (
    SELECT
        COUNT(*) AS total,
        SUM(CASE WHEN t.status = 'waiting'   THEN 1 ELSE 0 END) AS waiting,
        SUM(CASE WHEN t.status = 'pending'   THEN 1 ELSE 0 END) AS pending,
        SUM(CASE WHEN t.status = 'processing' THEN 1 ELSE 0 END) AS processing,
        SUM(CASE WHEN t.status = 'completed' THEN 1 ELSE 0 END) AS completed,
        SUM(CASE WHEN t.status = 'failed'    THEN 1 ELSE 0 END) AS failed,
        SUM(CASE WHEN t.status = 'cancelled' THEN 1 ELSE 0 END) AS cancelled,
        SUM(CASE WHEN t.status = 'discarded' THEN 1 ELSE 0 END) AS discarded,
        MIN(t.started_at) AS first_started_at,
        MAX(t.completed_at) AS last_completed_at
    FROM task t WHERE t.batch_id = b.id
) sub ON true
LEFT JOIN batch_owner bo ON bo.batch_id = b.id
ORDER BY b.created_at DESC
```

Two idioms worth copying:

- **`LEFT JOIN LATERAL ... ON true`** — the `ON true` means "always join, even
  if the lateral produced nothing" — batches with zero tasks still appear with
  `COALESCE(sub.total, 0)`.
- **Conditional aggregation** — `SUM(CASE WHEN t.status = 'completed' THEN 1
  ELSE 0 END)` builds a per-batch status histogram in one pass. (The newer
  `FILTER (WHERE ...)` would do the same — see §16 for why it isn't used.)

---

## 9. `UNION ALL` with literal rows

The activity timeline merges heterogeneous sources into one feed: each branch
selects the same column shape, with **literal constant columns** filling in
fields a source doesn't have (`dashboard.go:83-120`):

```sql
SELECT 'document_uploaded' AS event_type,
       d.created_at AS event_time,
       d.title AS title,
       '' AS payload_file_path,
       d.document_id AS ref_id,
       '' AS batch_id,
       '' AS task_id
FROM document d
WHERE d.deleted_at IS NULL

UNION ALL

SELECT CASE WHEN t.status = 'completed' THEN 'task_completed' ELSE 'task_failed' END,
       t.completed_at,
       COALESCE(t.payload->>'file_name', ''),
       ...
FROM task t
WHERE t.status IN ('completed', 'failed') AND t.completed_at IS NOT NULL

UNION ALL
-- batch_created events...

ORDER BY event_time DESC
LIMIT 30
```

- The `CASE WHEN` in the second branch derives the event type from data.
- `UNION ALL` (not `UNION`) keeps duplicates — events are never deduplicated
  away.
- The trailing `ORDER BY ... LIMIT 30` applies to the *whole* union.

---

## 10. JSONB: documents-in-a-column

The `task` table stores arbitrary per-task data in JSONB columns
(`00001_baseline.sql:46-47`):

```sql
payload JSONB,
result JSONB,
```

The pipeline writes task-specific payloads (consume: file path + document id;
enrich: document id) into the same table. What the SQL uses it
for: extraction with `->>` (text; NULL if missing) — the successor link's
own `task.parent_task_id` column (migration `00056`) is what replaced the
payload-key joins the old queries read, and the remaining in-query reads are
display projections like `ListBatchTaskProgress`. The Go side declares these
columns as `*json.RawMessage` via the sqlc override in `sqlc.yaml`, so
handlers pass payloads through untouched and only the SQL reads inside them.

JSONB is *not* used for search — text lives in `text_content` (full-text
indexed) — which keeps payloads for machine data only.

---

## 11. SQL functions as gates

Plain SQL functions (not triggers) are used to centralize business conditions
so they can be reused across queries.

The resource-lock gate (`lock_is_held`, created by
`00049_named_resource_locks.sql` over the lock rows of `system_signal`):

```sql
CREATE OR REPLACE FUNCTION lock_is_held(lock_name text) RETURNS boolean AS $$
BEGIN
  RETURN EXISTS (
    SELECT 1 FROM system_signal
    WHERE name = lock_name
      AND value = 'true'
      AND (data->>'started_at')::timestamptz > NOW() - INTERVAL '30 minutes'
  );
END;
$$ LANGUAGE plpgsql;
```

```sql
-- name: GetNextPendingTaskOfType :one
SELECT id FROM task
WHERE status = 'pending' AND task_type = @task_type
  AND NOT EXISTS (
    SELECT 1 FROM system_signal s
    WHERE s.name = ANY(@wait_locks::text[]) AND lock_is_held(s.name)
  )
ORDER BY created_at LIMIT 1;
```

The function encodes the locks' *staleness rule* in one place; every worker
poll (and the maintenance runs themselves) consults it. The waits each task
type gates on are declared once in the closed vocabulary table
(`types.LockDeclarationFor`): consume and enrich wait `maintenance`,
`thumbnail` waits `maintenance` and `storage` — the claim passes the type's
waits as `wait_locks`, and an empty wait list makes the `NOT EXISTS` vacuously
true, so the predicate can never leak into a type that declares none. The
locks are acquired with a
conditional multi-row UPDATE on the signal rows (§2) — an atomic
compare-and-swap over all of an execution's holds at once:

```sql
UPDATE system_signal SET value = 'true',
    data = jsonb_build_object('started_at', to_jsonb(NOW()),
                              'owner_token', $1::uuid::text, 'batch_id', $2::text)
WHERE name = ANY(@lock_names::text[])
  AND (value = 'false' OR (data->>'started_at')::timestamptz <= NOW() - INTERVAL '30 minutes');
```

Zero rows affected = a hold was already taken — same `:execrows` discipline
as the task claim (§7).

The store-changed flags are the counterexample where the mechanism is a
**trigger, not a gate**: `tag_store_changed()` (migration `00044`, narrowed to
the three remaining flags by `00045`) upserts the `system_signal` flags to
`'true'` whenever a row changes in `tag`,
`document_tag`, `tag_alias`, or `deleted_tag`, fired by AFTER row triggers so
the set rides the mutating statement's own transaction. Table-owned setting is
deliberate: the vocabulary has ~23 mutating queries called from the enricher's
persist transaction, the curation service, the reconciler, the identity pass,
the audit fills, and the seeders, and a Go-side call at each site is one forget
away from a silent gap. The UPDATE triggers' `WHEN` guards compare only the
semantic columns, so bookkeeping writes (orphan marks, the seeder's no-op
`ON CONFLICT` rewrites) do not fire — without that, the measurement's zero-work
skip would never hold on a running store. A `WHEN` referencing `OLD` is illegal
on a trigger that can fire for INSERT, so the INSERT/DELETE arms are separate
triggers carrying no guard.

---

## 12. LISTEN/NOTIFY: the database as message bus

The queue daemon doesn't poll Postgres blindly — it is woken up by the
database. Schema side (`00004_listen_notify.sql`, a PL/pgSQL trigger):

```sql
CREATE OR REPLACE FUNCTION notify_batch_queued()
RETURNS trigger AS $$
BEGIN
  IF NEW.status = 'queued' THEN
    PERFORM pg_notify('batch_queued', NEW.id);
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER batch_queued_notify
AFTER INSERT OR UPDATE ON batch
FOR EACH ROW EXECUTE FUNCTION notify_batch_queued();
```

Client side (`internal/ops/queue/notify.go`) — a **dedicated single-connection
pool** (LISTEN state is per-connection, so it must never be shared):

```go
poolCfg.MinConns = 1
poolCfg.MaxConns = 1

conn.Exec(ctx, "LISTEN batch_queued")
// ...
_, err := conn.Conn().WaitForNotification(ctx)
```

The daemon then re-checks the database. Notifications are a *hint*, not the
source of truth — the Go side drops them when busy (non-blocking send on a
buffered channel, `notify.go:68-71`) and a 30-second safety timer in the
daemon re-polls regardless; the listener reconnects with backoff if it dies.
The daemon-side semantics (wake-up loop, timer reset, housekeeping) are in
`task-system.md` §6.

---

## 13. Time arithmetic and dynamic intervals

**Parameterized intervals** — when the interval duration comes from config,
the codebase builds it with a cast of a concatenated string (`document.sql:165`):

```sql
DELETE FROM document
WHERE deleted_at < CURRENT_TIMESTAMP - ($1::text || ' days')::INTERVAL;
```

`($1::text || ' days')::INTERVAL` turns the bound parameter `'30'` into
`INTERVAL '30 days'`. The parameter stays a parameter — no string
interpolation into SQL.

**Duration math** — average task duration in milliseconds
(`dashboard.go:274-276`):

```sql
CAST(COALESCE(AVG(
    EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000
), 0.0) AS INTEGER) AS avg_duration_ms
```

Static staleness probes (`INTERVAL '15 seconds'`, `INTERVAL '7 days'`,
`INTERVAL '30 minutes'`) appear throughout the claim/lease/backup queries
(§7, §11).

---

## 14. Migrations with goose

Each migration is a file with `-- +goose Up` / `-- +goose Down` blocks
(annotations in SQL comments). The project uses the less common annotations
too:

```sql
-- +goose NO TRANSACTION          -- 00003_tsvector_index.sql:1
```

Required because `CREATE INDEX CONCURRENTLY` cannot run inside a transaction
(goose wraps each migration in one by default).

```sql
-- +goose StatementBegin          -- 00004_listen_notify.sql:4
CREATE OR REPLACE FUNCTION ...
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
```

Required for multi-statement bodies with semicolons inside (`$$ ... $$` is
dollar-quoting, which keeps the PL/pgSQL body free of quote-escaping — note
the function body contains `;`s that must not split the statement).

Migration hygiene in this repo: **never edit an applied migration** — add a
new numbered file (00007 renamed columns with `ALTER TABLE ... RENAME COLUMN`
instead of editing 00001). Every `Up` has a `Down` that drops what was created.

---

## 15. Admin operations from application code

The app talks to the `postgres` system catalog and runs DDL at boot and in
tests — features you rarely see in application SQL.

**Auto-create the database** (`internal/database/connection.go:36-59`): connect
to the maintenance DB, check `pg_database`, create if missing:

```sql
SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)
CREATE DATABASE "name"          -- identifier quoted via quoteIdent()
```

`CREATE DATABASE` cannot be parameterized (it's DDL), so the name is
double-quote-escaped (`"` → `""`) before interpolation — the injection-safe
way to interpolate an identifier, contrasted with parameterizing *values*.

**Force-drop test databases** (`internal/database/dbtest.go:84`):

```sql
DROP DATABASE IF EXISTS edub_test_database WITH (FORCE)
```

`WITH (FORCE)` (PostgreSQL 13+) terminates any lingering connections to the
database and drops it — without it, a leaked connection makes the DROP fail
with "database is being accessed by other users". That's how the per-package
test harness guarantees cleanup.

**Reset identity sequences in tests** (`dbtest.go:178`):

```sql
ALTER TABLE tag ALTER COLUMN id RESTART WITH 1
```

Because the IDs are identity columns (§2), tests reset them with this instead
of fiddling with `setval()` on a separate sequence.

---

## 16. Notably absent features (and why)

Knowing what *isn't* here is as instructive as what is:

| Feature | Why it's absent |
|---|---|
| `SELECT ... FOR UPDATE SKIP LOCKED` | The task claim uses an atomic guarded UPDATE (`WHERE status='pending'` + `RowsAffected`) instead — one statement, no row lock held, no explicit unlock, works identically across retries. See §7. |
| `pg_advisory_lock` | Backup mutual exclusion uses a **signal-row lock** (§11) instead — visible/queryable state, staleness rule in a SQL function, and no session-lifetime ownership to leak. |
| `pg_trgm` / `ILIKE` / `LIKE`-with-`%`-prefix | Name search is `LIKE $1` with the caller appending `%` (prefix match only) — an index-friendly pattern. Fuzzy/substring search would need trigram indexes, which aren't needed. |
| `FILTER (WHERE ...)` | The codebase predates it or simply prefers portability: `SUM(CASE WHEN ... THEN 1 ELSE 0 END)` is the classic form that works everywhere. |
| `gen_random_uuid()` | UUIDs are generated in Go (`google/uuid`) and passed in as TEXT PKs — keeps the DB layer free of pgcrypto and the IDs available before the insert. |
| Enum types (`CREATE TYPE ... AS ENUM`) | Most statuses are TEXT + `CHECK (col IN (...))` — adding a value is a plain migration, not an `ALTER TYPE` (which is awkwardly transactional in older versions). Real enums exist where sqlc maps them to Go types with a registry: `batch_origin`/`batch_status`/`task_type`/`task_status` (00013; `batch_source` became `batch_origin` in 00047) and `tag_verdict` (`same`/`variant`/`related`, 00017, mapped to `types.TagVerdict`). |
| `date_trunc` | Dashboard day-bucketing uses `date(created_at)` — enough for daily granularity. |
| Window functions (`OVER (...)`) | The batch overview's per-group aggregates fit `LATERAL` + `GROUP BY`; no running-total/partition-rank need has appeared. |
| Full-text search on `jsonb` | FTS targets `text_content` only; JSONB payloads are machine data. |
| `SERIAL` | Identity columns everywhere (§2). |
| `TIMESTAMP` (without time zone) | `TIMESTAMPTZ` everywhere — never store local-wall-clock. |

---

## 17. Feature → file quick reference

| Feature | Where |
|---|---|
| Identity columns, partial indexes, composite PKs, CHECKs | `sql/schema/migrations/00001_baseline.sql` |
| Generated tsvector column | `sql/schema/migrations/00002_tsvector.sql` |
| GIN + CONCURRENTLY + NO TRANSACTION | `sql/schema/migrations/00003_tsvector_index.sql` |
| Trigger + `pg_notify` | `sql/schema/migrations/00004_listen_notify.sql` + `internal/ops/queue/notify.go` |
| Signal store + SQL gate function | `sql/schema/migrations/00041_system_signal.sql`, `sql/schema/migrations/00049_named_resource_locks.sql`, `sql/queries/lock.sql`, `sql/queries/task.sql:13-23` |
| Soft delete + partial index | `sql/schema/migrations/00007_trash_soft_delete.sql` |
| Idempotent multi-row seeds with `ON CONFLICT` | `sql/schema/seed-*.sql` |
| Upsert + `excluded` | `sql/queries/batch.sql:24-31`, `tag.sql:17-20`, `people.sql`, `document_tag.sql`, `deleted_tag.sql` |
| Optimistic claim (`:execrows`) | `sql/queries/task.sql:99-104` |
| Partial unique index + `ON CONFLICT (cols) WHERE predicate DO NOTHING` | `00017_tag_verdict_events.sql` (fresh verdicts deduped per pair/version) + `00024_tag_quality_events.sql` (fresh tag-quality verdicts deduped per name/version) + `sql/queries/tag_verdict_event.sql`, `sql/queries/tag_quality_event.sql` |
| `DISTINCT ON (...) ... IN (SELECT unnest(...), unnest(...))` | `sql/queries/tag_verdict_event.sql` (latest verdict per pair, batched) |
| `DISTINCT ON (col) ... = ANY($1::text[])` (batched cache read) | `sql/queries/tag_quality_event.sql` (latest rubric verdict per tag name) |
| LATERAL + conditional aggregation | `sql/queries/batch.sql:137-166` |
| Scalar subqueries, `NOT EXISTS`, `UNION ALL` literals | `internal/database/dashboard.go:83-120,239-248` |
| JSONB `->>` extraction | `internal/database/dashboard.go:98-100` |
| FTS `@@`, `ts_rank`, `ts_headline`, dynamic WHERE building | `internal/database/structured_search.go` |
| Dynamic intervals `($1::text || ' days')::INTERVAL` | `sql/queries/task.sql:233`, `document.sql:149` |
| `EXTRACT(EPOCH ...)` duration | `internal/database/dashboard.go:271-284` |
| `pg_database` + `CREATE DATABASE` + quoting | `internal/database/connection.go:36-63` |
| `DROP DATABASE ... WITH (FORCE)`, `RESTART WITH 1` | `internal/database/dbtest.go:76-85,177-179` |
| goose annotations | `sql/schema/migrations/*.sql` (Up/Down, StatementBegin/End, NO TRANSACTION) |

---

## 18. Gotchas

- **`ON CONFLICT` requires a matching unique index.** `DO NOTHING` without a
  target works on any unique violation; `ON CONFLICT (cols)` fails at runtime
  if no unique constraint covers those columns.
- **Partial index predicates must be implied by the query.** An index on
  `task(created_at) WHERE status='pending'` is useless to a query without the
  `status = 'pending'` condition. Keep the two in lockstep.
- **`@@` requires matching configs.** `to_tsvector('simple', ...)` only
  matches `plainto_tsquery('simple', ...)` — mix `'english'` on one side and
  the match silently misses. Same rule for `ts_headline`/`ts_rank` configs.
- **`CREATE INDEX CONCURRENTLY` can't run in a transaction** (hence
  `-- +goose NO TRANSACTION`), and it can leave an `INVALID` index behind if
  the build is interrupted — that's why the migration uses
  `IF NOT EXISTS` plus the separate generated-column migration.
- **`SERIAL` and identity are not interchangeable in the catalog** — `ALTER
  COLUMN id RESTART WITH 1` works for identity; `setval('seq')` is the SERIAL
  way. The test harness uses the identity form.
- **`GENERATED ALWAYS` columns reject explicit inserts** (`OVERRIDING SYSTEM
  VALUE` needed) — by design, and a common "why is my insert failing" moment.
- **`DROP DATABASE` can't run inside a transaction** and fails on live
  connections — `WITH (FORCE)` handles the second, an autocommit Exec the
  first.
- **`RETURNING` on a conflicting upsert returns no row** — detect "already
  existed" via `sql.ErrNoRows`, not by parsing the conflict.
- **`NOW()`/`CURRENT_TIMESTAMP` are transaction-stable**: identical for the
  whole transaction, which is what the heartbeat/backup-lock logic relies on.
  For per-row "clock" time you'd need `clock_timestamp()`.
- **`||` with a parameter needs a cast** (`$1::text`) — the concat operator
  can't infer the parameter's type from the literal side.
- **`->>` yields NULL for missing keys** — wrap in `COALESCE` before
  concatenating or comparing.
- **`UNION` (without ALL) dedups** — the timeline uses `UNION ALL` because
  events are never duplicates.
- **Grouping by an alias** (`GROUP BY day`) is PostgreSQL-specific but legal;
  don't use it if you ever need the query to be portable.
- **sqlc placeholders are `$1`-based**, not `?` — and the dynamic builder in
  `structured_search.go` must count them carefully (`nextIndex()` walks
  `len(args)+1`) because the same filter query is assembled twice (rows +
  count) with identical numbering.
- **`EXECUTE FUNCTION` is the current trigger syntax**; `EXECUTE PROCEDURE`
  still parses but is deprecated in PostgreSQL 18+.

---

*Last verified against the tree: 2026-08-03. If code and doc disagree, code wins.*
